package store

import (
	"database/sql"
	"errors"
	"time"
)

// conversationSchema is the schema that keeps the headless desk's conversation and its Claude session.
const conversationSchema = 11

// Conversation roles.
const (
	RoleEmil   = "emil"
	RoleDesk   = "desk"
	RoleReport = "report"
)

var ErrNoSession = errors.New("no such session")

// Entry is one line of the desk's conversation: Emil's message, the desk's reply with its tool
// calls folded into Steps, or a report from one of its sessions.
type Entry struct {
	ID               int64
	At               time.Time
	Role, Text       string
	Steps            int
	Session, Outcome string
	DeliveredAt      time.Time
}

const entryCols = `id, at, role, text, steps, session_id, outcome, delivered_at`

func scanEntry(r scanner) (Entry, error) {
	var e Entry
	var at, delivered sql.NullString
	if err := r.Scan(&e.ID, &at, &e.Role, &e.Text, &e.Steps, &e.Session, &e.Outcome, &delivered); err != nil {
		return e, err
	}
	return e, parseTimes(at, &e.At, delivered, &e.DeliveredAt)
}

func queryEntries(q querier, query string, args ...any) ([]Entry, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddEntry appends e to the conversation, stamped now, and returns it with its id.
func (s *Store) AddEntry(e Entry) (Entry, error) {
	if s.stale {
		return e, ErrStale
	}
	e.At = s.now()
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`INSERT INTO conversation (at, role, text, steps, session_id, outcome, delivered_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			fmtTime(e.At), e.Role, e.Text, e.Steps, e.Session, e.Outcome, nullTime(e.DeliveredAt))
		if err != nil {
			return err
		}
		e.ID, err = res.LastInsertId()
		return err
	})
	return e, err
}

// UpdateEntry sets the text and steps of an entry, as the desk's reply grows during its turn.
func (s *Store) UpdateEntry(id int64, text string, steps int) error {
	return s.tx(func(tx *wtx) error {
		_, err := tx.Exec(`UPDATE conversation SET text = ?, steps = ? WHERE id = ?`, text, steps, id)
		return err
	})
}

// SetOutcome sets an entry's outcome: prism server marks a desk reply it hid as reasked.
func (s *Store) SetOutcome(id int64, outcome string) error {
	return s.tx(func(tx *wtx) error {
		_, err := tx.Exec(`UPDATE conversation SET outcome = ? WHERE id = ?`, outcome, id)
		return err
	})
}

// Conversation is up to limit entries before the entry with id before (0: the newest), oldest first.
func (s *Store) Conversation(before int64, limit int) ([]Entry, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Entry
	err := s.read(func(q querier) (err error) {
		out, err = queryEntries(q, `SELECT `+entryCols+` FROM conversation WHERE ? = 0 OR id < ? ORDER BY id DESC LIMIT ?`, before, before, limit)
		return err
	})
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

// UndeliveredEmil are Emil's messages the desk has not been given yet, oldest first.
func (s *Store) UndeliveredEmil() ([]Entry, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Entry
	err := s.read(func(q querier) (err error) {
		out, err = queryEntries(q, `SELECT `+entryCols+` FROM conversation WHERE role = 'emil' AND delivered_at IS NULL ORDER BY id`)
		return err
	})
	return out, err
}

// DeliverEntries stamps delivered_at on Emil's messages written to the desk.
func (s *Store) DeliverEntries(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE conversation SET delivered_at = ? WHERE id = ? AND delivered_at IS NULL`, at, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeskClaude is the Claude session the headless desk runs in, "" when it never ran.
func (s *Store) DeskClaude() (string, error) {
	if s.stale {
		return "", nil
	}
	var id string
	err := s.read(func(q querier) error {
		err := q.QueryRow(`SELECT claude_session FROM desk WHERE id = 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return id, err
}

// SetDeskSession records the headless desk's Claude session and its status. Hooks skip a
// stream-json session, so this is what gives the desk its sessions row: no track, owner desk,
// host background, named desk. Its sessions' reports need that row as their parent.
func (s *Store) SetDeskSession(id, status string) error {
	if s.stale {
		return ErrStale
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		if _, err := tx.Exec(`INSERT INTO desk (id, claude_session) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET claude_session = excluded.claude_session`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sessions (id, owner, host, name, status, created_at, updated_at) VALUES (?, 'desk', 'background', 'desk', ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET host = 'background', ended_at = NULL, status = excluded.status, updated_at = excluded.updated_at`, id, status, at, at)
		return err
	})
}

// EndDeskSession forgets the headless desk's Claude session, so the next desk starts fresh
// instead of resuming it, and marks that session's row done and stopped (prism desk restart).
func (s *Store) EndDeskSession(id string) error {
	if s.stale {
		return ErrStale
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		if _, err := tx.Exec(`UPDATE desk SET claude_session = '' WHERE id = 1`); err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		_, err := tx.Exec(`UPDATE sessions SET host = 'stopped', status = 'done', activity = '', ended_at = COALESCE(ended_at, ?), updated_at = ? WHERE id = ?`, at, at, id)
		return err
	})
}

// DeskSessionStart is when the headless desk's Claude session id was first recorded, zero when
// it has no row.
func (s *Store) DeskSessionStart(id string) (time.Time, error) {
	var t time.Time
	if s.stale || id == "" {
		return t, nil
	}
	err := s.read(func(q querier) error {
		var created sql.NullString
		err := q.QueryRow(`SELECT created_at FROM sessions WHERE id = ?`, id).Scan(&created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return parseTimes(created, &t)
	})
	return t, err
}

// SetOwner gives a session to emil or the desk.
func (s *Store) SetOwner(id, owner string) error {
	return s.sessionUpdate(id, `UPDATE sessions SET owner = ? WHERE id = ?`, owner, id)
}

// MarkSeen stamps seen_at on a session, now.
func (s *Store) MarkSeen(id string) error {
	return s.sessionUpdate(id, `UPDATE sessions SET seen_at = ? WHERE id = ?`, fmtTime(s.now()), id)
}

func (s *Store) sessionUpdate(id, query string, args ...any) error {
	if s.stale {
		return ErrStale
	}
	return s.tx(func(tx *wtx) error {
		res, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, ErrNoSession)
		}
		return nil
	})
}

// RecentQuestions are the open questions, those closed at or after since and the answers still
// waiting to be typed into an agent, oldest first.
func (s *Store) RecentQuestions(since time.Time) ([]Question, error) {
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.closed_at IS NULL OR q.closed_at >= ? OR (q.from_kind = 'agent' AND q.answered_at IS NOT NULL AND q.delivered_at IS NULL)`, fmtTime(since))
		return err
	})
	return out, err
}
