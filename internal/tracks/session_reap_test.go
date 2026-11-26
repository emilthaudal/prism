package tracks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	reapLimited = "11111111-aaaa-4a4a-8888-aaaaaaaaaaaa"
	reapCut     = "22222222-aaaa-4a4a-8888-aaaaaaaaaaaa"
	reapBusy    = "33333333-aaaa-4a4a-8888-aaaaaaaaaaaa"
	reapRecent  = "44444444-aaaa-4a4a-8888-aaaaaaaaaaaa"
)

// writeTranscript puts a transcript for id under a fake ~/.claude/projects.
func writeTranscript(t *testing.T, home, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// usageLimitRecord is the synthetic record Claude Code writes when the usage limit cuts a turn.
func usageLimitRecord(resets time.Time) string {
	return `{"type":"assistant","apiError":"usage_limit_reached","error":"rate_limit","quotaLimits":{"status":"rejected","resetsAt":` +
		strconv.FormatInt(resets.Unix(), 10) + `},"message":{"model":"<synthetic>","content":[{"type":"text","text":"You've hit your 5-hour limit"}]}}`
}

func TestStalledSessionsTellAUsageLimitFromAnyOtherCutTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 11, 25, 15, 40, 0, 0, time.UTC)
	writeTranscript(t, home, reapLimited, `{"type":"user","message":{"content":"go"}}`, usageLimitRecord(now.Add(time.Hour)), `{"type":"system","subtype":"informational"}`)
	writeTranscript(t, home, reapCut, `{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`)
	old := now.Add(-10 * time.Minute)
	list := []store.Session{
		{ID: reapLimited, Host: "background", Status: "working", UpdatedAt: old},
		{ID: reapCut, Host: "background", Status: "working", UpdatedAt: old},
		{ID: reapBusy, Host: "background", Status: "working", UpdatedAt: old},
		{ID: reapRecent, Host: "background", Status: "working", UpdatedAt: now.Add(-time.Minute)},
	}
	agents := map[string]claudeAgent{
		reapLimited: {SessionID: reapLimited, PID: 1, Status: "idle"},
		reapCut:     {SessionID: reapCut, PID: 2, Status: "idle"},
		reapBusy:    {SessionID: reapBusy, PID: 3, Status: "busy"},
		reapRecent:  {SessionID: reapRecent, PID: 4, Status: "idle"},
	}
	got := stalledSessions(list, agents, now)
	if len(got) != 2 {
		t.Fatalf("stalls %+v", got)
	}
	if got[0].ID != reapLimited || !got[0].Hold || !store.UsageLimited(got[0].Why) {
		t.Errorf("usage limit: %+v", got[0])
	}
	if got[1].ID != reapCut || got[1].Hold || store.UsageLimited(got[1].Why) {
		t.Errorf("cut turn: %+v", got[1])
	}

	// Once the limit resets, the held session turns error, so its notes go out and resume it.
	list = []store.Session{{ID: reapLimited, Host: "background", Status: "blocked", WaitingFor: store.UsageLimitWhy("16:40"), UpdatedAt: old}}
	got = stalledSessions(list, agents, now.Add(2*time.Hour))
	if len(got) != 1 || got[0].Hold || store.UsageLimited(got[0].Why) {
		t.Errorf("after the reset: %+v", got)
	}
	// Before it resets, a held session is left as it is.
	if got = stalledSessions(list, agents, now); len(got) != 0 {
		t.Errorf("before the reset: %+v", got)
	}
}

// EMI-426 (d): claude --resume of a session whose process lives on starts a copy, which got
// as-1701's note. The delivery stops the copy and keeps the note held.
func TestADeliveryThatResumesACopyStopsItAndFails(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
d=` + shellQuote(bin) + `
case "$1" in
agents) echo '[]'; exit 0;;
stop) echo "stop $2" >> "$d/calls"; exit 0;;
esac
echo "Resuming…"
echo "backgrounded · fork0001 · copy"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := deliverNote(store.Session{ID: reapCut, Host: "background"}, "note")
	if err == nil || !strings.Contains(err.Error(), "copy fork0001") {
		t.Fatalf("err %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(bin, "calls"))
	if got := string(b); got != "stop 22222222\nstop fork0001\n" {
		t.Errorf("calls %q", got)
	}
}

// A resume that names the session's own short id is the session itself: never stopped.
func TestADeliveryThatResumesTheSessionItselfStopsNothingAfter(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
d=` + shellQuote(bin) + `
case "$1" in
agents) echo '[]'; exit 0;;
stop) echo "stop $2" >> "$d/calls"; exit 0;;
esac
echo "backgrounded · 22222222 · cut"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := deliverNote(store.Session{ID: reapCut, Host: "background"}, "note"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(bin, "calls"))
	if got := string(b); got != "stop 22222222\n" {
		t.Errorf("calls %q", got)
	}
}

func TestADeliveryWaitsForTheStoppedProcessAndGivesUpIfItLivesOn(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
d=` + shellQuote(bin) + `
case "$1" in
agents) echo '[{"sessionId":"` + reapCut + `","pid":42,"status":"busy"}]'; exit 0;;
stop) exit 0;;
esac
echo resumed >> "$d/calls"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := exitWait
	exitWait = 300 * time.Millisecond
	defer func() { exitWait = old }()
	err := deliverNote(store.Session{ID: reapCut, Host: "background"}, "note")
	if err == nil || !strings.Contains(err.Error(), "still runs") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "calls")); err == nil {
		t.Error("resumed a session whose process still runs")
	}
}
