package tracks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func checkedPane(t *testing.T, mode, check string, ranAgo time.Duration, ran bool) *paneModel {
	t.Helper()
	th, _ := setup(t)
	putCard(t, th, "w", detailCard("w", mode, "", "check: "+check+"\n", "G", "Waits.", ""))
	if ran {
		if err := saveChecks(map[string]checkState{"w: " + check: {Answer: true, RanAt: time.Now().Add(-ranAgo)}}); err != nil {
			t.Fatal(err)
		}
	}
	m := newPane()
	m.now = time.Now()
	return m
}

// forYouHeader matches the header's count of what needs Emil when it is not zero.
var forYouHeader = regexp.MustCompile(`◆ \d+ for you`)

func TestHeaderLeadsWithWhoNeedsYou(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "1. q")
	card(t, th, "b", "focus", "", "w", "")
	m := newPane()
	h := m.header()
	if !strings.Contains(h, warnSt.Bold(true).Render("◆ 1 for you")) {
		t.Errorf("need you is not warning bold: %q", h)
	}
	if strings.Contains(ansi.Strip(h), "focus") {
		t.Errorf("the focus count is back: %q", ansi.Strip(h))
	}
	card(t, th, "a", "focus", "", "w", "")
	m.reload()
	if h := m.header(); !strings.Contains(h, dimSt.Render("0 for you")) || strings.Contains(h, "◆") {
		t.Errorf("nobody needs you: %q", h)
	}
}

func TestHeaderAndFooterShareTheListsLeftEdge(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	lines := strings.Split(plain(newPane(), 120, 14), "\n")
	col := func(l, s string) int { return strings.Index(l, s) }
	if h, g := col(lines[0], "Desk"), col(lines[3], "FOCUS"); h != g || h != 1 {
		t.Errorf("Desk at %d, FOCUS at %d:\n%s", h, g, strings.Join(lines, "\n"))
	}
	foot := lines[len(lines)-1]
	if f := len(foot) - len(strings.TrimLeft(foot, " ")); f != 1 {
		t.Errorf("footer starts at column %d: %q", f, foot)
	}
}

