package tracks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

var patternT0 = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

// answered is a fixture question with two options, the first recommended, answered by Emil
// with option pick (0 for words) at minute min.
func answered(id int64, question string, pick int, words string, min int) store.Question {
	return store.Question{ID: id, Question: question, From: "agent", Recommended: 1,
		Options:      []store.QOption{{Label: "Yes"}, {Label: "No"}},
		AnswerOption: pick, AnswerText: words, AnsweredBy: "emil",
		CreatedAt: patternT0, AnsweredAt: patternT0.Add(time.Duration(min) * time.Minute)}
}

func patternByKey(ps []pattern, key string) *pattern {
	for i := range ps {
		if ps[i].Key == key {
			return &ps[i]
		}
	}
	return nil
}

func TestFindPatternsProposesOnlyKindsEmilAlwaysAnswersWithTheRecommendation(t *testing.T) {
	qs := []store.Question{
		answered(1, "Close this track?", 1, "", 1),
		answered(2, "Close this track? The fix is merged.", 1, "", 2),
		answered(3, "Close this track?", 0, "Go with your recommendation: yes", 3),
		answered(4, "Install EMI-1 into Prism?", 1, "", 4),
		answered(5, "Install EMI-2 into Prism?", 2, "", 5), // Emil overrode the recommendation
		answered(6, "Install EMI-3 into Prism?", 1, "", 6),
		answered(7, "Start a track for EMI-9?", 1, "", 7), // one is too few
		answered(8, "Deploy pantry #1 to production?", 1, "", 8),
		answered(9, "Deploy pantry #2 to production?", 1, "", 9),
		answered(10, "Deploy pantry #3 to production?", 1, "", 10),
		answered(11, "What colour should the bar be?", 1, "", 11), // no known kind
	}
	// The desk answering, or no recommendation at all, says nothing about Emil's trust.
	byDesk := answered(12, "Close this track?", 2, "", 12)
	byDesk.AnsweredBy = "desk"
	noRec := answered(13, "Close this track?", 2, "", 13)
	noRec.Recommended = 0
	qs = append(qs, byDesk, noRec)

	ps := findPatterns(qs, 3)
	close := patternByKey(ps, "close-track")
	if close == nil || !close.Propose || close.Answered != 3 || close.Followed != 3 || close.Skill != "track" || len(close.Examples) != 3 {
		t.Fatalf("close-track: %+v", close)
	}
	if in := patternByKey(ps, "install"); in == nil || in.Propose || in.Followed != 2 || !strings.Contains(in.Why, "1 of 3") {
		t.Errorf("install, overridden once, was proposed: %+v", in)
	}
	if st := patternByKey(ps, "start-track"); st == nil || st.Propose || !strings.Contains(st.Why, "fewer than 3") {
		t.Errorf("start-track with one answer: %+v", st)
	}
	if pr := patternByKey(ps, "deploy-production"); pr == nil || pr.Propose || !strings.HasPrefix(pr.Why, "kept as an ask") {
		t.Errorf("production deploys were proposed: %+v", pr)
	}
	if len(ps) != 4 {
		t.Errorf("want 4 kinds, got %d: %+v", len(ps), ps)
	}
}

func TestFindPatternsWaitsOnAnOpenProposalAndCountsOnlyAnswersAfterAnAnsweredOne(t *testing.T) {
	qs := []store.Question{
		answered(1, "Close this track?", 1, "", 1),
		answered(2, "Close this track?", 1, "", 2),
		answered(3, "Close this track?", 1, "", 3),
	}
	proposal := store.Question{ID: 4, From: "desk", Question: `Make "Close this track?" a standing rule?`, Recommended: 1,
		Options:  []store.QOption{{Label: "Yes"}, {Label: "No"}},
		Evidence: []store.Evidence{{Text: "3 of 3", Source: patternSource + "close-track"}}, CreatedAt: patternT0.Add(4 * time.Minute)}

	open := findPatterns(append(qs, proposal), 3)
	if p := patternByKey(open, "close-track"); p == nil || p.Propose || !strings.Contains(p.Why, "D4, still open") {
		t.Fatalf("open proposal: %+v", p)
	}

	// Emil said no: only answers after his no count, so it waits for three new ones.
	proposal.AnswerOption, proposal.AnsweredBy, proposal.AnsweredAt = 2, "emil", patternT0.Add(5*time.Minute)
	qs = append(qs, proposal, answered(5, "Close this track?", 1, "", 6), answered(6, "Close this track?", 1, "", 7))
	if p := patternByKey(findPatterns(qs, 3), "close-track"); p == nil || p.Propose || p.Answered != 2 {
		t.Fatalf("after a no, before 3 new answers: %+v", p)
	}
	qs = append(qs, answered(7, "Close this track?", 1, "", 8))
	if p := patternByKey(findPatterns(qs, 3), "close-track"); p == nil || !p.Propose || p.Answered != 3 {
		t.Fatalf("after a no and 3 new answers: %+v", p)
	}
}

func TestPatternsCmdReadsAnsweredDecisions(t *testing.T) {
	decisionHome(t, "alpha")
	for i := 0; i < 3; i++ {
		if out, code := do("decision", "add", "--desk", "--question", "Install main?", "--option", "install|now", "--option", "wait|later", "--recommend", "1"); code != 0 {
			t.Fatalf("add: %d %q", code, out)
		}
	}
	for _, id := range []string{"D1", "D2", "D3"} {
		if out, code := do("decide", id, "1"); code != 0 {
			t.Fatalf("decide %s: %d %q", id, code, out)
		}
	}
	out, code := do("patterns", "--json")
	if code != 0 {
		t.Fatalf("patterns: %d %q", code, out)
	}
	var doc struct{ Patterns []pattern }
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if p := patternByKey(doc.Patterns, "install"); p == nil || !p.Propose || p.Answered != 3 {
		t.Fatalf("install not proposed: %s", out)
	}
	if out, _ := do("patterns"); !strings.Contains(out, "→ Install a finished change: 3 of 3") {
		t.Errorf("text:\n%s", out)
	}
	if _, code := do("patterns", "--days", "0"); code == 0 {
		t.Error("--days 0 was accepted")
	}
}
