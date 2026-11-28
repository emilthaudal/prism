package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// milestonesSchema is the schema that added the milestones table (EMI-485).
const milestonesSchema = 16

// milestonePRSchema is the schema that gave a milestone an optional PR ref and its state (EMI-474).
const milestonePRSchema = 17

// A track's milestones are the steps to its goal: an ordered list of outcomes someone could
// check, each reached or not. Only the track's agent changes the list. A milestone may carry a
// PR ref; tracks check then refreshes the PR's state and ticks the milestone once it is merged.

var (
	ErrNoMilestone    = errors.New("no such milestone")
	ErrEmptyMilestone = errors.New("a milestone needs a title")
	ErrDupMilestone   = errors.New("two milestones have the same title")
	ErrBadPR          = errors.New("a PR ref is owner/repo#N")
)

// PR states, in the order a PR moves through them.
const (
	PROpen     = "open"
	PRGreen    = "green"
	PRApproved = "approved"
	PRMerged   = "merged"
	PRClosed   = "closed"
)

var prRef = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*#[0-9]{1,9}$`)

// Milestone is one step on the way to a track's goal.
type Milestone struct {
	Title     string
	ReachedAt time.Time // zero while it is not reached
	PR        string    // owner/repo#N, empty when the milestone has none
	PRState   string    // one of the PR states, empty until tracks check has looked
}

// Done says the milestone is reached.
func (m Milestone) Done() bool { return !m.ReachedAt.IsZero() }

func cleanTitle(s string) string { return strings.Join(strings.Fields(s), " ") }

func scanMilestone(sc interface{ Scan(...any) error }, extra ...any) (Milestone, error) {
	var (
		m               Milestone
		at, pr, prState sql.NullString
	)
	if err := sc.Scan(append(extra, &m.Title, &at, &pr, &prState)...); err != nil {
		return Milestone{}, err
	}
	var err error
	if m.ReachedAt, err = parseTime(at); err != nil {
		return Milestone{}, err
	}
	m.PR, m.PRState = pr.String, prState.String
	return m, nil
}

func milestonesOf(q querier, trackID int64) ([]Milestone, error) {
	rows, err := q.Query(`SELECT title, reached_at, pr, pr_state FROM milestones WHERE track_id = ? ORDER BY position`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
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
		rows, err := q.Query(`SELECT m.track_id, m.title, m.reached_at, m.pr, m.pr_state FROM milestones m JOIN tracks t ON t.id = m.track_id
		  WHERE t.closed_at IS NULL ORDER BY m.track_id, m.position`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			m, err := scanMilestone(rows, &id)
			if err != nil {
				return err
			}
			out[id] = append(out[id], m)
		}
		return rows.Err()
	})
	return out, err
}