func TestHeaderChecksAge(t *testing.T) {
	m := checkedPane(t, "waiting", "pr 5 approved", 4*time.Minute, true)
	if h := m.header(); !strings.Contains(h, dimSt.Render("checks 4m ago")) {
		t.Errorf("fresh checks: %q", h)
	}
	m = checkedPane(t, "waiting", "pr 5 approved", 45*time.Minute, true)
	if h := m.header(); !strings.Contains(h, warnSt.Render("checks 45m ago")) {
		t.Errorf("stale checks are not yellow: %q", h)
	}
	m = checkedPane(t, "waiting", "pr 5 approved", 0, false)
	if h := m.header(); !strings.Contains(h, dimSt.Render("checks not run yet")) {
		t.Errorf("checks that never ran: %q", h)
	}
	// Only a parked card has a check: nothing to be late for.
	m = checkedPane(t, "parked", "pr 5 approved", 45*time.Minute, true)
	if h := ansi.Strip(m.header()); strings.Contains(h, "checks") {
		t.Errorf("parked card's check shown: %q", h)
	}
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	if err := saveChecks(map[string]checkState{"gone: x": {RanAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if h := ansi.Strip(newPane().header()); strings.Contains(h, "checks") {
		t.Errorf("no card has a check, yet: %q", h)
	}
}

func TestHeaderDropsChecksFirst(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "a", detailCard("a", "waiting", "", "check: pr 5 approved\n", "G", "W", "1. q\n"))
	putCard(t, th, "x", detailCard("x", "focus", "", "", "G", "W", ""))
	closeTrack("x", time.Now(), "")
	foldBack(t)
	saveChecks(map[string]checkState{"a: pr 5 approved": {Answer: true, RanAt: time.Now().Add(-2 * time.Minute)}})
	m := newPane()
	m.now = time.Now()
	head := func(w int) string { return strings.Split(plain(m, w, 12), "\n")[0] }
	if h := head(100); !strings.Contains(h, "1 track · ◆ 1 for you · handled 0 today · 1 closed today · checks 2m ago") {
		t.Errorf("wide: %q", h)
	}
	if h := head(70); strings.Contains(h, "checks") || !strings.Contains(h, "1 closed today") {
		t.Errorf("checks go before closed today: %q", h)
	}
	if h := head(34); strings.Contains(h, "closed") || !strings.Contains(h, "◆ 1 for you") || strings.Contains(h, "track") || strings.Contains(h, "handled") {
		t.Errorf("closed today, then the track count, then handled go before the count for you: %q", h)
	}
}

func TestKeyLinesBoldKeysDimLabels(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	m.w = 120
	want := boldSt.Render("↑↓") + dimSt.Render(" move") + "  " + boldSt.Render("enter") + dimSt.Render(" open")
	if k := m.keysLine(); !strings.Contains(strings.ReplaceAll(k, dimSt.Render("  "), "  "), want) {
		t.Errorf("long form: %q", k)
	}
	m.w = 60
	if k := m.keysLine(); !strings.Contains(k, boldSt.Render("↑↓ enter")) || !strings.Contains(ansi.Strip(k), "tab next") || strings.Contains(ansi.Strip(k), "move") {
		t.Errorf("terse form: %q", k)
	}
	m.w = 26
	if k := m.keysLine(); k != boldSt.Render(keysShort) {
		t.Errorf("initials: %q", k)
	}
	m.w = 100
	m.reply = &replyState{track: "a", ed: lineEditor{limit: maxAnswer}}
	if f := m.footer(); !strings.Contains(f, styleHints(editHints)) {
		t.Errorf("edit keys: %q", f)
	}
	m.reply = nil
	m.form = newTrackForm()
	if f := m.footer(); !strings.Contains(f, styleHints(requestHints)) {
		t.Errorf("form keys: %q", f)
	}
	m.form = nil
	m.status = answeredNote
	if f := m.footer(); !strings.Contains(f, boldSt.Render("r")+dimSt.Render(" reply")) || !strings.Contains(ansi.Strip(f), answeredNote) {
		t.Errorf("answered note: %q", f)
	}
}

func TestKeysShowOnlyWhatWorksNow(t *testing.T) {
	th, _ := setup(t)
	empty := plain(newPane(), 120, 12)
	foot := empty[strings.LastIndex(empty, "\n")+1:]
	for _, gone := range []string{"x close", "enter open", "tab next", "mode", "move"} {
		if strings.Contains(foot, gone) {
			t.Errorf("%q offered with no track: %q", gone, foot)
		}
	}
	if !strings.Contains(foot, "n new") || !strings.Contains(foot, "q quit") {
		t.Errorf("no track: n and q still work: %q", foot)
	}
	card(t, th, "a", "focus", "", "w", "")
	out := plain(newPane(), 120, 12)
	if foot := out[strings.LastIndex(out, "\n")+1:]; !strings.Contains(foot, "x close") || !strings.Contains(foot, "f b w p mode") {
		t.Errorf("a track is selected: %q", foot)
	}
}

// closePrompt presses x on the selected track and returns the footer, ANSI stripped.
func closePrompt(t *testing.T, m *paneModel, name string, w int) string {
	t.Helper()
	selectTrack(t, m, name)
	m.w = w
	key(m, "x")
	return strings.TrimSpace(ansi.Strip(m.footer()))
}

func TestClosePromptNoSession(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "bare", "focus", "", "w", "")
	m := newPane()
	closePrompt(t, m, "bare", 120)
	want := warnSt.Render("Close bare? Archives the card.") + "  " + styleHints(confirmHints)
	if f := m.footer(); !strings.Contains(f, want) {
		t.Errorf("question not in warning colour with bold keys: %q", f)
	}
}

func TestClosePromptDefaultDoesNotPromiseTheSessionEnds(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "eb6e0e", "w", "")
	m := newPane()
	m.rex.states["eb6e0e"] = agentState{State: "idle"}
	want := "Close a? Archives the card; ends its session if nothing in it can be lost.  y close · n keep"
	if f := closePrompt(t, m, "a", 140); f != want {
		t.Errorf("idle: %q", f)
	}
	m.confirm = ""
	m.rex.states["eb6e0e"] = agentState{State: "done"}
	if f := closePrompt(t, m, "a", 140); f != want {
		t.Errorf("done: %q", f)
	}
}

func TestClosePromptNamesAgentStateOutsideRest(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "eb6e0e", "w", "")
	m := newPane()
	for _, state := range []string{"working", "blocked"} {
		m.confirm = ""
		m.rex.states["eb6e0e"] = agentState{State: state}
		want := "Close a? Its agent is " + state + "; archives the card, keeps the session while it is busy.  y close · n keep"
		if f := closePrompt(t, m, "a", 140); f != want {
			t.Errorf("%s: %q", state, f)
		}
	}
}

