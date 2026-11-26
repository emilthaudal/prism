package tracks

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	batchIndex = "Emil's answer to your ask 1, \"Which index?\": products."
	batchMerge = "Emil's answer to your ask 2, \"Merge?\": later."
)

func typedSends(bin string) int {
	b, _ := os.ReadFile(filepath.Join(bin, "rex.calls"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "send --session") {
			n++
		}
	}
	return n
}

func answersIn(t *testing.T, args ...any) (out []answerInput) {
	t.Helper()
	for i := 0; i < len(args); i += 3 {
		out = append(out, answerInput{ID: args[i].(string), Option: args[i+1].(*int), Text: args[i+2].(string)})
	}
	return out
}

func TestABatchOfAnswersIsOneTypedMessageAndOneCall(t *testing.T) {
	bin := queuedHome(t, "done")
	status, queued, err := answerBatch([]batchAnswer{{ID: 1, Option: 1}, {ID: 2, Option: 2}}, "emil")
	if err != nil || queued || status != "answered D1, D2" {
		t.Fatalf("%q %v %v", status, queued, err)
	}
	if got, want := sentText(bin), "Emil's answers: "+batchIndex+" "+batchMerge; got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
	if n := typedSends(bin); n != 1 {
		t.Errorf("%d messages typed", n)
	}
	for _, id := range []int64{1, 2} {
		if q := questionNow(t, id); q.Open() || q.DeliveredAt.IsZero() {
			t.Errorf("D%d not recorded as answered and delivered: %+v", id, q)
		}
	}
}

func TestABatchAnswerInWordsIsQuotedLikeASingleOne(t *testing.T) {
	bin := queuedHome(t, "done")
	if _, _, err := answerBatch([]batchAnswer{{ID: 1, Text: "neither, check the plan"}, {ID: 2, Option: 1}}, "emil"); err != nil {
		t.Fatal(err)
	}
	want := "Emil's answers: Emil's answer to your ask 1, \"Which index?\": neither, check the plan Emil's answer to your ask 2, \"Merge?\": now."
	if got := sentText(bin); got != want {
		t.Errorf("typed %q", got)
	}
}

func TestABatchForABusyAgentIsQueuedTogetherAndTypedOnce(t *testing.T) {
	bin := queuedHome(t, "working")
	status, queued, err := answerBatch([]batchAnswer{{ID: 1, Option: 1}, {ID: 2, Option: 2}}, "emil")
	if err != nil || !queued || status != "queued D1, D2: typed into a when its agent stops" {
		t.Fatalf("%q %v %v", status, queued, err)
	}
	if sentText(bin) != "" {
		t.Fatalf("typed %q", sentText(bin))
	}
	if a, b := questionNow(t, 1), questionNow(t, 2); a.AnsweredAt.IsZero() || !a.AnsweredAt.Equal(b.AnsweredAt) || !a.DeliveredAt.IsZero() || !b.DeliveredAt.IsZero() {
		t.Fatalf("not recorded together as queued: %+v %+v", a, b)
	}
	now := time.Now()
	d := fakeDeliverer(&now)
	agentIs(t, bin, "done", "")
	d.once()
	if got, want := sentText(bin), "Emil's answers: "+batchIndex+" "+batchMerge; got != want {
		t.Errorf("typed %q", got)
	}
	if n := typedSends(bin); n != 1 {
		t.Errorf("%d messages typed", n)
	}
	if questionNow(t, 1).DeliveredAt.IsZero() || questionNow(t, 2).DeliveredAt.IsZero() {
		t.Error("not marked delivered")
	}
}

