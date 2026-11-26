package store

import (
	"errors"
	"fmt"
	"slices"
)

// blockerSep joins the names of a track's blockers, as the card shows them.
const blockerSep = ", "

var (
	ErrBadRank    = errors.New("rank must be a whole number from 1")
	ErrBadBlocker = errors.New("blocked_by must name other open tracks")
)

func nullRank(r int) any {
	if r <= 0 {
		return nil
	}
	return r
}

// setBlockers makes names the tracks t waits on, in their order. Each must resolve to another
// open track, and none may already wait on t, so the tracks never block each other in a ring.
func setBlockers(tx *wtx, t Track, names []string) error {
	var ids []int64
	for _, n := range names {
		r, err := resolve(tx, n)
		switch {
		case errors.Is(err, ErrNoTrack):
			return fmt.Errorf("%w: no track %s", ErrBadBlocker, n)
		case err != nil:
			return err
		case r.Closed:
			return fmt.Errorf("%w: %s is closed", ErrBadBlocker, n)
		case r.Track.ID == t.ID:
			return fmt.Errorf("%w: %s cannot wait on itself", ErrBadBlocker, t.Name)
		}
		var loops int
		if err := tx.QueryRow(`WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT k.blocker_id FROM blockers k JOIN up ON k.track_id = up.id)
		  SELECT count(*) FROM up WHERE id = ?`, r.Track.ID, t.ID).Scan(&loops); err != nil {
			return err
		}
		if loops > 0 {
			return fmt.Errorf("%w: %s already waits on %s", ErrBadBlocker, r.Track.Name, t.Name)
		}
		if !slices.Contains(ids, r.Track.ID) {
			ids = append(ids, r.Track.ID)
		}
	}
	if _, err := tx.Exec(`DELETE FROM blockers WHERE track_id = ?`, t.ID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`INSERT INTO blockers (track_id, blocker_id, position) VALUES (?, ?, ?)`, t.ID, id, i+1); err != nil {
			return err
		}
	}
	return nil
}

// touchBlocked marks the tracks that wait on id for rendering, so their blocked_by shows its
// name now.
func touchBlocked(tx *wtx, id int64) error {
	rows, err := tx.Query(`SELECT track_id FROM blockers WHERE blocker_id = ?`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return err
		}
		tx.touch(t)
	}
	return rows.Err()
}