func TestClosePromptNamesTheTrackSharingTheSession(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "eb6e0e", "w", "")
	card(t, th, "b", "focus", "eb6e0e", "w", "")
	m := newPane()
	m.rex.states["eb6e0e"] = agentState{State: "idle"}
	if f := closePrompt(t, m, "a", 140); f != "Close a? Archives the card, keeps the session: track b uses it.  y close · n keep" {
		t.Errorf("shared: %q", f)
	}
}

func TestClosePromptKeepsTheKeysInANarrowWindow(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "eb6e0e", "w", "")
	m := newPane()
	f := closePrompt(t, m, "a", 50)
	if !strings.HasSuffix(f, "  y close · n keep") || !strings.Contains(f, "…") || lipgloss.Width(f) > 50 {
		t.Errorf("narrow: %q", f)
	}
}

func TestStartOfferShape(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "bare", "focus", "", "w", "")
	m := newPane()
	m.w = 120
	m.offer = offerMsg{name: "bare"}
	if f := m.footer(); !strings.Contains(f, warnSt.Render("No agent for bare. Start one?")+"  "+styleHints(offerHints)) {
		t.Errorf("offer: %q", f)
	}
}

// The prompts promise what closeTrack does: with one pane and an agent that is working or
// blocked, the card is archived and the session is kept.
func TestCloseTrackKeepsTheSessionOfAnAgentNotAtRest(t *testing.T) {
	stubChildren(t)
	for state, status := range map[string]string{
		"working": `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"}]}`,
		"blocked": `{"records":[{"state":"blocked","kind":"permission","app":"claude-code","effective_app":"claude-code"}]}`,
	} {
		th, bin := setup(t)
		endStub(t, bin, oneSession, oneBlock) // one pane
		blockFile(t, bin, "status", "block:aaa", status)
		card(t, th, "la", "focus", "eb6e0e", "w", "")
		_, note, err := closeTrack("la", time.Now(), "")
		if err != nil || !strings.Contains(note, "kept its Rex session: its agent is "+state) {
			t.Errorf("%s: note %q, err %v", state, note, err)
		}
		if strings.Contains(rexCalls(bin), "kill") {
			t.Errorf("%s: session killed", state)
		}
		if _, err := os.Stat(cardPath("la")); err == nil {
			t.Errorf("%s: the card was not archived", state)
		}
	}
}

