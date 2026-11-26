package tracks

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	// serveBinaryPoll is how often serve looks at its own binary for a new build.
	serveBinaryPoll = 2 * time.Second
	// serveRestartMaxWait is how long a replaced serve waits for the desk's turn to end before it
	// restarts anyway.
	serveRestartMaxWait = 10 * time.Minute
)

// binaryStamp tells one build of a file from the next: `make install` writes a new file, so its
// inode, size or modification time changes.
type binaryStamp struct {
	ino  uint64
	size int64
	mod  time.Time
}

func stampOf(path string) (binaryStamp, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return binaryStamp{}, false
	}
	s := binaryStamp{size: fi.Size(), mod: fi.ModTime()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		s.ino = uint64(st.Ino)
	}
	return s, true
}

// buildLabel names the build of the file at path by when it was written and its size, such as
// "26 Nov 12:06:41, 22 MB"; empty when there is no such file.
func buildLabel(path string) string {
	s, ok := stampOf(path)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s, %d MB", s.mod.Format("2 Jan 15:04:05"), (s.size+1<<19)>>20)
}

// serveBuildLine is the line tracks desk prints about the running serve: its build next to the
// installed one, and whether the two differ; "not running" when there is no serve.
func serveBuildLine(si serveInfo, running bool, installed string) string {
	switch {
	case !running:
		return "serve: not running"
	case si.Build == "":
		return "serve: running, build unknown (started before builds were recorded); installed " + installed
	case si.Build == installed:
		return "serve: running the installed build (" + installed + ")"
	}
	return fmt.Sprintf("serve: running build %s, installed %s: it has not restarted on the install yet (a desk turn may still be running)", si.Build, installed)
}

// watchBinary reports true once the file at path holds a new build: changed from what it was when
// the watch began and unchanged over one more poll, so a build still being written isn't run.
// It then waits until idle says yes (the desk is between turns), or maxWait at most. It reports
// false when ctx ends first.
func watchBinary(ctx context.Context, path string, every time.Duration, idle func() bool, maxWait time.Duration) bool {
	start, ok := stampOf(path)
	if !ok {
		return false
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	var seen binaryStamp
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
		if !since.IsZero() {
			if idle() || time.Since(since) >= maxWait {
				return true
			}
			continue
		}
		cur, ok := stampOf(path)
		switch {
		case !ok || cur == start:
			seen = binaryStamp{}
		case cur == seen:
			since = time.Now()
			if idle() {
				return true
			}
		default:
			seen = cur
		}
	}
}

// between is whether the desk is not in a turn, so restarting serve cuts nothing short. Without a
// desk (serve --mock) it always is.
func (r *deskRunner) between() bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.busy
}

// reexecServe replaces this process with the binary now at its path, with the same arguments, so
// the pid, serve.log and the clients' way of finding serve all stay; the new serve writes a new
// serve.json, which clients read on their next request.
func reexecServe(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restarting on the new build: %w", err)
	}
	argv := append([]string{exe, "serve"}, args...)
	if err := syscall.Exec(exe, argv, os.Environ()); err != nil {
		return fmt.Errorf("restarting on the new build: %w", err)
	}
	return nil
}
