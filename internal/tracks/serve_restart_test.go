package tracks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeBuildLine(t *testing.T) {
	cases := []struct {
		si      serveInfo
		running bool
		want    string
	}{
		{serveInfo{}, false, "serve: not running"},
		{serveInfo{Build: "a"}, true, "serve: running the installed build (a)"},
		{serveInfo{}, true, "serve: running, build unknown (started before builds were recorded); installed a"},
	}
	for _, c := range cases {
		if got := serveBuildLine(c.si, c.running, "a"); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	if got := serveBuildLine(serveInfo{Build: "old"}, true, "new"); !strings.Contains(got, "build old, installed new") {
		t.Errorf("differing builds: %q", got)
	}
}

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func watchAsync(path string, idle func() bool, maxWait time.Duration) (context.CancelFunc, <-chan bool) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- watchBinary(ctx, path, 10*time.Millisecond, idle, maxWait) }()
	return cancel, done
}

func TestWatchBinaryFiresOnANewBuild(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tracks")
	writeExe(t, exe, "build 1")
	cancel, done := watchAsync(exe, func() bool { return true }, time.Minute)
	defer cancel()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("fired before the binary changed")
	default:
	}
	writeExe(t, exe, "build 2, longer")
	select {
	case got := <-done:
		if !got {
			t.Fatal("reported false on a new build")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not fire on a new build")
	}
}

func TestWatchBinaryWaitsForTheDesksTurn(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tracks")
	writeExe(t, exe, "build 1")
	var idle atomic.Bool
	cancel, done := watchAsync(exe, idle.Load, time.Minute)
	defer cancel()
	writeExe(t, exe, "build 2, longer")
	select {
	case <-done:
		t.Fatal("restarted while the desk was in a turn")
	case <-time.After(100 * time.Millisecond):
	}
	idle.Store(true)
	select {
	case got := <-done:
		if !got {
			t.Fatal("reported false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not fire once the desk's turn ended")
	}
}

func TestWatchBinaryGivesUpWaitingAfterMaxWait(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tracks")
	writeExe(t, exe, "build 1")
	cancel, done := watchAsync(exe, func() bool { return false }, 50*time.Millisecond)
	defer cancel()
	writeExe(t, exe, "build 2, longer")
	select {
	case got := <-done:
		if !got {
			t.Fatal("reported false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waited past maxWait")
	}
}

func TestWatchBinaryIgnoresAMissingFile(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tracks")
	writeExe(t, exe, "build 1")
	cancel, done := watchAsync(exe, func() bool { return true }, time.Minute)
	time.Sleep(50 * time.Millisecond) // let the watch take its first stamp
	os.Remove(exe)
	select {
	case <-done:
		t.Fatal("fired on a removed binary")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if <-done {
		t.Fatal("reported true after cancel")
	}
}