func TestStatusErrorsShowAMarkAndBaseNames(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	m.w = 140
	m.Update(noteMsg("could not open EMI-1: open /Users/x/.local/state/tracks/cards/a.md: permission denied"))
	f := m.footer()
	if want := badSt.Render(fit(errMark+"could not open EMI-1: open a.md: permission denied", 138)); !strings.Contains(f, want) {
		t.Errorf("footer %q, want %q", f, want)
	}
	if strings.Contains(f, "/Users") {
		t.Errorf("a full path is shown: %q", f)
	}
	m.Update(noteMsg("Not sent: its agent is working"))
	if f := ansi.Strip(m.footer()); !strings.HasPrefix(strings.TrimSpace(f), "✕ Not sent") {
		t.Errorf("not sent: %q", f)
	}
	m.Update(noteMsg("Opened EMI-1"))
	if f := ansi.Strip(m.footer()); strings.Contains(f, "✕") {
		t.Errorf("a note is not an error: %q", f)
	}
	for in, want := range map[string]string{
		"open ~/a/b.md: no":                   "open b.md: no",
		"two /x/y.md and (/z/w.md)":           "two y.md and (w.md)",
		"see https://example.com/a/b for why": "see https://example.com/a/b for why",
		"/only: nothing":                      "/only: nothing",
	} {
		if got := baseNames(in); got != want {
			t.Errorf("baseNames(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHelpShowsTheLastError(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	if out := plain(m, 100, 30); strings.Contains(out, "Last error") {
		t.Errorf("an error before any failure:\n%s", out)
	}
	m.Update(noteMsg("could not open EMI-1: open /x/y/a.md: permission denied"))
	m.Update(noteMsg("Opened EMI-1")) // the footer moves on; the help keeps the failure
	m.lastErrAt = time.Now().Add(-4*time.Minute - 5*time.Second)
	key(m, "?")
	out := plain(m, 100, 30)
	if !strings.Contains(out, "Last error  4m ago: could not open EMI-1: open a.md: permission denied") {
		t.Errorf("help:\n%s", out)
	}
	if strings.Index(out, "Last error") < strings.Index(out, "quit") {
		t.Errorf("the error belongs under the keys:\n%s", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %d is %d wide", i, lipgloss.Width(l))
		}
	}
	// The same failure shown for many ticks is one failure.
	m.help = false
	m.Update(noteMsg("could not x"))
	first := m.lastErrAt
	m.Update(tickMsg{})
	if !m.lastErrAt.Equal(first) {
		t.Error("a status that stays up was recorded again")
	}
	if out := plain(m, 80, 16); strings.Contains(out, "Last error") && !strings.Contains(out, "quit") {
		t.Errorf("a short window must keep the keys whole:\n%s", out)
	}
}

func modeCards(t *testing.T) *paneModel {
	t.Helper()
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	card(t, th, "b", "background", "", "w", "")
	card(t, th, "c", "background", "", "w", "")
	// c waits on someone, so it shows in WAITING while its stored mode is background.
	b, err := os.ReadFile(cardPath("c"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(cardPath("c"), []byte(strings.Replace(string(b), "rex_session: \n", "rex_session: \nwaiting_on: Rasmus\n", 1))); err != nil {
		t.Fatal(err)
	}
	return newPane()
}

func TestModeKeysKeepTheSelectionAndSayWhereItWent(t *testing.T) {
	m := modeCards(t)
	selectTrack(t, m, "b")
	for _, tc := range []struct{ key, mode, label string }{
		{"f", "focus", "FOCUS"}, {"p", "parked", "PARKED"}, {"b", "background", "BACKGROUND"},
	} {
		press(m, tc.key)
		if m.cur != "b" || m.selected().Name != "b" {
			t.Fatalf("%s: the selection moved to %q", tc.key, m.cur)
		}
		if got := loadCardMust(t, "b").mode(); got != tc.mode {
			t.Errorf("%s: mode %q", tc.key, got)
		}
		if want := "Moved b to " + tc.label; m.flash != want || !strings.Contains(plain(m, 100, 20), want) {
			t.Errorf("%s: flash %q, want %q", tc.key, m.flash, want)
		}
		if !strings.Contains(plain(m, 100, 20), "▌b") {
			t.Errorf("%s: the cursor is not on b", tc.key)
		}
	}
}

func TestModeKeyForTheModeItHasChangesNothing(t *testing.T) {
	m := modeCards(t)
	selectTrack(t, m, "c")
	before, _ := os.ReadFile(cardPath("c"))
	press(m, "b")
	if want := "c is already in BACKGROUND"; m.flash != want || !strings.Contains(plain(m, 100, 20), want) {
		t.Errorf("flash %q, want %q", m.flash, want)
	}
	if after, _ := os.ReadFile(cardPath("c")); string(after) != string(before) {
		t.Error("the card was rewritten")
	}
	if evs := readEvents(); len(evs) != 0 {
		t.Errorf("a mode event was logged: %+v", evs)
	}
}

func formPane(t *testing.T, w, h int) *paneModel {
	t.Helper()
	m := modeCards(t)
	t.Setenv("TRACKS_DEFAULT_DIR", "") // so the dir is ~/Developer, which shows as ~/Developer
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Developer"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.Update(tickMsg{})
	m.w, m.h = w, h
	press(m, "n")
	typeText(m, "Fix the refund retry window")
	return m
}

func TestNewTrackFormBesideTheList(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {100, 24}, {80, 24}, {80, 10}} {
		m := formPane(t, size[0], size[1])
		out := plain(m, size[0], size[1])
		lines := strings.Split(out, "\n")
		if len(lines) != size[1] {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) > size[0] {
				t.Errorf("%v: line %d is %d wide: %q", size, i, lipgloss.Width(l), l)
			}
		}
		if size[1] < 20 {
			continue
		}
		var left, right []string
		for _, l := range lines {
			if r, ok := rightCol(l); ok {
				right = append(right, strings.TrimRight(r, " "))
				left = append(left, strings.TrimRight(l[:strings.Index(l, " │ ")], " "))
			}
		}
		got := strings.Join(right, "\n")
		for _, want := range []string{"New track", "› Fix the refund retry window", "name  fix-refund-retry-window", "dir   ~/Developer", requestNote[:20]} {
			if !strings.Contains(got, want) {
				t.Errorf("%v: missing %q in the detail:\n%s", size, want, out)
			}
		}
		list := strings.Join(left, "\n")
		for _, want := range []string{" FOCUS", " BACKGROUND", " WAITING", "▌a", " b ", " c "} {
			if !strings.Contains(list, want) {
				t.Errorf("%v: the list is not beside the form (%q):\n%s", size, want, out)
			}
		}
		if strings.Contains(out, "What do you want?") {
			t.Errorf("%v: the box is back:\n%s", size, out)
		}
	}
}

func TestNewTrackFormListIsDimmed(t *testing.T) {
	m := formPane(t, 120, 30)
	m.Update(tickMsg{})
	raw := m.render()
	tint := ansi.NewStyle().BackgroundColor(selUnknownBg).String()
	if strings.Contains(raw, tint) || strings.Contains(raw, selSt.Render("▌")) {
		t.Error("the selection tint is still on the dimmed list")
	}
	if !strings.Contains(raw, dimSt.Render(" FOCUS")) {
		t.Errorf("list rows are not dim:\n%q", raw)
	}
}

func TestNewTrackFormKeepsTheBoxWhereThereIsNoRoom(t *testing.T) {
	m := formPane(t, 79, 24)
	out := plain(m, 79, 24)
	if !strings.Contains(out, "What do you want?") || strings.Contains(out, "New track") {
		t.Errorf("narrow:\n%s", out)
	}
	th, _ := setup(t)
	_ = th
	m = newPane()
	press(m, "n")
	if out := plain(m, 160, 40); !strings.Contains(out, "What do you want?") {
		t.Errorf("no tracks to show beside it:\n%s", out)
	}
}

func TestWhichStatusesAreFailures(t *testing.T) {
	for s, want := range map[string]bool{
		"could not set mode: x":                                true,
		"Not sent: its agent is working":                       true,
		"Typed into a but not sent: its agent exited.":         true,
		"Closed a, but could not update its Rex session: boom": true,
		"Not sent: the reply is empty":                         false,
		"Opened EMI-1":                                         false,
		"Closed a; kept its Rex session: its agent is working": false,
		"Started a, but it could not be selected":              false,
		"Already answered; waiting for the agent":              false,
	} {
		if got := isErrStatus(s); got != want {
			t.Errorf("isErrStatus(%q) = %v, want %v", s, got, want)
		}
	}
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	m.w = 120
	m.Update(noteMsg("Not sent: the reply is empty"))
	if f := ansi.Strip(m.footer()); strings.Contains(f, "✕") || m.lastErr != "" {
		t.Errorf("a slip was kept as an error: %q, %q", f, m.lastErr)
	}
	m.Update(noteMsg("Typed into a but not sent: its agent exited. Clear /x/y/in.txt"))
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "✕ Typed into a") || !strings.HasSuffix(strings.TrimSpace(m.lastErr), "Clear in.txt") {
		t.Errorf("typed: %q, %q", f, m.lastErr)
	}
	m.Update(noteMsg("Opened /x/y/z.md"))
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "Opened /x/y/z.md") {
		t.Errorf("a note's path was shortened: %q", f)
	}
}

