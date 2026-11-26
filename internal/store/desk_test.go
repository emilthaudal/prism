package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// seedAsk writes a desk ask as the pane's band raised them, for the readers that still show them.
func seedAsk(t *testing.T, s *Store, track, question, rec string, evidence ...string) DeskAsk {
	t.Helper()
	ev, err := json.Marshal(append([]string{}, evidence...))
	if err != nil {
		t.Fatal(err)
	}
	at := s.now()
	res, err := s.db.Exec(`INSERT INTO desk_asks (track, question, recommendation, evidence, created_at) VALUES (?, ?, ?, ?, ?)`, track, question, rec, string(ev), fmtTime(at))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return DeskAsk{ID: id, Track: track, Question: question, Recommendation: rec, Evidence: evidence, CreatedAt: at}
}

// seedAnswer records Emil's band answer to ask id, as the band did.
func seedAnswer(t *testing.T, s *Store, id int64, kind, text string) {
	t.Helper()
	at := fmtTime(s.now())
	if _, err := s.db.Exec(`UPDATE desk_asks SET answer_kind = ?, answer = ?, answered_at = ?, closed_at = ? WHERE id = ?`, kind, text, at, at, id); err != nil {
		t.Fatal(err)
	}
}

func seedPickUp(t *testing.T, s *Store, id int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE desk_asks SET picked_up_at = ? WHERE id = ?`, fmtTime(s.now()), id); err != nil {
		t.Fatal(err)
	}
}

func TestAStoredDeskAskReadsBackWithItsEvidence(t *testing.T) {
	s := newStore(t)
	a := seedAsk(t, s, "pan-88", "merge #212?", "yes", "CI green", "approved by Ana")
	v, err := s.Desk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Asks) != 1 || v.Asks[0].ID != a.ID || v.Asks[0].Question != "merge #212?" || v.Asks[0].Recommendation != "yes" ||
		!reflect.DeepEqual(v.Asks[0].Evidence, []string{"CI green", "approved by Ana"}) || !v.Asks[0].CreatedAt.Equal(a.CreatedAt) {
		t.Errorf("open asks %+v, want %+v", v.Asks, a)
	}
}

func TestStoredDeskAsksComeOldestFirst(t *testing.T) {
	s := newStore(t)
	first := seedAsk(t, s, "a", "one?", "")
	second := seedAsk(t, s, "b", "two?", "")
	v, _ := s.Desk(time.Time{})
	if len(v.Asks) != 2 || v.Asks[0].ID != first.ID || v.Asks[1].ID != second.ID {
		t.Errorf("asks %+v", v.Asks)
	}
}

func TestClosingADeskAskRemovesItFromTheOpenAsksOnce(t *testing.T) {
	s := newStore(t)
	a := seedAsk(t, s, "a", "one?", "")
	if err := s.CloseDeskAsk(a.ID); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Desk(time.Time{}); len(v.Asks) != 0 {
		t.Errorf("a closed ask is still open: %+v", v.Asks)
	}
	if err := s.CloseDeskAsk(a.ID); !errors.Is(err, ErrNoDeskAsk) {
		t.Errorf("closing it twice: %v", err)
	}
	if err := s.CloseDeskAsk(999); !errors.Is(err, ErrNoDeskAsk) {
		t.Errorf("closing an unknown ask: %v", err)
	}
	if got, _ := s.DeskAnswers(time.Time{}); len(got) != 0 {
		t.Errorf("a closed ask is an answer: %+v", got)
	}
}

// An ask the band raised when Emil took back a desk answer closes like any other now.
func TestClosingAnAskTheUndoRaised(t *testing.T) {
	s := newStore(t)
	a := seedAsk(t, s, "a", "one?", "")
	if _, err := s.db.Exec(`UPDATE desk_asks SET raised_by = 'undo' WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseDeskAsk(a.ID); err != nil {
		t.Errorf("closing it: %v", err)
	}
}

