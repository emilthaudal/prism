package store

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenDoesNotCreateAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "tracks.db")
	if _, err := Open(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open of a missing database: %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); err == nil {
		t.Error("Open made the directory")
	}
	s, err := Open(path, WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("Open of an existing database: %v", err)
	}
	s.Close()
}

func TestParseTimeRefusesAnUnknownFormat(t *testing.T) {
	if _, err := parseTime(sql.NullString{String: "2026-11-21T09:00:00Z", Valid: true}); err == nil {
		t.Error("a time in RFC3339 without nanoseconds parsed")
	}
	if tm, err := parseTime(sql.NullString{String: fmtTime(epoch), Valid: true}); err != nil || !tm.Equal(epoch) {
		t.Errorf("own format: %v, %v", tm, err)
	}
	if tm, err := parseTime(sql.NullString{}); err != nil || !tm.IsZero() {
		t.Errorf("NULL: %v, %v", tm, err)
	}
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.db.Exec(`UPDATE tracks SET created_at = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("alpha"); err == nil {
		t.Error("Get returned a track whose stored time is not in the store's format")
	}
}

func TestSchemaHasRenderHash(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	var h string
	if err := s.db.QueryRow(`SELECT render_hash FROM tracks`).Scan(&h); err != nil || h != "" {
		t.Errorf("render_hash = %q, %v", h, err)
	}
	if got := get(t, s, "alpha").Track.RenderHash; got != "" {
		t.Errorf("Track.RenderHash = %q", got)
	}
}

func TestImportWritesTimesInTheStoreFormat(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 20, 10, 0, 0, 123456000, time.FixedZone("x", 3600))
	notes, err := s.Import(ImportData{
		Tracks: []ImportTrack{{
			Track: Track{Name: "alpha", Mode: "focus", UpdatedAt: at, ChangedAt: at, CreatedAt: at, AnsweredAt: at},
			Asks:  []ImportAsk{{Kind: "agent", Text: "q", CreatedAt: at, SeenAt: at}},
		}},
		Checks: []ImportCheck{{Track: "alpha", Text: "pr 1 merged", AskedAt: at, RanAt: at}},
		Events: []ImportEvent{{Event: Event{At: at, Event: "open", TrackName: "alpha"}, Owner: "alpha"}},
	})
	if err != nil || len(notes) != 0 {
		t.Fatalf("Import: %v, %v", err, notes)
	}
	for _, q := range []string{
		`SELECT updated_at FROM tracks`, `SELECT changed_at FROM tracks`, `SELECT created_at FROM tracks`,
		`SELECT answered_at FROM tracks`, `SELECT created_at FROM asks`, `SELECT seen_at FROM asks`,
		`SELECT asked_at FROM checks`, `SELECT ran_at FROM checks`, `SELECT at FROM events`,
	} {
		var v string
		if err := s.db.QueryRow(q).Scan(&v); err != nil || v != fmtTime(at) {
			t.Errorf("%s = %q, %v, want %q", q, v, err, fmtTime(at))
		}
	}
	// Every read parses what Import wrote.
	if _, err := s.AllTracks(); err != nil {
		t.Error(err)
	}
	if _, err := s.AllEvents(); err != nil {
		t.Error(err)
	}
	if _, err := s.AllChecks(); err != nil {
		t.Error(err)
	}
}

func TestImportSkipsWhatTheSchemaCannotHold(t *testing.T) {
	s := newStore(t)
	notes, err := s.Import(ImportData{
		Tracks: []ImportTrack{
			{Track: Track{Name: "dup", Mode: "focus"}},
			{Track: Track{Name: "DUP", Mode: "focus"}},
		},
		Checks: []ImportCheck{{Track: "ghost", Text: "pr 1 merged"}},
		Events: []ImportEvent{{Event: Event{Event: "open", TrackName: "ghost"}, Owner: "ghost"}},
	})
	if err != nil || len(notes) != 3 {
		t.Errorf("Import: %v, notes %q, want three", err, notes)
	}
}
