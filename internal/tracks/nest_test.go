package tracks

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	kidASess = "session:0020-kida-aaaaaa"
	kidBSess = "session:0021-kidb-bbbbbb"
)

func space(m *paneModel) { m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) }

// nestHome is leadHome with two threads of boss, a-kid and b-kid, each in a session of its own.
func nestHome(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = leadHome(t)
	for name, sess := range map[string]string{"a-kid": kidASess, "b-kid": kidBSess} {
		addThread(t, name)
		if err := setMeta(name, "rex_session", sess); err != nil {
			t.Fatal(err)
		}
	}
	return th, bin
}

func nestPane(t *testing.T, w, h int, states map[string]agentState) *paneModel {
	t.Helper()
	m := newPane()
	m.now = time.Now()
	for k, v := range states {
		m.rex.states[k] = v
	}
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// rowNames are the names the list shows, top to bottom.
func rowNames(m *paneModel) []string {
	var out []string
	for _, c := range m.cards {
		out = append(out, c.Name)
	}
	return out
}

func listLine(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if l, _, _ := strings.Cut(l, " │ "); strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func TestThreadsNestUnderTheirLeadFoldedByDefault(t *testing.T) {
	nestHome(t)
	m := nestPane(t, 120, 30, map[string]agentState{
		kidASess: {State: "working"},
		kidBSess: {State: "done", Msg: "pushed"},
	})
	if got := strings.Join(rowNames(m), ","); got != "boss,plain" {
		t.Fatalf("folded rows: %s", got)
	}
	out := plain(m, 120, 30)
	if strings.Contains(out, "a-kid") || strings.Contains(out, "b-kid") {
		t.Errorf("a folded lead lists its threads:\n%s", out)
	}
	if l := listLine(out, "boss"); !strings.Contains(l, "●✓") {
		t.Errorf("the lead's row has no glyph per thread, working then done: %q", l)
	}
	if l := listLine(out, "plain"); strings.Contains(l, "●") || strings.Contains(l, "✓") {
		t.Errorf("a track without threads shows glyphs: %q", l)
	}

	key(m, "down")
	key(m, "up")
	space(m)
	if got := strings.Join(rowNames(m), ","); got != "boss,a-kid,b-kid,plain" {
		t.Fatalf("unfolded rows: %s", got)
	}
	out = plain(m, 120, 30)
	for _, kid := range []string{"a-kid", "b-kid"} {
		l := listLine(out, kid)
		if !strings.Contains(l, " └ "+kid) {
			t.Errorf("%s is not indented under its lead: %q", kid, l)
		}
	}
	if !strings.Contains(listLine(out, "a-kid"), "working") || !strings.Contains(listLine(out, "b-kid"), "done") {
		t.Errorf("a thread's own row lost its state:\n%s", out)
	}
	if l := listLine(out, "boss"); !strings.Contains(l, "●✓") {
		t.Errorf("an unfolded lead keeps its glyphs: %q", l)
	}

	key(m, "down")
	if m.cur != "a-kid" {
		t.Fatalf("down from the lead lands on %q", m.cur)
	}
	space(m)
	if m.cur != "boss" || strings.Join(rowNames(m), ",") != "boss,plain" {
		t.Errorf("space on a thread folds its lead and selects it: cur %q rows %v", m.cur, rowNames(m))
	}
}

func TestSpaceShowsAt80ColumnsInTheFooterOnALeadAndInTheHelp(t *testing.T) {
	nestHome(t)
	m := nestPane(t, 80, 24, nil)
	lines := strings.Split(plain(m, 80, 24), "\n")
	if foot := lines[len(lines)-1]; m.cur != "boss" || !strings.Contains(foot, "space") {
		t.Errorf("footer on the lead at 80 columns: %q", foot)
	}
	key(m, "down")
	lines = strings.Split(plain(m, 80, 24), "\n")
	if foot := lines[len(lines)-1]; strings.Contains(foot, "space") {
		t.Errorf("footer on a track without threads offers space: %q", foot)
	}
	key(m, "?")
	if out := plain(m, 80, 24); !strings.Contains(out, "space") {
		t.Errorf("help at 80 columns lacks space:\n%s", out)
	}
}

func TestCloseQuestionKeepsHowManySessionsStayAt80Columns(t *testing.T) {
	closeHome(t, ownedIdle)
	m := nestPane(t, 80, 24, nil)
	key(m, "x")
	out := plain(m, 80, 24)
	if !strings.Contains(out, "Close boss and 2 threads? 1 session stays.") {
		t.Errorf("footer at 80 columns:\n%s", out)
	}
}

func TestSpaceOnATrackWithoutThreadsSaysSo(t *testing.T) {
	nestHome(t)
	m := nestPane(t, 120, 30, nil)
	key(m, "down") // plain
	if m.cur != "plain" {
		t.Fatalf("cur %q", m.cur)
	}
	space(m)
	if !strings.Contains(m.flash, "plain has no threads") || len(m.cards) != 2 {
		t.Errorf("flash %q rows %v", m.flash, rowNames(m))
	}
}

func TestFoldedThreadStaysFoldedAcrossReloadsAndUnfoldedOnesStay(t *testing.T) {
	nestHome(t)
	m := nestPane(t, 120, 30, nil)
	space(m)
	m.reload()
	if got := strings.Join(rowNames(m), ","); got != "boss,a-kid,b-kid,plain" {
		t.Errorf("a reload folded it: %s", got)
	}
	key(m, "down")
	m.unfolded["boss"] = false
	m.reload()
	if m.cur != "boss" {
		t.Errorf("the thread under the cursor left the list; the cursor is on %q, not its lead", m.cur)
	}
}

func TestOnlyTheLeadsAsksCountAndTabNeverLandsOnAThread(t *testing.T) {
	nestHome(t)
	if out, code := cardIn("asks:\n1. which database?\n2. which region?\n", "card", "a-kid"); code != 0 {
		t.Fatal(out)
	}
	states := map[string]agentState{kidASess: {State: "idle"}, kidBSess: {State: "idle"}}

	m := nestPane(t, 120, 30, states)
	if _, needs := m.counts(); needs != 0 {
		t.Errorf("a thread's asks count: %d need you", needs)
	}
	if h := plain(m, 120, 30); !strings.Contains(h, "0 for you") {
		t.Errorf("header:\n%s", h)
	}
	if items := needsOrder(m.all, states, nil, m.now); len(items) != 0 {
		t.Errorf("needsOrder lists %s", items[0].card.Name)
	}
	key(m, "tab")
	if m.cur != "boss" || !strings.Contains(m.flash, nothingNeedsYou) {
		t.Errorf("tab with only a thread's asks: cur %q flash %q", m.cur, m.flash)
	}

	if out, code := cardIn("asks:\n1. merge it?\n", "card", "boss"); code != 0 {
		t.Fatal(out)
	}
	m = nestPane(t, 120, 30, states)
	if _, needs := m.counts(); needs != 1 {
		t.Errorf("%d need you, want the lead alone", needs)
	}
	if h := plain(m, 120, 30); !strings.Contains(h, "◆ 1 for you") {
		t.Errorf("header:\n%s", h)
	}
	space(m) // unfolded, the thread's row shows its asks but still does not count
	items := needsOrder(m.all, states, nil, m.now)
	if len(items) != 1 || items[0].card.Name != "boss" || items[0].why != "1 ask" {
		t.Errorf("needsOrder: %+v", items)
	}
	key(m, "down")
	key(m, "tab")
	if m.cur != "boss" {
		t.Errorf("tab from a thread goes to %q, want the lead", m.cur)
	}
	if h := plain(m, 120, 30); !strings.Contains(h, "◆ 1 for you") {
		t.Errorf("header with the lead's threads shown:\n%s", h)
	}
}

func TestABlockedOrErroredThreadNeedsYouAndTabUnfoldsItsLead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state agentState
		why   string
	}{
		{"permission", agentState{State: "blocked", Kind: "permission", Msg: "Allow docker compose up?"}, "permission · thread of boss"},
		{"question", agentState{State: "blocked", Kind: "question", Msg: "Which carrier?"}, "question · thread of boss"},
		{"error", agentState{State: "error", Msg: "boom"}, "error · thread of boss"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nestHome(t)
			if out, code := cardIn("asks:\n1. which database?\n", "card", "a-kid"); code != 0 {
				t.Fatal(out)
			}
			states := map[string]agentState{kidASess: tc.state, kidBSess: {State: "idle"}}
			m := nestPane(t, 120, 30, states)
			if _, needs := m.counts(); needs != 1 {
				t.Errorf("%d need you, want the blocked thread", needs)
			}
			if h := plain(m, 120, 30); !strings.Contains(h, "◆ 1 for you") {
				t.Errorf("header:\n%s", h)
			}
			items := needsOrder(m.all, states, nil, m.now)
			if len(items) != 1 || items[0].card.Name != "a-kid" || !strings.HasPrefix(items[0].why, tc.why) || strings.Contains(items[0].why, "ask") {
				t.Fatalf("needsOrder: %+v", items)
			}
			if got := strings.Join(rowNames(m), ","); got != "boss,plain" {
				t.Fatalf("rows before tab: %s", got)
			}
			key(m, "tab")
			if m.cur != "a-kid" || !m.unfolded["boss"] || strings.Join(rowNames(m), ",") != "boss,a-kid,b-kid,plain" {
				t.Errorf("tab: cur %q rows %v", m.cur, rowNames(m))
			}
			key(m, "tab")
			if m.cur != "a-kid" {
				t.Errorf("a second tab leaves the only track that needs you: %q", m.cur)
			}
		})
	}
}

