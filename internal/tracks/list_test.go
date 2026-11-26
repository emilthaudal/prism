package tracks

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// listCard is a card with the fields the list reads; extra is more frontmatter lines.
func listCard(name, mode, sess, extra, where string) string {
	return "---\nname: " + name + "\nmode: " + mode + "\ndir: %DIR%\nrex_session: " + sess + "\n" + extra +
		"---\n## Goal\nG\n\n## Where it is\n" + where + "\n\n## Needs me\n\n"
}

func listPane(t *testing.T, cards map[string]string, states map[string]agentState, w, h int) *paneModel {
	t.Helper()
	th, _ := setup(t)
	for n, body := range cards {
		putCard(t, th, n, body)
	}
	m := newPane()
	m.now = time.Now()
	for k, v := range states {
		m.rex.states[k] = v
	}
	m.Update(sinceMsg{name: m.cur, err: errNotGit.Error(), at: m.now})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// leftLines is the list column of each rendered line, trailing space cut.
func leftLines(out string) []string {
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, " │ "); i >= 0 {
			l = l[:i]
		}
		ls = append(ls, strings.TrimRight(l, " "))
	}
	return ls
}

func TestWhereLineUnderEachTrackFrom100Columns(t *testing.T) {
	old := time.Now().Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339)
	cards := map[string]string{
		"alpha":      listCard("alpha", "focus", "", "updated: "+time.Now().Add(-90*time.Minute).UTC().Format(time.RFC3339)+"\n", "Tier 2: product code.\n\nPR 42 is open and waits on review.\nSecond line stays out."),
		"stale":      listCard("stale", "background", "", "updated: "+old+"\n", "Watching a deploy."),
		"long":       listCard("long", "background", "", "", strings.Repeat("word ", 40)),
		"w-chk":      listCard("w-chk", "waiting", "", "check: pr 51 approved\nwaiting_on: the checkout team\n", "Waiting for approval."),
		"w-chk-only": listCard("w-chk-only", "waiting", "", "check: pr 52 approved\n", "ignored"),
		// a long name widens the list column, so the sub-lines above are not cut
		"a-long-name-that-widens-the-list-column": listCard("a-long-name-that-widens-the-list-column", "background", "", "", "x"),
		"w-on":   listCard("w-on", "waiting", "", "waiting_on: Robin's review\n", "Where text."),
		"w-bare": listCard("w-bare", "waiting", "", "", "Falls back to where."),
	}
	m := listPane(t, cards, nil, 120, 40)
	got := leftLines(ansi.Strip(m.render()))
	find := func(prefix string) string {
		t.Helper()
		for i, l := range got {
			if strings.Contains(l, prefix) && i+1 < len(got) {
				return got[i+1]
			}
		}
		t.Fatalf("no row %q in:\n%s", prefix, strings.Join(got, "\n"))
		return ""
	}
	for name, want := range map[string]string{
		"alpha":      "   PR 42 is open and waits on review. · 1h",
		"stale":      "   Watching a deploy. · 3d",
		"w-chk ":     "   check: pr 51 approved · the checkout team · 0m",
		"w-chk-only": "   check: pr 52 approved · 0m",
		"w-on ":      "   Robin's review · 0m",
		"w-bare":     "   Falls back to where. · 0m",
	} {
		if g := find(name); g != want {
			t.Errorf("%s: sub-line %q, want %q", name, g, want)
		}
	}
	long := find("long ")
	if !strings.HasPrefix(long, "   word word") || !strings.Contains(long, "… · 0m") || lipgloss.Width(long) > m.leftW() {
		t.Errorf("long where-line not cut to the list width (%d): %q", m.leftW(), long)
	}

	raw := m.render()
	if !strings.Contains(raw, warnSt.Render("3d")) {
		t.Errorf("age over 2 days is not yellow")
	}
	if strings.Contains(raw, warnSt.Render("1h")) || strings.Contains(raw, warnSt.Render("0m")) {
		t.Errorf("a recent age is yellow")
	}

	narrow := listPane(t, cards, nil, 99, 40)
	for _, l := range leftLines(ansi.Strip(narrow.render())) {
		if strings.Contains(l, "checkout team") || strings.Contains(l, "PR 42") || strings.Contains(l, "Watching") {
			t.Errorf("sub-line under 100 columns: %q", l)
		}
	}
	if n := len(leftLines(ansi.Strip(narrow.render()))); n != 40 {
		t.Fatalf("height %d", n)
	}
}

