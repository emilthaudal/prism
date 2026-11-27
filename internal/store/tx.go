package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// DefaultGoal is the goal of a track nobody described yet.
const DefaultGoal = "One line: what done looks like."

const (
	kindAgent = "agent"
	kindCheck = "check"

	closedByAgent      = "agent"
	closedByApplied    = "applied"
	closedBySuperseded = "superseded"
	closedByClosed     = "closed"

	notStarted = "Not started."

	sqliteBusy = 5 // SQLITE_BUSY, the primary result code of every busy extended code

	// MetaRole and MetaParent are the card keys, kept with the card's extra keys, that make a
	// track a lead or one of a lead's threads.
	MetaRole   = "role"
	MetaParent = "parent"
	RoleLead   = "lead"
)

var (
	validName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	recommendMode = regexp.MustCompile(`Recommend: set mode to (focus)\.$`)
	modes         = []string{"focus", "background", "waiting", "parked"}
)

// NewTrackSpec is what a new track starts with.
type NewTrackSpec struct {
	Name, Mode, Tier, Dir, Goal string
	// Meta is kept with the card's extra frontmatter keys.
	Meta map[string]string
}

func insertTrack(s *Store, q *wtx, spec NewTrackSpec) (Track, error) {
	if !validName.MatchString(spec.Name) {
		return Track{}, fmt.Errorf("%w: %q", ErrBadName, spec.Name)
	}
	if spec.Mode == "" {
		spec.Mode = "background"
	}
	if !slices.Contains(modes, spec.Mode) {
		return Track{}, ErrBadMode
	}
	if spec.Goal == "" {
		spec.Goal = DefaultGoal
	}
	var taken int
	if err := q.QueryRow(`SELECT count(*) FROM names WHERE name = ?`, spec.Name).Scan(&taken); err != nil {
		return Track{}, err
	}
	if taken > 0 {
		return Track{}, ErrNameTaken
	}
	now := fmtTime(s.now())
	extra := Extra{Meta: spec.Meta}.encode()
	res, err := q.Exec(`INSERT INTO tracks (name, mode, tier, dir, goal, where_text, extra, updated_at, changed_at, created_at)
	  VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?)`, spec.Name, spec.Mode, spec.Tier, spec.Dir, spec.Goal, notStarted, extra, now, now)
	if err != nil {
		return Track{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Track{}, err
	}
	if _, err := q.Exec(`INSERT INTO names (name, track_id) VALUES (?, ?)`, spec.Name, id); err != nil {
		return Track{}, err
	}
	q.touch(id)
	return trackByID(q, id)
}

// NewTrack inserts a track and its name. A name that is taken, closed or renamed away is refused
// with ErrNameTaken, whatever its case.
func (s *Store) NewTrack(spec NewTrackSpec) (Track, error) {
	var t Track
	err := s.tx(func(tx *wtx) (err error) {
		t, err = insertTrack(s, tx, spec)
		return err
	})
	return t, err
}

// Bind is tracks bind: it resolves name through every name the track has held, makes the track
// when there is none and records the calling session on it. A closed or renamed track is
// reported and left alone.
func (s *Store) Bind(name, rexSession, dir string) (Resolved, error) {
	var res Resolved
	err := s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		switch {
		case errors.Is(err, ErrNoTrack):
			t, err := insertTrack(s, tx, NewTrackSpec{Name: name, Dir: dir})
			if err != nil {
				return err
			}
			r = Resolved{Track: t}
		case err != nil:
			return err
		}
		res = r
		if r.Closed || r.RenamedTo != "" {
			return nil
		}
		if rexSession != "" && r.Track.RexSession != rexSession {
			res.Track.RexSession = rexSession
			return s.save(tx, &res.Track, false)
		}
		return nil
	})
	return res, err
}

// CardUpdate is what an agent sends to tracks card. A nil pointer or slice leaves the field as
// it is; an empty Asks removes the agent's asks.
type CardUpdate struct {
	Tier      *string
	TierWhy   string // replaces the stored reason when not empty
	Dir       *string
	Links     *string
	Check     *string
	WaitingOn *string
	Goal      *string
	Where     *string // lines joined by "\n"
	Later     *string // lines joined by "\n"
	// Role is "lead" or "" and is refused on a thread, whose parent is set only by Spawn.
	Role *string
	Asks []string
	// Rank, Today and BlockedBy are Emil's and the desk's; Rank 0 and an empty BlockedBy clear.
	Rank      *int
	Today     *string
	BlockedBy *[]string
}