func TestFormErrorsAreKeptAsTheLastError(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	m.w = 120
	m.form = newTrackForm()
	m.form.err = requestRequired
	m.Update(tickMsg{})
	if m.lastErr != "" || strings.Contains(ansi.Strip(m.footer()), "✕") {
		t.Errorf("a missing request is not a failure: %q", m.lastErr)
	}
	m.form.err = "could not write the card: open /x/cards/a.md: denied"
	m.Update(tickMsg{})
	if m.lastErr != "could not write the card: open a.md: denied" || !strings.Contains(ansi.Strip(m.footer()), "✕ could not write the card") {
		t.Errorf("form error: %q", m.lastErr)
	}
}

func TestModeKeyOnACardWithAnUnknownOrEmptyModeWritesIt(t *testing.T) {
	// A card with no mode: line at all cannot be written by setMeta; that keeps its error.
	for _, mode := range []string{"later", ""} {
		th, _ := setup(t)
		putCard(t, th, "odd", "---\nname: odd\nmode: "+mode+"\ndir: %DIR%\n---\n## Goal\nG\n")
		m := newPane()
		press(m, "b")
		if got := loadCardMust(t, "odd").mode(); got != "background" {
			t.Errorf("mode %q: wrote %q", mode, got)
		}
		if m.flash != "Moved odd to BACKGROUND" {
			t.Errorf("mode %q: flash %q", mode, m.flash)
		}
	}
}

func TestKeysAt80ColumnsKeepAnswerKeys(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "1. Ok? Recommend: yes.")
	setMeta("a", "mode", "focus")
	putCard(t, th, "a", detailCard("a", "focus", "", "links: PAN-5\n", "G", "W", "1. Ok? Recommend: yes.\n"))
	out := plain(newPane(), 80, 24)
	foot := out[strings.LastIndex(out, "\n")+1:]
	if !strings.Contains(foot, answerKeys) || strings.Contains(foot, "o link") || strings.TrimSpace(foot) == keysShort {
		t.Errorf("footer %q", foot)
	}
	if lipgloss.Width(foot) > 80 {
		t.Errorf("footer is %d wide", lipgloss.Width(foot))
	}
}
