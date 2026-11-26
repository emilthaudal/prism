package tracks

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func plain(m *paneModel, w, h int) string {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return ansi.Strip(m.render())
}

func key(m *paneModel, k string) {
	msg := tea.KeyPressMsg{Text: k}
	switch k {
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	m.Update(msg)
}

func TestSetModeKeepsEverythingElse(t *testing.T) {
	th, _ := setup(t)
	noImport(t) // line surgery on the card text; the store renders the card whole
	orig := "---\nname: x\nmode: focus        # focus | background\ndir: /a # b\nwaiting_on:   PR #4\n---\n## Goal\nmode: not me\n"
	if err := writeAtomic(cardPath("x"), []byte(orig)); err != nil {
		t.Fatal(err)
	}
	if err := setMode("x", "waiting"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cardPath("x"))
	want := strings.Replace(orig, "mode: focus ", "mode: waiting ", 1)
	if string(b) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", b, want)
	}
	if setMode("missing", "focus") == nil {
		t.Error("missing card should error")
	}
	_ = th
}

func TestSetModeKeepsCRLF(t *testing.T) {
	setup(t)
	noImport(t) // line surgery on the card text; the store renders the card whole
	for _, line := range []string{"mode: focus\r\n", "mode: focus   # f | b\r\n"} {
		orig := "---\r\nname: x\r\n" + line + "---\r\n## Goal\r\n"
		if err := writeAtomic(cardPath("x"), []byte(orig)); err != nil {
			t.Fatal(err)
		}
		if err := setMode("x", "waiting"); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(cardPath("x"))
		if want := strings.Replace(orig, "mode: focus", "mode: waiting", 1); string(b) != want {
			t.Fatalf("got %q want %q", b, want)
		}
	}
}

func TestGroupingOrder(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "w1", "waiting", "", "w", "")
	card(t, th, "b1", "background", "", "b", "")
	card(t, th, "b2", "background", "", "b", "1. q")
	card(t, th, "f1", "focus", "", "f", "")
	card(t, th, "pk", "parked", "", "p", "")
	var names []string
	for _, c := range orderedCards(loadCards()) {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, " "); got != "f1 b2 b1 w1 pk" {
		t.Fatalf("order %s", got)
	}
}

func TestParkedShownButNeverNeedsYou(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "pk", "parked", "", "p", "1. q? Recommend: y")
	m := newPane()
	out := plain(m, 100, 14)
	if !strings.Contains(out, "PARKED") || !strings.Contains(out, "pk") {
		t.Errorf("parked card not listed:\n%s", out)
	}
	if _, needs := m.counts(); needs != 0 || forYouHeader.MatchString(out) {
		t.Errorf("parked asks counted as need you (%d):\n%s", needs, out)
	}
}

