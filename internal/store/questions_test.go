package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func twoOptions() []QOption {
	return []QOption{{Label: "merge", Description: "now"}, {Label: "wait", Description: "for review"}}
}

func TestAddQuestionRefusesNoOptionsOrNoRecommendation(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	for name, sp := range map[string]QuestionSpec{
		"no options":        {Track: "alpha", From: "desk", Question: "Merge?", Recommended: 1},
		"no recommendation": {Track: "alpha", From: "desk", Question: "Merge?", Options: twoOptions()},
		"recommend past":    {Track: "alpha", From: "desk", Question: "Merge?", Options: twoOptions(), Recommended: 3},
		"ten options":       {Track: "alpha", From: "desk", Question: "Merge?", Options: make([]QOption, 10), Recommended: 1},
		"bad from":          {Track: "alpha", From: "emil", Question: "Merge?", Options: twoOptions(), Recommended: 1},
	} {
		if _, err := s.AddQuestion(sp); err == nil {
			t.Errorf("%s: added", name)
		}
	}
	if qs, _ := s.OpenQuestions(); len(qs) != 0 {
		t.Fatalf("refused questions left rows: %v", qs)
	}
	q, err := s.AddQuestion(QuestionSpec{Track: "alpha", From: "desk", Question: "Merge?", Options: twoOptions(), Recommended: 1,
		Evidence: []Evidence{{"CI green", "circleci"}}, Links: []string{"https://x/1"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "question", []any{q.Track, q.From, q.Recommended, q.Evidence, q.Links, q.Options}, []any{"alpha", "desk", 1,
		[]Evidence{{"CI green", "circleci"}}, []string{"https://x/1"}, twoOptions()})
}

func TestDecideEachFormAndClose(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	add := func() Question {
		q, err := s.AddQuestion(QuestionSpec{Track: "alpha", From: "desk", Question: "Merge?", Options: twoOptions(), Recommended: 1})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	q := add()
	got, err := s.Decide(q.ID, 2, "", "emil")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "option answer", []any{got.Answer(), got.AnsweredBy, got.Open()}, []any{"wait", "emil", false})
	if _, err := s.Decide(q.ID, 1, "", "emil"); err == nil {
		t.Error("answered a closed question")
	}
	q = add()
	got, _ = s.Decide(q.ID, 0, "  do it tomorrow ", "desk")
	eq(t, "text answer", []any{got.Answer(), got.AnsweredBy, got.AnswerOption}, []any{"do it tomorrow", "desk", 0})
	q = add()
	for _, bad := range []struct {
		opt  int
		text string
	}{{3, ""}, {0, ""}, {1, "both"}} {
		if _, err := s.Decide(q.ID, bad.opt, bad.text, "emil"); err == nil {
			t.Errorf("decided with %v", bad)
		}
	}
	if err := s.CloseQuestion(q.ID, "moot"); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.OpenQuestions(); len(open) != 0 {
		t.Fatalf("open after close: %v", open)
	}
	if err := s.CloseQuestion(q.ID, ""); err == nil {
		t.Error("closed twice")
	}
	if undone, _ := s.UndeliveredAnswers(); len(undone) != 2 {
		t.Fatalf("undelivered answers %d, want the two answered", len(undone))
	}
	_ = s.MarkDelivered([]int64{1})
	if undone, _ := s.UndeliveredAnswers(); len(undone) != 1 {
		t.Fatalf("undelivered after delivery %d, want 1", len(undone))
	}
}

func TestAsksMirrorWithAStableIDAndCloseOnRemoval(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	card := func(asks ...string) {
		if _, err := s.Card("alpha", CardUpdate{Asks: asks, Where: ptr("w")}); err != nil {
			t.Fatal(err)
		}
	}
	card("Which index: 1) products 2) orders? Recommend: 2, it is smaller.", "Ship it yes/no? Recommend: yes", "Plain words?")
	qs, _ := s.OpenQuestions()
	if len(qs) != 3 {
		t.Fatalf("%d questions, want 3: %v", len(qs), qs)
	}
	a, b, c := qs[0], qs[1], qs[2]
	eq(t, "numbered", []any{a.Question, a.Options, a.Recommended, a.Recommendation, a.From},
		[]any{"Which index:", []QOption{{Label: "products"}, {Label: "orders"}}, 2, "2, it is smaller.", "agent"})
	eq(t, "yes recommendation makes yes/no options", []any{b.Options, b.Recommended, b.Recommendation}, []any{[]QOption{{Label: "Yes"}, {Label: "No"}}, 1, "yes"})
	eq(t, "bare", []any{len(c.Options), c.Recommended}, []any{0, 0})
	// A rewrite in another order, with one ask dropped and one added, keeps the ids of the kept.
	card("Plain words?", "Ship it yes/no? Recommend: yes", "A new one?")
	qs, _ = s.OpenQuestions()
	ids := map[string]int64{}
	for _, q := range qs {
		ids[q.Question] = q.ID
	}
	if len(qs) != 3 || ids["Plain words?"] != c.ID || ids["Ship it yes/no?"] != b.ID {
		t.Fatalf("ids after rewrite: %v (want plain %d, ship %d)", ids, c.ID, b.ID)
	}
	if gone, _ := s.GetQuestion(a.ID); gone.Open() {
		t.Error("the removed ask's question is still open")
	}
	// An answered question is not reopened by the ask staying on the card.
	if _, err := s.Decide(b.ID, 0, "yes", "emil"); err != nil {
		t.Fatal(err)
	}
	card("Plain words?", "Ship it yes/no? Recommend: yes", "A new one?")
	if qs, _ = s.OpenQuestions(); len(qs) != 2 {
		t.Fatalf("%d open, want 2", len(qs))
	}
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	if qs, _ = s.OpenQuestions(); len(qs) != 0 {
		t.Fatalf("%d open after the track closed", len(qs))
	}
}

func TestParseAsk(t *testing.T) {
	for in, want := range map[string]ParsedAsk{
		"Which? (1) one (2) two (3) three or four Recommend: 1":      {"Which?", []QOption{{Label: "one"}, {Label: "two"}, {Label: "three or four"}}, 1, "1", nil},
		"Use a/b? Recommend: b because fast":                         {Question: "Use a/b?"},
		"Read Documents/la-delivery-model-audit first":               {Question: "Read Documents/la-delivery-model-audit first"},
		"Which? 1) one 2) two 3) three Recommend: 1, 2 and 5 first.": {"Which?", []QOption{{Label: "one"}, {Label: "two"}, {Label: "three"}}, 0, "1, 2 and 5 first.", nil},
		"Which? 1) one 2) two Recommend: 2 since 1 is slow":          {"Which?", []QOption{{Label: "one"}, {Label: "two"}}, 0, "2 since 1 is slow", nil},
		"Which? 1) one 2) two Recommend: yes":                        {"Which?", []QOption{{Label: "one"}, {Label: "two"}}, 0, "yes", nil},
		"1. only one":                                                {Question: "1. only one"},
		"Ship it? Recommend: yes, it is flagged":                     {"Ship it?", []QOption{{Label: "Yes"}, {Label: "No"}}, 1, "yes, it is flagged", nil},
		"Ship it? Recommend: No since CI is red":                     {"Ship it?", []QOption{{Label: "Yes"}, {Label: "No"}}, 2, "No since CI is red", nil},
		"Ship it? Recommend: YES.":                                   {"Ship it?", []QOption{{Label: "Yes"}, {Label: "No"}}, 1, "YES.", nil},
		"Which window? Recommend: nobody should":                     {Question: "Which window?"},
		"Retry window? Recommend: 72h since the carrier retries":     {Question: "Retry window?"},
		"Why is it red?":                                             {Question: "Why is it red?"},
	} {
		got := ParseAsk(in)
		if got.Question != want.Question || got.Recommended != want.Recommended || !reflect.DeepEqual(got.Options, want.Options) {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
}

func TestMigrationMirrorsTheOpenAsksAlreadyThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"Go? 1) yes 2) no Recommend: 1"}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM questions`, `PRAGMA user_version = 8`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2 := openAt(t, path, testClock())
	qs, _ := s2.OpenQuestions()
	if len(qs) != 1 || qs[0].Recommended != 1 || qs[0].Track != "alpha" {
		t.Fatalf("after migrating: %+v", qs)
	}
}

// oldDB is a database as schema from left it: the columns and table later schemas added are gone.
func oldDB(t *testing.T, from int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"Go? 1) yes 2) no Recommend: 1"}}); err != nil {
		t.Fatal(err)
	}
	drops := []string{`ALTER TABLE questions DROP COLUMN attachments`}
	if from < 11 {
		drops = append(drops, `DROP TABLE conversation`, `ALTER TABLE desk DROP COLUMN claude_session`)
	}
	if from < 10 {
		drops = append(drops, `DELETE FROM questions`, `ALTER TABLE questions DROP COLUMN ask_id`, `ALTER TABLE questions DROP COLUMN recommendation`)
	}
	if from < 9 {
		drops = append(drops, `DROP TABLE desk_queue`)
	}
	for _, q := range append(drops, fmt.Sprintf(`PRAGMA user_version = %d`, from)) {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	return path
}

func TestDatabasesAt8To10MigrateToCurrentInOneOpenWithOneBackup(t *testing.T) {
	for _, from := range []int{8, 9, 10, 11} {
		path := oldDB(t, from)
		var backups []int
		s, err := Open(path, WithClock(testClock()), WithBackup(func(v int, copyDB func(string) error) error {
			backups = append(backups, v)
			return copyDB(filepath.Join(t.TempDir(), "copy.db"))
		}))
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "backups from "+fmt.Sprint(from), backups, []int{from})
		eq(t, "version", userVersion(t, s), schemaVersion)
		qs, _ := s.OpenQuestions()
		if len(qs) != 1 || qs[0].Recommended != 1 {
			t.Errorf("from %d: asks not mirrored: %+v", from, qs)
		}
		for _, q := range []string{`SELECT count(*) FROM desk_queue`, `SELECT count(*) FROM conversation`, `SELECT claude_session FROM desk`} {
			if _, err := s.db.Exec(q); err != nil {
				t.Errorf("from %d: %v", from, err)
			}
		}
		s.Close()
	}
}

func TestMigrateFromSchema8AddsQuestionColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	for _, q := range []string{`ALTER TABLE questions DROP COLUMN ask_id`, `ALTER TABLE questions DROP COLUMN recommendation`, `ALTER TABLE questions DROP COLUMN attachments`, `PRAGMA user_version = 8`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2 := openAt(t, path, testClock())
	mustTrack(t, s2, "alpha")
	if _, err := s2.Card("alpha", CardUpdate{Asks: []string{"Go? yes/no"}}); err != nil {
		t.Fatal(err)
	}
	if qs, _ := s2.OpenQuestions(); len(qs) != 1 {
		t.Fatalf("%d questions after migrating", len(qs))
	}
}

func askedStore(t *testing.T, path string) *Store {
	t.Helper()
	s := openAt(t, path, testClock())
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"Go? 1) yes 2) no Recommend: 1", "Ship? 1) yes 2) no Recommend: 1"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnAnswerIsQueuedUntilItIsMarkedDelivered(t *testing.T) {
	s := askedStore(t, filepath.Join(t.TempDir(), "tracks.db"))
	if _, err := s.Decide(1, 1, "", "emil"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideDelivered(2, 2, "", "emil"); err != nil {
		t.Fatal(err)
	}
	qs, err := s.QueuedAnswers()
	if err != nil || len(qs) != 1 || qs[0].ID != 1 || !qs[0].DeliveredAt.IsZero() {
		t.Fatalf("queued %+v %v", qs, err)
	}
	if err := s.MarkDelivered([]int64{1}); err != nil {
		t.Fatal(err)
	}
	if qs, _ := s.QueuedAnswers(); len(qs) != 0 {
		t.Errorf("still queued: %+v", qs)
	}
}

func TestReopenQuestionPutsAnUndeliveredAnswerBackWithANote(t *testing.T) {
	s := askedStore(t, filepath.Join(t.TempDir(), "tracks.db"))
	s.Decide(1, 1, "", "emil")
	s.DecideDelivered(2, 1, "", "emil")
	for _, id := range []int64{1, 2} {
		if err := s.ReopenQuestion(id, "could not type"); err != nil {
			t.Fatal(err)
		}
	}
	one, _ := s.GetQuestion(1)
	if !one.Open() || !one.AnsweredAt.IsZero() || one.Answer() != "" || len(one.Evidence) != 1 || one.Evidence[0].Text != "could not type" {
		t.Errorf("not reopened: %+v", one)
	}
	if two, _ := s.GetQuestion(2); two.Open() || two.AnsweredAt.IsZero() {
		t.Errorf("a delivered answer was taken back: %+v", two)
	}
}

func TestMigrationTo13MarksEarlierAgentAnswersDelivered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := askedStore(t, path)
	s.Decide(1, 1, "", "emil")
	if _, err := s.db.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openAt(t, path, testClock())
	if qs, _ := s.QueuedAnswers(); len(qs) != 0 {
		t.Errorf("an answer from before the queue is queued: %+v", qs)
	}
	if q, _ := s.GetQuestion(1); q.DeliveredAt.IsZero() {
		t.Error("not marked delivered")
	}
}
