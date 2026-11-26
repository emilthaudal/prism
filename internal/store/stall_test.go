package store

import (
	"path/filepath"
	"testing"
	"time"
)

// startDeskOwned starts a session the desk owns.
func startDeskOwned(t *testing.T, s *Store, id string) {
	t.Helper()
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "desk-parent", Background: true})
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: id, Background: true, DeskParent: "desk-parent"})
}

// EMI-426: a turn that ended while its background agents run must not take a note, since
// delivering stops the session and resumes it, which forked as-1701.
func TestANoteWaitsWhileTheSessionsSubagentsRunUnlessSentNow(t *testing.T) {
	s := newStore(t)
	startDeskOwned(t, s, "busy")
	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "busy", Prompt: "go", StartPrompt: "go"})
	hookSession(t, s, SessionEvent{Event: SessionAgentStart, ID: "busy", AgentID: "a1", AgentName: "review"})
	hookSession(t, s, SessionEvent{Event: SessionStop, ID: "busy", BackgroundTasks: 1})
	eq(t, "status", session(t, s, "busy").Status, "done")

	n, err := s.SendNote("busy", "desk", "heads-up", false, false)
	if err != nil || n.Deliver {
		t.Fatalf("sent while its agent runs: %+v, %v", n, err)
	}
	if notes, err := s.DeliverableNotes(); err != nil || len(notes) != 0 {
		t.Fatalf("deliverable while its agent runs: %+v, %v", notes, err)
	}
	if n, err = s.SendNote("busy", "desk", "now", false, true); err != nil || !n.Deliver || n.Text != "heads-up\n\nnow" {
		t.Fatalf("--now: %+v, %v", n, err)
	}

	hookSession(t, s, SessionEvent{Event: SessionAgentStop, ID: "busy", AgentID: "a1"})
	if n, err = s.SendNote("busy", "desk", "after", false, false); err != nil || !n.Deliver {
		t.Fatalf("after its agent stopped: %+v, %v", n, err)
	}
}

func TestAStallHoldsAUsageLimitedSessionAndFreesAnyOther(t *testing.T) {
	now := time.Date(2026, 11, 25, 15, 40, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := openAt(t, filepath.Join(t.TempDir(), "tracks.db"), clock)
	for _, id := range []string{"limited", "cut", "fresh"} {
		startDeskOwned(t, s, id)
		hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: id, Prompt: "go", StartPrompt: "go"})
	}
	hookSession(t, s, SessionEvent{Event: SessionAgentStart, ID: "limited", AgentID: "a1", AgentName: "impl"})
	before := now.Add(time.Second)
	now = now.Add(2 * time.Second)
	hookSession(t, s, SessionEvent{Event: SessionTool, ID: "fresh", Activity: "Bash: ls"})
	err := s.MarkStalled([]Stall{
		{ID: "limited", Hold: true, Why: UsageLimitWhy("17:00")},
		{ID: "cut", Why: "its turn ended without a Stop hook"},
		{ID: "fresh", Why: "its turn ended without a Stop hook"},
	}, before)
	if err != nil {
		t.Fatal(err)
	}
	x := session(t, s, "limited")
	eq(t, "limited", []string{x.Status, x.WaitingFor}, []string{"blocked", "usage limit: resets 17:00"})
	if !UsageLimited(x.WaitingFor) {
		t.Error("UsageLimited is false for its own text")
	}
	if agents, _ := s.Subagents(); len(agents) != 0 {
		t.Errorf("subagents outlive the cut turn: %+v", agents)
	}
	eq(t, "cut", session(t, s, "cut").Status, "error")

	// A lost Stop while subagents run is a wait on them, not a cut turn: no stall.
	startDeskOwned(t, s, "waiting")
	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "waiting", Prompt: "go", StartPrompt: "go"})
	hookSession(t, s, SessionEvent{Event: SessionAgentStart, ID: "waiting", AgentID: "w1", AgentName: "review"})
	now = now.Add(time.Second)
	if err := s.MarkStalled([]Stall{{ID: "waiting", Why: "its turn ended without a Stop hook"}}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	eq(t, "waiting on its subagent", session(t, s, "waiting").Status, "working")
	eq(t, "fresh, touched after the read", session(t, s, "fresh").Status, "working")

	if n, err := s.SendNote("limited", "desk", "continue", false, false); err != nil || n.Deliver {
		t.Fatalf("a note to a usage-limited session went out: %+v, %v", n, err)
	}
	if n, err := s.SendNote("cut", "desk", "continue", false, false); err != nil || !n.Deliver {
		t.Fatalf("a note to a cut-off session waits: %+v, %v", n, err)
	}
}
