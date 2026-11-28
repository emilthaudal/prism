package store

import (
	"database/sql"
	"errors"
	"time"
)

// ErrEmils is a send to a session Emil owns: the desk sends only to its own.
var ErrEmils = errors.New("the session is Emil's")

// NoteSend is what SendNote did: the note's message id and its whole text, and whether the
// caller must deliver it now (the session is neither working nor blocked) or it stays held.
type NoteSend struct {
	ID      int64
	Text    string
	Deliver bool
}

// noteSep goes between a held note and the text a later send adds to it.
const noteSep = "\n\n"

// busyWhere is the condition, on a sessions row s, under which a note waits: the session works,
// is blocked, or ended its turn while its subagents still run. Delivering stops the session and
// resumes it, which would cut those subagents' work, or fork the session while its process
// lives on for them (EMI-426).
const busyWhere = `(s.status IN ('working', 'blocked') OR EXISTS (
	SELECT 1 FROM session_subagents a WHERE a.session_id = s.id AND a.status = 'working'))`

// SendNote adds text to the session's one held note, or replaces it, and when the session is
// not busy (busyWhere), or now is true, claims the note for delivery: it is stamped delivered
// before the caller resumes the session, so the hook reads the prompt it brings as the desk's.
// now is for a session the desk or Emil confirmed is stuck.
func (s *Store) SendNote(session, from, text string, replace, now bool) (NoteSend, error) {
	if s.stale {
		return NoteSend{}, ErrStale
	}
	var out NoteSend
	err := s.tx(func(tx *wtx) error {
		var owner string
		var busy bool
		if err := tx.QueryRow(`SELECT owner, `+busyWhere+` FROM sessions s WHERE id = ?`, session).Scan(&owner, &busy); err != nil {
			return err
		}
		if owner != "desk" {
			return ErrEmils
		}
		at := fmtTime(s.now())
		var held string
		err := tx.QueryRow(`SELECT id, text FROM messages WHERE session_id = ? AND kind = 'note' AND held = 1 AND delivered_at IS NULL
			ORDER BY id LIMIT 1`, session).Scan(&out.ID, &held)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.Exec(`INSERT INTO messages (session_id, from_kind, kind, text, created_at, held) VALUES (?, ?, 'note', ?, ?, 1)`,
				session, from, text, at)
			if err != nil {
				return err
			}
			if out.ID, err = res.LastInsertId(); err != nil {
				return err
			}
			out.Text = text
		case err != nil:
			return err
		default:
			out.Text = text
			if !replace {
				out.Text = held + noteSep + text
			}
			if _, err := tx.Exec(`UPDATE messages SET text = ?, from_kind = ? WHERE id = ?`, out.Text, from, out.ID); err != nil {
				return err
			}
		}
		if busy && !now {
			return nil
		}
		out.Deliver = true
		_, err = tx.Exec(`UPDATE messages SET held = 0, delivered_at = ? WHERE id = ?`, at, out.ID)
		return err
	})
	return out, err
}

// HeldNote is a note held for a session the desk owns that is neither working nor blocked now.
type HeldNote struct {
	ID      int64
	Session Session
	Text    string
}

// DeliverableNotes are the held notes whose session the desk owns and has stopped working.
func (s *Store) DeliverableNotes() ([]HeldNote, error) {
	if s.stale {
		return nil, ErrStale
	}
	list, err := s.sessionsWhere(`s.owner = 'desk' AND NOT ` + busyWhere)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	var out []HeldNote
	err = s.read(func(q querier) error {
		for _, x := range list {
			var n HeldNote
			err := q.QueryRow(`SELECT id, text FROM messages WHERE session_id = ? AND kind = 'note' AND held = 1 AND delivered_at IS NULL
				ORDER BY id LIMIT 1`, x.ID).Scan(&n.ID, &n.Text)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			n.Session = x
			out = append(out, n)
		}
		return nil
	})
	return out, err
}