// CardResult is the outcome of Card: the track, and its open asks in order.
type CardResult struct {
	Resolved
	Asks []Ask
}

func set(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

// Card is tracks card: it applies the fields given, reconciles the agent's asks and stamps
// updated_at and changed_at. A closed track is reported and left alone.
func (s *Store) Card(name string, u CardUpdate) (CardResult, error) {
	var out CardResult
	err := s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		out.Resolved = r
		if r.Closed {
			return nil
		}
		s.afterRead()
		t := &out.Track
		set(&t.Tier, u.Tier)
		if u.TierWhy != "" {
			t.TierWhy = u.TierWhy
		}
		set(&t.Dir, u.Dir)
		set(&t.Links, u.Links)
		set(&t.WaitingOn, u.WaitingOn)
		set(&t.Goal, u.Goal)
		set(&t.Where, u.Where)
		set(&t.Later, u.Later)
		if u.Role != nil {
			if err := setRole(t, *u.Role); err != nil {
				return err
			}
		}
		if u.Rank != nil {
			if *u.Rank < 0 {
				return ErrBadRank
			}
			t.Rank = *u.Rank
		}
		set(&t.Today, u.Today)
		if u.BlockedBy != nil {
			if err := setBlockers(tx, *t, *u.BlockedBy); err != nil {
				return err
			}
		}
		if u.Check != nil && *u.Check != t.Check {
			t.Check = *u.Check
			// A seen ask for a check the card no longer has would otherwise survive every write;
			// an unseen one stays until Emil has seen it, as on the files.
			if _, err := tx.Exec(`UPDATE asks SET closed_at = ?, closed_by = ? WHERE track_id = ? AND kind = ? AND closed_at IS NULL
			  AND seen_at IS NOT NULL AND check_id IN (SELECT id FROM checks WHERE track_id = ? AND text <> ?)`,
				fmtTime(s.now()), closedBySuperseded, t.ID, kindCheck, t.ID, t.Check); err != nil {
				return err
			}
		}
		if u.Asks != nil {
			if err := s.reconcileAsks(tx, t.ID, u.Asks); err != nil {
				return err
			}
		}
		if err := s.save(tx, t, true); err != nil {
			return err
		}
		if err := s.mirror(tx, t.ID, s.now()); err != nil {
			return err
		}
		out.Asks, err = openAsks(tx, t.ID)
		return err
	})
	return out, err
}