func TestListColumnSizesToLongestName(t *testing.T) {
	long := "gatekeeper-guided-reviews-risky-diff"
	cards := map[string]string{
		long: listCard(long, "focus", "", "", "w"),
		"b":  listCard("b", "background", "", "", "w"),
	}
	m := listPane(t, cards, nil, 120, 30)
	if want := lipgloss.Width(long) + listFixed + stateWidth; m.leftW() != want {
		t.Fatalf("leftW %d, want %d", m.leftW(), want)
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, long+" ") {
		t.Errorf("name truncated at 120:\n%s", out)
	}
	m = listPane(t, map[string]string{"b": cards["b"]}, nil, 120, 30)
	if m.leftW() != leftWidth {
		t.Errorf("short names: leftW %d, want the minimum %d", m.leftW(), leftWidth)
	}
	huge := strings.Repeat("x", 90)
	m = listPane(t, map[string]string{huge: listCard(huge, "focus", "", "", "w")}, nil, 120, 30)
	if m.leftW() != 60 {
		t.Errorf("leftW %d, want half the window", m.leftW())
	}
}

func TestStateColumnIsAGlyphUnder100Columns(t *testing.T) {
	cards := map[string]string{"a": listCard("a", "focus", "s-a", "", "w")}
	states := map[string]agentState{"s-a": {State: "blocked", Kind: "question", Msg: "Which one?"}}
	m := listPane(t, cards, states, 90, 20)
	row := leftLines(ansi.Strip(m.render()))[5] // after the Needs you band and its rule
	if !strings.Contains(row, "▌a") || !strings.HasSuffix(row, "?") || strings.Contains(row, "question") {
		t.Errorf("narrow row %q", row)
	}
	m = listPane(t, cards, states, 100, 20)
	if row := leftLines(ansi.Strip(m.render()))[5]; !strings.Contains(row, "? question") {
		t.Errorf("wide row %q", row)
	}
	for _, g := range []string{"●", "○", "?", "◆", "✓", "✕", "!", "•", "▌", "┃"} {
		if lipgloss.Width(g) != 1 {
			t.Errorf("%q is %d cells wide", g, lipgloss.Width(g))
		}
	}
}

func TestErrorShowsBangAndNeedsYou(t *testing.T) {
	cards := map[string]string{
		"boom":  listCard("boom", "background", "s-boom", "", "w"),
		"boom2": listCard("boom2", "background", "s-boom2", "", "w"),
		"calm":  listCard("calm", "background", "s-calm", "", "w"),
	}
	cards["boom2"] = strings.Replace(cards["boom2"], "## Needs me\n", "## Needs me\n1. Retry?\n", 1)
	states := map[string]agentState{
		"s-boom": {State: "error", Msg: "migration failed"}, "s-boom2": {State: "error"}, "s-calm": {State: "idle"},
	}
	for _, w := range []int{120, 90} {
		m := listPane(t, cards, states, w, 20)
		var bang, asks string
		for _, l := range leftLines(ansi.Strip(m.render())) {
			switch {
			case strings.Contains(l, "boom2"):
				asks = l
			case strings.Contains(l, "boom"):
				bang = l
			}
		}
		if !strings.Contains(bang, " !") || strings.Contains(asks, " !") || !strings.Contains(asks, "◆1") {
			t.Errorf("%d cols: bang row %q, asks row %q", w, bang, asks)
		}
		if !strings.Contains(m.render(), badSt.Render("  !")) {
			t.Errorf("%d cols: the ! is not red", w)
		}
		if _, needs := m.counts(); needs != 2 || !strings.Contains(ansi.Strip(m.header()), "3 for you") { // two tracks, three decisions
			t.Errorf("%d cols: %d tracks need you, want 2, and the header 3 for you", w, needs)
		}
	}
	// parked tracks never need you, error or not
	cards["boom"] = strings.Replace(cards["boom"], "mode: background", "mode: parked", 1)
	m := listPane(t, cards, states, 120, 20)
	if _, needs := m.counts(); needs != 1 {
		t.Errorf("a parked error counts: %d", needs)
	}
}