func TestABatchWithARefusedAnswerRecordsAndTypesNothing(t *testing.T) {
	bin := queuedHome(t, "done")
	for name, items := range map[string][]batchAnswer{
		"bad option": {{ID: 1, Option: 1}, {ID: 2, Option: 3}},
		"empty":      {{ID: 1, Option: 1}, {ID: 2}},
		"twice":      {{ID: 1, Option: 1}, {ID: 1, Option: 2}},
		"none":       nil,
	} {
		if _, _, err := answerBatch(items, "emil"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if sentText(bin) != "" || !questionNow(t, 1).Open() || !questionNow(t, 2).Open() {
		t.Errorf("something was typed or recorded: %q", sentText(bin))
	}
}

func TestABatchIsOneAskersAnswers(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Which index? 1) products 2) orders Recommend: 1")
	card(t, th, "b", "focus", "", "w", "1. Merge? 1) now 2) later Recommend: 1")
	agentIs(t, bin, "done", "")
	importNow(t)
	if _, _, err := answerBatch([]batchAnswer{{ID: 1, Option: 1}, {ID: 2, Option: 1}}, "emil"); err == nil || !strings.Contains(err.Error(), "different askers") {
		t.Errorf("err %v", err)
	}
	if sentText(bin) != "" || !questionNow(t, 1).Open() {
		t.Error("something was typed or recorded")
	}
}

func TestAskingForMoreTypesTheAskAndLeavesTheQuestionOpen(t *testing.T) {
	bin := queuedHome(t, "done")
	status, queued, err := askQuestion(1, "which is faster?", false, nil)
	if err != nil || queued || status != "asked about D1" {
		t.Fatalf("%q %v %v", status, queued, err)
	}
	if got, want := sentText(bin), "Emil asks about your ask \"Which index?\": which is faster?"; got != want {
		t.Errorf("typed %q", got)
	}
	if q := questionNow(t, 1); !q.Open() || !q.AnsweredAt.IsZero() {
		t.Errorf("the question was answered: %+v", q)
	}
	if _, _, err := askQuestion(1, "  ", false, nil); err == nil {
		t.Error("an empty question was sent")
	}
}

func TestShowMeTheOptionsAsksForOneImagePerOption(t *testing.T) {
	bin := queuedHome(t, "done")
	if _, _, err := askQuestion(1, "", true, nil); err != nil {
		t.Fatal(err)
	}
	want := "Render each option of your ask \"Which index?\" to an image and ask again with (see: PATH) after each option, but only if the options are visual (icons, colours, layouts, screenshots). If they are words or plans an image can't show, say so in one line and leave your ask as it is."
	if got := sentText(bin); got != want {
		t.Errorf("typed %q", got)
	}
}

func TestAnAskToABusyAgentIsHeldAndTypedWhenItStops(t *testing.T) {
	bin := queuedHome(t, "working")
	pendingAsks = askQueue{}
	status, queued, err := askQuestion(1, "why?", false, nil)
	if err != nil || !queued || status != "queued ask on D1: typed into a when its agent stops" {
		t.Fatalf("%q %v %v", status, queued, err)
	}
	now := time.Now()
	d := fakeDeliverer(&now)
	d.once()
	if sentText(bin) != "" {
		t.Fatalf("typed %q", sentText(bin))
	}
	agentIs(t, bin, "done", "")
	d.once()
	if got := sentText(bin); got != "Emil asks about your ask \"Which index?\": why?" {
		t.Errorf("typed %q", got)
	}
	if len(pendingAsks.all()) != 0 || !questionNow(t, 1).Open() {
		t.Error("the ask is still held, or the question closed")
	}
}

func TestTheAnswersAndAskRoutesAnswerLikeTheAnswerRoute(t *testing.T) {
	bin := queuedHome(t, "done")
	l := &liveSource{}
	zero, one := 0, 1
	_, body, err := l.ask("D1", "why?", false)
	if err != nil || body.(map[string]string)["delivery"] != "delivered" {
		t.Fatalf("ask: %v %v", body, err)
	}
	_, body, err = l.answers(answersIn(t, "D1", &zero, "", "D2", &one, ""))
	if err != nil || body.(map[string]string)["status"] != "answered D1, D2" {
		t.Fatalf("answers: %v %v", body, err)
	}
	if want := "Emil's answers: " + batchIndex + " " + batchMerge; sentText(bin) != want {
		t.Errorf("typed %q", sentText(bin))
	}
	_, _, err = l.answers(answersIn(t, "D1", &zero, ""))
	if ae, ok := err.(apiError); !ok || ae.code != http.StatusConflict {
		t.Errorf("answering a closed question again: %v", err)
	}
}
