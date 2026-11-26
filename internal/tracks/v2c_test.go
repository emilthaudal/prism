package tracks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func mk(name, mode, updated, needs string) *Card {
	on := ""
	if mode == "waiting" {
		on = "waiting_on: Rasmus\n" // waiting is computed from a wait: a bare waiting reads as background
	}
	return parseCard(name, "---\nname: "+name+"\nmode: "+mode+"\nrex_session: s-"+name+"\n"+on+"updated: "+updated+"\n---\n## Needs me\n"+needs+"\n")
}

func st(state, kind string, ago time.Duration) agentState {
	return agentState{State: state, Kind: kind, At: t0.Add(-ago)}
}

func names(items []needItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.card.Name)
	}
	return strings.Join(out, " ")
}

func iso(ago time.Duration) string { return t0.Add(-ago).Format(time.RFC3339) }

func TestNeedsOrderLevelsGroupsAndAge(t *testing.T) {
	cards := []*Card{
		mk("done-focus", "focus", iso(time.Hour), ""),
		mk("ask-bg", "background", iso(5*time.Hour), "1. q? Recommend: yes."),
		mk("blocked-wait", "waiting", iso(time.Hour), ""),
		mk("blocked-bg-new", "background", iso(time.Hour), ""),
		mk("blocked-bg-old", "background", iso(time.Hour), ""),
		mk("blocked-focus", "focus", iso(time.Hour), ""),
		mk("ask-focus", "focus", iso(time.Hour), "1. q?"),
		mk("ask-wait", "waiting", iso(9*time.Hour), "1. q?"),
		mk("ask-focus-old", "focus", iso(8*time.Hour), "1. q?"),
		mk("idle", "focus", iso(time.Hour), ""),
		mk("parked", "parked", iso(time.Hour), "1. q?"),
		mk("done-bg", "background", iso(time.Hour), ""),
	}
	states := map[string]agentState{
		"s-done-focus":     st("done", "", time.Hour),
		"s-done-bg":        st("done", "", 9*time.Hour),
		"s-blocked-wait":   st("blocked", "permission", time.Minute),
		"s-blocked-bg-new": st("blocked", "question", time.Minute),
		"s-blocked-bg-old": st("blocked", "permission", 2*time.Hour),
		"s-blocked-focus":  st("blocked", "permission", time.Minute),
		"s-idle":           st("idle", "", time.Hour),
		"s-parked":         st("blocked", "permission", time.Hour),
	}
	got := names(needsOrder(cards, states, nil, t0))
	want := "blocked-focus blocked-bg-old blocked-bg-new blocked-wait ask-focus-old ask-focus ask-wait ask-bg done-focus done-bg"
	if got != want {
		t.Fatalf("order\n got %s\nwant %s", got, want)
	}
}

func TestNeedsOrderTiesAndWhy(t *testing.T) {
	cards := []*Card{
		mk("b", "focus", iso(time.Hour), "1. q"),
		mk("a", "focus", iso(time.Hour), "1. q\n2. r"),
		mk("blocked-ask", "focus", iso(time.Hour), "1. q"),
		mk("noupdate", "focus", "", "1. q"),
	}
	states := map[string]agentState{"s-blocked-ask": st("blocked", "question", time.Minute)}
	items := needsOrder(cards, states, nil, t0)
	if got := names(items); got != "blocked-ask a b noupdate" {
		t.Fatalf("ties by name, unknown age last: %s", got)
	}
	why := map[string]string{}
	for _, it := range items {
		why[it.card.Name] = it.why
	}
	if why["blocked-ask"] != "question · 1 ask" || why["a"] != "2 asks" || why["b"] != "1 ask" {
		t.Errorf("why: %v", why)
	}
	for _, kind := range []string{"permission", "done"} {
		s := st("blocked", "permission", 0)
		if kind == "done" {
			s = st("done", "", 0)
		}
		it := needsOrder([]*Card{mk("x", "focus", "", "")}, map[string]agentState{"s-x": s}, nil, t0)
		if len(it) != 1 || it[0].why != kind {
			t.Errorf("%s: %+v", kind, it)
		}
	}
	if got := needsOrder([]*Card{mk("x", "focus", "", "")}, map[string]agentState{"s-x": {State: "idle"}}, nil, t0); len(got) != 0 {
		t.Errorf("idle needs nothing: %v", got)
	}
	if got := needsOrder([]*Card{mk("x", "focus", "", "")}, map[string]agentState{"s-x": {State: "blocked", Kind: "permission", Exited: true}}, nil, t0); len(got) != 0 {
		t.Errorf("an exited agent does not need you: %v", got)
	}
}

