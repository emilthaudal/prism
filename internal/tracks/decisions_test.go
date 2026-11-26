package tracks

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/emilthaudal/prism/internal/store"
)

func decisionHome(t *testing.T, names ...string) (th, bin string) {
	t.Helper()
	th, bin = setup(t)
	for _, n := range names {
		card(t, th, n, "focus", "", "w", "")
	}
	importNow(t)
	return th, bin
}

const addArgs = "decision add alpha --question Merge? --option merge|now --option wait|for_review --recommend 1"

func TestDecisionAddPrintsTheIDAndRefusesWhatLacksOptionsOrARecommendation(t *testing.T) {
	decisionHome(t, "alpha")
	out, code := do(strings.Fields(addArgs + " --evidence CI_green|circleci --link https://x/1 --from desk")...)
	if code != 0 || out != "D1\n" {
		t.Fatalf("add: exit %d %q", code, out)
	}
	for name, args := range map[string]string{
		"no option":         "decision add alpha --question Merge? --recommend 1",
		"no recommendation": "decision add alpha --question Merge? --option a --option b",
		"recommend past":    "decision add alpha --question Merge? --option a --recommend 2",
		"no question":       "decision add alpha --option a --recommend 1",
		"no track":          "decision add nope --question Q --option a --recommend 1",
	} {
		if out, code := do(strings.Fields(args)...); code == 0 || !strings.Contains(out, "refused") && name != "no question" {
			t.Errorf("%s: exit %d %q", name, code, out)
		}
	}
	args := strings.Fields("decision add alpha --question Q --recommend 1")
	for i := 0; i < 10; i++ {
		args = append(args, "--option", "o")
	}
	if _, code := do(args...); code == 0 {
		t.Error("ten options were accepted")
	}
	if out, _ := do("decisions"); strings.Count(out, "\n") != 2 {
		t.Errorf("refusals left rows:\n%s", out)
	}
}

