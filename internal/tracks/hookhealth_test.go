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

	"github.com/charmbracelet/x/ansi"
)

func runHookCmd(t *testing.T, input string) int {
	t.Helper()
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	var out, errb bytes.Buffer
	return Run([]string{"hook"}, strings.NewReader(input), &out, &errb)
}

func TestHookRecordsSuccess(t *testing.T) {
	setup(t)
	if code := runHookCmd(t, `{"hook_event_name":"Stop"}`); code != 0 {
		t.Fatalf("exit %d", code)
	}
	r, ok := readHookRecord(hookOKPath())
	if !ok || r.Version == "" || time.Since(r.At) > time.Minute {
		t.Errorf("hook.ok = %+v, %v", r, ok)
	}
	if _, err := os.Stat(hookErrPath()); err == nil {
		t.Error("hook.err written on success")
	}
	// An event the hook does not use is not a run worth recording.
	os.Remove(hookOKPath())
	runHookCmd(t, `{"hook_event_name":"PreToolUse"}`)
	if _, err := os.Stat(hookOKPath()); err == nil {
		t.Error("hook.ok written for an ignored event")
	}
}

func TestHookRecordsBadInput(t *testing.T) {
	setup(t)
	if code := runHookCmd(t, `not json`); code != 0 {
		t.Fatalf("exit %d", code)
	}
	r, ok := readHookRecord(hookErrPath())
	if !ok || !strings.Contains(r.Error, "bad hook input") || r.Version == "" {
		t.Errorf("hook.err = %+v, %v", r, ok)
	}
}

func TestHookPanicExitsZeroAndWritesErr(t *testing.T) {
	setup(t)
	old := processHook
	processHook = func(io.Reader) (bool, error) { panic("boom") }
	defer func() { processHook = old }()
	if code := runHookCmd(t, `{"hook_event_name":"Stop"}`); code != 0 {
		t.Fatalf("exit %d after a panic", code)
	}
	r, ok := readHookRecord(hookErrPath())
	if !ok || !strings.Contains(r.Error, "panic: boom") {
		t.Errorf("hook.err = %+v, %v", r, ok)
	}
}

