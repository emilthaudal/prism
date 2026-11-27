package store

import (
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"
)

// sessionsSchema is the schema that added sessions, questions and messages.
const sessionsSchema = 8

// activitySchema is the schema that added a session's activity and its subagents.
const activitySchema = 14

const (
	// handBackWindow is how long a delivered message's text still counts as the desk's prompt.
	handBackWindow = 10 * time.Minute
	lastAnswerMax  = 4000
)

// Session kinds of hook event, as SessionHook takes them.
const (
	SessionStart   = "start"
	SessionPrompt  = "prompt"
	SessionStop    = "stop"
	SessionBlocked = "blocked"
	SessionEnd     = "end"
	// SessionTool is a tool the session's own agent is about to use; Activity is what it does.
	SessionTool = "tool"
	// SessionAgentQueued is the Agent tool starting a subagent, named by AgentName; the
	// SessionAgentStart that follows takes the name.
	SessionAgentQueued = "agent_queued"
	SessionAgentStart  = "agent_start"
	SessionAgentStop   = "agent_stop"
	// SessionAgentTool is a tool a subagent is about to use: a session blocked while its own
	// agent waits on the Agent tool waited on that subagent's prompt, which is answered now.
	SessionAgentTool = "agent_tool"
)

// backfillSessions gives every open track that has a Claude session a sessions row.
func backfillSessions(tx *wtx, now time.Time) error {
	at := fmtTime(now)
	_, err := tx.Exec(`INSERT OR IGNORE INTO sessions (id, track_id, owner, host, cwd, created_at, updated_at)
		SELECT claude_session, id, 'emil', 'rex', claude_cwd, ?, ? FROM tracks
		WHERE closed_at IS NULL AND claude_session != '' ORDER BY id`, at, at)
	return err
}

// SessionEvent is what the hook knows about a session when one of its events fires.
type SessionEvent struct {
	Event      string // SessionStart, SessionPrompt, SessionStop, SessionBlocked or SessionEnd
	ID         string
	Source     string // SessionStart's source: startup, resume, clear, compact
	Background bool
	RexSession string
	Cwd        string
	Model      string
	Prompt     string
	LastAnswer string
	WaitingFor string
	// Activity is what the session's agent is doing now, as one short line.
	Activity string
	// AgentID is the subagent a SessionAgentStart or SessionAgentStop is about; AgentName is its
	// name: the Agent tool's description, else its type.
	AgentID, AgentName string
	// BackgroundTasks is how many background tasks a Stop payload lists as still running.
	BackgroundTasks int
	// DeskParent and StartPrompt come from the settings of a session the desk started: its row
	// is the desk's from the first hook, and its start prompt is not Emil's.
	DeskParent  string
	StartPrompt string
}

// Session is a sessions row, with the name of its track when it has one.
type Session struct {
	ID, Track, ParentID, Owner, Host, Name, Cwd, Model, Status, WaitingFor, Activity, LastAnswer string
	TrackID                                                                                      int64
	SeenAt, CreatedAt, UpdatedAt, EndedAt                                                        time.Time
}

// Subagent is a subagent a session started: working, or done until the session's next prompt.
type Subagent struct {
	Session, ID, Name, Status string
}

