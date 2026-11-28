package store

import "testing"

// heldNoteOnTrack starts a busy desk session on a new track and holds a note for it.
func heldNoteOnTrack(t *testing.T, s *Store, track, id string) {
	t.Helper()
	mustTrack(t, s, track)
	startDeskOwned(t, s, id)
	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: id, Prompt: "go", StartPrompt: "go"})
	if _, err := s.db.Exec(`UPDATE sessions SET track_id = (SELECT id FROM tracks WHERE name = ?) WHERE id = ?`, track, id); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SendNote(id, "desk", "later", false, false); err != nil || n.Deliver {
		t.Fatalf("held note: %+v, %v", n, err)
	}
}

func heldCount(t *testing.T, s *Store, id string) (n int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT count(*) FROM messages WHERE session_id = ? AND kind = 'note' AND held = 1 AND delivered_at IS NULL`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func noteDropEvents(t *testing.T, s *Store, kind string) (n int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE event = 'note_drop' AND kind = ?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// EMI-498: a note held for a closed track failed at every stop.
func TestClosingATrackDropsItsHeldNoteAndLogsIt(t *testing.T) {
	s := newStore(t)
	heldNoteOnTrack(t, s, "alpha", "a1")
	heldNoteOnTrack(t, s, "beta", "b1")
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	eq(t, "alpha held", heldCount(t, s, "a1"), 0)
	eq(t, "beta held", heldCount(t, s, "b1"), 1)
	eq(t, "close drops logged", noteDropEvents(t, s, "close"), 1)
}

func TestDropNoteDeletesTheHeldNoteByHand(t *testing.T) {
	s := newStore(t)
	heldNoteOnTrack(t, s, "alpha", "a1")
	n, err := s.DropNote("a1")
	if err != nil || n != 1 {
		t.Fatalf("DropNote = %d, %v", n, err)
	}
	eq(t, "held", heldCount(t, s, "a1"), 0)
	if n, err = s.DropNote("a1"); err != nil || n != 0 {
		t.Fatalf("second DropNote = %d, %v", n, err)
	}
	eq(t, "drop logged", noteDropEvents(t, s, "drop"), 1)
}

func TestDropClosedNotesRemovesNotesLeftOnClosedTracks(t *testing.T) {
	s := newStore(t)
	heldNoteOnTrack(t, s, "alpha", "a1")
	heldNoteOnTrack(t, s, "beta", "b1")
	// a note left by a build before closing dropped it
	if _, err := s.db.Exec(`UPDATE tracks SET closed_at = '2026-11-20T10:00:00Z' WHERE name = 'alpha'`); err != nil {
		t.Fatal(err)
	}
	n, err := s.DropClosedNotes()
	if err != nil || n != 1 {
		t.Fatalf("DropClosedNotes = %d, %v", n, err)
	}
	eq(t, "alpha held", heldCount(t, s, "a1"), 0)
	eq(t, "beta held", heldCount(t, s, "b1"), 1)
	eq(t, "sweep logged", noteDropEvents(t, s, "closed"), 1)
}