// SetMilestones makes titles the track's whole list, in their order. A title that was already on
// the list keeps its reached state and PR; an empty list clears it. It returns the new list.
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
		kept := map[string]Milestone{}
		for _, m := range old {
			kept[m.Title] = m
		}
		if _, err := tx.Exec(`DELETE FROM milestones WHERE track_id = ?`, t.ID); err != nil {
			return err
		}
		out = nil
		for i, title := range clean {
			m := kept[title]
			m.Title = title
			if _, err := tx.Exec(`INSERT INTO milestones (track_id, position, title, reached_at, pr, pr_state) VALUES (?, ?, ?, ?, ?, ?)`,
				t.ID, i+1, title, nullTime(m.ReachedAt), nullString(m.PR), nullString(m.PRState)); err != nil {
				return err
			}
			out = append(out, m)
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
		idx, err := findMilestone(list, ref)
		if err != nil {
			return err
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

// findMilestone is the index of the milestone ref names: its number from 1 or its exact title.
func findMilestone(list []Milestone, ref string) (int, error) {
	if k, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil && k >= 1 && k <= len(list) {
		return k - 1, nil
	}
	if i := slices.IndexFunc(list, func(m Milestone) bool { return m.Title == cleanTitle(ref) }); i >= 0 {
		return i, nil
	}
	return -1, fmt.Errorf("%w: %q (the track has %d)", ErrNoMilestone, ref, len(list))
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SetMilestonePR puts a PR ref on one milestone, or takes it off when pr is empty. A new ref
// starts with no state until tracks check has looked at it. It returns the milestone and its number.
func (s *Store) SetMilestonePR(name, ref, pr string) (Milestone, int, error) {
	if s.stale {
		return Milestone{}, 0, ErrStale
	}
	if pr != "" && !prRef.MatchString(pr) {
		return Milestone{}, 0, fmt.Errorf("%w: %q", ErrBadPR, pr)
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
		idx, err := findMilestone(list, ref)
		if err != nil {
			return err
		}
		got, n = list[idx], idx+1
		if got.PR == pr {
			return nil
		}
		got.PR, got.PRState = pr, ""
		if _, err := tx.Exec(`UPDATE milestones SET pr = ?, pr_state = NULL WHERE track_id = ? AND position = ?`, nullString(pr), t.ID, n); err != nil {
			return err
		}
		return s.save(tx, &t, true)
	})
	return got, n, err
}

// WatchedMilestone is an unreached milestone with a PR ref on a track tracks check watches.
type WatchedMilestone struct {
	TrackID  int64
	Track    string
	Dir      string
	Position int
	Title    string
	PR       string
	PRState  string
}

// WatchedMilestones lists the unreached milestones with a PR ref on open tracks that are not parked.
func (s *Store) WatchedMilestones() ([]WatchedMilestone, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []WatchedMilestone
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT t.id, t.name, t.dir, m.position, m.title, m.pr, coalesce(m.pr_state, '')
		  FROM milestones m JOIN tracks t ON t.id = m.track_id
		  WHERE t.closed_at IS NULL AND t.mode != 'parked' AND m.reached_at IS NULL AND m.pr IS NOT NULL
		  ORDER BY t.id, m.position`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w WatchedMilestone
			if err := rows.Scan(&w.TrackID, &w.Track, &w.Dir, &w.Position, &w.Title, &w.PR, &w.PRState); err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// MilestonePRResult says what RecordMilestonePR did.
type MilestonePRResult struct {
	Old    string // the state before, empty when none was recorded
	Ticked bool   // the milestone was reached because its PR merged
	Wake   string // the desk session to tell, empty when nobody needs to hear
}

// RecordMilestonePR stores the state a PR is in for the milestone at position of a track. A merged
// PR ticks the milestone. A state change wakes the track's newest session when the desk owns it;
// a first state of open is recorded without a wake, since the agent just attached the PR. It does
// nothing when the milestone's PR or reached state changed since it was listed.
func (s *Store) RecordMilestonePR(trackID int64, position int, pr, state string) (MilestonePRResult, error) {
	var res MilestonePRResult
	if !slices.Contains([]string{PROpen, PRGreen, PRApproved, PRMerged, PRClosed}, state) {
		return res, fmt.Errorf("unknown PR state %q", state)
	}
	err := s.tx(func(tx *wtx) error {
		t, err := trackByID(tx, trackID)
		if err != nil {
			return err
		}
		var (
			cur, curState, reached sql.NullString
		)
		err = tx.QueryRow(`SELECT pr, pr_state, reached_at FROM milestones WHERE track_id = ? AND position = ?`, trackID, position).Scan(&cur, &curState, &reached)
		switch {
		case errors.Is(err, sql.ErrNoRows), err == nil && (cur.String != pr || reached.Valid):
			return nil
		case err != nil:
			return err
		}
		res.Old = curState.String
		if res.Old == state {
			return nil
		}
		if state == PRMerged {
			if _, err := tx.Exec(`UPDATE milestones SET pr_state = ?, reached_at = ? WHERE track_id = ? AND position = ?`, state, fmtTime(s.now()), trackID, position); err != nil {
				return err
			}
			res.Ticked = true
		} else if _, err := tx.Exec(`UPDATE milestones SET pr_state = ? WHERE track_id = ? AND position = ?`, state, trackID, position); err != nil {
			return err
		}
		if res.Old == "" && state == PROpen {
			return s.save(tx, &t, true)
		}
		var sid, owner string
		err = tx.QueryRow(`SELECT id, owner FROM sessions WHERE track_id = ? ORDER BY updated_at DESC, created_at DESC LIMIT 1`, trackID).Scan(&sid, &owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner == "desk" {
			res.Wake = sid
		}
		return s.save(tx, &t, true)
	})
	return res, err
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