func TestDeskLogKeepsTimeTrackKindAskedAndDid(t *testing.T) {
	s := newStore(t)
	e, err := s.LogDesk("pan-88", "nudged", "idle 40m", "told it lint is red on #212")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Desk(time.Time{})
	if len(v.Log) != 1 || !reflect.DeepEqual(v.Log[0], e) || e.At.IsZero() || e.ID == 0 {
		t.Errorf("log %+v, want [%+v]", v.Log, e)
	}
	if e.Track != "pan-88" || e.Kind != "nudged" || e.Asked != "idle 40m" || e.Did != "told it lint is red on #212" {
		t.Errorf("entry %+v", e)
	}
}

func TestDeskLogTakesEveryKindAndNoOther(t *testing.T) {
	s := newStore(t)
	for _, k := range DeskKinds {
		if _, err := s.LogDesk("a", k, "", "did it"); err != nil {
			t.Errorf("kind %s: %v", k, err)
		}
	}
	if _, err := s.LogDesk("a", "merged", "", "did it"); !errors.Is(err, ErrBadKind) {
		t.Errorf("kind merged: %v", err)
	}
	if _, err := s.LogDesk("a", "noted", "", " "); !errors.Is(err, ErrDeskFields) {
		t.Errorf("empty did: %v", err)
	}
	if _, err := s.LogDesk("a b", "noted", "", "x"); !errors.Is(err, ErrBadName) {
		t.Errorf("bad track: %v", err)
	}
	if v, _ := s.Desk(time.Time{}); len(v.Log) != len(DeskKinds) {
		t.Errorf("%d entries, want %d", len(v.Log), len(DeskKinds))
	}
}

func TestDeskLogReadsFromAPointInTime(t *testing.T) {
	s := newStore(t)
	old, _ := s.LogDesk("a", "noted", "", "old")
	cut := old.At.Add(time.Microsecond)
	fresh, _ := s.LogDesk("a", "noted", "", "fresh")
	v, _ := s.Desk(cut)
	if len(v.Log) != 1 || v.Log[0].ID != fresh.ID {
		t.Errorf("log since the cut: %+v", v.Log)
	}
	if v, _ := s.Desk(old.At); len(v.Log) != 2 {
		t.Errorf("a log entry made at the cut is read: %+v", v.Log)
	}
}

func TestDeskSessionBindsOnceAndMovesOnlyWhenForced(t *testing.T) {
	s := newStore(t)
	if sess, err := s.DeskSession(); err != nil || sess != "" {
		t.Fatalf("a new desk: %q, %v", sess, err)
	}
	if err := s.BindDesk("", false); err == nil {
		t.Error("bound the desk to no session")
	}
	if err := s.BindDesk("session:a", false); err != nil {
		t.Fatal(err)
	}
	if err := s.BindDesk("session:a", false); err != nil {
		t.Errorf("binding the same session again: %v", err)
	}
	if err := s.BindDesk("session:b", false); !errors.Is(err, ErrDeskBound) {
		t.Errorf("binding another session: %v", err)
	}
	if sess, _ := s.DeskSession(); sess != "session:a" {
		t.Errorf("a refused bind moved the desk to %q", sess)
	}
	if err := s.BindDesk("session:b", true); err != nil {
		t.Fatal(err)
	}
	if sess, _ := s.DeskSession(); sess != "session:b" {
		t.Errorf("a forced bind left the desk at %q", sess)
	}
}

func TestADatabaseFromBeforeTheDeskGainsItsTablesAndKeepsItsTracks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "old")
	for _, q := range []string{`DROP TABLE desk`, `DROP TABLE desk_asks`, `DROP TABLE desk_log`, `PRAGMA user_version = 1`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	seedAsk(t, s, "old", "still here?", "")
	if _, err := s.Get("old"); err != nil {
		t.Errorf("the track is gone: %v", err)
	}
}

