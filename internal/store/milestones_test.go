package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func titles(l []Milestone) []string {
	out := []string{}
	for _, m := range l {
		out = append(out, m.Title)
	}
	return out
}

func TestAVersion15DatabaseGainsMilestonesAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "a")
	if _, err := s.Card("a", CardUpdate{Where: ptr("halfway"), Asks: []string{"Merge?"}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE milestones`, `PRAGMA user_version = 15`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	eq(t, "user_version", userVersion(t, s), 17)
	a := get(t, s, "a")
	if a.Track.Where != "halfway" || len(a.Asks) != 1 {
		t.Errorf("a after the upgrade: %+v", a.Track)
	}
	if l, err := s.Milestones("a"); err != nil || len(l) != 0 {
		t.Fatalf("milestones after the upgrade: %v, %v", l, err)
	}
	if _, err := s.SetMilestones("a", []string{"one"}); err != nil {
		t.Fatal(err)
	}
}

func TestAVersion16DatabaseGainsMilestonePRColumnsAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "a")
	if _, err := s.SetMilestones("a", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MarkMilestone("a", "1", true); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Milestones("a")
	for _, q := range []string{`ALTER TABLE milestones DROP COLUMN pr`, `ALTER TABLE milestones DROP COLUMN pr_state`, `PRAGMA user_version = 16`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	eq(t, "user_version", userVersion(t, s), 17)
	after, err := s.Milestones("a")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("milestones changed by the upgrade: %v -> %v, %v", before, after, err)
	}
	if _, _, err := s.SetMilestonePR("a", "2", "acme/api#4"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordMilestonePR(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "a")
	if _, err := s.SetMilestones("a", []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetMilestonePR("a", "1", "bad"); !errors.Is(err, ErrBadPR) {
		t.Fatalf("bad ref: %v", err)
	}
	if _, _, err := s.SetMilestonePR("a", "1", "acme/api#4"); err != nil {
		t.Fatal(err)
	}
	w, err := s.WatchedMilestones()
	if err != nil || len(w) != 1 || w[0].PR != "acme/api#4" {
		t.Fatalf("watched: %+v %v", w, err)
	}
	tid := w[0].TrackID
	if _, err := s.RecordMilestonePR(tid, 1, "acme/api#4", "bogus"); err == nil {
		t.Error("unknown state accepted")
	}
	// A ref changed since the listing is not written to.
	if res, err := s.RecordMilestonePR(tid, 1, "acme/api#5", "merged"); err != nil || res.Ticked {
		t.Errorf("stale ref: %+v %v", res, err)
	}
	if res, err := s.RecordMilestonePR(tid, 1, "acme/api#4", "merged"); err != nil || !res.Ticked {
		t.Fatalf("merged: %+v %v", res, err)
	}
	if l, _ := s.Milestones("a"); !l[0].Done() || l[0].PRState != "merged" {
		t.Errorf("after merge: %+v", l[0])
	}
	if w, _ := s.WatchedMilestones(); len(w) != 0 {
		t.Errorf("a reached milestone is still watched: %+v", w)
	}
}

func TestSetTickUntickAndListMilestones(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "a")
	mustTrack(t, s, "b")
	if _, err := s.SetMilestones("a", []string{"design agreed", "store done", "app shows it"}); err != nil {
		t.Fatal(err)
	}
	if m, n, err := s.MarkMilestone("a", "2", true); err != nil || n != 2 || m.Title != "store done" || !m.Done() {
		t.Fatalf("tick by number: %+v %d %v", m, n, err)
	}
	if _, n, err := s.MarkMilestone("a", "app  shows it", true); err != nil || n != 3 {
		t.Fatalf("tick by title: %d %v", n, err)
	}
	if _, _, err := s.MarkMilestone("a", "nope", true); !errors.Is(err, ErrNoMilestone) {
		t.Errorf("unknown: %v", err)
	}
	if _, _, err := s.MarkMilestone("a", "3", false); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Milestones("a")
	if got := []bool{l[0].Done(), l[1].Done(), l[2].Done()}; !reflect.DeepEqual(got, []bool{false, true, false}) {
		t.Errorf("done: %v", got)
	}
	// A new list keeps the reached state of titles that stay, and drops the rest.
	l, err := s.SetMilestones("a", []string{"first", "store done", "design agreed"})
	if err != nil {
		t.Fatal(err)
	}
	if got := []bool{l[0].Done(), l[1].Done(), l[2].Done()}; !reflect.DeepEqual(got, []bool{false, true, false}) {
		t.Errorf("after reset: %v", got)
	}
	if l, _ := s.Milestones("b"); len(l) != 0 {
		t.Errorf("b: %v", l)
	}
	all, err := s.AllMilestones()
	if err != nil || len(all) != 1 {
		t.Fatalf("all: %v %v", all, err)
	}
	if _, err := s.SetMilestones("a", []string{"x", "x"}); !errors.Is(err, ErrDupMilestone) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := s.SetMilestones("a", []string{" "}); !errors.Is(err, ErrEmptyMilestone) {
		t.Errorf("empty: %v", err)
	}
	if l, err := s.SetMilestones("a", nil); err != nil || len(l) != 0 {
		t.Errorf("clear: %v %v", l, err)
	}
	if _, err := s.SetMilestones("nobody", []string{"x"}); !errors.Is(err, ErrNoTrack) {
		t.Errorf("no track: %v", err)
	}
}
