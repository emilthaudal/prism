package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RetiredName is a name a track held before it was renamed, and when it was given up.
type RetiredName struct {
	Name string
	At   time.Time
}

// ImportAsk is an ask found on a card. CheckText names the check an ask written by tracks
// check belongs to, so the ask links to that check's row.
type ImportAsk struct {
	Kind, Text, CheckText         string
	CreatedAt, SeenAt, AnsweredAt time.Time
}

// ImportTrack is one card, live or closed, as read from the files. Track.RenderHash is the hash
// of a live card's file, so a later edit to that file reads as a hand edit.
type ImportTrack struct {
	Track   Track
	Asks    []ImportAsk
	Retired []RetiredName
	// BlockedBy names the tracks this one waits on; each is any name a track has held.
	BlockedBy []string
}

// ImportCheck is one record of checks.json. Track is any name the track has held.
type ImportCheck struct {
	Track          string
	Text           string
	Answer         bool
	AskedAt, RanAt time.Time
}

// ImportEvent is one line of attention.jsonl. Event.TrackName is the name in the line; Owner is
// the name that track has now, which may differ after renames, and "" for no track.
type ImportEvent struct {
	Event Event
	Owner string
}

// ImportData is everything a migration reads from the files, in log order.
type ImportData struct {
	Tracks []ImportTrack
	Checks []ImportCheck
	Events []ImportEvent
}