func TestDeskAnswersListBandAnswersNotPickedUpFromAPointInTime(t *testing.T) {
	s := newStore(t)
	a := seedAsk(t, s, "pan-88", "merge #212?", "yes")
	b := seedAsk(t, s, "as", "which user?", "")
	c := seedAsk(t, s, "cc", "which db?", "")
	seedAnswer(t, s, a.ID, "accepted", "yes")
	old, _ := s.DeskAnswers(time.Time{})
	seedAnswer(t, s, b.ID, "reply", "the separate one")
	seedAnswer(t, s, c.ID, "reply", "postgres")
	seedPickUp(t, s, c.ID)
	if v, _ := s.Desk(time.Time{}); len(v.Asks) != 0 {
		t.Errorf("answered asks are still open: %+v", v.Asks)
	}
	got, err := s.DeskAnswers(time.Time{})
	if err != nil || len(got) != 2 || got[0].ID != a.ID || got[0].AnswerKind != "accepted" || got[0].Answer != "yes" || got[0].AnsweredAt.IsZero() ||
		got[1].ID != b.ID || got[1].Answer != "the separate one" {
		t.Fatalf("answers %+v, %v", got, err)
	}
	if got, _ := s.DeskAnswers(old[0].AnsweredAt.Add(time.Microsecond)); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("answers since the cut: %+v", got)
	}
}

func TestDeskLogRecentIsNewestFirstAndCapped(t *testing.T) {
	s := newStore(t)
	for _, d := range []string{"one", "two", "three"} {
		s.LogDesk("a", "noted", "", d)
	}
	got, err := s.DeskLogRecent(2)
	if err != nil || len(got) != 2 || got[0].Did != "three" || got[1].Did != "two" {
		t.Errorf("recent 2: %+v, %v", got, err)
	}
	if all, _ := s.DeskLogRecent(0); len(all) != 3 {
		t.Errorf("recent 0 reads %d, want all 3", len(all))
	}
}

func TestADeskFromBeforePickUpsGainsTheColumnAndKeepsItsAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	a := seedAsk(t, s, "old", "still here?", "yes")
	seedAnswer(t, s, a.ID, "accepted", "yes")
	for _, q := range []string{`ALTER TABLE desk_asks DROP COLUMN picked_up_at`, `PRAGMA user_version = 3`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	if got, _ := s.DeskAnswers(time.Time{}); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("the answer is gone: %+v", got)
	}
}

func TestADeskFromBeforeAnswersGainsTheAnswerColumnsAndKeepsItsAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	a := seedAsk(t, s, "old", "still here?", "yes")
	for _, q := range []string{`ALTER TABLE desk_asks DROP COLUMN answer_kind`, `ALTER TABLE desk_asks DROP COLUMN answer`, `ALTER TABLE desk_asks DROP COLUMN answered_at`, `PRAGMA user_version = 2`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	if v, _ := s.Desk(time.Time{}); len(v.Asks) != 1 || v.Asks[0].ID != a.ID {
		t.Fatalf("the ask is gone: %+v", v.Asks)
	}
	if err := s.CloseDeskAsk(a.ID); err != nil {
		t.Errorf("closing after the upgrade: %v", err)
	}
}

func TestADeskLogFromBeforeUndoGainsTheColumnAndKeepsItsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	e, err := s.LogDesk("old", "answered", "which version?", "4.6.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE desk_log DROP COLUMN undone_at`, `PRAGMA user_version = 4`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	got, err := s.DeskEntry(e.ID)
	if err != nil || got.Did != "4.6.0" || !got.UndoneAt.IsZero() {
		t.Fatalf("the entry after the upgrade: %+v, %v", got, err)
	}
	if err := s.UndoDeskAnswer(e.ID); err != nil {
		t.Errorf("taking it back after the upgrade: %v", err)
	}
	if got, _ := s.DeskEntry(e.ID); got.UndoneAt.IsZero() {
		t.Error("the entry is not marked taken back")
	}
	if err := s.UndoDeskAnswer(e.ID); !errors.Is(err, ErrUndone) {
		t.Errorf("a second undo: %v", err)
	}
}

func TestADeskFromBeforeRaisedByGainsTheColumnAtV6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	e, err := s.LogDesk("old", "answered", "which version?", "4.6.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE desk_asks DROP COLUMN raised_by`, `PRAGMA user_version = 5`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version %d, %v", v, err)
	}
	if err := s.UndoDeskAnswer(e.ID); err != nil {
		t.Fatalf("taking it back after the upgrade: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE desk_asks SET raised_by = 'undo'`); err != nil {
		t.Errorf("raised_by after the upgrade: %v", err)
	}
}
