package deskui

import (
	"slices"
	"strings"
)

type rowKind int

const (
	rowDesk rowKind = iota
	rowGroup
	rowParked
	rowTrack
	rowSession
	rowBlank
)

// leftRow is one line of the sessions column.
type leftRow struct {
	key   string
	kind  rowKind
	label string
	count int
	track *Track
	sess  *Session // the session itself, or a track's only session
	lead  int      // a track's sessions when it has more than one
	tree  string   // "├ " or "└ " for a nested session
}

func (r leftRow) selectable() bool { return r.kind != rowGroup && r.kind != rowBlank }

func (r leftRow) session() *Session {
	if r.kind == rowSession || (r.kind == rowTrack && r.lead == 0) {
		return r.sess
	}
	return nil
}

// view is what the middle column shows for this row.
func (r leftRow) view() string {
	switch {
	case r.kind == rowDesk:
		return ""
	case r.session() != nil:
		return "s:" + r.sess.ID
	case r.track != nil:
		return "t:" + r.track.ID
	}
	return ""
}

var modes = []struct{ mode, label string }{{"focus", "FOCUS"}, {"background", "BACKGROUND"}, {"waiting", "WAITING"}}

func landed(s Session) bool { return s.Changes != nil && s.Changes.Merged }

// liveSessions are a track's sessions that have not landed, in snapshot order.
func (m *Model) liveSessions(track string) []Session {
	var out []Session
	for _, s := range m.snap.Sessions {
		if s.Track == track && s.ID != m.snap.Desk.Session && !landed(s) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) tracksIn(mode string) []Track {
	var ts []Track
	for _, t := range m.snap.Tracks {
		if t.Mode == mode {
			ts = append(ts, t)
		}
	}
	slices.SortStableFunc(ts, func(a, b Track) int { return a.Rank - b.Rank })
	return ts
}

func (m *Model) leftRows() []leftRow {
	rows := []leftRow{{key: "desk", kind: rowDesk}}
	addTrack := func(t Track) {
		ss := m.liveSessions(t.ID)
		r := leftRow{key: "t:" + t.ID, kind: rowTrack, track: &t}
		if len(ss) == 1 {
			r.sess = &ss[0]
		} else if len(ss) > 1 {
			r.lead = len(ss)
		}
		rows = append(rows, r)
		if len(ss) > 1 {
			for i := range ss {
				tree := "├ "
				if i == len(ss)-1 {
					tree = "└ "
				}
				rows = append(rows, leftRow{key: "s:" + ss[i].ID, kind: rowSession, sess: &ss[i], track: &t, tree: tree})
			}
		}
	}
	for _, g := range modes {
		ts := m.tracksIn(g.mode)
		if len(ts) == 0 {
			continue
		}
		rows = append(rows, leftRow{kind: rowBlank}, leftRow{kind: rowGroup, label: g.label})
		for _, t := range ts {
			addTrack(t)
		}
	}
	var loose []Session
	for _, s := range m.snap.Sessions {
		if s.ID == m.snap.Desk.Session || landed(s) {
			continue
		}
		if _, ok := m.snap.track(s.Track); !ok {
			loose = append(loose, s)
		}
	}
	if len(loose) > 0 {
		rows = append(rows, leftRow{kind: rowBlank}, leftRow{kind: rowGroup, label: "NO TRACK"})
		for i := range loose {
			rows = append(rows, leftRow{key: "s:" + loose[i].ID, kind: rowSession, sess: &loose[i]})
		}
	}
	if parked := m.tracksIn("parked"); len(parked) > 0 {
		rows = append(rows, leftRow{kind: rowBlank}, leftRow{key: "parked", kind: rowParked, label: "PARKED", count: len(parked)})
		if m.parkedOpen {
			for _, t := range parked {
				addTrack(t)
			}
		}
	}
	return rows
}

// leftRowFor finds the row that shows a session: its own, or its track's when it is the only one.
func (m *Model) leftRowFor(session string) (leftRow, bool) {
	for _, r := range m.leftRows() {
		if s := r.session(); s != nil && s.ID == session {
			return r, true
		}
	}
	return leftRow{}, false
}

type boardKind int

const (
	brQuestion boardKind = iota
	brQueued
	brFlight
	brReady
	brWaiting
	brLanded
	brLandedItem
)

type boardRow struct {
	key  string
	kind boardKind
	q    *Question
	qd   *Question // an answered question waiting to be typed into its agent
	s    *Session
	t    *Track
}

func inFlight(s Session) bool {
	switch s.Status {
	case "working", "blocked", "error":
		return true
	case "idle":
		return s.Host != "stopped" || strings.Contains(s.Activity, "queued")
	}
	return false
}

func ready(s Session) bool { return s.Status == "done" && s.Changes != nil && !s.Changes.Merged }

// boardRows are the board's items in the order they are drawn: what needs Emil, what runs,
// what is ready to land, who is waited on, and what landed.
func (m *Model) boardRows() []boardRow {
	var rows []boardRow
	for _, q := range m.openQuestions() {
		rows = append(rows, boardRow{key: "q:" + q.ID, kind: brQuestion, q: &q})
	}
	for _, q := range m.queuedQuestions() {
		rows = append(rows, boardRow{key: "a:" + q.ID, kind: brQueued, qd: &q, t: m.trackNamed(q.Track)})
	}
	sessions := m.workSessions()
	for i, s := range sessions {
		if inFlight(s) {
			rows = append(rows, boardRow{key: "s:" + s.ID, kind: brFlight, s: &sessions[i]})
		}
	}
	for i, s := range sessions {
		if ready(s) {
			rows = append(rows, boardRow{key: "s:" + s.ID, kind: brReady, s: &sessions[i]})
		}
	}
	for _, t := range m.snap.Tracks {
		if t.WaitingOn != "" && t.Mode != "parked" {
			rows = append(rows, boardRow{key: "w:" + t.ID, kind: brWaiting, t: &t})
		}
	}
	ls := m.landedSessions()
	if len(ls) > 0 {
		rows = append(rows, boardRow{key: "landed", kind: brLanded})
		if m.landedOpen {
			for i := range ls {
				rows = append(rows, boardRow{key: "l:" + ls[i].ID, kind: brLandedItem, s: &ls[i]})
			}
		}
	}
	return rows
}

func (m *Model) openQuestions() []Question {
	var qs []Question
	for _, q := range m.snap.Questions {
		if q.Open() {
			qs = append(qs, q)
		}
	}
	slices.SortStableFunc(qs, func(a, b Question) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return qs
}

// queuedQuestions are the answers recorded while their agent was busy, oldest first.
func (m *Model) queuedQuestions() []Question {
	var qs []Question
	for _, q := range m.snap.Questions {
		if q.Queued() {
			qs = append(qs, q)
		}
	}
	slices.SortStableFunc(qs, func(a, b Question) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return qs
}

func (m *Model) trackNamed(id string) *Track {
	if t, ok := m.snap.track(id); ok {
		return &t
	}
	return nil
}

func (m *Model) workSessions() []Session {
	var out []Session
	for _, s := range m.snap.Sessions {
		if s.ID != m.snap.Desk.Session {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) landedSessions() []Session {
	var out []Session
	for _, s := range m.workSessions() {
		if landed(s) {
			out = append(out, s)
		}
	}
	return out
}

// openQuestionOn is the open question a session or track raised, for its chip.
func (m *Model) openQuestionOn(session, track string) (Question, bool) {
	for _, q := range m.openQuestions() {
		if (session != "" && q.Session == session) || (session == "" && q.Track == track) {
			return q, true
		}
	}
	return Question{}, false
}
