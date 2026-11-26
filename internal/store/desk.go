package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The desk is one record beside the tracks, not a track: the session it runs in, the asks it
// raised for Emil in the pane's band before it asked in chat, and the log of what it handled. Its rows name tracks by text, so a track the
// desk only started, or one that was renamed since, still reads in the log.

var (
	ErrDeskBound  = errors.New("the desk is bound to another Rex session")
	ErrBadKind    = errors.New("kind must be answered, asked, nudged, started, closed or noted")
	ErrNoDeskAsk  = errors.New("no such open desk ask")
	ErrDeskFields = errors.New("a desk entry needs a track and text")
	ErrNoEntry    = errors.New("no such entry in the desk's log")
	ErrUndone     = errors.New("that entry was taken back already")
	ErrNotUndo    = errors.New("only the desk's answers, starts and closes are taken back")
	ErrNotDesks   = errors.New("not the desk's track")
)

// StartedWork is why CloseUnstarted left a track open: it has started work.
type StartedWork struct{ Why string }

func (e *StartedWork) Error() string { return e.Why }

// DeskKinds are the kinds of a handled-log entry.
var DeskKinds = []string{"answered", "asked", "nudged", "started", "closed", "noted"}

// DeskAsk is one row of desk_asks. Emil's answer in the band closed the ask: Answer is the
// recommendation he accepted or the reply he typed, AnswerKind (accepted or reply) says which,
// and AnsweredAt is when.
type DeskAsk struct {
	ID                              int64
	Track, Question, Recommendation string
	Evidence                        []string
	CreatedAt, ClosedAt             time.Time
	AnswerKind, Answer              string
	AnsweredAt                      time.Time
}

// DeskLogEntry is one handled item: what the desk did about Track, and what it was asked.
// UndoneAt is when Emil took it back.
type DeskLogEntry struct {
	ID          int64
	At          time.Time
	Track, Kind string
	Asked, Did  string
	UndoneAt    time.Time
}

// DeskView is the desk as a reader sees it: its session, its open asks oldest first and the
// handled log from a point in time, oldest first.
type DeskView struct {
	Session string
	Asks    []DeskAsk
	Log     []DeskLogEntry
}

// DeskSession is the Rex session the desk is bound to, "" when none is.
func (s *Store) DeskSession() (string, error) {
	var sess string
	err := s.read(func(q querier) (err error) {
		sess, err = deskSession(q)
		return err
	})
	return sess, err
}

func deskSession(q querier) (string, error) {
	var sess string
	err := q.QueryRow(`SELECT rex_session FROM desk WHERE id = 1`).Scan(&sess)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sess, err
}

// BindDesk makes session the desk's. A desk bound to the same session stays as it is, and one
// bound to another is ErrDeskBound unless force is set.
func (s *Store) BindDesk(session string, force bool) error {
	if session == "" {
		return errors.New("the desk needs a Rex session to bind to")
	}
	return s.tx(func(tx *wtx) error {
		cur, err := deskSession(tx)
		switch {
		case err != nil:
			return err
		case cur == session:
			return nil
		case cur != "" && !force:
			return ErrDeskBound
		}
		_, err = tx.Exec(`INSERT INTO desk (id, rex_session) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET rex_session = excluded.rex_session`, session)
		return err
	})
}

func deskText(s string) bool { return strings.TrimSpace(s) != "" }

// CloseDeskAsk closes the open ask with this id, so it leaves the open asks.
func (s *Store) CloseDeskAsk(id int64) error {
	return s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`UPDATE desk_asks SET closed_at = ? WHERE id = ? AND closed_at IS NULL`, fmtTime(s.now()), id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNoDeskAsk
		}
		return nil
	})
}