// Import writes d into the database in one transaction. Data the schema cannot hold (a name two
// tracks claim, a check for a track nobody has) is skipped and described in notes.
func (s *Store) Import(d ImportData) (notes []string, err error) {
	err = s.tx(func(tx *wtx) error {
		notes = nil
		ids := map[string]int64{}
		var trackIDs []int64
		for _, it := range d.Tracks {
			t := it.Track
			res, err := tx.Exec(`INSERT INTO tracks (name, mode, tier, tier_why, dir, rex_session, claude_session, claude_cwd,
			  check_text, waiting_on, links, goal, where_text, standing, later, extra, answered_at, updated_at, changed_at,
			  created_at, closed_at, render_hash, rank, today) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				t.Name, t.Mode, t.Tier, t.TierWhy, t.Dir, t.RexSession, t.ClaudeSession, t.ClaudeCwd, t.Check, t.WaitingOn,
				t.Links, t.Goal, t.Where, t.Standing, t.Later, t.Extra.encode(), nullTime(t.AnsweredAt), fmtTime(t.UpdatedAt),
				fmtTime(t.ChangedAt), fmtTime(t.CreatedAt), nullTime(t.ClosedAt), t.RenderHash, nullRank(t.Rank), t.Today)
			if err != nil {
				return fmt.Errorf("track %s: %w", t.Name, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			trackIDs = append(trackIDs, id)
			claim := func(name string, retired time.Time) error {
				if _, taken := ids[strings.ToLower(name)]; taken {
					notes = append(notes, fmt.Sprintf("name %s is held by more than one track: kept on the first", name))
					return nil
				}
				if _, err := tx.Exec(`INSERT INTO names (name, track_id, retired_at) VALUES (?, ?, ?)`, name, id, nullTime(retired)); err != nil {
					return err
				}
				ids[strings.ToLower(name)] = id
				return nil
			}
			if err := claim(t.Name, time.Time{}); err != nil {
				return err
			}
			for _, r := range it.Retired {
				if err := claim(r.Name, r.At); err != nil {
					return err
				}
			}
		}
		checkIDs := map[[2]string]int64{}
		for _, c := range d.Checks {
			id, ok := ids[strings.ToLower(c.Track)]
			if !ok {
				notes = append(notes, fmt.Sprintf("check %q of %s: no such track, skipped", c.Text, c.Track))
				continue
			}
			res, err := tx.Exec(`INSERT OR IGNORE INTO checks (track_id, text, answer, asked_at, ran_at) VALUES (?, ?, ?, ?, ?)`,
				id, c.Text, c.Answer, nullTime(c.AskedAt), nullTime(c.RanAt))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				notes = append(notes, fmt.Sprintf("check %q of %s is kept twice: the second is skipped", c.Text, c.Track))
				continue
			}
			cid, err := res.LastInsertId()
			if err != nil {
				return err
			}
			checkIDs[[2]string{fmt.Sprint(id), c.Text}] = cid
		}
		for ti, it := range d.Tracks {
			id := trackIDs[ti]
			for i, a := range it.Asks {
				var check any
				if a.CheckText != "" {
					if cid, ok := checkIDs[[2]string{fmt.Sprint(id), a.CheckText}]; ok {
						check = cid
					}
				}
				if _, err := tx.Exec(`INSERT INTO asks (track_id, kind, text, position, check_id, created_at, seen_at, answered_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					id, a.Kind, a.Text, i+1, check, fmtTime(a.CreatedAt), nullTime(a.SeenAt), nullTime(a.AnsweredAt)); err != nil {
					return err
				}
			}
		}
		for ti, it := range d.Tracks {
			pos := 0
			for _, n := range it.BlockedBy {
				bid, ok := ids[strings.ToLower(n)]
				if !ok || bid == trackIDs[ti] {
					notes = append(notes, fmt.Sprintf("%s: blocked_by %s names no other track, skipped", it.Track.Name, n))
					continue
				}
				pos++
				if _, err := tx.Exec(`INSERT OR IGNORE INTO blockers (track_id, blocker_id, position) VALUES (?, ?, ?)`, trackIDs[ti], bid, pos); err != nil {
					return err
				}
			}
		}
		orphans := 0
		for _, e := range d.Events {
			var track any
			if id, ok := ids[strings.ToLower(e.Owner)]; ok && e.Owner != "" {
				track = id
			} else if e.Owner != "" {
				orphans++
			}
			ev := e.Event
			if _, err := tx.Exec(`INSERT INTO events (at, event, track_id, track_name, rex_session, rex_block, cwd, len, kind)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, fmtTime(ev.At), ev.Event, track, ev.TrackName, ev.RexSession, ev.RexBlock, ev.Cwd, ev.Len, ev.Kind); err != nil {
				return err
			}
		}
		if orphans > 0 {
			notes = append(notes, fmt.Sprintf("%d events name a track with no card: kept without a track", orphans))
		}
		for i, id := range trackIDs {
			if d.Tracks[i].Track.ClosedAt.IsZero() {
				if err := s.mirror(tx, id, s.now()); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return notes, err
}

// NamedCheck is a check with the current name of its track.
type NamedCheck struct {
	Track string
	Check
}

// AllTracks lists every track, live and closed, oldest first, with its open asks.
func (s *Store) AllTracks() ([]View, error) {
	var out []View
	err := s.read(func(q querier) (err error) {
		out, err = allTracks(q)
		return err
	})
	return out, err
}

// LiveTracks lists the tracks that are not closed, oldest first, with their open asks. Asks an
// import left open on a closed track never show here.
func (s *Store) LiveTracks() ([]View, error) {
	var out []View
	err := s.read(func(q querier) (err error) {
		out, err = tracksWhere(q, `closed_at IS NULL`)
		return err
	})
	return out, err
}

// ClosedTracks lists the closed tracks, oldest first, without their asks.
func (s *Store) ClosedTracks() ([]Track, error) {
	var out []Track
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT ` + trackCols + ` FROM tracks WHERE closed_at IS NOT NULL ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTrack(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func allTracks(q querier) ([]View, error) { return tracksWhere(q, `1`) }

func tracksWhere(q querier, where string) ([]View, error) {
	var out []View
	rows, err := q.Query(`SELECT ` + trackCols + ` FROM tracks WHERE ` + where + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, View{Resolved: Resolved{Track: t, Closed: t.Closed()}})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Asks, err = openAsks(q, out[i].Track.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AllChecks lists every check record with the current name of its track.
func (s *Store) AllChecks() ([]NamedCheck, error) {
	var out []NamedCheck
	err := s.read(func(q querier) (err error) {
		out, err = allChecks(q)
		return err
	})
	return out, err
}

func allChecks(q querier) ([]NamedCheck, error) {
	var out []NamedCheck
	rows, err := q.Query(`SELECT t.name, c.id, c.track_id, c.text, c.answer, c.asked_at, c.ran_at
	  FROM checks c JOIN tracks t ON t.id = c.track_id ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c NamedCheck
		var asked, ran sql.NullString
		if err := rows.Scan(&c.Track, &c.ID, &c.TrackID, &c.Text, &c.Answer, &asked, &ran); err != nil {
			return nil, err
		}
		if err := parseTimes(asked, &c.AskedAt, ran, &c.RanAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AllEvents lists every event in the order it was logged; TrackName is the name at the time and
// Current the name its track has now.
func (s *Store) AllEvents() ([]Event, error) {
	var out []Event
	err := s.read(func(q querier) (err error) {
		out, err = allEvents(q)
		return err
	})
	return out, err
}

func allEvents(q querier) ([]Event, error) {
	var out []Event
	rows, err := q.Query(`SELECT e.id, e.at, e.event, coalesce(e.track_id, 0), e.track_name, coalesce(t.name, e.track_name),
	  coalesce(e.rex_session, ''), coalesce(e.rex_block, ''), coalesce(e.cwd, ''), coalesce(e.len, 0), coalesce(e.kind, '')
	  FROM events e LEFT JOIN tracks t ON t.id = e.track_id ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var at sql.NullString
		if err := rows.Scan(&e.ID, &at, &e.Event, &e.TrackID, &e.TrackName, &e.Current, &e.RexSession, &e.RexBlock, &e.Cwd, &e.Len, &e.Kind); err != nil {
			return nil, err
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