// reconcileAsks makes the open asks the agent's list: a listed text that matches an open agent
// ask keeps its id and created_at, open agent asks not listed are closed, and check asks Emil
// has not seen stay, after the agent's. Seen check asks are dropped, as a rewrite drops them today.
func (s *Store) reconcileAsks(tx *wtx, trackID int64, texts []string) error {
	open, err := openAsks(tx, trackID)
	if err != nil {
		return err
	}
	now := fmtTime(s.now())
	var answered map[int64]bool
	if !s.stale { // another process is migrating: the questions table may not be there yet
		if answered, err = answeredAsks(tx, trackID); err != nil {
			return err
		}
	}
	var order []int64
	keep := map[int64]bool{}
	listed := map[string]bool{}
	for _, text := range texts {
		if text == "" || listed[text] {
			continue
		}
		listed[text] = true
		// A listed text that is an open check ask keeps that check row, as today's rewrite
		// dedupes by text: an agent re-sending what it read must not show it twice. An agent ask
		// whose answer the session already got is asked again: it is a new ask, with a new question,
		// since the answered one stays answered and the queue would show nothing.
		i := slices.IndexFunc(open, func(a Ask) bool {
			return (a.Kind == kindAgent || a.Kind == kindCheck) && a.Text == text && !keep[a.ID] && !answered[a.ID]
		})
		if i >= 0 {
			keep[open[i].ID] = true
			order = append(order, open[i].ID)
			continue
		}
		res, err := tx.Exec(`INSERT INTO asks (track_id, kind, text, position, created_at) VALUES (?, ?, ?, 0, ?)`,
			trackID, kindAgent, text, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		keep[id] = true
		order = append(order, id)
	}
	for _, a := range open {
		if a.Kind == kindCheck && a.SeenAt.IsZero() && !keep[a.ID] {
			keep[a.ID] = true
			order = append(order, a.ID)
		}
	}
	for _, a := range open {
		if !keep[a.ID] {
			if _, err := tx.Exec(`UPDATE asks SET closed_at = ?, closed_by = ? WHERE id = ?`, now, closedByAgent, a.ID); err != nil {
				return err
			}
		}
	}
	for i, id := range order {
		if _, err := tx.Exec(`UPDATE asks SET position = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return nil
}

// answeredAsks are the track's open asks whose question was answered and the answer delivered to
// the session, so a card write after that one which still lists the ask is asking it again.
func answeredAsks(tx *wtx, trackID int64) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT a.id FROM asks a WHERE a.track_id = ? AND a.closed_at IS NULL
		AND EXISTS (SELECT 1 FROM questions q WHERE q.ask_id = a.id AND q.answered_at IS NOT NULL AND q.delivered_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM questions q WHERE q.ask_id = a.id AND q.closed_at IS NULL)`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Rename is tracks rename: NEW joins names, OLD is retired, the track takes the new name and a
// rename event is logged. Events keep their track_id, so nothing is replayed.
func (s *Store) Rename(oldName, newName string) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, oldName)
		if err != nil {
			return err
		}
		switch {
		case r.Closed:
			return ErrClosed
		case r.RenamedTo != "":
			return fmt.Errorf("%s was renamed to %s", oldName, r.RenamedTo)
		}
		if !validName.MatchString(newName) {
			return fmt.Errorf("%w: %q", ErrBadName, newName)
		}
		var taken int
		if err := tx.QueryRow(`SELECT count(*) FROM names WHERE name = ?`, newName).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return ErrNameTaken
		}
		t := r.Track
		tx.oldNames[t.ID] = t.Name
		if _, err := tx.Exec(`INSERT INTO names (name, track_id) VALUES (?, ?)`, newName, t.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE names SET retired_at = ? WHERE name = ?`, fmtTime(s.now()), oldName); err != nil {
			return err
		}
		t.Name = newName
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		if err := s.renameChildren(tx, oldName, newName); err != nil {
			return err
		}
		if err := touchBlocked(tx, t.ID); err != nil {
			return err
		}
		return logEvent(tx, Event{At: s.now(), Event: "rename", TrackID: t.ID, TrackName: newName, Kind: oldName})
	})
}

// CloseTrack is tracks close: it closes the track and its open asks and logs a close event.
// Closing a closed track is ErrClosed.
func (s *Store) CloseTrack(name string) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		now := s.now()
		if _, err := tx.Exec(`UPDATE asks SET closed_at = ?, closed_by = ? WHERE track_id = ? AND closed_at IS NULL`,
			fmtTime(now), closedByClosed, t.ID); err != nil {
			return err
		}
		if err := s.mirror(tx, t.ID, now); err != nil {
			return err
		}
		t.ClosedAt = now
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		return logEvent(tx, Event{At: now, Event: "close", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir})
	})
}

// SetMode is the pane's mode keys: it moves changed_at but not updated_at, so the track does not
// read as unread, and logs a mode event.
func (s *Store) SetMode(name, mode string) error {
	if !slices.Contains(modes, mode) {
		return ErrBadMode
	}
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		t.Mode = mode
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		return logEvent(tx, Event{At: s.now(), Event: "mode", TrackID: t.ID, TrackName: t.Name, Kind: mode})
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// checkAskText is the ask tracks check adds, in the shape the card's check-ask matcher reads.
func checkAskText(text string, answer bool, mode string) string {
	key := fmt.Sprintf("Check %q is now %s.", text, yesNo(answer))
	if answer {
		// Waiting is computed now, so a yes needs no mode change: the ask itself brings the
		// track out of waiting (EMI-423). Old asks that say "set mode to focus" still match.
		return key + " Recommend: open the track."
	}
	return key + " Recommend: open the track and see why."
}

// CheckResult says whether the answer changed (Fired) and what RecordCheck did about it: added
// Ask for Emil, or, on a track whose newest session the desk owns, left that to the caller,
// which wakes session Wake with a note instead (EMI-431).
type CheckResult struct {
	Fired bool
	Ask   Ask
	Wake  string
}

// RecordCheck is the write half of tracks check, after the answer was fetched outside any
// transaction. It re-reads the track: a check text that changed meanwhile is ErrCardChanged, and
// a parked track is left alone. A stored answer that is missing counts as "no", so a check that
// is already true fires once. A new or flipped answer supersedes the old check ask, logs a check
// event and adds the new ask at the end, unless the track's newest session is the desk's: that
// session owns the track's PRs to the door, so it is woken (Wake) and Emil is not asked.
func (s *Store) RecordCheck(name, text string, answer bool) (CheckResult, error) {
	var res CheckResult
	err := s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		t := r.Track
		switch {
		case r.Closed:
			return ErrClosed
		case t.Check != text:
			return ErrCardChanged
		case t.Mode == "parked":
			return nil
		}
		now := s.now()
		var (
			checkID int64
			old     bool
		)
		err = tx.QueryRow(`SELECT id, answer FROM checks WHERE track_id = ? AND text = ?`, t.ID, text).Scan(&checkID, &old)
		if errors.Is(err, sql.ErrNoRows) {
			rs, err := tx.Exec(`INSERT INTO checks (track_id, text, answer) VALUES (?, ?, 0)`, t.ID, text)
			if err != nil {
				return err
			}
			if checkID, err = rs.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if answer == old {
			if _, err := tx.Exec(`UPDATE checks SET ran_at = ? WHERE id = ?`, fmtTime(now), checkID); err != nil {
				return err
			}
			if !answer {
				return nil
			}
			// A check that is already true wakes the desk's session if it stopped without acting on
			// it (EMI-439). Only the track's newest session counts, as in the flip path, and only
			// while no [check] note went to it since the answer flipped: one extra wake per flip,
			// never one per run.
			var sid string
			err := tx.QueryRow(`SELECT s.id FROM sessions s
				WHERE s.id = (SELECT id FROM sessions WHERE track_id = ? ORDER BY updated_at DESC, created_at DESC LIMIT 1)
				AND s.owner = 'desk' AND s.ended_at IS NULL AND s.status IN ('idle', 'done')
				AND NOT EXISTS (SELECT 1 FROM messages m JOIN checks c ON c.id = ?
					WHERE m.session_id = s.id AND m.from_kind = 'system' AND m.text LIKE '[check]%'
					AND (c.asked_at IS NULL OR m.created_at >= c.asked_at))`, t.ID, checkID).Scan(&sid)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			res.Fired, res.Wake = true, sid
			return nil
		}
		if _, err := tx.Exec(`UPDATE checks SET answer = ?, asked_at = ?, ran_at = ? WHERE id = ?`,
			answer, fmtTime(now), fmtTime(now), checkID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE asks SET closed_at = ?, closed_by = ? WHERE check_id = ? AND closed_at IS NULL`,
			fmtTime(now), closedBySuperseded, checkID); err != nil {
			return err
		}
		res.Fired = true
		var sid, owner string
		err = tx.QueryRow(`SELECT id, owner FROM sessions WHERE track_id = ? ORDER BY updated_at DESC, created_at DESC LIMIT 1`, t.ID).Scan(&sid, &owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner == "desk" {
			res.Wake = sid
			if err := s.save(tx, &t, true); err != nil {
				return err
			}
			return logEvent(tx, Event{At: now, Event: "check", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir, Kind: yesNo(answer)})
		}
		var pos int
		if err := tx.QueryRow(`SELECT coalesce(max(position), 0) + 1 FROM asks WHERE track_id = ? AND closed_at IS NULL`, t.ID).Scan(&pos); err != nil {
			return err
		}
		rs, err := tx.Exec(`INSERT INTO asks (track_id, kind, text, position, check_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			t.ID, kindCheck, checkAskText(text, answer, t.Mode), pos, checkID, fmtTime(now))
		if err != nil {
			return err
		}
		id, err := rs.LastInsertId()
		if err != nil {
			return err
		}
		if res.Ask, err = scanAsk(tx.QueryRow(`SELECT `+askCols+` FROM asks WHERE id = ?`, id)); err != nil {
			return err
		}
		if err := s.save(tx, &t, true); err != nil {
			return err
		}
		return logEvent(tx, Event{At: now, Event: "check", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir, Kind: yesNo(answer)})
	})
	return res, err
}

// ApplyCheckAsk is the pane taking a check ask's recommendation: it closes the ask by id, sets
// mode focus when that is what it recommends and logs an answer event. Applied is false when the
// ask is not an open check ask any more, which makes a second apply a no-op.
func (s *Store) ApplyCheckAsk(askID int64) (applied bool, err error) {
	err = s.tx(func(tx *wtx) error {
		a, err := scanAsk(tx.QueryRow(`SELECT `+askCols+` FROM asks WHERE id = ?`, askID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if a.Kind != kindCheck || !a.Open() {
			return nil
		}
		t, err := trackByID(tx, a.TrackID)
		if err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.Exec(`UPDATE asks SET closed_at = ?, closed_by = ? WHERE id = ?`, fmtTime(now), closedByApplied, a.ID); err != nil {
			return err
		}
		if m := recommendMode.FindStringSubmatch(a.Text); m != nil {
			t.Mode = m[1]
		}
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		applied = true
		return logEvent(tx, Event{At: now, Event: "answer", TrackID: t.ID, TrackName: t.Name, Cwd: t.Dir, Kind: "applied"})
	})
	return applied, err
}

// AnswerSent is the pane after it pressed Enter on an answer: the open asks the answer accepted
// get answered_at and every other open ask loses it, since it describes one answer; then e is
// logged as an answer event of the track.
func (s *Store) AnswerSent(name string, askIDs []int64, e Event) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		now := s.now()
		if _, err := tx.Exec(`UPDATE asks SET answered_at = NULL WHERE track_id = ? AND closed_at IS NULL`, t.ID); err != nil {
			return err
		}
		for _, id := range askIDs {
			if _, err := tx.Exec(`UPDATE asks SET answered_at = ? WHERE id = ? AND track_id = ? AND closed_at IS NULL`, fmtTime(now), id, t.ID); err != nil {
				return err
			}
		}
		if err := s.save(tx, &t, false); err != nil {
			return err
		}
		e.At, e.Event, e.TrackID, e.TrackName = now, "answer", t.ID, t.Name
		if e.Cwd == "" {
			e.Cwd = t.Dir
		}
		return logEvent(tx, e)
	})
}

// sessionKeys are the frontmatter keys SetMeta writes to their own columns.
var sessionKeys = []string{"rex_session", "claude_session", "claude_cwd"}

// SetMeta sets frontmatter values that are not the agent's content, as the file code's setMetas
// does: rex_session, claude_session and claude_cwd go to their columns, any other key is kept
// with the card's extra keys (an empty value removes it). It moves changed_at, not updated_at.
func (s *Store) SetMeta(name string, kv map[string]string) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		old := t
		old.Extra.Meta = map[string]string{}
		for k, v := range t.Extra.Meta {
			old.Extra.Meta[k] = v
		}
		for k, v := range kv {
			switch k {
			case sessionKeys[0]:
				t.RexSession = v
			case sessionKeys[1]:
				t.ClaudeSession = v
			case sessionKeys[2]:
				t.ClaudeCwd = v
			case "answered":
				at, err := time.Parse(time.RFC3339, v)
				if v != "" && err != nil {
					return fmt.Errorf("answered: %w", err)
				}
				t.AnsweredAt = at
			default:
				if t.Extra.Meta == nil {
					t.Extra.Meta = map[string]string{}
				}
				if v == "" {
					delete(t.Extra.Meta, k)
				} else {
					t.Extra.Meta[k] = v
				}
			}
		}
		if t.RexSession == old.RexSession && t.ClaudeSession == old.ClaudeSession && t.ClaudeCwd == old.ClaudeCwd &&
			t.AnsweredAt.Equal(old.AnsweredAt) && t.Extra.encode() == old.Extra.encode() {
			return nil
		}
		return s.save(tx, &t, false)
	})
}

// SetAnswered stamps answered_at at on the open asks numbered ns and clears it on the others,
// as the pane's record of one answer; it logs no event.
func (s *Store) SetAnswered(name string, ns []int, at time.Time) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		open, err := openAsks(tx, r.Track.ID)
		if err != nil {
			return err
		}
		for _, a := range open {
			var v any
			if slices.Contains(ns, a.N) {
				v = fmtTime(at)
			}
			if _, err := tx.Exec(`UPDATE asks SET answered_at = ? WHERE id = ?`, v, a.ID); err != nil {
				return err
			}
		}
		t := r.Track
		return s.save(tx, &t, false)
	})
}

// AddAsk adds an agent ask at the end of the track's open asks, unless an open ask already
// holds the text, and stamps updated_at as a content change.
func (s *Store) AddAsk(name, text string) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		open, err := openAsks(tx, t.ID)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(open, func(a Ask) bool { return strings.Contains(a.Text, text) }) {
			return nil
		}
		pos := 1
		if len(open) > 0 {
			pos = open[len(open)-1].Position + 1
		}
		if _, err := tx.Exec(`INSERT INTO asks (track_id, kind, text, position, created_at) VALUES (?, ?, ?, ?, ?)`,
			t.ID, kindAgent, text, pos, fmtTime(s.now())); err != nil {
			return err
		}
		if err := s.mirror(tx, t.ID, s.now()); err != nil {
			return err
		}
		return s.save(tx, &t, true)
	})
}

// Names maps every name a track gave up by a rename, in lowercase, to the name it has now.
func (s *Store) Names() (map[string]string, error) {
	out := map[string]string{}
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT n.name, t.name FROM names n JOIN tracks t ON t.id = n.track_id WHERE n.retired_at IS NOT NULL`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var old, now string
			if err := rows.Scan(&old, &now); err != nil {
				return err
			}
			out[strings.ToLower(old)] = now
		}
		return rows.Err()
	})
	return out, err
}

