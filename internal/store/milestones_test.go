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
	eq(t, "user_version", userVersion(t, s), 16)
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
