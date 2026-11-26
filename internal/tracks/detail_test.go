package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// detailCard is a card whose sections the tests fill in.
func detailCard(name, mode, sess, extra, goal, where, needs string) string {
	return "---\nname: " + name + "\nmode: " + mode + "\ntier: 2\ndir: %DIR%\nrex_session: " + sess + "\n" + extra +
		"---\n## Goal\n" + goal + "\n\n## Where it is\n" + where + "\n\n## Needs me\n" + needs + "\n## Standing orders\n- x\n"
}

// detailLines is the detail column of the render, trailing blanks trimmed.
func detailLines(m *paneModel, w, h int) []string {
	var out []string
	for _, l := range strings.Split(plain(m, w, h), "\n") {
		if r, ok := rightCol(l); ok {
			out = append(out, strings.TrimRight(r, " "))
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func indexOfPrefix(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

func TestDetailOrderAndSpacing(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "links: PAN-5\n",
		"Ship the thing.", "Refunds are done.\nCI is green.\nTier 2: product code.", "1. Merge? Recommend: yes.\n"))
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "done", Msg: "Pane built."}
	m.Update(sinceMsg{name: "alpha", lines: []string{"a1b2c3 Add pane"}, at: m.now.Add(-3 * time.Hour)})
	lines := detailLines(m, 120, 30)
	want := []string{
		"alpha  T2 · focus",
		"✓ done · Pane built.",
		"",
		"Refunds are done.",
		"CI is green.",
		"",
		"┃ Needs me  1 accept · r reply",
		"┃",
		"┃ 1  Merge?",
		"┃    → yes.",
		"",
		"GOAL",
		"Ship the thing.",
		"Links  PAN-5  o open",
		"",
		"Since you last looked · 3h",
		"a1b2c3 Add pane",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("detail:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "" && lines[i-1] == "" {
			t.Errorf("two blank lines in a row at %d", i)
		}
	}
	// Every section starts at the left edge, the bar's column included.
	for _, p := range []string{"Refunds", "┃ Needs me", "GOAL", "Since you"} {
		if indexOfPrefix(lines, p) < 0 {
			t.Errorf("section %q does not start at the left edge:\n%s", p, strings.Join(lines, "\n"))
		}
	}
	if strings.Contains(strings.Join(lines, "\n"), "Where it is") {
		t.Error("Where it is has no heading")
	}
}

func TestDetailNeedsBlockSitsFlushWithoutTheBar(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", "1. Merge? Recommend: yes.\n"))
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "working", Msg: "busy"} // asks on a working agent do not need him
	lines := detailLines(m, 120, 30)
	if indexOfPrefix(lines, "Needs me") < 0 || indexOfPrefix(lines, "1  Merge?") < 0 {
		t.Errorf("Needs me not flush left:\n%s", strings.Join(lines, "\n"))
	}
}

func TestWhereBlockHeadlineBoldRestDim(t *testing.T) {
	got := whereBlock("Headline of it.\nTier 2: x\nDetail one.\nDetail two.", 76)
	if len(got) != 3 {
		t.Fatalf("lines: %q", got)
	}
	if !strings.Contains(got[0], "\x1b[1m") || strings.Contains(got[0], "\x1b[2m") {
		t.Errorf("headline not bold: %q", got[0])
	}
	for _, l := range got[1:] {
		if !strings.Contains(l, "\x1b[2m") || strings.Contains(l, "\x1b[1m") {
			t.Errorf("detail not dim: %q", l)
		}
	}
	if long := whereBlock("A long headline that wraps around the narrow measure here.\nDetail.", 20); !strings.Contains(long[1], "\x1b[1m") || !strings.Contains(long[len(long)-1], "\x1b[2m") {
		t.Errorf("a wrapped headline stays bold and the detail dim: %q", long)
	}
}

func TestDetailDoneAgentSaysDone(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", ""))
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "done"}
	if lines := detailLines(m, 120, 30); lines[1] != "✓ done" {
		t.Errorf("done without a message: %q", lines[1])
	}
	m.rex.states["s1"] = agentState{State: "done", Msg: "All merged."}
	if lines := detailLines(m, 120, 30); lines[1] != "✓ done · All merged." {
		t.Errorf("done with a message: %q", lines[1])
	}
	m.rex.states["s1"] = agentState{State: "working", Msg: "Running tests"}
	if lines := detailLines(m, 120, 30); lines[1] != "● Running tests" {
		t.Errorf("working keeps its message: %q", lines[1])
	}
}