func TestViewNormal(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "alpha", "focus", "s1", "Pane built.", "1. Merge? Recommend: yes.")
	card(t, th, "beta", "waiting", "", "w", "")
	foldBack(t)
	os.WriteFile(cardPath("beta"), []byte("---\nname: beta\nmode: waiting\nwaiting_on: PR #4512 approved\n---\n"), 0o644)
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", RexSession: "s1"})
	m := newPane()
	m.now = time.Now()
	m.Update(sinceMsg{name: "alpha", lines: []string{"a1b2c3 Add pane"}, at: time.Now().Add(-3*time.Hour - time.Minute)})
	out := plain(m, 100, 24) // the Needs you band takes 4 lines here, the detail needs the 17 it had
	for _, s := range []string{"0 for you", "FOCUS", "WAITING", "▌alpha", "● working", "PR #4512 approved", "G alpha", "GOAL", "Pane built.", "Needs me", "Since you last looked · 3h", "a1b2c3 Add pane", "enter open"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	if forYouHeader.MatchString(out) {
		t.Errorf("asks on a working agent do not need you:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Errorf("height %d", len(lines))
	}
	for _, l := range lines {
		if len([]rune(l)) > 100 {
			t.Errorf("over width: %q", l)
		}
	}
}

func TestViewEmptyAndNarrow(t *testing.T) {
	th, _ := setup(t)
	out := plain(newPane(), 100, 12)
	if !strings.Contains(out, "No tracks yet") || !strings.Contains(out, "/track NAME") {
		t.Errorf("empty:\n%s", out)
	}
	card(t, th, "alpha", "focus", "s1", "Pane built.", "")
	out = plain(newPane(), 40, 12)
	if strings.Contains(out, "│") || strings.Contains(out, "Where it is") || !strings.Contains(out, "alpha") {
		t.Errorf("narrow:\n%s", out)
	}
	if !strings.Contains(out, keysShort) {
		t.Errorf("short keys missing:\n%s", out)
	}
}

func TestFocusWarning(t *testing.T) {
	th, _ := setup(t)
	for _, n := range []string{"a", "b", "c"} {
		card(t, th, n, "focus", "", "w", "")
	}
	if out := plain(newPane(), 100, 14); !strings.Contains(out, "3, plan says 2") {
		t.Errorf("no warning:\n%s", out)
	}
}

func TestKeysMoveRegroupAndEnter(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", "sa", "w", "")
	card(t, th, "b", "background", "sb", "w", "")
	m := newPane()
	key(m, "down")
	if m.cur != "b" {
		t.Fatalf("cursor %s", m.cur)
	}
	key(m, "f")
	b, _ := os.ReadFile(cardPath("b"))
	setInPlace := strings.Contains(string(b), "mode: focus   # c") || dbExists() && loadCardMust(t, "b").mode() == "focus"
	if !setInPlace || m.cur != "b" {
		t.Errorf("mode not set or cursor lost: %q cur=%s", b, m.cur)
	}
	key(m, "p")
	if m.cur != "b" || m.cards[len(m.cards)-1].Name != "b" {
		t.Errorf("parked card left the list or lost the cursor: cur=%s", m.cur)
	}
	if out := plain(m, 100, 14); !strings.Contains(out, "PARKED") {
		t.Errorf("no PARKED group:\n%s", out)
	}
	key(m, "up")
	if msg := openCmd(m.selected(), nil)(); !strings.Contains(string(msg.(statusMsg)), "block not known") {
		t.Errorf("status without a block %q", msg)
	}
	evs := []Event{{Event: "prompt", RexSession: "sa", RexBlock: "blk1"}, {Event: "open", RexSession: "sa", RexBlock: "pane"}}
	if msg := openCmd(m.selected(), evs)(); msg != statusMsg("") {
		t.Errorf("status %q", msg)
	}
	calls, _ := os.ReadFile(bin + "/rex.calls")
	if !strings.Contains(string(calls), "session_id=session:x-sa") || !strings.Contains(string(calls), "block focus -s session:x-sa blk1") {
		t.Errorf("rex calls: %s", calls)
	}
	var opens, modes []Event
	for _, e := range readEvents() {
		switch e.Event {
		case "open":
			opens = append(opens, e)
		case "mode":
			modes = append(modes, e)
		}
	}
	if len(opens) != 2 || opens[1].Track != "a" {
		t.Errorf("open events %+v", opens)
	}
	if len(modes) != 2 || modes[0].Track != "b" || modes[0].Kind != "focus" || modes[1].Kind != "parked" {
		t.Errorf("mode events %+v", modes)
	}
}

func TestFloatClosesOnJumpAndEsc(t *testing.T) {
	setup(t)
	m := newPane()
	if _, cmd := m.Update(statusMsg("")); cmd != nil {
		t.Error("docked pane quit after a jump")
	}
	m.float = true
	if _, cmd := m.Update(statusMsg("could not focus")); cmd != nil || m.status == "" {
		t.Error("float closed on a failed jump")
	}
	if _, cmd := m.Update(statusMsg("")); cmd == nil {
		t.Error("float stayed open after a jump")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd == nil {
		t.Error("esc did not close the float")
	}
	if !strings.Contains(plain(m, 100, 12), "esc close") {
		t.Error("footer does not say esc closes")
	}
}

func TestEnterKeepsSinceUntilCursorMoves(t *testing.T) {
	setup(t)
	m := newPane()
	m.since["a"] = sinceInfo{lines: []string{"c1 commit"}, at: time.Now()}
	m.hold = "a"
	m.Update(sinceMsg{name: "a", at: time.Now()})
	if len(m.since["a"].lines) != 1 {
		t.Error("since blanked after enter")
	}
	m.hold = ""
	m.Update(sinceMsg{name: "a", at: time.Now()})
	if len(m.since["a"].lines) != 0 {
		t.Error("since not refreshed once released")
	}
}

func TestOpenFailureShowsStatusAndRefreshKeepsCursor(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	card(t, th, "b", "focus", "sb", "w", "")
	os.WriteFile(bin+"/rex", []byte("#!/bin/sh\nexit 1\n"), 0o755)
	m := newPane()
	key(m, "down")
	m.Update(openCmd(m.selected(), nil)())
	if !strings.Contains(plain(m, 100, 12), "could not switch Rex") {
		t.Error("status missing")
	}
	card(t, th, "0new", "focus", "", "w", "")
	m.stamp = ""
	m.Update(tickMsg{})
	if m.cur != "b" {
		t.Errorf("cursor moved to %s", m.cur)
	}
}