// DeskAnswers reads the asks Emil answered at or after since that the desk has not picked up
// yet, oldest first.
func (s *Store) DeskAnswers(since time.Time) ([]DeskAsk, error) {
	var out []DeskAsk
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT id, track, question, recommendation, evidence, created_at, answer_kind, answer, answered_at FROM desk_asks WHERE answered_at IS NOT NULL AND answered_at >= ? AND picked_up_at IS NULL ORDER BY id`, fmtTime(since))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a DeskAsk
			var ev string
			var created, answered sql.NullString
			if err := rows.Scan(&a.ID, &a.Track, &a.Question, &a.Recommendation, &ev, &created, &a.AnswerKind, &a.Answer, &answered); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(ev), &a.Evidence); err != nil {
				return fmt.Errorf("desk ask %d: evidence: %w", a.ID, err)
			}
			if err := parseTimes(created, &a.CreatedAt, answered, &a.AnsweredAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// DeskLogRecent reads the newest limit entries of the handled log, newest first; a limit of 0 or less reads all.
func (s *Store) DeskLogRecent(limit int) ([]DeskLogEntry, error) {
	var out []DeskLogEntry
	err := s.read(func(q querier) error {
		all, err := deskLogSince(q, time.Time{})
		if err != nil {
			return err
		}
		slices.Reverse(all)
		if limit > 0 && len(all) > limit {
			all = all[:limit]
		}
		out = all
		return nil
	})
	return out, err
}

// LogDesk adds one entry to the handled log, stamped now.
func (s *Store) LogDesk(track, kind, asked, did string) (DeskLogEntry, error) {
	switch {
	case !validName.MatchString(track):
		return DeskLogEntry{}, fmt.Errorf("%w: %q", ErrBadName, track)
	case !slices.Contains(DeskKinds, kind):
		return DeskLogEntry{}, ErrBadKind
	case !deskText(did):
		return DeskLogEntry{}, ErrDeskFields
	}
	e := DeskLogEntry{At: s.now(), Track: track, Kind: kind, Asked: asked, Did: did}
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`INSERT INTO desk_log (at, track, kind, asked, did) VALUES (?, ?, ?, ?, ?)`, fmtTime(e.At), track, kind, asked, did)
		if err != nil {
			return err
		}
		e.ID, err = res.LastInsertId()
		return err
	})
	return e, err
}

// Desk reads the desk with the log entries made at or after since.
func (s *Store) Desk(since time.Time) (DeskView, error) {
	var v DeskView
	err := s.read(func(q querier) (err error) {
		if v.Session, err = deskSession(q); err != nil {
			return err
		}
		if v.Asks, err = openDeskAsks(q); err != nil {
			return err
		}
		v.Log, err = deskLogSince(q, since)
		return err
	})
	return v, err
}

func openDeskAsks(q querier) ([]DeskAsk, error) {
	rows, err := q.Query(`SELECT id, track, question, recommendation, evidence, created_at FROM desk_asks WHERE closed_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeskAsk
	for rows.Next() {
		var a DeskAsk
		var ev string
		var at sql.NullString
		if err := rows.Scan(&a.ID, &a.Track, &a.Question, &a.Recommendation, &ev, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ev), &a.Evidence); err != nil {
			return nil, fmt.Errorf("desk ask %d: evidence: %w", a.ID, err)
		}
		if a.CreatedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func deskLogSince(q querier, since time.Time) ([]DeskLogEntry, error) {
	rows, err := q.Query(`SELECT `+deskLogCols+` FROM desk_log WHERE at >= ? ORDER BY id`, fmtTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeskLogEntry
	for rows.Next() {
		e, err := scanDeskEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const deskLogCols = `id, at, track, kind, asked, did, undone_at`

func scanDeskEntry(r scanner) (DeskLogEntry, error) {
	var e DeskLogEntry
	var at, undone sql.NullString
	if err := r.Scan(&e.ID, &at, &e.Track, &e.Kind, &e.Asked, &e.Did, &undone); err != nil {
		return e, err
	}
	err := parseTimes(at, &e.At, undone, &e.UndoneAt)
	return e, err
}

// DeskEntry reads one entry of the handled log.
func (s *Store) DeskEntry(id int64) (DeskLogEntry, error) {
	var e DeskLogEntry
	err := s.read(func(q querier) (err error) {
		e, err = deskEntry(q, id)
		return err
	})
	return e, err
}

func deskEntry(q querier, id int64) (DeskLogEntry, error) {
	e, err := scanDeskEntry(q.QueryRow(`SELECT `+deskLogCols+` FROM desk_log WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNoEntry
	}
	return e, err
}

// undoable reads entry id and checks it is of kind and not taken back yet.
func undoable(q querier, id int64, kind string) (DeskLogEntry, error) {
	e, err := deskEntry(q, id)
	switch {
	case err != nil:
		return e, err
	case e.Kind != kind:
		return e, ErrNotUndo
	case !e.UndoneAt.IsZero():
		return e, ErrUndone
	}
	return e, nil
}

func markUndone(tx *wtx, id int64, at time.Time) error {
	_, err := tx.Exec(`UPDATE desk_log SET undone_at = ? WHERE id = ?`, fmtTime(at), id)
	return err
}

// trackMoves are the events that show a track's agent did something: a turn it finished, a
// rename, a close.
var trackMoves = []string{"stop", "rename", "close"}

// movedSince is the first thing the track did at or after since, "" when it did nothing: a write
// to its card, or one of trackMoves.
func movedSince(q querier, t Track, since time.Time) (string, error) {
	if !t.UpdatedAt.IsZero() && !t.UpdatedAt.Before(since) {
		return "wrote its card at " + t.UpdatedAt.Local().Format("15:04:05"), nil
	}
	rows, err := q.Query(`SELECT event, at FROM events WHERE track_id = ? AND event IN (?, ?, ?) ORDER BY id`, t.ID, trackMoves[0], trackMoves[1], trackMoves[2])
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var ev string
		var at sql.NullString
		if err := rows.Scan(&ev, &at); err != nil {
			return "", err
		}
		when, err := parseTime(at)
		if err != nil {
			return "", err
		}
		if !when.Before(since) {
			what := map[string]string{"stop": "finished a turn", "rename": "was renamed", "close": "was closed"}[ev]
			return what + " at " + when.Local().Format("15:04:05"), nil
		}
	}
	return "", rows.Err()
}

// AnswerActedOn reads the desk's answer in entry id and says how its track has acted on it
// since it was sent, "" when it has not: the track wrote its card, finished a turn, was renamed
// or closed at or after the entry's time, which is when the answer was typed.
func (s *Store) AnswerActedOn(id int64) (e DeskLogEntry, acted string, err error) {
	err = s.read(func(q querier) error {
		if e, err = undoable(q, id, "answered"); err != nil {
			return err
		}
		r, err := resolve(q, e.Track)
		switch {
		case errors.Is(err, ErrNoTrack):
			acted = "the track is gone"
			return nil
		case err != nil:
			return err
		}
		acted, err = movedSince(q, r.Track, e.At)
		return err
	})
	return e, acted, err
}

// UndoDeskAnswer marks the desk's answer in entry id as taken back, so a second undo finds
// ErrUndone. The track's own ask stays on its card for Emil to answer.
func (s *Store) UndoDeskAnswer(id int64) error {
	return s.tx(func(tx *wtx) error {
		if _, err := undoable(tx, id, "answered"); err != nil {
			return err
		}
		return markUndone(tx, id, s.now())
	})
}

// CloseUnstarted takes back the start in entry id: it closes the track that start made, only
// when the track never started work, and marks the entry taken back. Never started work means
// the track still has the name it started with, was created within window of the entry, and
// its agent has never written its card, raised an ask or finished a turn; otherwise the error
// is a *StartedWork that says what it did. It returns the closed track.
func (s *Store) CloseUnstarted(id int64, window time.Duration) (Track, error) {
	var t Track
	err := s.tx(func(tx *wtx) error {
		e, err := undoable(tx, id, "started")
		if err != nil {
			return err
		}
		r, err := resolve(tx, e.Track)
		switch {
		case err != nil:
			return err
		case r.Closed:
			return ErrClosed
		case r.RenamedTo != "":
			return &StartedWork{e.Track + " renamed itself to " + r.RenamedTo}
		case r.Track.CreatedAt.Sub(e.At).Abs() > window:
			return &StartedWork{r.Track.Name + " was not made by this start"}
		}
		t = r.Track
		asks, err := openAsks(tx, t.ID)
		if err != nil {
			return err
		}
		did, err := movedSince(tx, t, time.Time{})
		switch {
		case err != nil:
			return err
		case len(asks) > 0:
			return &StartedWork{t.Name + " has asks"}
		case did != "":
			return &StartedWork{t.Name + " " + did}
		}
		now := s.now()
		t.ClosedAt = now
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		if err := logEvent(tx, Event{At: now, Event: "close", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir}); err != nil {
			return err
		}
		return markUndone(tx, id, now)
	})
	return t, err
}

// DeskOwnsTrack is nil when every session that ever ran track name is one the desk owns, and
// there is at least one: the desk started it and Emil never took it over. A track Emil reopened
// after the desk closed it is his too. Otherwise it is ErrNotDesks, with the reason.
func (s *Store) DeskOwnsTrack(name string) error {
	return s.read(func(q querier) error {
		r, err := resolve(q, name)
		if err != nil {
			return err
		}
		var all, desk int
		if err := q.QueryRow(`SELECT count(*), coalesce(sum(owner = 'desk'), 0) FROM sessions WHERE track_id = ?`, r.Track.ID).Scan(&all, &desk); err != nil {
			return err
		}
		var reopened int
		if err := q.QueryRow(`SELECT count(*) FROM events WHERE track_id = ? AND event = 'reopen'`, r.Track.ID).Scan(&reopened); err != nil {
			return err
		}
		switch {
		case reopened > 0:
			return fmt.Errorf("%w: Emil reopened %s", ErrNotDesks, name)
		case all == 0:
			return fmt.Errorf("%w: no session of %s ran from the desk", ErrNotDesks, name)
		case desk < all:
			return fmt.Errorf("%w: %s is Emil's", ErrNotDesks, name)
		}
		return nil
	})
}

// ReopenClosed takes back the close in entry id: it opens the track again, as it was when it
// closed, and marks the entry taken back. The archive file of the close stays; a later close
// writes a new one. It returns the reopened track.
func (s *Store) ReopenClosed(id int64) (Track, error) {
	var t Track
	err := s.tx(func(tx *wtx) error {
		e, err := undoable(tx, id, "closed")
		if err != nil {
			return err
		}
		r, err := resolve(tx, e.Track)
		switch {
		case err != nil:
			return err
		case !r.Closed:
			return ErrNotClosed
		}
		t = r.Track
		now := s.now()
		t.ClosedAt, t.Extra.File = time.Time{}, ""
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		if err := logEvent(tx, Event{At: now, Event: "reopen", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir}); err != nil {
			return err
		}
		return markUndone(tx, id, now)
	})
	return t, err
}

// MarkDeskUndone marks entry id as taken back, as when the start it logged was ended at once.
func (s *Store) MarkDeskUndone(id int64) error {
	return s.tx(func(tx *wtx) error {
		if _, err := deskEntry(tx, id); err != nil {
			return err
		}
		return markUndone(tx, id, s.now())
	})
}
