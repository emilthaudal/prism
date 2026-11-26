package tracks

import (
	"os"
	"strings"
	"testing"
)

// EMI-296: close refuses while asks remain, and carries what is left to inbox.md.
func TestCloseKeepsLeftovers(t *testing.T) {
	actsHome(t, "2: product code", versionAsk, "Close this track? Recommend: yes")
	if out, code := do("card", "solo", "later:\nRotate the staging password\n"); code == 0 && out == "" {
		t.Log("card has no stdin form here")
	}
	out, code := do("close", "solo")
	if code == 0 || !strings.Contains(out, "Which template-ci orb version?") || strings.Contains(out, "Close this track?") {
		t.Fatalf("close without --yes: exit %d, %q", code, out)
	}
	if v, err := withView("solo"); err != nil || v.Closed {
		t.Fatalf("solo closed despite the refusal: %v", err)
	}
	if _, code := do("close", "--yes", "solo"); code != 0 {
		t.Fatal("close --yes failed")
	}
	b, _ := os.ReadFile(inboxPath())
	if !strings.Contains(string(b), "solo: unanswered ask: Which template-ci orb version?") || strings.Contains(string(b), "Close this track?") {
		t.Errorf("inbox.md = %q", b)
	}
	if got := carriedOver(); got != strings.Count(string(b), inboxOpen) || got == 0 {
		t.Errorf("carriedOver = %d", got)
	}
	if out, code := do("archive", "--leftovers"); code != 0 || !strings.Contains(out, "solo  ask: Which template-ci orb version?") {
		t.Errorf("archive --leftovers: exit %d, %q", code, out)
	}
}
