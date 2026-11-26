package tracks

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFocusFailureStillCountsAsJumped(t *testing.T) {
	if !jumped("could not focus the agent's block: exit status 1") {
		t.Error("a selected session with a failed block focus must stop the round")
	}
	if jumped("could not switch Rex to the session: not found") {
		t.Error("a failed select is not a jump")
	}
}

func TestArchivedCardUsesTheCardsOwnName(t *testing.T) {
	setup(t)
	arch := func(file, name string) {
		foldBack(t)
		if err := writeAtomic(filepath.Join(archiveDir(), file), []byte("---\nname: "+name+"\n---\n## Outcome\nx\n")); err != nil {
			t.Fatal(err)
		}
	}
	arch("2026-11-16-pan-213.md", "pan-213")
	if got := archivedCard("pan"); got != "" {
		t.Errorf("track pan-213's archive counted as pan's: %q", got)
	}
	if got := archivedCard("pan-213"); got != "2026-11-16-pan-213.md" {
		t.Errorf("pan-213: %q", got)
	}
	arch("2026-11-17-pan-2.md", "pan")
	if got := archivedCard("pan"); got != "2026-11-17-pan-2.md" {
		t.Errorf("pan's second archive not found: %q", got)
	}
}

func TestOldPromptIsNotWorking(t *testing.T) {
	now := time.Now()
	evs := []Event{{At: now.Add(-3 * time.Hour), Event: "prompt", RexSession: "s"}}
	if got := liveState(evs, "s", now); got == "working" {
		t.Errorf("a prompt 3h ago with no stop reads as %q", got)
	}
	evs[0].At = now.Add(-5 * time.Minute)
	if got := liveState(evs, "s", now); got != "working" {
		t.Errorf("a fresh prompt reads as %q", got)
	}
}
