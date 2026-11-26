package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestAVersion6DatabaseKeepsItsTracksAndGainsThePortfolioAtV7(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "a")
	mustTrack(t, s, "b")
	if _, err := s.Card("a", CardUpdate{Where: ptr("halfway"), Asks: []string{"Merge?"}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE blockers`, `ALTER TABLE tracks DROP COLUMN rank`, `ALTER TABLE tracks DROP COLUMN today`, `PRAGMA user_version = 6`} {
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
	live, err := s.LiveTracks()
	if err != nil || len(live) != 2 {
		t.Fatalf("tracks after the upgrade: %d, %v", len(live), err)
	}
	a := get(t, s, "a")
	if a.Track.Where != "halfway" || len(a.Asks) != 1 || a.Track.Rank != 0 || a.Track.Today != "" || a.Track.BlockedBy != nil {
		t.Errorf("a after the upgrade: %+v asks %v", a.Track, texts(a.Asks))
	}
	if _, err := s.Card("a", CardUpdate{Rank: intp(3), Today: ptr("merged"), BlockedBy: &[]string{"b"}}); err != nil {
		t.Fatalf("the portfolio after the upgrade: %v", err)
	}
	a = get(t, s, "a")
	if a.Track.Rank != 3 || a.Track.Today != "merged" || !reflect.DeepEqual(a.Track.BlockedBy, []string{"b"}) {
		t.Errorf("a: rank %d today %q blocked_by %v", a.Track.Rank, a.Track.Today, a.Track.BlockedBy)
	}
}

func TestThePortfolioSurvivesWritesThatLeaveItOut(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "a")
	mustTrack(t, s, "b")
	mustTrack(t, s, "c")
	if _, err := s.Card("a", CardUpdate{Rank: intp(2), Today: ptr("ship it"), BlockedBy: &[]string{"c", "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Card("a", CardUpdate{Where: ptr("later"), Asks: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("a", "waiting"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind("a", "sess", "/x"); err != nil {
		t.Fatal(err)
	}
	a := get(t, s, "a").Track
	if a.Rank != 2 || a.Today != "ship it" || !reflect.DeepEqual(a.BlockedBy, []string{"c", "b"}) {
		t.Errorf("rank %d today %q blocked_by %v", a.Rank, a.Today, a.BlockedBy)
	}
	text := Render(a, nil)
	for _, want := range []string{"rank: 2 ", "today: ship it ", "blocked_by: c, b "} {
		if !strings.Contains(text, want) {
			t.Errorf("render lacks %q:\n%s", want, text)
		}
	}
	if _, err := s.Card("a", CardUpdate{Rank: intp(0), Today: ptr(""), BlockedBy: &[]string{}}); err != nil {
		t.Fatal(err)
	}
	a = get(t, s, "a").Track
	if a.Rank != 0 || a.Today != "" || a.BlockedBy != nil {
		t.Errorf("after clearing: rank %d today %q blocked_by %v", a.Rank, a.Today, a.BlockedBy)
	}
	if text := Render(a, nil); strings.Contains(text, "rank:") || strings.Contains(text, "today:") || strings.Contains(text, "blocked_by:") {
		t.Errorf("an unset portfolio still renders:\n%s", text)
	}
}

func TestBlockedByRefusesWhatIsNotAnotherOpenTrackAndWritesNothing(t *testing.T) {
	s := newStore(t)
	for _, n := range []string{"a", "b", "c", "gone"} {
		mustTrack(t, s, n)
	}
	if err := s.CloseTrack("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Card("b", CardUpdate{BlockedBy: &[]string{"c"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Card("c", CardUpdate{BlockedBy: &[]string{"a"}}); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"nosuch"}, {"gone"}, {"a"}, {"c", "b"}} {
		if _, err := s.Card("a", CardUpdate{Today: ptr("x"), BlockedBy: &names}); !errors.Is(err, ErrBadBlocker) {
			t.Errorf("blocked_by %v: %v, want ErrBadBlocker", names, err)
		}
	}
	if a := get(t, s, "a").Track; a.Today != "" || a.BlockedBy != nil {
		t.Errorf("a refused write landed: today %q blocked_by %v", a.Today, a.BlockedBy)
	}
	if _, err := s.Card("a", CardUpdate{Rank: intp(-1)}); !errors.Is(err, ErrBadRank) {
		t.Errorf("rank -1: %v", err)
	}
}

func TestImportReadsThePortfolioFromTheCards(t *testing.T) {
	s := newStore(t)
	d := ImportData{Tracks: []ImportTrack{
		{Track: Track{Name: "a", Mode: "focus", Rank: 1, Today: "ship"}, BlockedBy: []string{"old-b", "nosuch"}},
		{Track: Track{Name: "b", Mode: "background"}, Retired: []RetiredName{{Name: "old-b"}}},
	}}
	notes, err := s.Import(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "nosuch") {
		t.Errorf("notes %v", notes)
	}
	a := get(t, s, "a").Track
	if a.Rank != 1 || a.Today != "ship" || !reflect.DeepEqual(a.BlockedBy, []string{"b"}) {
		t.Errorf("rank %d today %q blocked_by %v", a.Rank, a.Today, a.BlockedBy)
	}
}
