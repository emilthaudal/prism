package tracks

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// prism desk restart: Emil's is refused mid-turn, the desk's own waits for its turn to end, and
// the fresh desk starts without --resume, opens with the newest hand-off and then gets what queued.
func TestDeskRestartStartsAFreshDeskKeepingWhatQueued(t *testing.T) {
	_, bin := actsHome(t, "2: product code")
	fakeStreamClaude(t, bin)
	hold := filepath.Join(bin, "hold")
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	desk := newDeskRunner(io.Discard)
	runDesk(t, desk)
	eventually(t, "the first turn", func() bool { return len(stdinMessages(t, bin)) == 1 })
	eventually(t, "the desk's session", func() bool { desk.mu.Lock(); defer desk.mu.Unlock(); return desk.session != "" })

	var ae apiError
	if _, err := desk.restart(false); !errors.As(err, &ae) || ae.code != 409 {
		t.Fatalf("Emil's restart mid-turn: %v", err)
	}
	err := withBareStore(func(st *store.Store) error {
		if _, err := st.LogDesk("desk", "noted", "desk restart", "HAND-OFF 26 Nov 14:00: old one"); err != nil {
			return err
		}
		_, err := st.LogDesk("desk", "noted", "desk restart", "HAND-OFF 26 Nov 15:00: emi-432 waits on review")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := desk.restart(true); err != nil || !strings.Contains(msg, "once this turn ends") {
		t.Fatalf("the desk's own restart: %q %v", msg, err)
	}
	if _, err := desk.post("still there?"); err != nil {
		t.Fatal(err)
	}
	desk.event("solo done: PR 7 open")
	os.Remove(hold)

	eventually(t, "the fresh desk's queued turn", func() bool { return len(stdinMessages(t, bin)) == 3 })
	m := stdinMessages(t, bin)
	if !strings.HasPrefix(m[1], deskFirst+"\n\n") || !strings.Contains(m[1], "HAND-OFF 26 Nov 15:00: emi-432 waits on review") || strings.Contains(m[1], "old one") {
		t.Errorf("fresh desk's first message:\n%s", m[1])
	}
	if m[2] != "Emil: still there?\nEvents:\n- solo done: PR 7 open" {
		t.Errorf("queued after the restart:\n%s", m[2])
	}
	args, _ := os.ReadFile(filepath.Join(bin, "claude.args"))
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 || strings.Contains(string(args), "--resume") {
		t.Errorf("runs:\n%s", args)
	}
}

// A restart while the desk is idle ends its session at once, so a prism server started later
// starts fresh too.
func TestDeskRestartWhenIdleForgetsTheSession(t *testing.T) {
	_, bin := actsHome(t, "2: product code")
	fakeStreamClaude(t, bin)
	desk := newDeskRunner(io.Discard)
	runDesk(t, desk)
	eventually(t, "the desk idle", func() bool {
		s, _ := desk.state()
		desk.mu.Lock()
		defer desk.mu.Unlock()
		return s == "idle" && desk.session != ""
	})
	// The fresh claude names a new session; that one must become the desk's.
	const fresh = "ffffffff-0000-4a4a-8888-ffffffffffff"
	if err := os.WriteFile(filepath.Join(bin, "sid"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := desk.restart(false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second run", func() bool { return len(stdinMessages(t, bin)) == 2 })
	eventually(t, "the new session recorded as the desk's", func() bool {
		var id string
		withBareStore(func(st *store.Store) (err error) { id, err = st.DeskClaude(); return err })
		return id == fresh
	})
	if row, ok := deskSessionRow(t, fakeDeskSession); !ok || row.Host != "stopped" {
		t.Errorf("the old desk session still runs: %+v %v", row, ok)
	}
	if m := stdinMessages(t, bin); m[1] != deskFirst {
		t.Errorf("no hand-off, so just %q: %q", deskFirst, m[1])
	}
	args, _ := os.ReadFile(filepath.Join(bin, "claude.args"))
	if strings.Contains(string(args), "--resume") {
		t.Errorf("a restart resumed:\n%s", args)
	}
}

func TestEndDeskSessionStopsTheRowAndForgetsIt(t *testing.T) {
	actsHome(t, "2: product code")
	err := withBareStore(func(st *store.Store) error {
		if err := st.SetDeskSession(fakeDeskSession, "idle"); err != nil {
			return err
		}
		return st.EndDeskSession(fakeDeskSession)
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	withBareStore(func(st *store.Store) (err error) { id, err = st.DeskClaude(); return err })
	if id != "" {
		t.Errorf("desk session kept: %q", id)
	}
	if row, ok := deskSessionRow(t, fakeDeskSession); !ok || row.Host != "stopped" || row.Status != "done" {
		t.Errorf("old row: %+v %v", row, ok)
	}
}

// A desk past the turn or age threshold gets one line in its next batch, once.
func TestALongRunningDeskIsNudgedOnce(t *testing.T) {
	decisionHome(t, "alpha")
	old := deskLongTurns
	deskLongTurns = 2
	t.Cleanup(func() { deskLongTurns = old })
	var stdin bytes.Buffer
	desk := newDeskRunner(io.Discard)
	desk.stdin, desk.alive, desk.session, desk.since = nopCloser{&stdin}, true, fakeDeskSession, time.Now()

	turn := func(ev string) string {
		stdin.Reset()
		desk.event(ev)
		deskTurn(desk, "ok")
		return stdin.String()
	}
	if got := turn("one"); strings.Contains(got, "context is long") {
		t.Errorf("nudged at turn 1: %s", got)
	}
	turn("two")
	if got := turn("three"); !strings.Contains(got, "your context is long") || !strings.Contains(got, "hand off and restart") {
		t.Errorf("not nudged past the threshold: %s", got)
	}
	if got := turn("four"); strings.Contains(got, "context is long") {
		t.Errorf("nudged twice: %s", got)
	}

	desk.mu.Lock()
	desk.nudged, desk.turns, desk.since = false, 0, time.Now().Add(-deskLongAge-time.Minute)
	desk.mu.Unlock()
	if got := turn("five"); !strings.Contains(got, "12h old") {
		t.Errorf("not nudged past the age: %s", got)
	}
}