// SessionHook keeps a session's row current from one hook event, creating the row when the
// session is new to the store. It never renders a view: no card shows a session. While another
// process migrates the database to the schema with sessions, it writes nothing.
func (s *Store) SessionHook(e SessionEvent) error {
	if s.stale {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		now := s.now()
		at := fmtTime(now)
		host := "rex"
		if e.Background {
			host = "background"
		}
		owner, parent := "emil", sql.NullString{}
		if e.DeskParent != "" {
			owner, parent = "desk", sql.NullString{String: e.DeskParent, Valid: true}
		}
		if _, err := tx.Exec(`INSERT INTO sessions (id, parent_id, owner, host, cwd, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`, e.ID, parent, owner, host, e.Cwd, e.Model, at, at); err != nil {
			return err
		}
		if err := sessionTrack(tx, e); err != nil {
			return err
		}
		var err error
		switch e.Event {
		case SessionStart:
			fresh := e.Source == "" || e.Source == "startup" || e.Source == "resume"
			_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL,
				cwd = COALESCE(NULLIF(?, ''), cwd), model = COALESCE(NULLIF(?, ''), model),
				status = CASE WHEN ? THEN 'idle' ELSE status END,
				waiting_for = CASE WHEN ? THEN '' ELSE waiting_for END,
				activity = CASE WHEN ? THEN '' ELSE activity END, updated_at = ? WHERE id = ?`,
				host, e.Cwd, e.Model, fresh, fresh, fresh, at, e.ID)
			if err == nil && fresh {
				_, err = tx.Exec(`DELETE FROM session_subagents WHERE session_id = ?`, e.ID)
			}
		case SessionPrompt:
			if err = handBack(tx, e, now); err == nil {
				_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL, status = 'working', waiting_for = '', updated_at = ? WHERE id = ?`, host, at, e.ID)
			}
			if err == nil {
				_, err = tx.Exec(`DELETE FROM session_subagents WHERE session_id = ? AND status != 'working'`, e.ID)
			}
		case SessionTool:
			_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL, status = 'working', waiting_for = '', activity = ?, updated_at = ? WHERE id = ?`,
				host, e.Activity, at, e.ID)
		case SessionAgentQueued:
			_, err = tx.Exec(`INSERT INTO session_subagents (session_id, agent_id, name, status, updated_at) VALUES (?, ?, ?, 'queued', ?)`,
				e.ID, queuedAgentPrefix+e.AgentID, e.AgentName, at)
			if err == nil {
				_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL, status = 'working', waiting_for = '', activity = ?, updated_at = ? WHERE id = ?`,
					host, e.Activity, at, e.ID)
			}
		case SessionAgentStart:
			err = agentStarted(tx, e, at)
		case SessionAgentTool:
			_, err = tx.Exec(`UPDATE sessions SET status = 'working', waiting_for = '', updated_at = ? WHERE id = ? AND status = 'blocked'
				AND (activity IN ('Agent', 'Task') OR activity LIKE 'Agent: %' OR activity LIKE 'Task: %')`, at, e.ID)
		case SessionAgentStop:
			_, err = tx.Exec(`INSERT INTO session_subagents (session_id, agent_id, name, status, updated_at) VALUES (?, ?, ?, 'done', ?)
				ON CONFLICT(session_id, agent_id) DO UPDATE SET status = 'done', updated_at = excluded.updated_at`, e.ID, e.AgentID, e.AgentName, at)
		case SessionStop:
			_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL, status = 'done', waiting_for = '', activity = '', last_answer = ?, updated_at = ? WHERE id = ?`,
				host, clip(e.LastAnswer, lastAnswerMax), at, e.ID)
			if err == nil {
				err = turnEnded(tx, e, at)
			}
			if err == nil {
				err = reportToParent(tx, e.ID, clip(e.LastAnswer, lastAnswerMax), at)
			}
		case SessionBlocked:
			var was string
			if err = tx.QueryRow(`SELECT status FROM sessions WHERE id = ?`, e.ID).Scan(&was); err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE sessions SET host = ?, ended_at = NULL, status = 'blocked', waiting_for = ?, updated_at = ? WHERE id = ?`,
				host, e.WaitingFor, at, e.ID)
			// A permission prompt fires both PermissionRequest and Notification: one report.
			if err == nil && was != "blocked" {
				err = reportToParent(tx, e.ID, e.WaitingFor, at)
			}
		case SessionEnd:
			_, err = tx.Exec(`UPDATE sessions SET host = 'stopped', ended_at = ?, activity = '', `+endedMidTurn+`, updated_at = ? WHERE id = ?`, at, at, e.ID)
			if err == nil {
				_, err = tx.Exec(`DELETE FROM session_subagents WHERE session_id = ?`, e.ID)
			}
		}
		return err
	})
}

// queuedAgentPrefix marks the agent_id of a subagent the Agent tool is about to start, which
// holds the tool's description until SubagentStart says the real id.
const queuedAgentPrefix = "queued:"

