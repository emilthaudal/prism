package store

import (
	"strings"
	"testing"
)

// DeleteThread is for a thread whose agent never started; anything with work, asks or an
// outcome stays.
func TestDeleteThreadOnlyTakesAThreadThatNeverStarted(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "boss")
	mustTrack(t, s, "plain")
	if _, err := s.Card("boss", CardUpdate{Role: ptr(RoleLead)}); err != nil {
		t.Fatal(err)
	}
	spawn := func(name string) {
		t.Helper()
		if _, err := s.Spawn("boss", SpawnSpec{Name: name, Dir: "/work/" + name, Goal: "g"}); err != nil {
			t.Fatal(err)
		}
	}
	spawn("fresh")
	spawn("worked")
	spawn("asking")
	spawn("closed")
	if _, err := s.Card("worked", CardUpdate{Where: ptr("did a lot of work")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Card("asking", CardUpdate{Asks: []string{"Which one? Recommend: A."}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseTrack("closed"); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"plain":  "is not a thread",
		"boss":   "is not a thread",
		"worked": "has started work",
		"asking": "has asks",
		"closed": "is closed",
	} {
		if err := s.DeleteThread(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("DeleteThread(%s) = %v, want an error with %q", name, err, want)
		}
		if _, err := s.Get(name); err != nil {
			t.Errorf("%s is gone after a refused delete: %v", name, err)
		}
	}
	// plain waits on fresh: deleting fresh drops it from plain's blocked_by.
	if _, err := s.Card("plain", CardUpdate{BlockedBy: &[]string{"fresh"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteThread("fresh"); err != nil {
		t.Fatalf("DeleteThread(fresh) = %v", err)
	}
	if _, err := s.Get("fresh"); err == nil {
		t.Error("fresh is still there")
	}
	if p := get(t, s, "plain"); p.Track.BlockedBy != nil {
		t.Errorf("plain still waits on the deleted thread: %v", p.Track.BlockedBy)
	}
}