func TestNeedsOrderHookFallbackCountsAsBlocked(t *testing.T) {
	c := mk("h", "focus", "", "")
	evs := []Event{{At: t0.Add(-time.Minute), Event: "notify", RexSession: "s-h"}}
	items := needsOrder([]*Card{c}, nil, evs, t0)
	if len(items) != 1 || items[0].level != needBlocked || items[0].why != "needs you" {
		t.Fatalf("%+v", items)
	}
}

func TestStepNeedWraps(t *testing.T) {
	items := needsOrder([]*Card{mk("a", "focus", "", "1. q"), mk("b", "focus", "", "1. q"), mk("c", "background", "", "1. q")}, nil, nil, t0)
	for _, tc := range []struct {
		cur  string
		d    int
		want string
	}{
		{"a", 1, "b"}, {"b", 1, "c"}, {"c", 1, "a"}, {"a", -1, "c"}, {"c", -1, "b"},
		{"zzz", 1, "a"}, {"zzz", -1, "c"}, {"", 1, "a"},
	} {
		if got := stepNeed(items, tc.cur, tc.d); got != tc.want {
			t.Errorf("%s %+d: got %s want %s", tc.cur, tc.d, got, tc.want)
		}
	}
	if stepNeed(nil, "a", 1) != "" {
		t.Error("nothing needs you")
	}
}

func tabKey(m *paneModel, shift bool) {
	if shift {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		return
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
}

func TestPaneTabWalksNeedingTracksAndWraps(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "f-calm", "focus", "", "w", "")
	card(t, th, "f-ask", "focus", "", "w", "1. q? Recommend: yes.")
	card(t, th, "b-ask", "background", "", "w", "1. q?")
	card(t, th, "w-calm", "waiting", "", "w", "")
	card(t, th, "w-ask", "waiting", "", "w", "1. q?")
	m := newPane()
	plain(m, 100, 24)
	if m.cur != "f-ask" {
		t.Fatalf("starts on %s", m.cur)
	}
	var seq []string
	for range 4 {
		tabKey(m, false)
		seq = append(seq, m.cur)
	}
	if got := strings.Join(seq, " "); got != "b-ask w-ask f-ask b-ask" {
		t.Fatalf("tab: %s", got)
	}
	tabKey(m, true)
	tabKey(m, true)
	if m.cur != "w-ask" {
		t.Fatalf("shift+tab wraps back: %s", m.cur)
	}
	key(m, "down")
	for m.cur != "f-calm" {
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	tabKey(m, false)
	if m.cur != "f-ask" {
		t.Errorf("from a calm track tab goes to the first: %s", m.cur)
	}
}

func TestPaneTabWhenNothingNeedsYou(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	card(t, th, "b", "background", "", "w", "")
	m := newPane()
	plain(m, 100, 14)
	tabKey(m, false)
	if m.cur != "a" {
		t.Errorf("cursor moved to %s", m.cur)
	}
	if out := plain(m, 100, 14); !strings.Contains(out, nothingNeedsYou) {
		t.Errorf("footer:\n%s", out)
	}
	m.flashUntil = time.Now().Add(-time.Second)
	m.Update(tickMsg{})
	out := strings.Split(plain(m, 100, 14), "\n")
	if foot := out[len(out)-1]; strings.Contains(foot, nothingNeedsYou) {
		t.Errorf("flash did not clear:\n%s", foot)
	}
}

func TestPaneTabUsesLiveRexState(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "blocked", "permission", "Wants to run: ls"), false)
	card(t, th, "calm", "focus", "", "w", "")
	card(t, th, "live", "waiting", sessFull, "w", "")
	m := newPane()
	settle(t, m)
	tabKey(m, false)
	if m.cur != "live" {
		t.Errorf("blocked agent not reached: %s", m.cur)
	}
}

func TestTracksNextJumpsLikeEnterAndPrints(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "blocked", "permission", "Wants to run: ls"), false)
	card(t, th, "calm", "focus", "", "w", "")
	card(t, th, "live", "background", sessFull, "w", "")
	out, code := do("next")
	if code != 0 || out != "→ live · permission\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	calls := rexCalls(bin)
	for _, want := range []string{"do session.select session_id=" + sessFull, "block focus -s " + sessFull + " " + blkB} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing rex call %q in:\n%s", want, calls)
		}
	}
	if dbExists() {
		if ev := openEvents(t); len(ev) != 1 || ev[0].Track != "live" {
			t.Errorf("open event not logged: %+v", ev)
		}
	} else if b, _ := os.ReadFile(logPath()); !strings.Contains(string(b), `"event":"open"`) || !strings.Contains(string(b), `"track":"live"`) {
		t.Errorf("open event not logged: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "rex.bad")); len(b) > 0 {
		t.Errorf("rex without --autostart=false: %s", b)
	}
}