// HookEvent is what tracks hook knows about one Claude Code hook call. Event is "prompt", "stop",
// "notify" or "session_start".
type HookEvent struct {
	Event, RexSession, RexBlock, Cwd, Kind string
	Len                                    int
	ClaudeSession, ClaudeCwd               string
}

// hookTrack is the open track holding the hook's Rex session, nil when there is none.
func hookTrack(q querier, rexSession string) (*Track, error) {
	if rexSession == "" {
		return nil, nil
	}
	row := q.QueryRow(`SELECT `+trackCols+` FROM tracks WHERE rex_session = ? AND closed_at IS NULL ORDER BY id LIMIT 1`, rexSession)
	t, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// hookEventRow is the event row a hook call logs, under t when it is not nil.
func hookEventRow(h HookEvent, t *Track, at time.Time) Event {
	e := Event{At: at, Event: h.Event, RexSession: h.RexSession, RexBlock: h.RexBlock, Cwd: h.Cwd, Len: h.Len, Kind: h.Kind}
	if t != nil {
		e.TrackID, e.TrackName = t.ID, t.Name
	}
	return e
}

// IsBusy is whether err is SQLite giving up on the lock another writer holds.
func IsBusy(err error) bool {
	var c interface{ Code() int }
	return errors.As(err, &c) && c.Code()&0xff == sqliteBusy
}

// HookLog logs only the event of a hook call, for when Hook could not get the lock in time:
// the event is kept even when the change to the track is not.
func (s *Store) HookLog(h HookEvent) error {
	if h.Event == "session_start" {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		t, err := hookTrack(tx, h.RexSession)
		if err != nil {
			return err
		}
		return logEvent(tx, hookEventRow(h, t, s.now()))
	})
}