// agentStarted gives the subagent that started the oldest queued name, else its type as the name.
func agentStarted(tx *wtx, e SessionEvent, at string) error {
	name := e.AgentName
	var queued string
	err := tx.QueryRow(`SELECT agent_id FROM session_subagents WHERE session_id = ? AND status = 'queued' ORDER BY rowid LIMIT 1`, e.ID).Scan(&queued)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	default:
		if err := tx.QueryRow(`DELETE FROM session_subagents WHERE session_id = ? AND agent_id = ? RETURNING name`, e.ID, queued).Scan(&name); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO session_subagents (session_id, agent_id, name, status, updated_at) VALUES (?, ?, ?, 'working', ?)
		ON CONFLICT(session_id, agent_id) DO UPDATE SET status = 'working', updated_at = excluded.updated_at`, e.ID, e.AgentID, name, at)
	return err
}

// turnEnded drops the subagents that never started, and marks the working ones done unless the
// Stop payload lists background tasks still running.
func turnEnded(tx *wtx, e SessionEvent, at string) error {
	if _, err := tx.Exec(`DELETE FROM session_subagents WHERE session_id = ? AND status = 'queued'`, e.ID); err != nil {
		return err
	}
	if e.BackgroundTasks > 0 {
		return nil
	}
	_, err := tx.Exec(`UPDATE session_subagents SET status = 'done', updated_at = ? WHERE session_id = ? AND status = 'working'`, at, e.ID)
	return err
}

// Subagents lists the subagents of every session, oldest first.
func (s *Store) Subagents() ([]Subagent, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Subagent
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT session_id, agent_id, name, status FROM session_subagents WHERE status != 'queued' ORDER BY rowid`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Subagent
			if err := rows.Scan(&x.Session, &x.ID, &x.Name, &x.Status); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// reportToParent writes text as a report from child to its parent session, while the desk owns
// the child: a session Emil took over stops reporting.
func reportToParent(tx *wtx, child, text, at string) error {
	_, err := tx.Exec(`INSERT INTO messages (session_id, from_kind, from_session, kind, text, created_at)
		SELECT c.parent_id, 'session', c.id, 'report', ?, ? FROM sessions c JOIN sessions p ON p.id = c.parent_id
		WHERE c.id = ? AND c.owner = 'desk'`, text, at, child)
	return err
}

// sessionTrack gives a session without a track the open track that names it as its Claude
// session, else, for a session in Rex, the open track bound to its Rex session.
func sessionTrack(tx *wtx, e SessionEvent) error {
	var has sql.NullInt64
	if err := tx.QueryRow(`SELECT track_id FROM sessions WHERE id = ?`, e.ID).Scan(&has); err != nil || has.Valid {
		return err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM tracks WHERE closed_at IS NULL AND claude_session = ? ORDER BY id LIMIT 1`, e.ID).Scan(&id)
	if err == sql.ErrNoRows && !e.Background && e.RexSession != "" {
		err = tx.QueryRow(`SELECT id FROM tracks WHERE closed_at IS NULL AND rex_session = ? ORDER BY id LIMIT 1`, e.RexSession).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE sessions SET track_id = ? WHERE id = ?`, id, e.ID)
	return err
}

// handBack gives a desk-owned session to Emil when a prompt arrives that is not the text of a
// message tracks delivered to it lately: Emil typed it himself. A turn Claude Code starts on its
// own, such as a background task's notification, is nobody typing.
func handBack(tx *wtx, e SessionEvent, now time.Time) error {
	if e.StartPrompt != "" && strings.TrimSpace(e.Prompt) == strings.TrimSpace(e.StartPrompt) {
		return nil
	}
	if harnessPrompt(e.Prompt) {
		return nil
	}
	_, err := tx.Exec(`UPDATE sessions SET owner = 'emil' WHERE id = ? AND owner = 'desk' AND NOT EXISTS (
		SELECT 1 FROM messages WHERE session_id = ? AND delivered_at >= ? AND trim(text, ' '||char(9)||char(10)||char(13)) = ?)`,
		e.ID, e.ID, fmtTime(now.Add(-handBackWindow)), strings.TrimSpace(e.Prompt))
	return err
}

// harnessPrompt is whether a prompt is one Claude Code wrote itself: a background task's
// notification, a system reminder, or a subagent's or another session's message (a finished
// review's report arrives as one, and another Claude session's message as a cross-session
// message), alone or ahead of the rest of the turn's text.
func harnessPrompt(p string) bool {
	t := strings.TrimPrefix(strings.TrimSpace(p), "Another Claude session sent a message:")
	t = strings.TrimSpace(t)
	for _, tag := range []string{"<task-notification>", "<system-reminder>", "<agent-message ", "<cross-session-message "} {
		if strings.HasPrefix(t, tag) {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Sessions lists every session of an open track and every session with no track, the most
// recently changed first.
func (s *Store) Sessions() ([]Session, error) {
	return s.sessionsWhere(`s.track_id IS NULL OR t.closed_at IS NULL`)
}

// Holders are the sessions other than self that run on the open track called name: their
// process lives, whatever their turn's state, since a session that finished its turn takes the
// desk's next note. tracks bind refuses a track that has one (EMI-427).
func (s *Store) Holders(name, self string) ([]Session, error) {
	return s.sessionsWhere(`t.closed_at IS NULL AND s.host != 'stopped' AND s.id != ?
		AND t.id = (SELECT track_id FROM names WHERE name = ?)`, self, name)
}

func (s *Store) sessionsWhere(where string, args ...any) ([]Session, error) {
	if s.stale {
		return nil, ErrStale
	}
	var out []Session
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT s.id, COALESCE(t.name, ''), COALESCE(s.track_id, 0), COALESCE(s.parent_id, ''), s.owner, s.host,
			s.name, s.cwd, s.model, s.status, s.waiting_for, s.activity, s.last_answer, s.seen_at, s.created_at, s.updated_at, s.ended_at
			FROM sessions s LEFT JOIN tracks t ON t.id = s.track_id
			WHERE `+where+` ORDER BY s.updated_at DESC, s.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Session
			var seen, created, updated, ended sql.NullString
			if err := rows.Scan(&x.ID, &x.Track, &x.TrackID, &x.ParentID, &x.Owner, &x.Host, &x.Name, &x.Cwd, &x.Model,
				&x.Status, &x.WaitingFor, &x.Activity, &x.LastAnswer, &seen, &created, &updated, &ended); err != nil {
				return err
			}
			if err := parseTimes(seen, &x.SeenAt, created, &x.CreatedAt, updated, &x.UpdatedAt, ended, &x.EndedAt); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// endedMidTurn is the SET clause that turns a session that ended while working or blocked to
// error: it works no more, so the notes held for it go out, which resumes it.
const endedMidTurn = `status = CASE WHEN status IN ('working', 'blocked') THEN 'error' ELSE status END, waiting_for = ''`

// usageLimitPrefix starts the waiting_for of a session the usage limit cut off.
const usageLimitPrefix = "usage limit: "

// UsageLimitWhy is the waiting_for of a session the usage limit cut off, which resets at resets.
func UsageLimitWhy(resets string) string { return usageLimitPrefix + "resets " + resets }

// UsageLimited is whether a waiting_for says the usage limit cut the session off.
func UsageLimited(waitingFor string) bool { return strings.HasPrefix(waitingFor, usageLimitPrefix) }

// Stall is a session whose turn ended without its Stop hook. Hold keeps it blocked, which holds
// its notes, while the usage limit has not reset; otherwise it turns error, so its notes go out
// and resume it. Why says which, for waiting_for.
type Stall struct {
	ID, Why string
	Hold    bool
}

// MarkStalled writes each stall on its session, unless a hook touched the row at or after
// before, the time the processes were read: such a session may be working again. Its
// subagents end with the turn.
func (s *Store) MarkStalled(stalls []Stall, before time.Time) error {
	if len(stalls) == 0 || s.stale {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		for _, x := range stalls {
			status := "error"
			if x.Hold {
				status = "blocked"
			}
			// A turn whose Stop hook was lost while its subagents run is waiting on them, not cut
			// off: only a usage limit, which ends them too, stalls it.
			res, err := tx.Exec(`UPDATE sessions SET status = ?, waiting_for = ?, activity = '', updated_at = ?
				WHERE id = ? AND host != 'stopped' AND status IN ('working', 'blocked') AND updated_at < ?
				AND (? OR NOT EXISTS (SELECT 1 FROM session_subagents WHERE session_id = sessions.id AND status = 'working'))`,
				status, x.Why, at, x.ID, fmtTime(before), UsageLimited(x.Why) || x.Hold)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM session_subagents WHERE session_id = ? AND status != 'done'`, x.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkStopped sets host stopped on the sessions named that a hook has not touched since before,
// the time their liveness was read: a session that started or worked after it may be alive.
func (s *Store) MarkStopped(ids []string, before time.Time) error {
	if len(ids) == 0 || s.stale {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		at := fmtTime(s.now())
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE sessions SET host = 'stopped', ended_at = COALESCE(ended_at, ?), activity = '', `+endedMidTurn+`
				WHERE id = ? AND host != 'stopped' AND updated_at < ?`, at, id, fmtTime(before)); err != nil {
				return err
			}
			// A session that is gone runs no subagents: left working, they would hold its notes.
			if _, err := tx.Exec(`DELETE FROM session_subagents WHERE session_id = ? AND EXISTS (
				SELECT 1 FROM sessions WHERE id = ? AND host = 'stopped')`, id, id); err != nil {
				return err
			}
		}
		return nil
	})
}
