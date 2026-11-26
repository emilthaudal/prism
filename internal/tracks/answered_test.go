package tracks

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func answeredCard(t *testing.T, needs string) {
	t.Helper()
	writeFile(t, cardPath("tr"), "---\nname: tr\nmode: focus\nrex_session: "+rsSess+"\nupdated: 2026-11-19T10:00:00Z\n---\n## Goal\nG\n\n## Needs me\n"+needs)
}

func lsTrackNamed(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, tr := range lsJSON(t).Tracks {
		if tr["name"] == name {
			return tr
		}
	}
	t.Fatalf("no track %s", name)
	return nil
}

func TestHookMarksAnsweredUntilStop(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "1. Pick one? Recommend: A.\n")

	if v, ok := lsTrackNamed(t, "tr")["answered_at"]; !ok || v != nil {
		t.Fatalf("answered_at before any prompt: %v (present %v)", v, ok)
	}

	// A system message is not Emil answering.
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"<task-notification>done</task-notification>","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered"] != "" {
		t.Fatalf("system prompt stamped answered: %q", c.Meta["answered"])
	}

	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 A","cwd":"/c"}`)
	c := loadCardMust(t, "tr")
	if c.Meta["answered"] == "" || c.mode() != "focus" || !metaTime(c, "updated").Equal(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("meta after prompt: %+v", c.Meta)
	}
	if v := lsTrackNamed(t, "tr")["answered_at"]; v != c.Meta["answered"] {
		t.Errorf("answered_at %v, want %q", v, c.Meta["answered"])
	}

	// The agent rewrites the card at the end of its turn: updated moves past answered.
	foldBack(t)
	text := regexp.MustCompile(`(?m)^updated: .*$`).ReplaceAllString(readFile(cardPath("tr")), "updated: 2999-01-01T00:00:00Z")
	writeFile(t, cardPath("tr"), text)
	if v := lsTrackNamed(t, "tr")["answered_at"]; v != nil {
		t.Errorf("answered_at after the agent's rewrite: %v", v)
	}

	hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered"] != "" {
		t.Errorf("Stop left answered: %q", c.Meta["answered"])
	}
	// The files keep the emptied line; a render leaves an empty stamp out.
	if n := strings.Count(readFile(cardPath("tr")), "answered:"); n != 1 && !(dbExists() && n == 0) {
		t.Errorf("%d answered lines", n)
	}
}

func TestAnsweredStampExpiresAndSessionStartClearsIt(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "1. Pick one?\n")
	stale := time.Now().Add(-answeredFor - time.Minute).UTC().Format(time.RFC3339)
	writeFile(t, cardPath("tr"), strings.Replace(readFile(cardPath("tr")), "updated:", "answered: "+stale+"\nupdated:", 1))
	if v := lsTrackNamed(t, "tr")["answered_at"]; v != nil {
		t.Errorf("a stamp older than %v still counts: %v", answeredFor, v)
	}
	hook(t, `{"hook_event_name":"SessionStart","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered"] != "" {
		t.Errorf("SessionStart left answered: %q", c.Meta["answered"])
	}
	if evs := readEvents(); len(evs) != 0 {
		t.Errorf("SessionStart logged events: %+v", evs)
	}
}

// A held cards lock costs the hook its card write, never the prompt event, and the hook gives
// up well inside Claude Code's 2-second limit.
func TestHookLogsThePromptWhileTheCardsAreLocked(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "1. Pick one?\n")
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		withCardsLock(func() error { close(held); <-release; return nil })
	}()
	<-held
	start := time.Now()
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 A","cwd":"/c","session_id":"abc-1"}`)
	took := time.Since(start)
	close(release)
	<-done
	if took > 1500*time.Millisecond {
		t.Errorf("the hook waited %v", took)
	}
	if evs := readEvents(); len(evs) != 1 || evs[0].Event != "prompt" || evs[0].Track != "tr" {
		t.Errorf("events %+v", evs)
	}
	if cardLockWait != 5*time.Second {
		t.Errorf("the hook left the lock wait at %v", cardLockWait)
	}
}

func TestHookAnsweredNeedsAsks(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "")
	before := readFile(cardPath("tr"))
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"status?","cwd":"/c"}`)
	hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
	if got := readFile(cardPath("tr")); got != before {
		t.Errorf("card without asks changed:\n%s", got)
	}
}
