package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// AddPermissionQuestion writes the question a permission prompt asks for a session the desk
// owns, from the agent, with the session's track and no recommendation; ok is false for any
// other session.
func (s *Store) AddPermissionQuestion(session, question string, options []string) (id int64, ok bool, err error) {
	return s.AddPromptQuestion(session, question, options, nil)
}

// promptShareWindow is how close together two hooks' identical prompts must come to share a question.
const promptShareWindow = 5 * time.Second

// AddPromptQuestion is AddPermissionQuestion with links, such as the page an MCP server's
// approval prompt asks Emil to open.
func (s *Store) AddPromptQuestion(session, question string, options, links []string) (id int64, ok bool, err error) {
	if s.stale {
		return 0, false, nil
	}
	opts := make([]QOption, len(options))
	for i, l := range options {
		opts[i].Label = l
	}
	err = s.tx(func(tx *wtx) error {
		var owner string
		var track sql.NullInt64
		err := tx.QueryRow(`SELECT owner, track_id FROM sessions WHERE id = ?`, session).Scan(&owner, &track)
		if errors.Is(err, sql.ErrNoRows) || err == nil && owner != "desk" {
			return nil
		}
		if err != nil {
			return err
		}
		// The same prompt can reach two hooks, the session's own and Emil's global one, which
		// fire together: both wait on one question. Two prompts that only look alike, such as
		// parallel subagents' commands that start alike, rarely come within seconds.
		err = tx.QueryRow(`SELECT id FROM questions WHERE session_id = ? AND from_kind = 'agent' AND ask_id IS NULL
			AND question = ? AND options = ? AND links = ? AND created_at >= ? AND answered_at IS NULL AND closed_at IS NULL
			ORDER BY id DESC LIMIT 1`,
			session, question, jsonOf(opts), strings.Join(links, ","), fmtTime(s.now().Add(-promptShareWindow))).Scan(&id)
		if err == nil {
			ok = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := tx.Exec(`INSERT INTO questions (track_id, session_id, from_kind, question, options, links, created_at) VALUES (?, ?, 'agent', ?, ?, ?, ?)`,
			track, session, question, jsonOf(opts), strings.Join(links, ","), fmtTime(s.now()))
		if err != nil {
			return err
		}
		ok = true
		id, err = res.LastInsertId()
		return err
	})
	return id, ok, err
}

// TimeOutQuestion closes a question nobody answered, with note as why; false when it was
// answered or closed meanwhile.
func (s *Store) TimeOutQuestion(id int64, note string) (bool, error) {
	var n int64
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`UPDATE questions SET closed_at = ?, answer_text = ? WHERE id = ? AND closed_at IS NULL`, fmtTime(s.now()), note, id)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	return n == 1, err
}

// SessionOwner is the owner of a session and its parent session, empty when there is no row.
func (s *Store) SessionOwner(id string) (owner, parent string, err error) {
	err = s.read(func(q querier) error {
		err := q.QueryRow(`SELECT owner, COALESCE(parent_id, '') FROM sessions WHERE id = ?`, id).Scan(&owner, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return owner, parent, err
}

// DeskQuestions are the questions an agent of a session the desk owns asks outside its card,
// open or closed at or after since.
func (s *Store) DeskQuestions(since time.Time) ([]Question, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.from_kind = 'agent' AND q.ask_id IS NULL
			AND q.session_id IN (SELECT id FROM sessions WHERE owner = 'desk') AND (q.closed_at IS NULL OR q.closed_at >= ?)`, fmtTime(since))
		return err
	})
	return out, err
}