func TestErrorOrdersAfterBlockedBeforeAsks(t *testing.T) {
	cards := []*Card{
		mk("done", "focus", iso(time.Hour), ""),
		mk("ask", "focus", iso(time.Hour), "1. q?"),
		mk("err", "background", iso(time.Hour), ""),
		mk("err-ask", "waiting", iso(time.Hour), "1. q?"),
		mk("blocked", "waiting", iso(time.Hour), ""),
		mk("err-parked", "parked", iso(time.Hour), ""),
	}
	states := map[string]agentState{
		"s-done": st("done", "", time.Hour), "s-err": st("error", "", time.Hour), "s-err-ask": st("error", "", time.Hour),
		"s-blocked": st("blocked", "permission", time.Hour), "s-err-parked": st("error", "", time.Hour),
	}
	items := needsOrder(cards, states, nil, t0)
	if got, want := names(items), "blocked err err-ask ask done"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	if items[1].why != "error" || items[2].why != "error · 1 ask" {
		t.Errorf("why: %q, %q", items[1].why, items[2].why)
	}
	if got := stepNeed(items, "blocked", 1); got != "err" {
		t.Errorf("tab from blocked goes to %s, want err", got)
	}
}

func TestSelectionTintAndMarker(t *testing.T) {
	cards := map[string]string{
		"a": listCard("a", "focus", "", "", "first where"),
		"b": listCard("b", "focus", "", "", "second where"),
	}
	m := listPane(t, cards, nil, 120, 20)
	raw := strings.Split(m.render(), "\n")
	tinted := func(l string) bool { return strings.Contains(l, "\x1b[48;5;243m") }
	var selRow, selSub, otherRow bool
	for _, l := range raw {
		p := ansi.Strip(l)
		switch {
		case strings.Contains(p, "▌a"):
			selRow = tinted(l) && strings.Contains(l, selSt.Render("▌")) && strings.Contains(l, boldSt.Render(fit("a", leftWidth-listFixed-stateWidth)))
			if w := lipgloss.Width(strings.Split(p, " │ ")[0]); w != m.leftW() {
				t.Errorf("tint row is %d wide, want %d", w, m.leftW())
			}
		case strings.HasPrefix(p, "   first where"):
			selSub = tinted(l)
		case strings.Contains(p, " b "):
			otherRow = tinted(l)
		}
	}
	if !selRow || !selSub || otherRow {
		t.Errorf("tint: selected row %v, its sub-line %v, other row %v", selRow, selSub, otherRow)
	}
	// Without colour the marker and bold are what is left.
	if p := ansi.Strip(m.render()); strings.Count(p, "▌") != 1 {
		t.Errorf("▌ must mean only the selection:\n%s", p)
	}
	m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	if out := m.render(); !strings.Contains(out, "\x1b[48;5;254m") || strings.Contains(out, "\x1b[48;5;243m") {
		t.Errorf("light terminal not tinted with the light grey")
	}
	m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	if out := m.render(); !strings.Contains(out, "\x1b[48;5;237m") || strings.Contains(out, "\x1b[48;5;243m") {
		t.Errorf("dark terminal not tinted with the dark grey")
	}
}

func TestNeedsMeBarIsNotTheSelectionMarker(t *testing.T) {
	cards := map[string]string{"a": strings.Replace(listCard("a", "focus", "", "", "w"), "## Needs me\n", "## Needs me\n1. Ship it? Recommend: yes\n", 1)}
	m := listPane(t, cards, nil, 120, 20)
	out := ansi.Strip(m.render())
	if strings.Count(out, "▌") != 2 || !strings.Contains(out, "┃ Needs me") { // the band's row and the list's
		t.Errorf("bar glyphs:\n%s", out)
	}
	if !strings.Contains(m.render(), warnSt.Render("┃")) {
		t.Errorf("the Needs me bar is not warning colour")
	}
}

func TestUnreadDotUntilOpened(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "sa", "w", "")
	card(t, th, "b", "background", "", "w", "")
	m := newPane()
	m.now = time.Now()
	dot := func(name string) bool {
		for _, l := range leftLines(ansi.Strip(m.render())) {
			if strings.Contains(l, name+" ") || strings.Contains(l, name+"   ") {
				return strings.HasPrefix(l, "•")
			}
		}
		t.Fatalf("no row for %s", name)
		return false
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	if !dot("a") || !dot("b") {
		t.Fatalf("a card changed within the day and never opened shows no dot:\n%s", ansi.Strip(m.render()))
	}
	if !strings.Contains(m.render(), warnSt.Render("•")) {
		t.Error("the dot is not yellow")
	}

	// enter opens the track and logs it, which clears the dot on the next reload
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did nothing")
	}
	m.Update(cmd())
	m.stamp = ""
	m.Update(tickMsg{})
	if dot("a") || !dot("b") {
		t.Errorf("after enter: a unread %v, b unread %v\n%s", dot("a"), dot("b"), ansi.Strip(m.render()))
	}

	// the agent writing its card after the open brings it back
	body, _ := os.ReadFile(cardPath("a"))
	at := time.Now().Add(time.Minute).Truncate(time.Second)
	stamp := at.UTC().Format(time.RFC3339)
	foldBack(t)
	if err := writeAtomic(cardPath("a"), []byte(strings.Replace(string(body), "dir:", "updated: "+stamp+"\ndir:", 1))); err != nil {
		t.Fatal(err)
	}
	// A write stamped at a time leaves the file's mtime no earlier than the stamp.
	if err := os.Chtimes(cardPath("a"), at, at); err != nil {
		t.Fatal(err)
	}
	m.stamp = ""
	m.Update(tickMsg{})
	if !dot("a") {
		t.Errorf("a card stamped after the open shows no dot")
	}
}

