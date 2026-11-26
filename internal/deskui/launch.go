package deskui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	serveStartWait = 10 * time.Second
	servePollEvery = 100 * time.Millisecond
	dialTimeout    = 500 * time.Millisecond
	// serveLoserMsg is what a second serve prints when the first holds serve.lock.
	serveLoserMsg = "runs already"
)

var errQuit = errors.New("quit")

// tracksBinary is the tracks that starts serve: TRACKS_BIN, else the one on PATH, else this one.
func tracksBinary() string {
	if b := os.Getenv("TRACKS_BIN"); b != "" {
		return b
	}
	if b, err := exec.LookPath("tracks"); err == nil {
		return b
	}
	b, _ := os.Executable()
	return b
}

// serveReachable says whether serve.json names a live serve that accepts connections.
func serveReachable(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, "serve.json"))
	if err != nil {
		return false
	}
	var si struct {
		URL string `json:"url"`
		PID int    `json:"pid"`
	}
	if json.Unmarshal(b, &si) != nil {
		return false
	}
	if si.PID > 0 {
		if err := syscall.Kill(si.PID, 0); err != nil && !errors.Is(err, syscall.EPERM) {
			return false
		}
	}
	u, err := url.Parse(si.URL)
	if err != nil || u.Host == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", u.Host, dialTimeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// EnsureServe starts prism server, detached, when none is reachable, and waits for its serve.json.
func EnsureServe() error { return ensureServe(tracksBinary(), tracksHome(), serveStartWait) }

func ensureServe(bin, home string, wait time.Duration) error {
	if serveReachable(home) {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(home, "serve.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	offset, _ := logf.Seek(0, io.SeekEnd)

	// --detach starts serve outside this process's tree and returns; serve then writes to its log.
	cmd := exec.Command(bin, "serve", "--detach")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start prism server: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(wait)
	tick := time.NewTicker(servePollEvery)
	defer tick.Stop()
	for {
		if serveReachable(home) {
			return nil
		}
		select {
		case err := <-exited:
			exited = nil
			if msg := serveLogSince(logPath, offset); err != nil && !strings.Contains(msg, serveLoserMsg) {
				if msg == "" {
					msg = "it exited without a message"
				}
				return fmt.Errorf("prism server did not start: %s (log: %s)", msg, logPath)
			}
		case <-tick.C:
		}
		if msg := serveRefusal(serveLogSince(logPath, offset)); msg != "" {
			return fmt.Errorf("prism server did not start: %s (log: %s)", msg, logPath)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prism server did not write serve.json within %s (log: %s)", wait, logPath)
		}
	}
}

// serveRefusal is the error a detached serve exited with, as tracks prints it ("tracks: …"), from
// what it wrote to its log; a serve that lost serve.lock to another is no refusal.
func serveRefusal(log string) string {
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "tracks: ") && !strings.Contains(line, serveLoserMsg) {
			return strings.TrimPrefix(line, "tracks: ")
		}
	}
	return ""
}

// serveLogSince is what serve wrote to its log after offset, trimmed.
func serveLogSince(path string, offset int64) string {
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) <= offset {
		return ""
	}
	return strings.TrimSpace(string(b[offset:]))
}

// connectHTTP starts serve when needed and connects. A failed start is shown as it is, with
// a retry on Enter.
func connectHTTP(in io.Reader, errw io.Writer) (*HTTP, error) {
	sc := bufio.NewScanner(in)
	for {
		err := EnsureServe()
		if err == nil {
			return NewHTTP()
		}
		fmt.Fprintln(errw, "prism tui:", err)
		fmt.Fprint(errw, "Enter to retry, q to quit: ")
		if !sc.Scan() || strings.EqualFold(strings.TrimSpace(sc.Text()), "q") {
			return nil, errQuit
		}
	}
}