func TestTracksNextNothingAndEdgeCases(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "calm", "focus", "", "w", "")
	if out, code := do("next"); code != 0 || out != nothingNeedsYou+"\n" {
		t.Errorf("nothing: %d %q", code, out)
	}
	if calls := rexCalls(bin); strings.Contains(calls, "session.select") {
		t.Errorf("selected with nothing to do:\n%s", calls)
	}
	if _, code := do("next", "extra"); code == 0 {
		t.Error("extra args accepted")
	}
	card(t, th, "nosess", "focus", "", "w", "1. q?")
	card(t, th, "withsess", "waiting", sessFull, "w", "1. q?")
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), false)
	if out, code := do("next"); code != 0 || out != "→ withsess · 1 ask\n" {
		t.Errorf("skips a track it cannot jump to: %d %q", code, out)
	}
	foldBack(t)
	os.Remove(cardPath("withsess"))
	if out, code := do("next"); code == 0 || !strings.Contains(out, "→ nosess · 1 ask") || !strings.Contains(out, "no rex_session") {
		t.Errorf("nothing jumpable: %d %q", code, out)
	}
}

func TestParseLinks(t *testing.T) {
	got := parseLinks("EMI-273, PAN-213 ,https://github.com/acme/shop-api/pull/623, pan-9, EMI-273, https://example.com/a/b/")
	want := []link{
		{"EMI-273", "https://linear.app/example/issue/EMI-273"},
		{"PAN-213", "https://example.atlassian.net/browse/PAN-213"},
		{"PR #623", "https://github.com/acme/shop-api/pull/623"},
		{"PAN-9", "https://example.atlassian.net/browse/PAN-9"},
		{"example.com", "https://example.com/a/b/"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for _, junk := range []string{
		"", ",", "EMI", "EMI-", "-273", "273", "EMI-27x", "A-1", "EMI 273", "EMI-273 PR #4",
		"javascript:alert(1)", "file:///etc/passwd", "ftp://x.com/a", "https://", "http:///a", "//x.com/a",
		"https://u:p@x.com/", "https://x.com/a b", "https://x.com/\x07", "open -a Calculator", "$(id)", "`id`",
		"https://x.com/\x00", "x.com/a", "mailto:a@b.c",
	} {
		if got := parseLinks(junk); len(got) != 0 {
			t.Errorf("%q accepted: %v", junk, got)
		}
	}
	if got := parseLinks("EMI-1, junk, https://x.com/ok"); len(got) != 2 {
		t.Errorf("junk should be skipped, not poison the rest: %v", got)
	}
}

func TestLinksParsedFromFrontmatterWithComment(t *testing.T) {
	c := parseCard("x", "---\nname: x\nlinks: EMI-1, https://github.com/o/r/pull/4   # the PR\n---\n")
	if ls := c.links(); len(ls) != 2 || ls[1].label != "PR #4" {
		t.Errorf("%v", ls)
	}
}

func TestPickLink(t *testing.T) {
	for _, tc := range []struct {
		n, last int
		since   time.Duration
		cycle   bool
		want    int
	}{
		{0, -1, 0, false, -1},
		{3, -1, 0, false, 0},
		{3, 0, time.Second, false, 1},
		{3, 2, time.Second, false, 0},
		{3, 0, 3 * time.Second, false, 0},
		{3, 1, 3 * time.Second, false, 0},
		{3, 0, time.Hour, true, 1},
		{3, 2, time.Hour, true, 0},
		{3, -1, 0, true, 0},
		{1, 0, time.Hour, true, 0},
		{2, 5, 0, false, 0},
	} {
		if got := pickLink(tc.n, tc.last, tc.since, tc.cycle); got != tc.want {
			t.Errorf("%+v: got %d", tc, got)
		}
	}
}

// openStub puts an `open` on PATH that records its argv, one argument per line.
func openStub(t *testing.T, bin string) string {
	t.Helper()
	log := filepath.Join(bin, "open.args")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + log + "; done\n"
	if err := os.WriteFile(filepath.Join(bin, "open"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestOpenURLPassesOneArgvEntryNoShell(t *testing.T) {
	_, bin := setup(t)
	log := openStub(t, bin)
	marker := filepath.Join(t.TempDir(), "pwned")
	u := "https://x.com/a;touch$IFS" + marker + "&&touch$IFS" + marker + "`touch$IFS" + marker + "`$(touch$IFS" + marker + ")"
	ls := parseLinks(u)
	if len(ls) != 1 {
		t.Fatalf("url rejected: %v", ls)
	}
	if err := openURL(ls[0].url); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if string(b) != u+"\n" {
		t.Errorf("argv %q", b)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a shell ran the URL")
	}
}

func TestPaneLinksShownAndOpenedInOrder(t *testing.T) {
	th, bin := setup(t)
	log := openStub(t, bin)
	card(t, th, "a", "focus", "", "w", "")
	txt, _ := os.ReadFile(cardPath("a"))
	withLinks := strings.Replace(string(txt), "rex_session:", "links: EMI-301, https://github.com/o/r/pull/812, bad one\nrex_session:", 1)
	os.WriteFile(cardPath("a"), []byte(withLinks), 0o644)
	m := newPane()
	out := plain(m, 100, 24)
	if !strings.Contains(out, "Links  EMI-301 · PR #812") || strings.Contains(out, "bad one") || !strings.Contains(out, "o link") {
		t.Fatalf("detail:\n%s", out)
	}
	run := func(k string) string {
		_, cmd := m.Update(tea.KeyPressMsg{Text: k})
		if cmd == nil {
			t.Fatalf("%s did nothing", k)
		}
		msg := cmd()
		m.Update(msg)
		return string(msg.(noteMsg))
	}
	if got := run("o"); got != "Opened EMI-301" {
		t.Errorf("first o: %s", got)
	}
	if got := run("o"); got != "Opened PR #812" {
		t.Errorf("o again within 2s: %s", got)
	}
	m.linkAt = time.Now().Add(-3 * time.Second)
	if got := run("o"); got != "Opened EMI-301" {
		t.Errorf("o after 2s starts over: %s", got)
	}
	if got := run("O"); got != "Opened PR #812" {
		t.Errorf("O: %s", got)
	}
	if got := run("O"); got != "Opened EMI-301" {
		t.Errorf("O wraps: %s", got)
	}
	b, _ := os.ReadFile(log)
	want := "https://linear.app/example/issue/EMI-301\nhttps://github.com/o/r/pull/812\nhttps://linear.app/example/issue/EMI-301\nhttps://github.com/o/r/pull/812\nhttps://linear.app/example/issue/EMI-301\n"
	if string(b) != want {
		t.Errorf("opened:\n%s", b)
	}
	if out := plain(m, 100, 24); !strings.Contains(out, "Opened EMI-301") {
		t.Errorf("footer:\n%s", out)
	}
}

func TestPaneOpenWithoutLinksSaysSo(t *testing.T) {
	th, bin := setup(t)
	log := openStub(t, bin)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	if _, cmd := m.Update(tea.KeyPressMsg{Text: "o"}); cmd != nil {
		t.Error("o without links ran something")
	}
	if out := plain(m, 100, 24); !strings.Contains(out, "No links on a") {
		t.Errorf("\n%s", out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("open ran")
	}
}

func TestHelpFits16RowsAndListsTabAndLinks(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	key(m, "?")
	out := plain(m, 100, 16)
	lines := strings.Split(out, "\n")
	if len(lines) != 16 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, s := range []string{"Keys", "tab", "next, prev", "o  O", "╭", "╰", "quit", "any key closes", "shift-drag"} {
		if !strings.Contains(out, s) {
			t.Errorf("overlay lacks %q:\n%s", s, out)
		}
	}
	if !strings.HasPrefix(lines[0], " Desk") {
		t.Errorf("header scrolled off:\n%s", out)
	}
}

func TestFooterShowsTabAndLinkAtCommonWidths(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	txt, _ := os.ReadFile(cardPath("a"))
	os.WriteFile(cardPath("a"), []byte(strings.Replace(string(txt), "rex_session:", "links: EMI-1\nrex_session:", 1)), 0o644)
	m := newPane()
	for _, w := range []int{80, 100, 140} {
		out := plain(m, w, 14)
		foot := strings.Split(out, "\n")[13]
		if !strings.Contains(foot, "tab next") || !strings.Contains(foot, "o link") || len([]rune(foot)) > w {
			t.Errorf("width %d footer %q", w, foot)
		}
	}
}
