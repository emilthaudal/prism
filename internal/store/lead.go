package store

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotLead        = errors.New("track is not a lead (role: lead)")
	ErrBadRole        = errors.New("role must be lead, or empty to clear it")
	ErrIsChild        = errors.New("a thread cannot be a lead")
	ErrTooManyThreads = errors.New("the lead has as many open threads as it may")
)

// ThreadCapError is ErrTooManyThreads with the lead's open threads counted.
type ThreadCapError struct{ Open, Max int }

func (e *ThreadCapError) Error() string {
	return fmt.Sprintf("%s: %d open, at most %d", ErrTooManyThreads, e.Open, e.Max)
}

func (e *ThreadCapError) Is(target error) bool { return target == ErrTooManyThreads }

// MetaVerify is a thread's own check command, run by tracks verify after lefthook's pre-push.
const MetaVerify = "verify"

// Role is the track's role, "" for an ordinary track.
func (t Track) Role() string { return t.Extra.Meta[MetaRole] }

// Parent is the name of the lead this track is a thread of, "" for an ordinary track or a lead.
func (t Track) Parent() string { return t.Extra.Meta[MetaParent] }

func setMetaKey(t *Track, key, val string) {
	if val == "" {
		delete(t.Extra.Meta, key)
		return
	}
	if t.Extra.Meta == nil {
		t.Extra.Meta = map[string]string{}
	}
	t.Extra.Meta[key] = val
}

// setRole makes t a lead or an ordinary track. A thread is refused: its parent is fixed by
// Spawn and a thread must not run threads of its own.
func setRole(t *Track, role string) error {
	switch {
	case role != "" && role != RoleLead:
		return ErrBadRole
	case role != "" && t.Parent() != "":
		return ErrIsChild
	}
	setMetaKey(t, MetaRole, role)
	return nil
}

// SpawnSpec is the thread a lead starts: its name, directory, goal and optional verify
// command. The tier is the lead's. Max, when above zero, is how many open threads the lead
// may have, this one included.
type SpawnSpec struct {
	Name, Dir, Goal, Verify string
	Max                     int
}

// Spawn makes a thread of the lead the name points to, in one transaction with the lead's
// check: the lead must exist, be open, carry role: lead and have room under spec.Max. The
// thread's parent is the lead's current name, and nothing but a rename of the lead writes it
// afterwards.
func (s *Store) Spawn(lead string, spec SpawnSpec) (Track, error) {
	var out Track
	err := s.tx(func(tx *wtx) error {
		r, err := resolve(tx, lead)
		switch {
		case err != nil:
			return err
		case r.Closed:
			return ErrClosed
		case r.Track.Role() != RoleLead:
			return ErrNotLead
		case r.Track.Parent() != "":
			return ErrIsChild
		}
		if spec.Max > 0 {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM tracks WHERE closed_at IS NULL AND json_extract(extra, '$.meta.parent') = ? COLLATE NOCASE`, r.Track.Name).Scan(&n); err != nil {
				return err
			}
			if n >= spec.Max {
				return &ThreadCapError{Open: n, Max: spec.Max}
			}
		}
		meta := map[string]string{MetaParent: r.Track.Name}
		if spec.Verify != "" {
			meta[MetaVerify] = spec.Verify
		}
		out, err = insertTrack(s, tx, NewTrackSpec{
			Name: spec.Name, Mode: "focus", Tier: r.Track.Tier, Dir: spec.Dir, Goal: spec.Goal, Meta: meta,
		})
		return err
	})
	return out, err
}

// DeleteThread removes a thread that never got to work, as when its agent stopped at a prompt
// at start: its rows go, its name is free again, and its events stay without the track.
func (s *Store) DeleteThread(name string) error { return s.deleteUnstarted(name, true) }

// DeleteUnstarted removes any open track that never got to work, as DeleteThread does a thread.
func (s *Store) DeleteUnstarted(name string) error { return s.deleteUnstarted(name, false) }

func (s *Store) deleteUnstarted(name string, thread bool) error {
	_, err := s.commit(func(tx *wtx) error {
		r, err := resolve(tx, name)
		switch {
		case err != nil:
			return err
		case thread && r.Track.Parent() == "":
			return fmt.Errorf("%s is not a thread", name)
		case r.Closed:
			return fmt.Errorf("%s is closed: its outcome stays", name)
		case strings.TrimSpace(r.Track.Where) != notStarted:
			return fmt.Errorf("%s has started work: only a thread that never started can be deleted", name)
		}
		id := r.Track.ID
		// The tracks that wait on this thread drop it from their blocked_by, and re-render.
		if err := touchBlocked(tx, id); err != nil {
			return err
		}
		var asks int
		if err := tx.QueryRow(`SELECT count(*) FROM asks WHERE track_id = ?`, id).Scan(&asks); err != nil {
			return err
		}
		if asks > 0 {
			return fmt.Errorf("%s has asks: only a thread that never started can be deleted", name)
		}
		qs := []string{
			`UPDATE events SET track_id = NULL WHERE track_id = ?`,
			`DELETE FROM asks WHERE track_id = ?`,
			`DELETE FROM checks WHERE track_id = ?`,
			`DELETE FROM names WHERE track_id = ?`,
			`DELETE FROM blockers WHERE track_id = ?1 OR blocker_id = ?1`,
			`DELETE FROM milestones WHERE track_id = ?`,
		}
		if !s.stale {
			qs = append(qs, `UPDATE sessions SET track_id = NULL WHERE track_id = ?`, `UPDATE questions SET track_id = NULL WHERE track_id = ?`,
				`DELETE FROM desk_queue WHERE track_id = ?`)
		}
		for _, q := range append(qs, `DELETE FROM tracks WHERE id = ?`) {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// renameChildren points the open threads of a renamed lead at its new name.
func (s *Store) renameChildren(tx *wtx, oldName, newName string) error {
	rows, err := tx.Query(`SELECT id FROM tracks WHERE closed_at IS NULL AND json_extract(extra, '$.meta.parent') = ? COLLATE NOCASE`, oldName)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		c, err := trackByID(tx, id)
		if err != nil {
			return err
		}
		setMetaKey(&c, MetaParent, newName)
		if err := s.save(tx, &c, false); err != nil {
			return fmt.Errorf("thread %s: %w", c.Name, err)
		}
	}
	return nil
}
