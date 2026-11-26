package tracks

import (
	"path/filepath"
	"strings"
	"testing"
)

// bgAsked is the desk-started session cleanup with one ask on its card. Its track has no
// rex_session: the session runs as claude --bg.
func bgAsked(t *testing.T) (id, bin string) {
	t.Helper()
	id, bin = deskStarted(t)
	if out, code := cardIn("where: PR 12 is open\nasks:\nMerge PR 12? Recommend: yes\n", "card", "cleanup"); code != 0 {
		t.Fatalf("card: %d %s", code, out)
	}
	v, err := getView("cleanup")
	if err != nil || v.Track.RexSession != "" || len(v.Asks) != 1 {
		t.Fatalf("cleanup: %v %+v", err, v.Track)
	}
	return id, bin
}

// EMI-400: Emil's answer from the app (tracks decide) reaches a background session as a note,
// at once when it is idle, and the ask counts as answered.
func TestEmilsAnswerReachesAnIdleBackgroundSession(t *testing.T) {
	id, bin := bgAsked(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Opened PR 12."})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("REX_SESSION", "") // Emil answers from outside the desk's session
	if out, code := do("decide", "D1", "1"); code != 0 || !strings.HasPrefix(out, "answered D1") {
		t.Fatalf("decide: %d %q", code, out)
	}
	want := "Emil's answer to your ask 1, \"Merge PR 12?\": Yes.\n<end>\n"
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != want {
		t.Errorf("delivered %q\nwant      %q", n, want)
	}
	if n := notes(t, id); len(n) != 1 || n[0].From != "emil" || n[0].Held || !n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
	if v, _ := getView("cleanup"); v.Asks[0].AnsweredAt.IsZero() {
		t.Errorf("the ask was not stamped answered")
	}
}

// While the background session works, the answer is held and goes when it stops.
func TestAnAnswerToAWorkingBackgroundSessionIsHeld(t *testing.T) {
	id, bin := bgAsked(t)
	v, _ := getView("cleanup")
	msg := sendAnswer("cleanup", "yes, merge it", "", []int{1}, []int64{v.Asks[0].ID})
	if !msg.sent || msg.status != "Held for cleanup: delivered when it stops" {
		t.Fatalf("send: %+v", msg)
	}
	if exists(filepath.Join(bin, "claude.calls")) {
		t.Errorf("woke a working session")
	}
	if n := notes(t, id); len(n) != 1 || n[0].Text != "yes, merge it" || !n[0].Held {
		t.Errorf("notes %+v", n)
	}
}

// tracks desk answer reaches a background session too, as the desk's note.
func TestTheDeskAnswersABackgroundSession(t *testing.T) {
	id, bin := bgAsked(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Opened PR 12."})
	if out, code := do("desk", "answer", "cleanup", "1", "yes"); code != 0 || out != "answered ask 1 on cleanup\n" {
		t.Fatalf("desk answer: %d %q", code, out)
	}
	want := `[desk] answer to your ask 1 ("Merge PR 12? Recommend: yes"): yes` + "\n<end>\n"
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != want {
		t.Errorf("delivered %q\nwant      %q", n, want)
	}
	if n := notes(t, id); len(n) != 1 || n[0].From != "desk" || !n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
}