// Hook is tracks hook as one transaction: the event, the Claude session on the track, and the
// answered stamp, which a prompt typed over open asks sets and a stop or session start clears.
// session_start logs no event. The track is the open one holding RexSession; none is fine.
func (s *Store) Hook(h HookEvent) error {
	return s.tx(func(tx *wtx) error {
		t, err := hookTrack(tx, h.RexSession)
		if err != nil {
			return err
		}
		now := s.now()
		if h.Event != "session_start" {
			if err := logEvent(tx, hookEventRow(h, t, now)); err != nil {
				return err
			}
		}
		if t == nil {
			return nil
		}
		changed := false
		// A new session replaces both values; the same one only fills in a cwd still unknown.
		switch {
		case h.ClaudeSession == "":
		case t.ClaudeSession != h.ClaudeSession:
			t.ClaudeSession, t.ClaudeCwd, changed = h.ClaudeSession, h.ClaudeCwd, true
		case t.ClaudeCwd == "" && h.ClaudeCwd != "":
			t.ClaudeCwd, changed = h.ClaudeCwd, true
		}
		switch {
		case h.Event == "prompt" && h.Kind != "system":
			asks, err := openAsks(tx, t.ID)
			if err != nil {
				return err
			}
			if len(asks) > 0 {
				t.AnsweredAt, changed = now, true
			}
		case (h.Event == "stop" || h.Event == "session_start") && !t.AnsweredAt.IsZero():
			t.AnsweredAt, changed = time.Time{}, true
			if _, err := tx.Exec(`UPDATE asks SET answered_at = NULL WHERE track_id = ? AND closed_at IS NULL`, t.ID); err != nil {
				return err
			}
		}
		if !changed {
			return nil
		}
		return s.save(tx, t, false)
	})
}