// ClaimNote stamps a held note delivered, false when another process claimed it first or the
// session is no longer the desk's.
func (s *Store) ClaimNote(id int64) (bool, error) {
	var n int64
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`UPDATE messages SET held = 0, delivered_at = ? WHERE id = ? AND held = 1 AND delivered_at IS NULL
			AND EXISTS (SELECT 1 FROM sessions WHERE id = messages.session_id AND owner = 'desk')`, fmtTime(s.now()), id)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	return n == 1, err
}

// ReleaseNote holds a claimed note again, after its delivery failed, with the text of any note
// held for the session since added after it, so the session keeps one held note.
func (s *Store) ReleaseNote(id int64) error {
	return s.tx(func(tx *wtx) error {
		var session, text string
		if err := tx.QueryRow(`SELECT session_id, text FROM messages WHERE id = ?`, id).Scan(&session, &text); err != nil {
			return err
		}
		var later int64
		var more string
		err := tx.QueryRow(`SELECT id, text FROM messages WHERE session_id = ? AND kind = 'note' AND held = 1 AND delivered_at IS NULL AND id != ?
			ORDER BY id LIMIT 1`, session, id).Scan(&later, &more)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			text += noteSep + more
			if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, later); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE messages SET held = 1, delivered_at = NULL, text = ? WHERE id = ?`, text, id)
		return err
	})
}

// DropNote deletes the held note of a session the desk owns, by hand, and logs it; n is how many
// notes it removed (0 when none was held). A claimed or delivered note stays.
func (s *Store) DropNote(session string) (n int, err error) {
	if s.stale {
		return 0, ErrStale
	}
	err = s.tx(func(tx *wtx) error {
		var owner string
		if err := tx.QueryRow(`SELECT owner FROM sessions WHERE id = ?`, session).Scan(&owner); err != nil {
			return err
		}
		if owner != "desk" {
			return ErrEmils
		}
		n, err = dropHeld(tx, `session_id = ?`, s.now(), "drop", session)
		return err
	})
	return n, err
}

// dropHeld deletes the held notes that where selects, and logs one note_drop event per track
// with kind why. A session with no track logs none.
func dropHeld(tx *wtx, where string, at time.Time, why string, args ...any) (int, error) {
	rows, err := tx.Query(`SELECT m.id, COALESCE(t.id, 0), COALESCE(t.name, ''), length(m.text) FROM messages m
		JOIN sessions s ON s.id = m.session_id LEFT JOIN tracks t ON t.id = s.track_id
		WHERE m.kind = 'note' AND m.held = 1 AND m.delivered_at IS NULL AND m.`+where, args...)
	if err != nil {
		return 0, err
	}
	var ids []int64
	var evs []Event
	for rows.Next() {
		var id int64
		e := Event{At: at, Event: "note_drop", Kind: why}
		if err := rows.Scan(&id, &e.TrackID, &e.TrackName, &e.Len); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		evs = append(evs, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for i, id := range ids {
		if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
			return 0, err
		}
		if err := logEvent(tx, evs[i]); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// DropClosedNotes deletes the held notes of sessions whose track is closed, which no session
// takes: delivering one fails at every stop. It returns how many it removed.
func (s *Store) DropClosedNotes() (n int, err error) {
	if s.stale {
		return 0, ErrStale
	}
	err = s.tx(func(tx *wtx) error {
		n, err = dropHeld(tx, `session_id IN (SELECT s.id FROM sessions s JOIN tracks t ON t.id = s.track_id
			WHERE s.owner = 'desk' AND t.closed_at IS NOT NULL)`, s.now(), "closed")
		return err
	})
	return n, err
}

// NotedSessions are the sessions a note was delivered to at or after since: delivering stops
// and resumes them. A session's first note is its start prompt, which stopped nothing.
func (s *Store) NotedSessions(since time.Time) (map[string]bool, error) {
	out := map[string]bool{}
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT DISTINCT session_id FROM messages WHERE kind = 'note' AND delivered_at >= ?
			AND id > (SELECT min(f.id) FROM messages f WHERE f.session_id = messages.session_id AND f.kind = 'note')`, fmtTime(since))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	return out, err
}
