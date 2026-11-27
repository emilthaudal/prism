package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// milestonesSchema is the schema that added the milestones table (EMI-485).
const milestonesSchema = 16

// A track's milestones are the steps to its goal: an ordered list of outcomes someone could
// check, each reached or not. Only the track's agent changes the list. A milestone is not tied
// to a check and never ticks itself.

var (
	ErrNoMilestone    = errors.New("no such milestone")
	ErrEmptyMilestone = errors.New("a milestone needs a title")
	ErrDupMilestone   = errors.New("two milestones have the same title")
)

// Milestone is one step on the way to a track's goal.
type Milestone struct {
	Title     string
	ReachedAt time.Time // zero while it is not reached
}

// Done says the milestone is reached.
func (m Milestone) Done() bool { return !m.ReachedAt.IsZero() }

func cleanTitle(s string) string { return strings.Join(strings.Fields(s), " ") }

func milestonesOf(q querier, trackID int64) ([]Milestone, error) {
	rows, err := q.Query(`SELECT title, reached_at FROM milestones WHERE track_id = ? ORDER BY position`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		var m Milestone
		var at sql.NullString
		if err := rows.Scan(&m.Title, &at); err != nil {
			return nil, err
		}
		if m.ReachedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Milestones lists the milestones of the track name points to, in order.
func (s *Store) Milestones(name string) ([]Milestone, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Milestone
	err := s.read(func(q querier) error {
		r, err := resolve(q, name)
		if err != nil {
			return err
		}
		out, err = milestonesOf(q, r.Track.ID)
		return err
	})
	return out, err
}

// AllMilestones lists the milestones of every open track, by track id. A track with none has no entry.
func (s *Store) AllMilestones() (map[int64][]Milestone, error) {
	out := map[int64][]Milestone{}
	if s.stale {
		return out, nil
	}
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT m.track_id, m.title, m.reached_at FROM milestones m JOIN tracks t ON t.id = m.track_id
		  WHERE t.closed_at IS NULL ORDER BY m.track_id, m.position`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var m Milestone
			var at sql.NullString
			if err := rows.Scan(&id, &m.Title, &at); err != nil {
				return err
			}
			if m.ReachedAt, err = parseTime(at); err != nil {
				return err
			}
			out[id] = append(out[id], m)
		}
		return rows.Err()
	})
	return out, err
}

// SetMilestones makes titles the track's whole list, in their order. A title that was already on
// the list keeps its reached state; an empty list clears it. It returns the new list.
func (s *Store) SetMilestones(name string, titles []string) ([]Milestone, error) {
	if s.stale {
		return nil, ErrStale
	}
	var clean []string
	seen := map[string]bool{}
	for _, t := range titles {
		t = cleanTitle(t)
		switch {
		case t == "":
			return nil, ErrEmptyMilestone
		case seen[t]:
			return nil, fmt.Errorf("%w: %q", ErrDupMilestone, t)
		}
		seen[t] = true
		clean = append(clean, t)
	}
	var out []Milestone
	err := s.tx(func(tx *wtx) error {
		t, err := openTrack(tx, name)
		if err != nil {
			return err
		}
		old, err := milestonesOf(tx, t.ID)
		if err != nil {
			return err
		}
		reached := map[string]time.Time{}
		for _, m := range old {
			reached[m.Title] = m.ReachedAt
		}
		if _, err := tx.Exec(`DELETE FROM milestones WHERE track_id = ?`, t.ID); err != nil {
			return err
		}
		out = nil
		for i, title := range clean {
			at := reached[title]
			if _, err := tx.Exec(`INSERT INTO milestones (track_id, position, title, reached_at) VALUES (?, ?, ?, ?)`, t.ID, i+1, title, nullTime(at)); err != nil {
				return err
			}
			out = append(out, Milestone{Title: title, ReachedAt: at})
		}
		return s.save(tx, &t, true)
	})
	return out, err
}

// MarkMilestone ticks (done) or unticks one milestone of the track. ref is its number from 1 or
// its exact title. Ticking one already reached keeps its first reached time. It returns the
// milestone and its number.
func (s *Store) MarkMilestone(name, ref string, done bool) (Milestone, int, error) {
	if s.stale {
		return Milestone{}, 0, ErrStale
	}
	var (
		got Milestone
		n   int
	)
	err := s.tx(func(tx *wtx) error {
		t, err := openTrack(tx, name)
		if err != nil {
			return err
		}
		list, err := milestonesOf(tx, t.ID)
		if err != nil {
			return err
		}
		idx := -1
		if k, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil && k >= 1 && k <= len(list) {
			idx = k - 1
		} else {
			for i, m := range list {
				if m.Title == cleanTitle(ref) {
					idx = i
				}
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: %q (the track has %d)", ErrNoMilestone, ref, len(list))
		}
		got, n = list[idx], idx+1
		switch {
		case done && got.Done(), !done && !got.Done():
			return nil
		case done:
			got.ReachedAt = s.now()
			_, err = tx.Exec(`UPDATE milestones SET reached_at = ? WHERE track_id = ? AND position = ?`, fmtTime(got.ReachedAt), t.ID, n)
		default:
			got.ReachedAt = time.Time{}
			_, err = tx.Exec(`UPDATE milestones SET reached_at = NULL WHERE track_id = ? AND position = ?`, t.ID, n)
		}
		if err != nil {
			return err
		}
		return s.save(tx, &t, true)
	})
	return got, n, err
}

// openTrack resolves name to an open track, following renames.
func openTrack(tx *wtx, name string) (Track, error) {
	r, err := resolve(tx, name)
	switch {
	case err != nil:
		return Track{}, err
	case r.Closed:
		return Track{}, ErrClosed
	}
	return r.Track, nil
}