func TestNextFollowsTheSameRule(t *testing.T) {
	nestHome(t)
	if out, code := cardIn("asks:\n1. which database?\n", "card", "a-kid"); code != 0 {
		t.Fatal(out)
	}
	if out, code := do("next"); code != 0 || !strings.Contains(out, nothingNeedsYou) {
		t.Errorf("tracks next with only a thread's ask: %d %q", code, out)
	}
}

func TestAThreadWhoseLeadIsGoneIsAnOrdinaryTrackAndCounts(t *testing.T) {
	nestHome(t)
	if out, code := cardIn("asks:\n1. which database?\n", "card", "a-kid"); code != 0 {
		t.Fatal(out)
	}
	if _, _, err := closeTrack("boss", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	m := nestPane(t, 120, 30, map[string]agentState{kidASess: {State: "idle"}, kidBSess: {State: "idle"}})
	if got := strings.Join(rowNames(m), ","); got != "a-kid,b-kid,plain" {
		t.Errorf("orphans are listed as tracks: %s", got)
	}
	if _, needs := m.counts(); needs != 1 {
		t.Errorf("an orphan's ask must count: %d need you", needs)
	}
	if items := needsOrder(m.all, nil, nil, m.now); len(items) != 1 || items[0].card.Name != "a-kid" {
		t.Errorf("needsOrder: %+v", items)
	}
}

func TestMoreThreadsThanGlyphsShowAPlus(t *testing.T) {
	nestHome(t)
	for _, n := range []string{"c-kid", "d-kid", "e-kid", "f-kid", "g-kid"} {
		addThread(t, n)
	}
	m := nestPane(t, 120, 30, nil)
	if l := listLine(plain(m, 120, 30), "boss"); !strings.Contains(l, "······+") {
		t.Errorf("seven threads, six glyphs and a plus: %q", l)
	}
}

const (
	closeSessions = `{"sessions":[{"session_id":"` + leadSess + `","label":"boss"},{"session_id":"` + kidASess + `","label":"a"},{"session_id":"` + kidBSess + `","label":"b"}]}`
)

// closeHome has boss with a-kid and b-kid, rex answering for their sessions with one idle pane
// in each, and an ordinary track that also names b-kid's session.
func closeHome(t *testing.T, status string) (bin string) {
	t.Helper()
	stubChildren(t)
	_, bin = nestHome(t)
	endStub(t, bin, closeSessions, oneBlock)
	blockFile(t, bin, "status", "block:aaa", status)
	blockFile(t, bin, "fg", "block:aaa", `{"last_exit":{"exit_code":0}}`)
	rawCard(t, "other", nil)
	if err := setMeta("other", "rex_session", kidBSess); err != nil {
		t.Fatal(err)
	}
	return bin
}

func killed(bin, sess string) bool {
	return strings.Contains(rexCalls(bin), "session kill "+sess)
}

func TestClosingALeadSaysHowManyThreadsAndClosesThemOnY(t *testing.T) {
	bin := closeHome(t, ownedIdle)
	m := nestPane(t, 160, 30, nil)
	key(m, "x")
	want := "Close boss and 2 threads? 1 session stays. Archives the card; ends its session if nothing in it can be lost."
	if m.confirmQ != want {
		t.Errorf("question:\n got %q\nwant %q", m.confirmQ, want)
	}
	if out := plain(m, 160, 30); !strings.Contains(out, "Close boss and 2 threads?") {
		t.Errorf("the footer does not ask:\n%s", out)
	}
	if v := loadCardMust(t, "a-kid"); v.Name == "" {
		t.Fatal("a-kid is not open before y")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	msg := cmd().(closedMsg)
	m.Update(msg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if !strings.HasPrefix(msg.note, "Closed boss and its 2 threads") || !strings.Contains(msg.note, "b-kid; kept its Rex session: track other uses it") {
		t.Errorf("note: %q", msg.note)
	}
	for _, name := range []string{"boss", "a-kid", "b-kid"} {
		if exists(cardPath(name)) {
			t.Errorf("%s is still open", name)
		}
	}
	if !exists(cardPath("plain")) || !exists(cardPath("other")) {
		t.Error("closing a lead closed a track that is not its thread")
	}
	if !killed(bin, leadSess) || !killed(bin, kidASess) {
		t.Errorf("the idle sessions of the lead and a-kid were not ended:\n%s", rexCalls(bin))
	}
	if killed(bin, kidBSess) {
		t.Errorf("b-kid's session, which track other uses, was ended:\n%s", rexCalls(bin))
	}
}

func TestClosingALeadNeverEndsASessionCloseWouldKeep(t *testing.T) {
	bin := closeHome(t, `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"}]}`)
	m := nestPane(t, 160, 30, map[string]agentState{kidASess: {State: "working"}, kidBSess: {State: "working"}, leadSess: {State: "working"}})
	key(m, "x")
	if !strings.Contains(m.confirmQ, "2 sessions stay") {
		t.Errorf("question: %q", m.confirmQ)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	msg := cmd().(closedMsg)
	if msg.err != nil || !strings.Contains(msg.note, "a-kid; kept its Rex session: its agent is working") {
		t.Errorf("note %q err %v", msg.note, msg.err)
	}
	if calls := rexCalls(bin); strings.Contains(calls, "kill") {
		t.Errorf("a busy agent's session was ended:\n%s", calls)
	}
	if exists(cardPath("a-kid")) || exists(cardPath("boss")) {
		t.Error("the cards are not archived")
	}
}

func TestClosingAThreadAloneIsPlainClose(t *testing.T) {
	closeHome(t, ownedIdle)
	m := nestPane(t, 160, 30, nil)
	space(m)
	key(m, "down")
	key(m, "x")
	if m.cur != "a-kid" || strings.Contains(m.confirmQ, "thread") {
		t.Errorf("cur %q question %q", m.cur, m.confirmQ)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	if msg := cmd().(closedMsg); msg.err != nil || !strings.HasPrefix(msg.note, "Closed a-kid") || !exists(cardPath("boss")) {
		t.Errorf("note %q err %v", msg.note, msg.err)
	}
}

func TestALeadWithoutThreadsClosesAsBefore(t *testing.T) {
	closeHome(t, ownedIdle)
	if _, _, err := closeTrack("a-kid", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := closeTrack("b-kid", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	m := nestPane(t, 160, 30, nil)
	key(m, "x")
	if strings.Contains(m.confirmQ, "thread") {
		t.Errorf("question: %q", m.confirmQ)
	}
}

func TestCloseLeadCountsAClosedThreadAsClosedAndFollowsARename(t *testing.T) {
	closeHome(t, ownedIdle)
	if _, _, err := closeTrack("a-kid", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if out, code := do("rename", "b-kid", "b-renamed"); code != 0 {
		t.Fatalf("rename: %d %s", code, out)
	}
	note, err := closeLead("boss", []string{"a-kid", "b-kid"}, time.Now(), "")
	if err != nil || !strings.HasPrefix(note, "Closed boss and its 2 threads") {
		t.Fatalf("note %q err %v", note, err)
	}
	for _, name := range []string{"boss", "b-renamed"} {
		if exists(cardPath(name)) {
			t.Errorf("%s is still open", name)
		}
	}
}

func TestCloseCommandNamesTheThreadsItLeavesOpen(t *testing.T) {
	closeHome(t, ownedIdle)
	out, code := do("close", "boss")
	if code != 0 || !strings.Contains(out, "left open: 2 threads of boss: a-kid, b-kid") {
		t.Errorf("close boss: %d %q", code, out)
	}
	if out, code := do("close", "a-kid"); code != 0 || strings.Contains(out, "left open") {
		t.Errorf("close a-kid: %d %q", code, out)
	}
}

func TestCloseLeadKeepsTheLeadWhenAThreadWillNotClose(t *testing.T) {
	closeHome(t, ownedIdle)
	if err := os.Remove(cardPath("a-kid")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	note, err := closeLead("boss", []string{"no-such-thread"}, time.Now(), "")
	if err == nil || !strings.Contains(err.Error(), "boss stays open") || note != "" {
		t.Errorf("note %q err %v", note, err)
	}
	if !exists(cardPath("boss")) {
		t.Error("the lead was closed with a thread unclosed")
	}
}