func TestDetailWaitingRows(t *testing.T) {
	th, _ := setup(t)
	extra := "waiting_on: Robin to review D3\ncheck: pr 51 approved\n"
	putCard(t, th, "w", detailCard("w", "waiting", "", extra, "G", "Waits on review.", ""))
	now := time.Now()
	if err := saveChecks(map[string]checkState{"w: pr 51 approved": {Answer: true, RanAt: now.Add(-4 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	m := newPane()
	m.now = now
	lines := detailLines(m, 120, 30)
	on, chk := indexOfPrefix(lines, "WAITING ON  "), indexOfPrefix(lines, "CHECK       ")
	if on < 0 || chk != on+1 || on != indexOfPrefix(lines, "Waits on review.")+1 {
		t.Fatalf("rows not directly under Where it is:\n%s", strings.Join(lines, "\n"))
	}
	if lines[on] != "WAITING ON  Robin to review D3" || lines[chk] != "CHECK       pr 51 approved · yes · ran 4m ago" {
		t.Errorf("rows:\n%s\n%s", lines[on], lines[chk])
	}

	// A file written before ran_at existed still reads, and says nothing about when.
	foldBack(t)
	if err := os.WriteFile(checksPath(), []byte(`{"w: pr 51 approved":{"answer":false,"asked":"2026-11-19T10:00:00Z"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if lines := detailLines(m, 120, 30); lines[indexOfPrefix(lines, "CHECK")] != "CHECK       pr 51 approved · no" {
		t.Errorf("old checks.json:\n%s", strings.Join(lines, "\n"))
	}

	// No answer recorded yet.
	foldBack(t)
	os.Remove(checksPath())
	m.reload()
	if lines := detailLines(m, 120, 30); lines[indexOfPrefix(lines, "CHECK")] != "CHECK       pr 51 approved · not run yet" {
		t.Errorf("no checks.json:\n%s", strings.Join(lines, "\n"))
	}

	// A check outside the vocabulary is never run.
	putCard(t, th, "w", detailCard("w", "waiting", "", "waiting_on: review\ncheck: the build looks fine\n", "G", "Waits.", ""))
	m.reload()
	if lines := detailLines(m, 120, 30); lines[indexOfPrefix(lines, "CHECK")] != "CHECK       the build looks fine · not a check tracks runs" {
		t.Errorf("unknown check:\n%s", strings.Join(lines, "\n"))
	}

	// Either row goes when its field is empty; other modes show neither.
	putCard(t, th, "w", detailCard("w", "waiting", "", "waiting_on: review\n", "G", "Waits.", ""))
	m.reload()
	lines = detailLines(m, 120, 30)
	if indexOfPrefix(lines, "WAITING ON") < 0 || indexOfPrefix(lines, "CHECK") >= 0 {
		t.Errorf("without a check:\n%s", strings.Join(lines, "\n"))
	}
	putCard(t, th, "w", detailCard("w", "waiting", "", "check: pr 51 approved\n", "G", "Waits.", ""))
	m.reload()
	lines = detailLines(m, 120, 30)
	if indexOfPrefix(lines, "WAITING ON") >= 0 || indexOfPrefix(lines, "CHECK") < 0 {
		t.Errorf("without waiting_on:\n%s", strings.Join(lines, "\n"))
	}
	putCard(t, th, "w", detailCard("w", "focus", "", extra, "G", "Waits.", ""))
	m.reload()
	lines = detailLines(m, 120, 30)
	if indexOfPrefix(lines, "WAITING ON") >= 0 || indexOfPrefix(lines, "CHECK") >= 0 {
		t.Errorf("a focus card shows waiting rows:\n%s", strings.Join(lines, "\n"))
	}
}

func TestWaitingRowsWrapUnderTheirValue(t *testing.T) {
	c := &Card{Name: "w", Meta: map[string]string{"waiting_on": "a very long thing to wait for that wraps around", "check": "pr 5 merged"}}
	for _, l := range waitingRows(c, nil, time.Now(), 30) {
		if got := len([]rune(l)); got < len(waitingLabel) {
			t.Errorf("short row %q", l)
		}
	}
	rows := waitingRows(c, nil, time.Now(), 30)
	if !strings.HasPrefix(ansi.Strip(rows[1]), strings.Repeat(" ", len(waitingLabel))) {
		t.Errorf("wrapped line not hung under the value: %q", rows[1])
	}
}

func TestCheckRecordsWhenItRan(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 approved")
	var ans string
	eval := func(string, check) (string, error) { return ans, nil }

	before := time.Now().Add(-time.Second)
	ans = no
	checks(t, false, eval)
	st, _ := loadChecks()
	got := st["w: pr 5 approved"]
	if got.Answer || got.RanAt.Before(before) {
		t.Errorf("an unchanged no records when it ran: %+v", got)
	}

	ans = pending
	first := got.RanAt
	time.Sleep(2 * time.Millisecond)
	checks(t, false, eval)
	if st, _ = loadChecks(); !st["w: pr 5 approved"].RanAt.Equal(first) {
		t.Errorf("a pending answer moved RanAt: %+v", st)
	}

	ans = yes
	checks(t, true, eval)
	if st, _ = loadChecks(); !st["w: pr 5 approved"].RanAt.Equal(first) {
		t.Errorf("a dry run wrote RanAt: %+v", st)
	}
	checks(t, false, eval)
	st, _ = loadChecks()
	got = st["w: pr 5 approved"]
	if !got.Answer || !got.RanAt.After(first) || got.Asked == "" {
		t.Errorf("a change records the answer and when: %+v", got)
	}

	// The same answer again moves only RanAt, and keeps when the ask was added.
	asked, ran := got.Asked, got.RanAt
	time.Sleep(2 * time.Millisecond)
	checks(t, false, eval)
	st, _ = loadChecks()
	if got = st["w: pr 5 approved"]; got.Asked != asked || !got.RanAt.After(ran) {
		t.Errorf("unchanged yes: %+v (was asked %s ran %s)", got, asked, ran)
	}

	// A file written before ran_at existed reads, and a first run fills it in.
	raw, _ := os.ReadFile(checksPath())
	if !dbExists() && !strings.Contains(string(raw), `"ran_at"`) {
		t.Errorf("ran_at not written:\n%s", raw)
	}
	foldBack(t)
	if err := os.WriteFile(checksPath(), []byte(`{"w: pr 5 approved":{"answer":true,"asked":"`+asked+`"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := loadChecks(); err != nil || !st["w: pr 5 approved"].RanAt.IsZero() {
		t.Fatalf("old file: %v %+v", err, st)
	}
	checks(t, false, eval)
	if st, _ = loadChecks(); st["w: pr 5 approved"].RanAt.IsZero() {
		t.Errorf("the first run on an old file leaves RanAt empty")
	}

	// A state that never ran writes no date at all.
	foldBack(t)
	if err := saveChecks(map[string]checkState{"x: y": {Answer: true}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(checksPath()); strings.Contains(string(b), "ran_at") || strings.Contains(string(b), "0001-01-01") {
		t.Errorf("zero RanAt written:\n%s", b)
	}
}

// answeredNow stamps the card as the hook would, later than its updated: stamp.
func answeredNow(t *testing.T, name, asks string) {
	t.Helper()
	at := time.Now().UTC()
	kv := [][2]string{{"updated", at.Add(-time.Hour).Format(time.RFC3339)}, {"answered", at.Format(time.RFC3339)}}
	if asks != "" {
		kv = append(kv, [2]string{"answered_asks", at.Format(time.RFC3339) + " " + asks})
	}
	if err := setMetas(name, kv); err != nil {
		t.Fatal(err)
	}
}

func TestAnsweredStateReplacesTheAsks(t *testing.T) {
	th, _ := setup(t)
	needs := "1. Merge it? Recommend: yes.\n2. Flag first? Recommend: no.\n"
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", needs))
	answeredNow(t, "alpha", "1,2")
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "working", Msg: "on it"}
	lines := strings.Join(detailLines(m, 120, 30), "\n")
	if !strings.Contains(lines, "Needs me\nAnswered 1, 2 · agent working\n") || strings.Contains(lines, "Merge it?") {
		t.Errorf("answered block:\n%s", lines)
	}

	// Digits do nothing, and the footer says why; r still opens a free reply.
	for _, k := range []string{"1", "2"} {
		if cmd := press(m, k); cmd != nil || m.reply != nil {
			t.Fatalf("%s acted on an answered track", k)
		}
		if m.status != "Already answered; waiting for the agent" || !strings.Contains(plain(m, 120, 30), "Already answered; waiting for the agent") {
			t.Fatalf("footer after %s: %q", k, m.status)
		}
	}
	m.rex.states["s1"] = agentState{State: "idle"}
	press(m, "r")
	if m.reply == nil || m.reply.asks != nil {
		t.Errorf("r did not open a free reply")
	}
	press(m, "esc")

	// The agent rewrites the card: updated moves past answered and the asks come back.
	if err := setMetas("alpha", [][2]string{{"updated", time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	lines = strings.Join(detailLines(m, 120, 30), "\n")
	if strings.Contains(lines, "Answered") || !strings.Contains(lines, "1  Merge it?") {
		t.Errorf("asks after the card changed:\n%s", lines)
	}
	press(m, "1")
	if m.reply == nil || !m.reply.prefilled() {
		t.Errorf("1 does not accept once the card changed: %q", m.status)
	}
}

func TestAnsweredWithoutRecordedNumbers(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", "1. Merge it? Recommend: yes.\n"))
	answeredNow(t, "alpha", "")
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "working"}
	if lines := strings.Join(detailLines(m, 120, 30), "\n"); !strings.Contains(lines, "Answered · agent working") {
		t.Errorf("free answer:\n%s", lines)
	}

	// Numbers recorded for an earlier answer do not describe this one.
	c := loadCardMust(t, "alpha")
	stale := cardAnswered(c).Add(-time.Hour).UTC().Format(time.RFC3339) + " 1"
	if err := setMetas("alpha", [][2]string{{"answered_asks", stale}}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if lines := strings.Join(detailLines(m, 120, 30), "\n"); !strings.Contains(lines, "Answered · agent working") || strings.Contains(lines, "Answered 1") {
		t.Errorf("stale numbers shown:\n%s", lines)
	}
}

func TestAnsweredStampOlderThanTheWindowShowsAsksAgain(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", "1. Merge it? Recommend: yes.\n"))
	old := time.Now().Add(-answeredFor - time.Minute).UTC()
	if err := setMetas("alpha", [][2]string{{"updated", old.Add(-time.Hour).Format(time.RFC3339)}, {"answered", old.Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "idle"}
	if lines := strings.Join(detailLines(m, 120, 30), "\n"); strings.Contains(lines, "Answered") {
		t.Errorf("a stale stamp still hides the asks:\n%s", lines)
	}
}

func TestSendRecordsTheAsksItAccepted(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Ok? Recommend: yes.\n2. Two? Recommend: no.\n3. Three? Recommend: yes.\n")
	agentIs(t, bin, "done", "")
	m := newPane()
	settle(t, m)

	press(m, "1")
	press(m, "3")
	cmd := press(m, "enter")
	if cmd == nil {
		t.Fatalf("no send: %q", m.status)
	}
	m.Update(cmd())
	c := loadCardMust(t, "a")
	stamp, list, _ := strings.Cut(c.Meta["answered_asks"], " ")
	if _, err := time.Parse(time.RFC3339, stamp); err != nil || list != "1,3" {
		t.Fatalf("answered_asks %q", c.Meta["answered_asks"])
	}
	// The hook's stamp lands a moment later, and the pair then reads as one answer.
	if err := setMetas("a", [][2]string{{"answered", time.Now().UTC().Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	if got := cardAnsweredAsks(loadCardMust(t, "a")); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("recorded asks %v", got)
	}

	// A free reply, or an edited prefill, wipes the numbers of the one before.
	settle(t, m)
	press(m, "1")
	typeText(m, " but later")
	m.Update(press(m, "enter")())
	if v := loadCardMust(t, "a").Meta["answered_asks"]; v != "" {
		t.Errorf("an edited reply kept %q", v)
	}
}

func TestSendLeavesNoAskRecordOnACardNeverAnsweredFromThePane(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Ok? Recommend: yes.\n")
	agentIs(t, bin, "done", "")
	m := newPane()
	settle(t, m)
	sendFrom(t, m, "just a thought")
	if strings.Contains(readCard("a"), "answered_asks") {
		t.Errorf("a free reply added a line:\n%s", readCard("a"))
	}
}

func TestStopAndSessionStartClearTheRecordedAsks(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "1. Pick one? Recommend: A.\n")
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 A","cwd":"/c"}`)
	if strings.Contains(readFile(cardPath("tr")), "answered_asks") {
		t.Fatalf("the hook added a line:\n%s", readFile(cardPath("tr")))
	}
	if err := setMetas("tr", [][2]string{{"answered_asks", time.Now().UTC().Format(time.RFC3339) + " 1"}}); err != nil {
		t.Fatal(err)
	}
	hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered"] != "" || c.Meta["answered_asks"] != "" {
		t.Errorf("Stop left %q %q", c.Meta["answered"], c.Meta["answered_asks"])
	}

	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 A","cwd":"/c"}`)
	setMetas("tr", [][2]string{{"answered_asks", time.Now().UTC().Format(time.RFC3339) + " 1"}})
	hook(t, `{"hook_event_name":"SessionStart","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered_asks"] != "" {
		t.Errorf("SessionStart left %q", c.Meta["answered_asks"])
	}
}

func TestDetailEmptyStates(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "empty", "---\nname: empty\nmode: focus\ndir: %DIR%\n---\n")
	m := newPane()
	m.now = time.Now()
	lines := detailLines(m, 120, 30)
	got := strings.Join(lines, "\n")
	want := "empty  focus\nno agent · enter starts one\n\nNot started.\n\nGOAL\nNo goal yet."
	if !strings.HasPrefix(got, want) {
		t.Errorf("empty card:\n%s\nwant prefix:\n%s", got, want)
	}
	for _, h := range []string{"Where it is", "Needs me", "Reply", "Links"} {
		if strings.Contains(got, h) {
			t.Errorf("empty heading %q:\n%s", h, got)
		}
	}
	if !strings.Contains(m.render(), "\x1b[2mno agent") && !strings.Contains(m.render(), "\x1b[2m"+"no agent") {
		t.Error("the no-agent line is not dim")
	}

	// A new card carries the placeholder as its Where it is text: it reads the same.
	putCard(t, th, "empty", detailCard("empty", "focus", "", "", "", notStarted, ""))
	m.reload()
	if got := strings.Join(detailLines(m, 120, 30)[1:], "\n"); !strings.HasPrefix(got, strings.SplitN(want, "\n", 2)[1]) {
		t.Errorf("placeholder card:\n%s", got)
	}
	if b := whereBlock(notStarted, 76); !strings.Contains(b[0], "\x1b[2m") || strings.Contains(b[0], "\x1b[1m") {
		t.Errorf("the placeholder is not dim: %q", b[0])
	}
}

func TestDetailGoalIsDimAndClamped(t *testing.T) {
	th, _ := setup(t)
	goal := strings.Repeat("word ", 60)
	putCard(t, th, "g", detailCard("g", "focus", "", "", goal, "W", ""))
	m := newPane()
	m.now = time.Now()
	lines := detailLines(m, 120, 30)
	i := indexOfPrefix(lines, "GOAL")
	if i < 0 || lines[i+goalRows+1] != "" && !strings.HasPrefix(lines[i+goalRows+1], "Since") {
		t.Fatalf("goal rows:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasSuffix(lines[i+goalRows], "…") {
		t.Errorf("clamped goal lacks the ellipsis: %q", lines[i+goalRows])
	}
	for _, l := range strings.Split(m.render(), "\n") {
		if strings.Contains(l, "word word") && !strings.Contains(l, "\x1b[2m") {
			t.Errorf("goal not dim: %q", l)
		}
	}
}

func TestSinceUsesTheListsLastSeenTime(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", ""))
	opened, typed := time.Now().Add(-5*time.Hour).UTC(), time.Now().Add(-90*time.Minute).UTC()
	appendEvent(Event{At: opened, Event: "open", Track: "alpha"})
	appendEvent(Event{At: typed, Event: "prompt", RexSession: "s1", Track: "alpha", Kind: "go"})
	m := newPane()
	m.now = time.Now()

	at, seen := m.lastSeen(m.cards[0])
	if !seen || !at.Equal(typed) {
		t.Fatalf("lastSeen %v %v, want %v", at, seen, typed)
	}
	msg := m.sinceCmd()().(sinceMsg)
	if !msg.at.Equal(at) || msg.first {
		t.Errorf("since says %v first=%v, the dot says %v", msg.at, msg.first, at)
	}
	m.Update(msg)
	if lines := strings.Join(detailLines(m, 120, 30), "\n"); !strings.Contains(lines, "Since you last looked · 1h") {
		t.Errorf("title:\n%s", lines)
	}

	// Only a prompt, never opened: seen, not "first look".
	putCard(t, th, "beta", detailCard("beta", "background", "s2", "", "G", "W", ""))
	appendEvent(Event{At: typed, Event: "prompt", RexSession: "s2", Track: "beta", Kind: "go"})
	m = newPane()
	m.now = time.Now()
	m.cur, m.idx = "beta", slicesIndexName(m, "beta")
	if msg := m.sinceCmd()().(sinceMsg); msg.first || !msg.at.Equal(typed) {
		t.Errorf("prompt-only track: %+v", msg)
	}

	// A system prompt is not Emil looking.
	putCard(t, th, "gamma", detailCard("gamma", "background", "s3", "", "G", "W", ""))
	appendEvent(Event{At: typed, Event: "prompt", RexSession: "s3", Track: "gamma", Kind: "system"})
	m = newPane()
	m.cur, m.idx = "gamma", slicesIndexName(m, "gamma")
	if msg := m.sinceCmd()().(sinceMsg); !msg.first {
		t.Errorf("a system prompt counted as a look: %+v", msg)
	}
}

func slicesIndexName(m *paneModel, name string) int {
	for i, c := range m.cards {
		if c.Name == name {
			return i
		}
	}
	return 0
}

func TestSinceLogFromTakesTheGivenTime(t *testing.T) {
	th, bin := setup(t)
	argv := filepath.Join(bin, "git.args")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" > "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	if _, err := sinceLogFrom(&Card{Name: "x", Meta: map[string]string{"dir": th}}, at); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(argv); !strings.Contains(string(got), "--since=2026-11-20T09:30:00Z") {
		t.Errorf("git asked for %q", got)
	}
}

// stampAnswered stamps the card answered ago, with updated: an hour before that.
func stampAnswered(t *testing.T, name string, ago time.Duration) {
	t.Helper()
	at := time.Now().Add(-ago).UTC()
	if err := setMetas(name, [][2]string{{"updated", at.Add(-time.Hour).Format(time.RFC3339)}, {"answered", at.Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
}

func TestAnsweredHoldsOnlyWhileTheAgentTookIt(t *testing.T) {
	needs := "1. Merge it? Recommend: yes.\n"
	for _, c := range []struct {
		name     string
		state    *agentState
		ago      time.Duration
		answered bool
	}{
		{"working", &agentState{State: "working"}, 29 * time.Minute, true},
		{"blocked", &agentState{State: "blocked", Kind: "question"}, 29 * time.Minute, true},
		{"idle", &agentState{State: "idle"}, 29 * time.Minute, false},
		{"error", &agentState{State: "error"}, 29 * time.Minute, false},
		{"exited", &agentState{Exited: true}, 29 * time.Minute, false},
		{"no agent", nil, 29 * time.Minute, false},
		{"idle just after Enter", &agentState{State: "idle"}, time.Second, true},
		{"no agent just after Enter", nil, time.Second, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, _ := setup(t)
			putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", needs))
			stampAnswered(t, "alpha", c.ago)
			m := newPane()
			m.now = time.Now()
			if c.state != nil {
				m.rex.states["s1"] = *c.state
			}
			lines := strings.Join(detailLines(m, 120, 30), "\n")
			if got := strings.Contains(lines, "Answered"); got != c.answered {
				t.Errorf("answered line %v, want %v:\n%s", got, c.answered, lines)
			}
			press(m, "1")
			if opened := m.reply != nil; opened == c.answered {
				t.Errorf("digit opened reply %v, status %q", opened, m.status)
			}
		})
	}
}

func TestCheckAsksStayAcceptableWhileAnswered(t *testing.T) {
	th, _ := setup(t)
	needs := "1. Check \"pr 5 approved\" is now yes. Recommend: set mode to focus.\n"
	putCard(t, th, "alpha", detailCard("alpha", "waiting", "s1", "", "G", "W", needs))
	stampAnswered(t, "alpha", time.Second)
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "working"}
	press(m, "1")
	if !strings.HasPrefix(m.status, "Applied") || loadCardMust(t, "alpha").mode() != "focus" {
		t.Errorf("check ask refused: %q", m.status)
	}
}

func TestAgentLineNeedsNoSessionToSayNoAgent(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", ""))
	m := newPane()
	m.now = time.Now()
	if lines := detailLines(m, 120, 30); lines[1] != "" {
		t.Errorf("a session with no state yet shows %q", lines[1])
	}
	putCard(t, th, "alpha", detailCard("alpha", "focus", "", "", "G", "W", ""))
	m.reload()
	if lines := detailLines(m, 120, 30); lines[1] != "no agent · enter starts one" {
		t.Errorf("no session shows %q", lines[1])
	}
}

func TestWaitingOnIsCappedAtTwoRows(t *testing.T) {
	c := &Card{Name: "w", Meta: map[string]string{"waiting_on": strings.Repeat("a long thing to wait for ", 20)}}
	rows := waitingRows(c, nil, time.Now(), 40)
	if len(rows) != waitingRowsMax || !strings.HasSuffix(ansi.Strip(rows[1]), "…") {
		t.Errorf("rows: %q", rows)
	}
}

func TestAnsweredLineAndEditorAreSeparated(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", "1. Merge it? Recommend: yes.\n"))
	stampAnswered(t, "alpha", time.Second)
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "working"}
	press(m, "r")
	lines := strings.Join(detailLines(m, 120, 30), "\n")
	if !strings.Contains(lines, "Answered · agent working\n\n›") {
		t.Errorf("no blank row before the editor:\n%s", lines)
	}
}

func TestCardTextEscapesNeverReachTheRender(t *testing.T) {
	th, _ := setup(t)
	where := "Head \x1b[2J\x1b[1;1Hmoved\x1b]0;title\x07 end\nmore \x1b[41mred"
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "waiting_on: x \x1b[31mred\n", "G \x1b[2Jgoal", where, "1. Ask \x1b[2Jthere? Recommend: yes.\n"))
	m := newPane()
	m.now = time.Now()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	raw := m.render()
	for _, bad := range []string{"\x1b[2J", "\x1b[1;1H", "\x1b]0;", "\x1b[41m", "\x1b[31m", "\x07"} {
		if strings.Contains(raw, bad) {
			t.Errorf("render carries %q", bad)
		}
	}
	if !strings.Contains(ansi.Strip(raw), "Head") {
		t.Errorf("text lost:\n%s", ansi.Strip(raw))
	}
}

func TestCheckRowIsCappedAtOneRow(t *testing.T) {
	c := &Card{Name: "w", Meta: map[string]string{"check": "pr " + strings.Repeat("unknown check words ", 20)}}
	rows := waitingRows(c, nil, time.Now(), 40)
	if len(rows) != 1 || !strings.HasSuffix(ansi.Strip(rows[0]), "…") || lipgloss.Width(rows[0]) > 40 {
		t.Errorf("rows: %q", rows)
	}
	c.Meta["check"] = "pr 5 approved"
	if rows := waitingRows(c, nil, time.Now(), 40); len(rows) != 1 || strings.HasSuffix(ansi.Strip(rows[0]), "…") {
		t.Errorf("a short check was cut: %q", rows)
	}
}

func TestUnknownBackgroundTintIsTheMidGrey(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "alpha", detailCard("alpha", "focus", "s1", "", "G", "W", ""))
	m := newPane()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if out := m.render(); !strings.Contains(out, "\x1b[48;5;243m") || strings.Contains(out, "\x1b[48;5;237m") {
		t.Error("unanswered background query does not use the fallback tint")
	}
}

func TestAnsweredShowsCheckAsksAndSaysWaitingWhenBlocked(t *testing.T) {
	th, _ := setup(t)
	needs := "1. Merge it? Recommend: yes.\n2. Check \"pr 5 approved\" is now yes. Recommend: set mode to focus.\n"
	putCard(t, th, "alpha", detailCard("alpha", "waiting", "s1", "", "G", "W", needs))
	stampAnswered(t, "alpha", time.Second)
	m := newPane()
	m.now = time.Now()
	m.rex.states["s1"] = agentState{State: "blocked", Kind: "question"}
	lines := strings.Join(detailLines(m, 120, 30), "\n")
	if !strings.Contains(lines, "agent waiting") || strings.Contains(lines, "agent working") {
		t.Errorf("blocked agent line:\n%s", lines)
	}
	if !strings.Contains(lines, "2  Check \"pr 5 approved\" is now yes.") || strings.Contains(lines, "Merge it?") {
		t.Errorf("check ask not shown under Answered:\n%s", lines)
	}
	m.rex.states["s1"] = agentState{State: "working"}
	if lines := strings.Join(detailLines(m, 120, 30), "\n"); !strings.Contains(lines, "agent working") {
		t.Errorf("working agent line:\n%s", lines)
	}
}

func TestModeKeyOnAnOldUnstampedUnseenCardLeavesNoDot(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "t-old", listCard("t-old", "focus", "s-old", "", "w"))
	old := time.Now().Add(-3 * 24 * time.Hour)
	if err := os.Chtimes(cardPath("t-old"), old, old); err != nil {
		t.Fatal(err)
	}
	m := newPane()
	m.now = time.Now()
	if d := unreadDots(m); d["t-old"] {
		t.Fatalf("old card starts unread: %v", d)
	}
	press(m, "b")
	if got := loadCardMust(t, "t-old").mode(); got != "background" {
		t.Fatalf("mode %q", got)
	}
	if d := unreadDots(m); d["t-old"] {
		t.Error("a mode key marked an old, unstamped, never-seen card unread")
	}
}

func TestOpenSinceWindowStartsAtTheLastPrompt(t *testing.T) {
	th, bin := setup(t)
	argv := filepath.Join(bin, "git.args")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" > "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	card(t, th, "a", "focus", "s1", "w", "")
	typed := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)
	appendEvent(Event{At: time.Now().Add(-5 * time.Hour).UTC(), Event: "open", Track: "a"})
	appendEvent(Event{At: typed, Event: "prompt", RexSession: "s1", Track: "a", Kind: "go"})
	// s1 is no Rex session here, so open exits 2 or 3 (EMI-290); it still prints the card and Since.
	if _, code := do("open", "a"); code == 1 {
		t.Fatal("open failed")
	}
	if got, _ := os.ReadFile(argv); !strings.Contains(string(got), "--since="+typed.Format(time.RFC3339)) {
		t.Errorf("git asked for %q, want since %s", got, typed.Format(time.RFC3339))
	}
}

func TestModeKeyAddsAMissingModeLine(t *testing.T) {
	th, _ := setup(t)
	txt := "---\nname: nomode\ndir: " + th + "\nrex_session: s1\n---\n## Goal\nG\n\n## Where it is\nw\n\n## Needs me\n\n"
	foldBack(t)
	if err := writeAtomic(cardPath("nomode"), []byte(txt)); err != nil {
		t.Fatal(err)
	}
	m := newPane()
	m.now = time.Now()
	press(m, "p")
	if strings.Contains(m.status, "could not set mode") || loadCardMust(t, "nomode").mode() != "parked" {
		t.Errorf("status %q, mode %q", m.status, loadCardMust(t, "nomode").mode())
	}
	// The files gain the line at the end of the frontmatter; a render puts mode in its place.
	if b, _ := os.ReadFile(cardPath("nomode")); !dbExists() && !strings.Contains(string(b), "\nmode: parked\n---\n") {
		t.Errorf("card:\n%s", b)
	}
}

func TestClosePromptMatchesShortAndFullSessionIds(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "session:x-eb6e0e", "w", "")
	card(t, th, "b", "focus", "eb6e0e", "w", "")
	m := newPane()
	if f := closePrompt(t, m, "a", 140); !strings.Contains(f, "keeps the session: track b uses it") {
		t.Errorf("full id first: %q", f)
	}
	if f := closePrompt(t, m, "b", 140); !strings.Contains(f, "keeps the session: track a uses it") {
		t.Errorf("short id first: %q", f)
	}
}

func TestWheelIgnoredWhileTheNewTrackFormIsOpen(t *testing.T) {
	m := formPane(t, 120, 30)
	m.scroll = 5
	m.Update(tea.MouseWheelMsg{X: 100, Y: 5, Button: tea.MouseWheelDown})
	m.Update(tea.MouseWheelMsg{X: 100, Y: 5, Button: tea.MouseWheelUp})
	if m.scroll != 5 {
		t.Errorf("wheel scrolled the hidden detail: %d", m.scroll)
	}
}