func TestHookUnwritableStateDirStillExitsZero(t *testing.T) {
	th, _ := setup(t)
	if err := os.WriteFile(th+"/blocker", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACKS_HOME", th+"/blocker/sub")
	if code := runHookCmd(t, `not json`); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestHookFailureOnlyWhileNewerThanOK(t *testing.T) {
	setup(t)
	if _, ok := hookFailure(); ok {
		t.Fatal("failure with no files")
	}
	recordHook(io.ErrUnexpectedEOF)
	if r, ok := hookFailure(); !ok || r.Error != io.ErrUnexpectedEOF.Error() {
		t.Errorf("failure = %+v, %v", r, ok)
	}
	time.Sleep(2 * time.Millisecond)
	recordHook(nil)
	if _, ok := hookFailure(); ok {
		t.Error("a later success should clear the failure")
	}
}

func hookPane(t *testing.T, mode string, state string, lastEvent time.Duration) *paneModel {
	t.Helper()
	th, _ := setup(t)
	card(t, th, "a", mode, "sess-a", "w", "")
	now := time.Now()
	if lastEvent > 0 {
		if err := appendEvent(Event{At: now.Add(-lastEvent), Event: "prompt", RexSession: "sess-a", Track: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	m := newPane()
	m.now = now
	if state != "" {
		m.rex.states["sess-a"] = agentState{State: state}
	}
	return m
}

func TestHeaderHookSilent(t *testing.T) {
	m := hookPane(t, "focus", "working", 5*time.Hour)
	if h := m.header(); !strings.Contains(h, warnSt.Render("hook silent for 5h")) {
		t.Errorf("silent hook: %q", h)
	}
}

func TestHeaderHookFailingNamesReason(t *testing.T) {
	m := hookPane(t, "focus", "working", 5*time.Hour)
	recordHook(io.ErrUnexpectedEOF)
	m.reload()
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook failing: unexpected EOF") {
		t.Errorf("failing hook: %q", h)
	}
}

func TestHeaderHookQuietInTheNormalCase(t *testing.T) {
	for name, m := range map[string]*paneModel{
		"recent event":     hookPane(t, "focus", "working", 20*time.Minute),
		"idle agent":       hookPane(t, "focus", "idle", 5*time.Hour),
		"no Rex state":     hookPane(t, "focus", "", 5*time.Hour),
		"background track": hookPane(t, "background", "working", 5*time.Hour),
		"no event yet":     hookPane(t, "focus", "working", 0),
	} {
		if h := ansi.Strip(m.header()); strings.Contains(h, "hook") {
			t.Errorf("%s: %q", name, h)
		}
	}
	// A failure on record says nothing while no focus track is working.
	m := hookPane(t, "focus", "idle", 20*time.Minute)
	recordHook(io.ErrUnexpectedEOF)
	m.reload()
	if h := ansi.Strip(m.header()); strings.Contains(h, "hook") {
		t.Errorf("failure shown with nothing working:%q", h)
	}
}

// A missing binary cannot write hook.err, so the header sees only silence: no files, no
// events for hours, and Rex still showing the agent working.
func TestHeaderHookMissingBinaryShowsSilence(t *testing.T) {
	m := hookPane(t, "focus", "working", 7*time.Hour)
	for _, p := range []string{hookOKPath(), hookErrPath()} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s exists", p)
		}
	}
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook silent for 7h") {
		t.Errorf("missing binary: %q", h)
	}
}

func TestHeaderHookSilenceSurvivesOtherWritersEvents(t *testing.T) {
	m := hookPane(t, "focus", "working", 5*time.Hour)
	for _, ev := range []string{"open", "answer", "need"} {
		if err := appendEvent(Event{At: time.Now(), Event: ev, RexSession: "sess-a", Track: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	m.reload()
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook silent for 5h") {
		t.Errorf("open/answer/need events cleared the warning: %q", h)
	}
}

func TestHeaderHookFailingShowsAtOnceWhenRecent(t *testing.T) {
	m := hookPane(t, "focus", "working", 20*time.Minute)
	recordHook(io.ErrUnexpectedEOF)
	m.reload()
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook failing: unexpected EOF") {
		t.Errorf("recent failure: %q", h)
	}
}

func TestHeaderHookStaleFailureFallsBackToSilence(t *testing.T) {
	m := hookPane(t, "focus", "working", 7*time.Hour)
	b := `{"at":"` + time.Now().Add(-6*time.Hour).UTC().Format(time.RFC3339) + `","version":"x","error":"lock timeout"}`
	if err := os.WriteFile(hookErrPath(), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	m.reload()
	h := ansi.Strip(m.header())
	if strings.Contains(h, "failing") || !strings.Contains(h, "hook silent for 7h") {
		t.Errorf("stale failure: %q", h)
	}
}

func TestHeaderHookQuietAfterStopWhenRexIsStale(t *testing.T) {
	m := hookPane(t, "focus", "working", 5*time.Hour)
	if err := appendEvent(Event{At: time.Now().Add(-4 * time.Hour), Event: "stop", RexSession: "sess-a", Track: "a"}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if h := ansi.Strip(m.header()); strings.Contains(h, "hook") {
		t.Errorf("a stopped turn shown as silent: %q", h)
	}
	if err := appendEvent(Event{At: time.Now().Add(-3*time.Hour - time.Minute), Event: "prompt", RexSession: "sess-a", Track: "a"}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook silent for 3h") {
		t.Errorf("a new turn that never stopped: %q", h)
	}
}

// The binary usually breaks between turns: the last event is a stop, and the next prompt is
// never logged while Rex reports the agent working again.
func TestHeaderHookBrokenAfterAStop(t *testing.T) {
	m := hookPane(t, "focus", "", 0)
	if err := appendEvent(Event{At: m.now.Add(-5 * time.Hour), Event: "stop", RexSession: "sess-a", Track: "a"}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.rex.states["sess-a"] = agentState{State: "working", At: time.Now().Add(-time.Hour)}
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook silent for 5h") {
		t.Errorf("missing binary after a stop: %q", h)
	}
	recordHook(io.ErrUnexpectedEOF)
	m.reload()
	m.rex.states["sess-a"] = agentState{State: "working", At: time.Now().Add(-time.Hour)}
	if h := ansi.Strip(m.header()); !strings.Contains(h, "hook failing: unexpected EOF") {
		t.Errorf("failing on every call after a stop: %q", h)
	}
}

func TestHeaderHookLongReasonIsCutNotDropped(t *testing.T) {
	m := hookPane(t, "focus", "working", 5*time.Hour)
	recordHook(errors.New("the cards stayed locked for 500ms: /Users/someone/.local/state/tracks/cards.lock"))
	m.reload()
	h := ansi.Strip(m.header())
	if !strings.Contains(h, "hook failing: the cards") || !strings.Contains(h, "…") {
		t.Errorf("long reason at width %d: %q", m.w, h)
	}
	if w := ansi.StringWidth(h); w > m.w {
		t.Errorf("header %d wide, pane %d", w, m.w)
	}
}

func TestWriteAtomicRemovesTempWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/hook.ok", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(dir+"/hook.ok", []byte("x")); err == nil {
		t.Fatal("rename over a directory succeeded")
	}
	left, _ := filepath.Glob(dir + "/.tmp-*")
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestBuildIDIsNotEmpty(t *testing.T) {
	if buildID() == "" {
		t.Error("empty build id")
	}
}