func TestDecisionAddDeskTakesNoTrackAndItsAnswerReachesTheDesk(t *testing.T) {
	decisionHome(t, "alpha")
	out, code := do(strings.Fields("decision add --desk --question Install_main? --option install|now --option wait|tonight --recommend 2")...)
	if code != 0 || out != "D1\n" {
		t.Fatalf("add --desk: exit %d %q", code, out)
	}
	for name, args := range map[string]string{
		"desk and a track": "decision add alpha --desk --question Q --option a --recommend 1",
		"desk from agent":  "decision add --desk --from agent --question Q --option a --recommend 1",
		"no track at all":  "decision add --question Q --option a --recommend 1",
	} {
		if _, code := do(strings.Fields(args)...); code == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if out, _ := do("decisions", "--json"); !strings.Contains(out, `"track": null`) {
		t.Errorf("desk decision has a track: %s", out)
	}
	if out, code := do("decide", "D1", "1"); code != 0 || out != "answered D1: install\n" {
		t.Fatalf("decide: %d %q", code, out)
	}
	lines, err := answeredLines()
	if err != nil || len(lines) != 1 || lines[0] != "answered D1: install" {
		t.Errorf("answered lines %v %v", lines, err)
	}
}

func TestDecisionsJSONShapeAndClose(t *testing.T) {
	decisionHome(t, "alpha")
	do(strings.Fields(addArgs + " --evidence CI_green|circleci --link https://x/1")...)
	out, code := do("decisions", "--json")
	if code != 0 {
		t.Fatal(out)
	}
	var doc struct {
		Schema    int `json:"schema"`
		Decisions []struct {
			ID, Track, From, Question string
			N                         int
			Options                   []struct {
				N                  int
				Label, Description string
			}
			Recommended *int
			Evidence    []struct{ Text, Source string }
			Links       []string
			Session     *string
		} `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != 1 || len(doc.Decisions) != 1 {
		t.Fatalf("json %v: %s", err, out)
	}
	d := doc.Decisions[0]
	if d.ID != "D1" || d.Track != "alpha" || d.From != "desk" || d.Recommended == nil || *d.Recommended != 1 || len(d.Options) != 2 ||
		d.Options[1].Label != "wait" || d.Options[1].Description != "for_review" || d.Evidence[0].Source != "circleci" || d.Links[0] != "https://x/1" || d.Session != nil {
		t.Errorf("decision: %+v", d)
	}
	if out, code := do("decision", "close", "D1", "--note", "moot"); code != 0 || out != "closed D1\n" {
		t.Fatalf("close: %d %q", code, out)
	}
	if out, _ := do("decisions", "--json"); !strings.Contains(out, `"decisions": []`) {
		t.Errorf("closed one still listed: %s", out)
	}
	if _, code := do("decision", "close", "D1"); code == 0 {
		t.Error("closed twice")
	}
}

func TestDecideEachFormAndWaitPrintsTheDeskItsAnswers(t *testing.T) {
	decisionHome(t, "alpha")
	for i := 0; i < 3; i++ {
		do(strings.Fields(addArgs)...)
	}
	if out, code := do("decide", "D1", "2"); code != 0 || out != "answered D1: wait\n" {
		t.Fatalf("option: %d %q", code, out)
	}
	if out, code := do("decide", "D2", "--text", "tomorrow, after standup", "--by", "desk"); code != 0 || out != "answered D2: tomorrow, after standup\n" {
		t.Fatalf("words: %d %q", code, out)
	}
	for name, args := range map[string]string{
		"answered twice": "decide D1 1",
		"option past":    "decide D3 3",
		"both forms":     "decide D3 1 --text x",
		"neither form":   "decide D3",
		"bad by":         "decide D3 1 --by bob",
		"unknown":        "decide D9 1",
		"not an id":      "decide x 1",
		"not an option":  "decide D3 first",
	} {
		if out, code := do(strings.Fields(args)...); code == 0 {
			t.Errorf("%s: accepted: %q", name, out)
		}
	}
	out, code := do("wait", "--all", "--timeout", "3s")
	if code != 0 || !strings.Contains(out, "answered D1: wait") || !strings.Contains(out, "answered D2: tomorrow, after standup") || strings.Contains(out, "D3") {
		t.Fatalf("wait: %d %q", code, out)
	}
	if out, code := do("wait", "--all", "--timeout", "1s"); code != 2 || strings.Contains(out, "answered") {
		t.Errorf("a second wait repeated the answers: %d %q", code, out)
	}
}

func TestAnAnswerToATrackAgentsQuestionGoesWhereTheAnswerToItsAskGoes(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Which index? 1) products 2) orders Recommend: 1")
	agentIs(t, bin, "done", "")
	importNow(t)
	if out, code := do("decisions"); code != 0 || !strings.Contains(out, "D1") || !strings.Contains(out, "1 products") {
		t.Fatalf("mirrored question not listed: %d %q", code, out)
	}
	if out, code := do("decide", "D1", "2"); code != 0 || out != "answered D1: orders\n" {
		t.Fatalf("decide: %d %q", code, out)
	}
	if got, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); string(got) != "Emil's answer to your ask 1, \"Which index?\": orders." {
		t.Errorf("typed %q", got)
	}
	var v store.View
	withStore(func(st *store.Store) (err error) { v, err = st.Get("a"); return err })
	if len(v.Asks) != 1 || v.Asks[0].AnsweredAt.IsZero() {
		t.Errorf("the ask was not stamped answered: %+v", v.Asks)
	}
}

func TestAnAnswerThatCannotBeDeliveredForAnotherReasonLeavesTheQuestionOpen(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Which index? 1) products 2) orders Recommend: 1")
	card(t, th, "b", "focus", "", "w", "1. Which index? 1) products 2) orders Recommend: 1")
	agentIs(t, bin, "idle", "")
	os.WriteFile(filepath.Join(bin, "fg"), []byte(`{"foreground":{"pid":99892,"name":"zsh"}}`), 0o644)
	importNow(t)
	for _, id := range []string{"D1", "D2"} {
		if out, code := do("decide", id, "1"); code == 0 || strings.Contains(out, "queued") {
			t.Fatalf("%s: answered an agent that is not busy: %d %q", id, code, out)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); len(got) != 0 {
		t.Errorf("typed %q", got)
	}
	out, _ := do("decisions")
	if !strings.Contains(out, "D1") || !strings.Contains(out, "D2") {
		t.Errorf("question closed by a failed answer:\n%s", out)
	}
}

const queuedAsks = "1. Which index? 1) products 2) orders Recommend: 1\n2. Merge? 1) now 2) later Recommend: 1"

func queuedHome(t *testing.T, state string) (bin string) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", queuedAsks)
	agentIs(t, bin, state, "")
	importNow(t)
	return bin
}

func questionNow(t *testing.T, id int64) (q store.Question) {
	t.Helper()
	if err := withStore(func(st *store.Store) (err error) { q, err = st.GetQuestion(id); return err }); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestAnAnswerToABusyAgentIsQueuedAndLeavesNeedsYou(t *testing.T) {
	bin := queuedHome(t, "working")
	opt := 0
	status, queued, err := answerQuestionQueued(1, 1, "", "emil", "")
	if err != nil || !queued || status != "queued D1: typed into a when its agent stops" {
		t.Fatalf("%q %v %v", status, queued, err)
	}
	if sentText(bin) != "" {
		t.Errorf("typed %q", sentText(bin))
	}
	q := questionNow(t, 1)
	if q.AnsweredAt.IsZero() || !q.DeliveredAt.IsZero() || q.Open() {
		t.Errorf("not recorded as answered and undelivered: %+v", q)
	}
	if out, _ := do("decisions"); strings.Contains(out, "D1 ") || !strings.Contains(out, "D2") {
		t.Errorf("D1 still needs Emil:\n%s", out)
	}
	if got := apiQuestionOf(q, nil).Delivery; got == nil || *got != "queued" {
		t.Errorf("delivery %v", got)
	}
	reply := map[string]string{}
	_, body, err := (&liveSource{}).question("D2", "answer", &opt, "", "")
	if err != nil {
		t.Fatal(err)
	}
	reply = body.(map[string]string)
	if reply["delivery"] != "queued" || reply["status"] != "queued D2: typed into a when its agent stops" {
		t.Errorf("api reply %v", reply)
	}
}

func fakeDeliverer(now *time.Time) *deliverer {
	d := newDeliverer(io.Discard)
	d.now = func() time.Time { return *now }
	return d
}

func TestQueuedAnswerIsTypedWhenTheAgentStops(t *testing.T) {
	bin := queuedHome(t, "working")
	answerQuestionQueued(1, 2, "", "emil", "")
	now := time.Now()
	d := fakeDeliverer(&now)
	d.once()
	if sentText(bin) != "" || questionNow(t, 1).DeliveredAt != (time.Time{}) {
		t.Fatal("delivered to a working agent")
	}
	agentIs(t, bin, "done", "")
	d.once()
	if got := sentText(bin); got != "Emil's answer to your ask 1, \"Which index?\": orders." {
		t.Errorf("typed %q", got)
	}
	q := questionNow(t, 1)
	if q.DeliveredAt.IsZero() {
		t.Error("not marked delivered")
	}
	if got := apiQuestionOf(q, nil).Delivery; got == nil || *got != "delivered" {
		t.Errorf("delivery %v", got)
	}
	os.Remove(filepath.Join(bin, "rex.sent"))
	now = now.Add(time.Minute)
	d.once()
	if sentText(bin) != "" {
		t.Errorf("typed again: %q", sentText(bin))
	}
}

func TestTwoQueuedAnswersForOneTrackAreTypedOneAtATime(t *testing.T) {
	bin := queuedHome(t, "working")
	answerQuestionQueued(1, 1, "", "emil", "")
	time.Sleep(1100 * time.Millisecond) // answered_at has whole-second resolution: D1 is the older answer
	answerQuestionQueued(2, 1, "", "emil", "")
	now := time.Now()
	d := fakeDeliverer(&now)
	agentIs(t, bin, "done", "")
	d.once()
	if got := sentText(bin); !strings.Contains(got, "ask 1,") {
		t.Fatalf("first typed %q", got)
	}
	os.Remove(filepath.Join(bin, "rex.sent"))
	d.once()
	now = now.Add(2 * time.Second)
	d.once()
	if got := sentText(bin); got != "" {
		t.Fatalf("a second answer typed into the same turn: %q", got)
	}
	now = now.Add(time.Minute)
	agentIs(t, bin, "working", "")
	d.once()
	if got := sentText(bin); got != "" {
		t.Fatalf("typed into a working agent: %q", got)
	}
	agentIs(t, bin, "done", "")
	d.once()
	if got := sentText(bin); !strings.Contains(got, "ask 2,") {
		t.Errorf("second typed %q", got)
	}
	if questionNow(t, 2).DeliveredAt.IsZero() {
		t.Error("second not marked delivered")
	}
}

func TestQueuedAnswerIsRetriedEveryMinuteAndGivenUpAfterThirtyMinutes(t *testing.T) {
	bin := queuedHome(t, "working")
	answerQuestionQueued(1, 1, "", "emil", "")
	os.WriteFile(filepath.Join(bin, "fg"), []byte(`{"foreground":{"pid":99892,"name":"zsh"}}`), 0o644)
	agentIs(t, bin, "idle", "")
	now := time.Now()
	d := fakeDeliverer(&now)
	callsAfter := func() int { return strings.Count(rexCalls(bin), "\n") }
	d.once()
	first := callsAfter()
	if first == 0 {
		t.Fatal("no try")
	}
	now = now.Add(30 * time.Second)
	d.once()
	if callsAfter() != first {
		t.Error("retried within a minute")
	}
	now = now.Add(31 * time.Second)
	d.once()
	if callsAfter() == first {
		t.Error("not retried after a minute")
	}
	if q := questionNow(t, 1); q.AnsweredAt.IsZero() || q.DeliveredAt != (time.Time{}) {
		t.Fatalf("given up early: %+v", q)
	}
	now = now.Add(deliverGiveUp)
	d.once()
	q := questionNow(t, 1)
	if !q.Open() || !q.AnsweredAt.IsZero() || q.Answer() != "" {
		t.Fatalf("not back in Needs you: %+v", q)
	}
	if n := len(q.Evidence); n != 1 || !strings.Contains(q.Evidence[0].Text, "was not delivered to a in 30 minutes") {
		t.Errorf("no note: %+v", q.Evidence)
	}
	if out, _ := do("decisions"); !strings.Contains(out, "D1") {
		t.Errorf("D1 not listed:\n%s", out)
	}
}

func TestACardRewriteKeepsTheDecisionIDAndRemovalClosesIt(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	importNow(t)
	cardIn("asks:\nShip it? yes/no Recommend: yes\nTabs or spaces?\n", "card", "a")
	out, _ := do("decisions", "--json")
	var first struct {
		Decisions []struct{ ID, Question string }
	}
	json.Unmarshal([]byte(out), &first)
	if len(first.Decisions) != 2 {
		t.Fatalf("after the first write: %s", out)
	}
	ids := map[string]string{}
	for _, d := range first.Decisions {
		ids[d.Question] = d.ID
	}
	cardIn("asks:\nTabs or spaces?\nShip it? yes/no Recommend: yes\nA third?\n", "card", "a")
	out, _ = do("decisions", "--json")
	var second struct {
		Decisions []struct{ ID, Question string }
	}
	json.Unmarshal([]byte(out), &second)
	if len(second.Decisions) != 3 {
		t.Fatalf("after the rewrite: %s", out)
	}
	for _, d := range second.Decisions {
		if want, ok := ids[d.Question]; ok && want != d.ID {
			t.Errorf("%q was %s, is %s", d.Question, want, d.ID)
		}
	}
	cardIn("asks:\n", "card", "a")
	if out, _ := do("decisions"); strings.Contains(out, "D") && strings.Count(out, "\n") != 1 {
		t.Errorf("removed asks left decisions open:\n%s", out)
	}
}

// storePane is a pane over tracks.db with the cards' asks mirrored as questions.
func storePane(t *testing.T, cards map[string]string, states map[string]agentState, w, h int) *paneModel {
	t.Helper()
	th, _ := setup(t)
	for n, body := range cards {
		putCard(t, th, n, body)
	}
	importNow(t)
	m := newPane()
	m.now = bandBase
	for k, v := range states {
		m.rex.states[k] = v
	}
	m.Update(sinceMsg{name: m.cur, err: errNotGit.Error(), at: m.now})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func twoTracks(asks string) map[string]string {
	return map[string]string{
		"alpha": bandCard("alpha", "focus", "s1", "", time.Hour, asks),
		"beta":  bandCard("beta", "focus", "s2", "", time.Hour, ""),
	}
}

var idleStates = map[string]agentState{"s1": {State: "idle", At: bandBase.Add(-time.Hour)}, "s2": {State: "idle", At: bandBase.Add(-time.Hour)}}

func (m *paneModel) bandText() string { return strings.Join(bandOf(plain(m, 120, 40)), "\n") }

func TestBandReadsQuestionsWithIDsRecommendationAndAge(t *testing.T) {
	m := storePane(t, twoTracks("1. Which index? 1) products 2) orders Recommend: 2, it is smaller."), idleStates, 120, 40)
	do("decision", "add", "beta", "--question", "Merge PR 418 now?", "--option", "merge now|CI green", "--option", "wait", "--recommend", "1")
	m.reload()
	m.now = time.Now().Add(2*time.Hour + time.Minute)
	band := m.bandText()
	for _, want := range []string{"NEEDS YOU 2", "alpha", "D1", "Which index?", "→ 2 orders", "beta", "D2", "Merge PR 418 now?", "→ 1 merge now", "2h"} {
		if !strings.Contains(band, want) {
			t.Errorf("band lacks %q:\n%s", want, band)
		}
	}
}

func pickTrack(t *testing.T, m *paneModel, name string) {
	t.Helper()
	for i, c := range m.cards {
		if c.Name == name {
			m.moveTo(i)
			return
		}
	}
	t.Fatalf("no track %s", name)
}

// feed executes a command the pane returned, and feeds its message back, as the program would.
func feed(m *paneModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			feed(m, c)
		}
	case nil:
	default:
		m.Update(msg)
	}
}

func openIDs(t *testing.T) []int64 {
	t.Helper()
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) { qs, err = st.OpenQuestions(); return err }); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return ids
}

func TestEnterTakesTheRecommendationAfterFiveSecondsAndZTakesItBack(t *testing.T) {
	m := storePane(t, twoTracks(""), idleStates, 120, 40)
	do("decision", "add", "beta", "--question", "Merge PR 418 now?", "--option", "merge now", "--option", "wait", "--recommend", "1", "--from", "desk")
	m.reload()
	pickTrack(t, m, "beta")
	press(m, "a")
	if !m.bandFocus {
		t.Fatal("a did not focus the band")
	}
	press(m, "enter")
	if band := m.bandText(); !strings.Contains(band, "merge now · 5 s") || !strings.Contains(band, "z undo") || !strings.Contains(band, "✓") {
		t.Fatalf("no countdown on the row:\n%s", band)
	}
	m.now = m.now.Add(3 * time.Second)
	feed(m, m.firePending())
	if band := m.bandText(); !strings.Contains(band, "merge now · 2 s") {
		t.Fatalf("the countdown did not move:\n%s", band)
	}
	if len(openIDs(t)) != 1 {
		t.Fatal("answered before the countdown ended")
	}
	press(m, "z")
	if len(m.pending) != 0 || strings.Contains(m.bandText(), "z undo") {
		t.Fatalf("z left the pending answer: %v", m.pending)
	}
	m.now = m.now.Add(10 * time.Second)
	feed(m, m.firePending())
	if len(openIDs(t)) != 1 {
		t.Fatal("an answer taken back was sent")
	}
	press(m, "enter")
	m.now = m.now.Add(answerDelay - time.Millisecond)
	feed(m, m.firePending())
	if len(openIDs(t)) != 1 {
		t.Fatal("sent a millisecond early")
	}
	m.now = m.now.Add(time.Millisecond)
	feed(m, m.firePending())
	if len(openIDs(t)) != 0 || !strings.Contains(m.status, "answered D1: merge now") {
		t.Fatalf("not answered at 5 s: %v, %q", openIDs(t), m.status)
	}
	out, _ := do("wait", "--all", "--timeout", "3s")
	if !strings.Contains(out, "answered D1: merge now") {
		t.Errorf("the desk is not told: %q", out)
	}
}

func TestDigitsPickAndRAnswersInWords(t *testing.T) {
	m := storePane(t, twoTracks(""), idleStates, 120, 40)
	do("decision", "add", "beta", "--question", "Merge?", "--option", "merge", "--option", "wait", "--recommend", "1")
	do("decision", "add", "beta", "--question", "Squash?", "--option", "yes", "--option", "no", "--recommend", "2")
	m.reload()
	pickTrack(t, m, "beta")
	press(m, "a")
	press(m, "2")
	if len(m.pending) != 1 || m.pending[0].label != "wait" || m.pending[0].option != 2 {
		t.Fatalf("2 picked %+v", m.pending)
	}
	press(m, "9")
	if len(m.pending) != 1 || !strings.Contains(m.status, "has 2 options") {
		t.Errorf("9 on two options: %+v %q", m.pending, m.status)
	}
	press(m, "]")
	press(m, "r")
	typeText(m, "only on Friday")
	press(m, "enter")
	if len(m.pending) != 2 || m.pending[1].words != "only on Friday" || m.pending[1].id != 2 || m.reply != nil {
		t.Fatalf("r answered %+v", m.pending)
	}
	m.now = m.now.Add(answerDelay)
	feed(m, m.firePending())
	if ids := openIDs(t); len(ids) != 0 {
		t.Fatalf("still open: %v", ids)
	}
	out, _ := do("wait", "--all", "--timeout", "3s")
	if !strings.Contains(out, "answered D1: wait") || !strings.Contains(out, "answered D2: only on Friday") {
		t.Errorf("wait: %q", out)
	}
}

func TestEnterWithoutARecommendedOptionDoesNothingButSayWhy(t *testing.T) {
	m := storePane(t, twoTracks("1. Read Documents/la-delivery-model-audit? Recommend: read it first"), idleStates, 120, 40)
	pickTrack(t, m, "alpha")
	press(m, "a")
	press(m, "enter")
	if len(m.pending) != 0 || !strings.Contains(m.status, "no recommended option") {
		t.Fatalf("enter on an unrecommended question: %v %q", m.pending, m.status)
	}
}

func TestTheDeskCannotRecordAnAnswerAsEmils(t *testing.T) {
	decisionHome(t, "alpha")
	do(strings.Fields(addArgs)...)
	if err := withStore(func(st *store.Store) error { return st.BindDesk("session:desk", true) }); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "session:desk")
	if out, code := do("decide", "D1", "1"); code == 0 || !strings.Contains(out, "--by desk") {
		t.Fatalf("the desk answered as Emil: %d %q", code, out)
	}
	t.Setenv("REX_SESSION", "session:other")
	if out, code := do("decide", "D1", "1"); code != 0 {
		t.Fatalf("another session: %d %q", code, out)
	}
}

func TestTheDeskDoesNotAnswerATrackAgentsQuestion(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Which index? 1) products 2) orders Recommend: 1")
	agentIs(t, bin, "done", "")
	importNow(t)
	if out, code := do("decide", "D1", "1", "--by", "desk"); code == 0 || !strings.Contains(out, "desk does not answer") {
		t.Fatalf("desk answered an agent's question: %d %q", code, out)
	}
	if got, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); len(got) != 0 {
		t.Errorf("typed %q", got)
	}
}

func TestSpaceExpandsEvidenceAndOOpensTheFirstLink(t *testing.T) {
	m := storePane(t, twoTracks(""), idleStates, 120, 40)
	do("decision", "add", "beta", "--question", "Merge?", "--option", "merge|now", "--option", "wait", "--recommend", "1",
		"--evidence", "CI green|circleci", "--link", "https://github.com/x/y/pull/1")
	m.reload()
	pickTrack(t, m, "beta")
	press(m, "a")
	if strings.Contains(m.bandText(), "circleci") {
		t.Fatal("evidence shown before space")
	}
	press(m, "space")
	band := m.bandText()
	for _, want := range []string{"★ 1 merge – now", "2 wait", "CI green (circleci)", "https://github.com/x/y/pull/1"} {
		if !strings.Contains(band, want) {
			t.Errorf("expanded row lacks %q:\n%s", want, band)
		}
	}
	if press(m, "o") == nil {
		t.Error("o opened nothing")
	}
	press(m, "space")
	if strings.Contains(m.bandText(), "circleci") {
		t.Error("space did not fold")
	}
}

func TestPaneKeysStillWorkWithoutTheBandFocus(t *testing.T) {
	m := storePane(t, twoTracks(""), idleStates, 120, 40)
	do("decision", "add", "beta", "--question", "Merge?", "--option", "merge", "--option", "wait", "--recommend", "1")
	m.reload()
	pickTrack(t, m, "beta")
	if press(m, "enter") == nil {
		t.Error("enter no longer opens the track")
	}
	if len(m.pending) != 0 {
		t.Error("enter answered without the band focus")
	}
	press(m, "tab")
	if m.bandFocus {
		t.Error("tab focused the band: only a does")
	}
	press(m, "a")
	press(m, "down")
	if m.bandFocus {
		t.Error("moving kept the band focus")
	}
}