// cutList is a list with headings, a gap, and tracks with and without a sub-line.
func cutList() (lines []string, kinds []byte) {
	add := func(k byte, l string) { lines, kinds = append(lines, l), append(kinds, k) }
	add(lineGap, " H1")
	for _, n := range []string{"1", "2", "3"} {
		add(lineTrack, "T"+n)
		add(lineSub, "sub-"+n)
	}
	add(lineGap, "")
	add(lineGap, " H2")
	add(lineTrack, "T4")
	add(lineTrack, "T5")
	add(lineSub, "sub-5")
	add(lineTrack, "T6")
	add(lineSub, "sub-6")
	return
}

func TestCutListKeepsTracksWithTheirSubLines(t *testing.T) {
	lines, kinds := cutList()
	for sel, k := range kinds {
		if k != lineTrack {
			continue
		}
		for h := 3; h < len(lines)+2; h++ {
			got := windowList(lines, kinds, sel, h)
			ctx := func() string { return strings.Join(got, "\n") }
			if len(got) > h {
				t.Fatalf("sel %d, height %d: %d lines:\n%s", sel, h, len(got), ctx())
			}
			hasNote := strings.Contains(got[len(got)-1], "more")
			body := got
			if hasNote {
				body = got[:len(got)-1]
			}
			if strings.HasPrefix(body[0], "sub-") {
				t.Errorf("sel %d, height %d starts on an orphan:\n%s", sel, h, ctx())
			}
			shown := map[string]bool{}
			for _, l := range body {
				shown[l] = true
			}
			hiddenTracks := 0
			for i, l := range lines {
				if kinds[i] != lineTrack {
					continue
				}
				if hasSub := i+1 < len(lines) && kinds[i+1] == lineSub; hasSub && shown[l] != shown[lines[i+1]] {
					t.Errorf("sel %d, height %d: %s and its sub-line are not shown together:\n%s", sel, h, l, ctx())
				}
				if !shown[l] && i > sel {
					hiddenTracks++
				}
			}
			if !shown[lines[sel]] || sel+1 < len(lines) && kinds[sel+1] == lineSub && !shown[lines[sel+1]] {
				t.Errorf("sel %d, height %d: selection or its sub-line cut:\n%s", sel, h, ctx())
			}
			if hasNote != (hiddenTracks > 0) || hasNote && !strings.Contains(got[len(got)-1], fmt.Sprintf("↓ %d more", hiddenTracks)) {
				t.Errorf("sel %d, height %d: note %v with %d tracks hidden below:\n%s", sel, h, hasNote, hiddenTracks, ctx())
			}
			if !hasNote && len(lines) > h && body[len(body)-1] != lines[len(lines)-1] {
				t.Errorf("sel %d, height %d: nothing is hidden below yet the list stops short:\n%s", sel, h, ctx())
			}
		}
	}
}

func TestCutListInThePane(t *testing.T) {
	cards := map[string]string{}
	for _, n := range []string{"track-1", "track-2", "track-3", "track-4", "track-5", "track-6"} {
		cards[n] = listCard(n, "focus", "", "", "where of "+n)
	}
	m := listPane(t, cards, nil, 120, 11)
	for range 2 {
		key(m, "down")
	}
	out := leftLines(plain(m, 120, 11))
	for i, l := range out {
		if strings.HasPrefix(strings.TrimLeft(l, "• ▌"), "track-") && !strings.HasPrefix(strings.TrimSpace(out[i+1]), "where of") && !strings.Contains(out[i+1], "more") {
			t.Errorf("%q has no where-line under it:\n%s", l, strings.Join(out, "\n"))
		}
	}
}

