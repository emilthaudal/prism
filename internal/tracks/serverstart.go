package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The Rex plugin runs `tracks resume --all --once-per-server` each time init.lua loads: when the
// server starts, and again on every config reload. A restarted server resumes the Claude
// sessions its hook reported and brings the other panes back as fresh shells, so this is where
// the track agents Rex left as shells come back on their own.
var (
	serverWait   = 60 * time.Second
	serverPoll   = time.Second
	resumeSettle = 3 * time.Second
	resumePasses = 4
	// betweenPasses gives restored shells time to finish starting before the next pass.
	betweenPasses = func() { time.Sleep(5 * time.Second) }
)

var instanceID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// resumeOncePerServer resumes the open tracks once per Rex server instance, so a config reload
// does nothing. A card skipped because its shell was not ready yet gets another pass.
func resumeOncePerServer(out io.Writer) error {
	lf, err := openResumeLog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracks: resume.log:", err)
		return resumeOnce(out, func() {})
	}
	defer lf.Close()
	err = resumeOnce(io.MultiWriter(out, lf), lf.trim)
	if err != nil {
		fmt.Fprintln(lf, "tracks:", err)
		// The log has the error: exit 1 without printing it again, so resume.err holds only
		// what the log missed.
		return exitCode(1)
	}
	return nil
}

// resumeOnce runs the resume; trim is called once this run owns the instance and resume.lock.
func resumeOnce(out io.Writer, trim func()) error {
	inst, err := waitServerInstance()
	if err != nil {
		return err
	}
	claimed, err := claimInstance(inst)
	if err != nil {
		return err
	}
	if !claimed {
		fmt.Fprintln(out, "already resumed for Rex server "+inst)
		return nil
	}
	// A run left from an earlier server may still be between passes. Only one run types at a
	// time, and a run stops once a newer server's run has claimed (and removed its claim).
	unlock, err := lockResume()
	if err != nil {
		return err
	}
	defer unlock()
	trim()
	current := func() bool {
		_, err := os.Stat(filepath.Join(home(), "resumed", inst))
		return err == nil
	}
	fmt.Fprintf(out, "%s: Rex server %s started, resuming tracks\n", time.Now().Format(time.RFC3339), inst)
	time.Sleep(resumeSettle)
	var pending []string
	for _, c := range loadCards() {
		if c.mode() == "parked" {
			fmt.Fprintln(out, resumeResult{name: c.Name, what: "skipped", detail: "parked: resume it by name"})
			continue
		}
		pending = append(pending, c.Name)
	}
	failed := 0
	for pass := 1; len(pending) > 0; pass++ {
		if pass > 1 {
			betweenPasses()
		}
		evs := readEvents()
		var again []string
		for _, name := range pending {
			if !current() {
				fmt.Fprintln(out, "Rex server "+inst+" was replaced; its newer run resumes the rest")
				return nil
			}
			c, err := loadCard(name)
			if err != nil || c.mode() == "parked" {
				continue // closed, renamed or parked since the last pass
			}
			r := resumeCard(c, evs, false)
			if r.what == "skipped" && retryable(r.detail) && pass < resumePasses {
				again = append(again, name)
				continue
			}
			if r.what == "failed" {
				failed++
			}
			fmt.Fprintln(out, r)
		}
		pending = again
	}
	if failed > 0 {
		return fmt.Errorf("%d tracks failed to resume", failed)
	}
	return nil
}

// retryable is whether a skip may clear once the restored shells have started.
func retryable(detail string) bool {
	for _, p := range []string{"Claude already runs", "the Claude session's directory is gone", "parked"} {
		if strings.HasPrefix(detail, p) {
			return false
		}
	}
	return true
}

// waitServerInstance waits for the Rex server to answer and returns its instance id, which a
// restart changes and a config reload keeps.
func waitServerInstance() (string, error) {
	deadline := time.Now().Add(serverWait)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		b, err := rexRun(ctx, "server", "status", "--json")
		cancel()
		if err == nil {
			var s struct {
				InstanceID string `json:"instance_id"`
			}
			if err := json.Unmarshal(b, &s); err != nil || !instanceID.MatchString(s.InstanceID) {
				return "", errors.New("rex server status gave no usable instance_id")
			}
			return s.InstanceID, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the Rex server did not answer within %s: %v", serverWait, err)
		}
		time.Sleep(serverPoll)
	}
}

// lockResume holds resume.lock in the tracks state directory until unlock; the lock goes with
// the process, so a run that dies frees it.
func lockResume() (unlock func(), err error) {
	return flockResume(syscall.LOCK_EX)
}

// errResumeBusy is a manual resume finding resume.lock held by another resume.
var errResumeBusy = errors.New("another resume is running; try again in a minute")

// tryLockResume is lockResume that fails with errResumeBusy instead of waiting.
func tryLockResume() (unlock func(), err error) {
	return flockResume(syscall.LOCK_EX | syscall.LOCK_NB)
}

func flockResume(how int) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(home(), "resume.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errResumeBusy
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// resumeLogKeep is how many lines of resume.log survive a trim. A run writes a few lines.
const resumeLogKeep = 200

// resumeLog is resume.log opened for appending. Its trim swaps the file by rename, so it is
// called only by the run that owns resume.lock, and the handle is reopened afterwards.
type resumeLog struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func openResumeLog() (*resumeLog, error) {
	l := &resumeLog{path: filepath.Join(home(), "resume.log")}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	l.f = f
	return l, err
}

func (l *resumeLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(b)
}

func (l *resumeLog) Close() error { return l.f.Close() }

// trim keeps the last resumeLogKeep lines. A failure leaves the log as it was.
func (l *resumeLog) trim() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if trimLog(l.path, resumeLogKeep) != nil {
		return
	}
	if f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		l.f.Close()
		l.f = f
	}
}

// trimLog keeps the last keep lines of the file at path; a missing or short file is left alone.
func trimLog(path string, keep int) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= keep {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".resume.log.")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(strings.Join(lines[len(lines)-keep:], ""))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// claimInstance records that this server instance is being resumed. It is false when another
// run already claimed it, and it forgets older instances.
func claimInstance(inst string) (bool, error) {
	dir := filepath.Join(home(), "resumed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, inst), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	f.Close()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != inst {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return true, nil
}