// SetSession records where the track runs, for resume and adopt. Empty values are left as they are.
func (s *Store) SetSession(name, rexSession, claudeSession, claudeCwd string) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		if r.Closed {
			return ErrClosed
		}
		t := r.Track
		old := t
		for _, p := range []struct {
			dst *string
			v   string
		}{{&t.RexSession, rexSession}, {&t.ClaudeSession, claudeSession}, {&t.ClaudeCwd, claudeCwd}} {
			if p.v != "" {
				*p.dst = p.v
			}
		}
		if t.RexSession == old.RexSession && t.ClaudeSession == old.ClaudeSession && t.ClaudeCwd == old.ClaudeCwd {
			return nil
		}
		return s.save(tx, &t, false)
	})
}

// View is a track with its open asks, which is what a card renders from.
type View struct {
	Resolved
	Asks []Ask
}

// Get resolves name and reads the track with its open asks.
func (s *Store) Get(name string) (View, error) {
	var v View
	err := s.read(func(q querier) (err error) {
		if v.Resolved, err = resolve(q, name); err != nil {
			return err
		}
		v.Asks, err = openAsks(q, v.Track.ID)
		return err
	})
	return v, err
}

// Events lists the events of the track name points to, oldest first.
func (s *Store) Events(name string) ([]Event, error) {
	var out []Event
	err := s.read(func(q querier) error {
		r, err := resolve(q, name)
		if err != nil {
			return err
		}
		rows, err := q.Query(`SELECT id, at, event, coalesce(track_name, ''), coalesce(rex_session, ''), coalesce(rex_block, ''),
		  coalesce(cwd, ''), coalesce(len, 0), coalesce(kind, '') FROM events WHERE track_id = ? ORDER BY id`, r.Track.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e := Event{TrackID: r.Track.ID}
			var at sql.NullString
			if err := rows.Scan(&e.ID, &at, &e.Event, &e.TrackName, &e.RexSession, &e.RexBlock, &e.Cwd, &e.Len, &e.Kind); err != nil {
				return err
			}
			if e.At, err = parseTime(at); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// Checks lists the check records of the track name points to.
func (s *Store) Checks(name string) ([]Check, error) {
	var out []Check
	err := s.read(func(q querier) error {
		r, err := resolve(q, name)
		if err != nil {
			return err
		}
		rows, err := q.Query(`SELECT id, text, answer, asked_at, ran_at FROM checks WHERE track_id = ? ORDER BY id`, r.Track.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c := Check{TrackID: r.Track.ID}
			var asked, ran sql.NullString
			if err := rows.Scan(&c.ID, &c.Text, &c.Answer, &asked, &ran); err != nil {
				return err
			}
			if err := parseTimes(asked, &c.AskedAt, ran, &c.RanAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// Render is the card text of the track name points to, with the generated line views carry.
func (s *Store) Render(name string) (string, error) {
	v, err := s.Get(name)
	if err != nil {
		return "", err
	}
	return RenderView(v.Track, v.Asks), nil
}