func TestSubLineEndsWhereRowsDoAndCollapsesWhitespace(t *testing.T) {
	cards := map[string]string{
		"w-tab": listCard("w-tab", "waiting", "", "waiting_on: the\treview   of\tRobin, and a long tail to make it need cutting off\n", "w"),
	}
	m := listPane(t, cards, nil, 120, 12)
	lines := leftLines(ansi.Strip(m.render()))
	var sub string
	for i, l := range lines {
		if strings.Contains(l, "w-tab") {
			sub = lines[i+1]
		}
	}
	if !strings.HasPrefix(sub, "   the review of Robin") || !strings.HasSuffix(sub, "… · 0m") {
		t.Fatalf("tab in waiting_on: %q", sub)
	}
	if w := lipgloss.Width(sub); w != m.leftW()-1 {
		t.Errorf("sub-line is %d wide, want %d, one short of the column", w, m.leftW()-1)
	}
}

// unreadDots renders the list and returns the names whose row starts with the dot.
func unreadDots(m *paneModel) map[string]bool {
	m.stamp = ""
	m.Update(tickMsg{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	out := map[string]bool{}
	for _, l := range leftLines(ansi.Strip(m.render())) {
		if !strings.HasPrefix(l, "•") && !strings.HasPrefix(l, " ") {
			continue
		}
		f := strings.Fields(strings.TrimLeft(strings.TrimLeft(l, "• "), "▌"))
		if len(f) > 0 && strings.HasPrefix(f[0], "t-") {
			out[f[0]] = strings.HasPrefix(l, "•")
		}
	}
	return out
}

func TestOwnWritesNeverMarkATrackUnread(t *testing.T) {
	th, _ := setup(t)
	agoISO := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	stamped := "updated: " + agoISO(time.Hour) + "\nanswered: \n"
	putCard(t, th, "t-stamped", listCard("t-stamped", "focus", "s-1", stamped, "w"))
	putCard(t, th, "t-plain", listCard("t-plain", "background", "s-2", "", "w"))
	for _, n := range []string{"t-stamped", "t-plain"} {
		if err := appendEvent(Event{At: time.Now().UTC(), Event: "open", Track: n}); err != nil {
			t.Fatal(err)
		}
	}
	m := newPane()
	m.now = time.Now()
	if d := unreadDots(m); d["t-stamped"] || d["t-plain"] {
		t.Fatalf("opened tracks start unread: %v", d)
	}
	for name, write := range map[string]func(n string) error{
		"mode key":    func(n string) error { return setMode(n, "waiting") },
		"answered":    func(n string) error { return setMeta(n, "answered", time.Now().UTC().Format(time.RFC3339)) },
		"rex_session": func(n string) error { return setMeta(n, "rex_session", "s-new") },
	} {
		for _, n := range []string{"t-stamped", "t-plain"} {
			if n == "t-plain" && name == "answered" {
				continue // no answered: line on this card
			}
			future := time.Now().Add(time.Minute)
			if err := write(n); err != nil {
				t.Fatalf("%s %s: %v", name, n, err)
			}
			if err := os.Chtimes(cardPath(n), future, future); err != nil {
				t.Fatal(err)
			}
			if d := unreadDots(m); d[n] {
				t.Errorf("%s marked %s unread", name, n)
			}
		}
	}
}

func TestPromptCountsAsSeeingAndParkedShowsNoDot(t *testing.T) {
	th, _ := setup(t)
	fresh := "updated: " + time.Now().Add(-time.Minute).UTC().Format(time.RFC3339) + "\n"
	putCard(t, th, "t-typed", listCard("t-typed", "focus", "s-typed", fresh, "w"))
	putCard(t, th, "t-system", listCard("t-system", "focus", "s-system", fresh, "w"))
	putCard(t, th, "t-never", listCard("t-never", "focus", "s-never", fresh, "w"))
	putCard(t, th, "t-parked", listCard("t-parked", "parked", "s-parked", fresh, "w"))
	m := newPane()
	m.now = time.Now()
	if d := unreadDots(m); !d["t-typed"] || !d["t-system"] || !d["t-never"] || d["t-parked"] {
		t.Fatalf("before any prompt: %v", d)
	}
	now := time.Now().UTC()
	for _, e := range []Event{
		{At: now, Event: "prompt", RexSession: "s-typed", Track: "t-typed"},
		{At: now, Event: "prompt", RexSession: "s-system", Track: "t-system", Kind: "system"},
	} {
		if err := appendEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if d := unreadDots(m); d["t-typed"] || !d["t-system"] || !d["t-never"] {
		t.Errorf("after prompts: %v (a typed prompt sees the track, a system one does not)", d)
	}
}
