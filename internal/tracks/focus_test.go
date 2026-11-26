package tracks

import (
	"strings"
	"testing"
)

// focusCards makes two focus tracks, a, then b, and a background track c.
func focusCards(t *testing.T) {
	t.Helper()
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	card(t, th, "b", "focus", "", "w", "")
	card(t, th, "c", "background", "", "w", "")
}

func TestAThirdFocusTrackIsRefusedOnTheCLIAndNamesTheTwo(t *testing.T) {
	focusCards(t)
	out, code := do("mode", "c", "focus")
	if code == 0 || !strings.Contains(out, "a and b") || loadCardMust(t, "c").mode() != "background" {
		t.Fatalf("third focus: %d %q, mode %q", code, out, loadCardMust(t, "c").mode())
	}
	if out, code := do("mode", "a", "focus"); code != 0 {
		t.Errorf("a track already in focus was refused: %d %q", code, out)
	}
	if out, code := do("mode", "c", "focus", "--swap", "c"); code == 0 || !strings.Contains(out, "not in focus") {
		t.Errorf("a swap with a track outside focus: %d %q", code, out)
	}
	if out, code := do("mode", "c", "focus", "--swap", "a"); code != 0 {
		t.Fatalf("swap: %d %q", code, out)
	}
	if got := [3]string{loadCardMust(t, "a").mode(), loadCardMust(t, "b").mode(), loadCardMust(t, "c").mode()}; got != [3]string{"background", "focus", "focus"} {
		t.Errorf("after the swap a, b, c are %v", got)
	}
}

func TestStartInFocusIsRefusedWhenFocusIsFullAndMakesNothing(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	// startHome's two focus tracks, boss and plain, fill focus.
	out, code := do("start", "work on d", "--dir", th, "--name", "d", "--mode", "focus")
	if code == 0 || !strings.Contains(out, "focus is full with boss and plain") {
		t.Fatalf("start in full focus: %d %q", code, out)
	}
	if storeNameTaken("d") {
		t.Error("the refused start made a track")
	}
}

func TestThePaneAsksWhichFocusTrackMovesToBackground(t *testing.T) {
	focusCards(t)
	m := newPane()
	selectTrack(t, m, "c")
	press(m, "f")
	if m.swap == nil || loadCardMust(t, "c").mode() != "background" {
		t.Fatalf("no swap question: %+v", m.swap)
	}
	if f := m.footer(); !strings.Contains(f, "Focus is full") || !strings.Contains(f, "a (older)") || !strings.Contains(f, "b") {
		t.Errorf("footer %q", f)
	}
	press(m, "esc")
	if m.swap != nil || loadCardMust(t, "c").mode() != "background" || loadCardMust(t, "a").mode() != "focus" {
		t.Fatal("esc changed something")
	}
	press(m, "f")
	press(m, "2")
	if got := [3]string{loadCardMust(t, "a").mode(), loadCardMust(t, "b").mode(), loadCardMust(t, "c").mode()}; got != [3]string{"focus", "background", "focus"} {
		t.Errorf("after picking b, a, b, c are %v", got)
	}
}

func TestServeAThirdFocusTrackAnswers409WithTheFocusTracks(t *testing.T) {
	_, c := liveHome(t)
	snap := c.snapshot()
	var focus []string
	var other string
	for _, tr := range snap.Tracks {
		if tr.Mode == "focus" {
			focus = append(focus, tr.Name)
		} else if other == "" {
			other = tr.ID
		}
	}
	if len(focus) < maxFocus || other == "" {
		t.Skipf("the fixture has %d focus tracks", len(focus))
	}
	code, v := c.post("/v1/tracks/"+other+"/mode", `{"mode":"focus"}`)
	if code != 409 || v["focus"] == nil {
		t.Fatalf("third focus: %d %v", code, v)
	}
	if code, _ := c.post("/v1/tracks/"+other+"/mode", `{"mode":"focus","swap":"`+focus[0]+`"}`); code != 200 {
		t.Errorf("swap: %d", code)
	}
}
