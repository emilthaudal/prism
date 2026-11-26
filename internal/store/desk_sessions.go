package store

import (
	"database/sql"
	"time"
)

// DeskStart is a background session the desk started on a track.
type DeskStart struct {
	ID, Parent, Track, Name, Cwd, Model, Prompt string
}

// StartDeskSession records a session the desk started: owner desk, host background, its parent
// the desk's own session, its track, and the prompt it started with as a message delivered to
// it, so the hook does not read that prompt as Emil's. The session's hooks may have written its
// row already: one they made knowing its parent keeps the owner they left, since Emil may have
// typed into it by now. A session that already stopped or blocked gets its report here.
func (s *Store) StartDeskSession(d DeskStart) error {
	if s.stale {
		return ErrStale
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		r, err := resolve(tx, d.Track)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO sessions (id, created_at, updated_at) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING`,
			d.Parent, at, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO sessions (id, track_id, parent_id, owner, host, name, cwd, model, status, created_at, updated_at)
			VALUES (?, ?, ?, 'desk', 'background', ?, ?, ?, 'working', ?, ?)
			ON CONFLICT(id) DO UPDATE SET track_id = excluded.track_id, parent_id = excluded.parent_id,
				owner = CASE WHEN sessions.parent_id IS NULL THEN 'desk' ELSE sessions.owner END,
				name = excluded.name, cwd = COALESCE(NULLIF(sessions.cwd, ''), excluded.cwd),
				model = COALESCE(NULLIF(sessions.model, ''), excluded.model),
				status = CASE WHEN sessions.status = 'idle' THEN 'working' ELSE sessions.status END, updated_at = excluded.updated_at`,
			d.ID, r.Track.ID, d.Parent, d.Name, d.Cwd, d.Model, at, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO messages (session_id, from_kind, kind, text, created_at, delivered_at) VALUES (?, 'desk', 'note', ?, ?, ?)`,
			d.ID, d.Prompt, at, at); err != nil {
			return err
		}
		var status, answer, waiting string
		if err := tx.QueryRow(`SELECT status, last_answer, waiting_for FROM sessions WHERE id = ?`, d.ID).Scan(&status, &answer, &waiting); err != nil {
			return err
		}
		switch status {
		case "done":
			return reportToParent(tx, d.ID, answer, at)
		case "blocked":
			return reportToParent(tx, d.ID, waiting, at)
		}
		return nil
	})
}

// DeskActive is how many sessions the desk owns that run and are working or blocked.
func (s *Store) DeskActive() (int, error) {
	if s.stale {
		return 0, ErrStale
	}
	var n int
	err := s.read(func(q querier) error {
		return q.QueryRow(`SELECT count(*) FROM sessions WHERE owner = 'desk' AND host != 'stopped' AND status IN ('working', 'blocked')`).Scan(&n)
	})
	return n, err
}

// QueuedStart is a start the desk asked for while it ran as many sessions as it may.
type QueuedStart struct {
	ID                                      int64
	Track, Goal, Dir, Parent, Model, Prompt string
	QueuedAt                                time.Time
	// Held is set when the track has a live or Emil's session already, or a bound session.
	Held bool
}

// QueueDeskStart queues a start of track's session, and says how many queued starts are ahead.
func (s *Store) QueueDeskStart(track, parent, model, prompt string) (ahead int, err error) {
	if s.stale {
		return 0, ErrStale
	}
	err = s.tx(func(tx *wtx) error {
		r, err := resolve(tx, track)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT count(*) FROM desk_queue q JOIN tracks t ON t.id = q.track_id
			WHERE q.started_at IS NULL AND t.closed_at IS NULL`).Scan(&ahead); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO desk_queue (track_id, parent_id, model, prompt, queued_at) VALUES (?, ?, ?, ?, ?)`,
			r.Track.ID, parent, model, prompt, fmtTime(s.now()))
		return err
	})
	return ahead, err
}

// QueuedDeskStarts are the starts not taken yet whose track is still open, oldest first.
func (s *Store) QueuedDeskStarts() ([]QueuedStart, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []QueuedStart
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT q.id, t.name, t.goal, t.dir, q.parent_id, q.model, q.prompt, q.queued_at,
			t.rex_session != '' OR t.claude_session != '' OR EXISTS (SELECT 1 FROM sessions s WHERE s.track_id = t.id AND (s.owner = 'emil' OR s.host != 'stopped'))
			FROM desk_queue q
			JOIN tracks t ON t.id = q.track_id WHERE q.started_at IS NULL AND t.closed_at IS NULL ORDER BY q.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x QueuedStart
			var at sql.NullString
			if err := rows.Scan(&x.ID, &x.Track, &x.Goal, &x.Dir, &x.Parent, &x.Model, &x.Prompt, &at, &x.Held); err != nil {
				return err
			}
			if err := parseTimes(at, &x.QueuedAt); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// TakeDeskStart marks a queued start taken, false when another process took it first.
func (s *Store) TakeDeskStart(id int64) (bool, error) {
	var n int64
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`UPDATE desk_queue SET started_at = ? WHERE id = ? AND started_at IS NULL`, fmtTime(s.now()), id)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	return n == 1, err
}

// DropDeskStart takes a queued start off the queue without starting it.
func (s *Store) DropDeskStart(id int64) error {
	return s.tx(func(tx *wtx) error {
		_, err := tx.Exec(`DELETE FROM desk_queue WHERE id = ? AND started_at IS NULL`, id)
		return err
	})
}

// DeskSessions are the sessions the desk started, whoever owns them now, and any session the
// desk owns, whatever their track's state.
func (s *Store) DeskSessions() ([]Session, error) {
	return s.sessionsWhere(`s.parent_id IS NOT NULL OR s.owner = 'desk'`)
}

// Report is a report a session the desk owns sent to its parent, not delivered yet.
type Report struct {
	ID                      int64
	From, Track, Name, Text string
	At                      time.Time
}

// DeskReports are the reports not delivered yet from sessions the desk owns, oldest first.
func (s *Store) DeskReports() ([]Report, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Report
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT m.id, s.id, COALESCE(t.name, ''), s.name, m.text, m.created_at FROM messages m
			JOIN sessions s ON s.id = m.from_session LEFT JOIN tracks t ON t.id = s.track_id
			WHERE m.kind = 'report' AND m.delivered_at IS NULL AND s.owner = 'desk' ORDER BY m.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Report
			var at sql.NullString
			if err := rows.Scan(&x.ID, &x.From, &x.Track, &x.Name, &x.Text, &at); err != nil {
				return err
			}
			if err := parseTimes(at, &x.At); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// DeliverReports marks reports delivered.
func (s *Store) DeliverReports(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE messages SET delivered_at = ? WHERE id = ? AND delivered_at IS NULL`, at, id); err != nil {
				return err
			}
		}
		return nil
	})
}
