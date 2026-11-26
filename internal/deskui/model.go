package deskui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// undoWindow is how long an answer waits, with its countdown showing, before it is sent.
const undoWindow = 5 * time.Second

type column int

const (
	colSessions column = iota
	colChat
	colBoard
)

type (
	snapshotMsg struct{ snap Snapshot }
	eventMsg    struct{ ev Event }
	connMsg     struct {
		err  error
		note string
	}
	transcriptMsg struct {
		id      string
		entries []Entry
		err     error
	}
	postedMsg struct {
		path string
		body []byte
		err  error
		done func(m *Model, body []byte) // runs on success
		fail func(m *Model, err error)   // runs on failure, after the status shows the error
	}
	tickMsg  struct{}
	eventsUp struct{ ch chan any }
)

// pending is an answer in its countdown: it is sent when deadline passes, unless z takes it back.
type pending struct {
	q        Question
	option   int // -1: answered in words
	text     string
	deadline time.Time
}

func (p *pending) label() string {
	if p.option >= 0 && p.option < len(p.q.Options) {
		return p.q.Options[p.option].Label
	}
	return "“" + p.text + "”"
}

type Model struct {
	b      Backend
	ctx    context.Context
	cancel context.CancelFunc
	events chan any

	w, h  int
	snap  Snapshot
	have  bool
	live  bool
	conn  string // why the stream is not live
	focus column

	leftKey    string
	parkedOpen bool

	view        string // "" Prism's chat, "s:ID" a session's transcript, "t:ID" a track
	transcripts map[string][]Entry
	scroll      int    // conversation lines scrolled up from the bottom
	lastShown   string // while pinned, the last desk entry shown; "" follows the conversation
	notes       []Entry

	text []rune
	cur  int
	// words is r on the board: the box types an answer to selQ in words. Otherwise the box under the question
	// panel is the desk line and its text goes to the desk; deskDraft keeps that text meanwhile.
	words     bool
	deskDraft []rune
	selQ      string
	qCopy     Question // the selected question as last seen, so an answer elsewhere doesn't resize the composer

	boardKey   string
	landedOpen bool
	pend       []*pending

	help      bool
	status    string
	statusErr bool

	now  func() time.Time
	loc  *time.Location
	open func(url string) error
}

func New(b Backend) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		b: b, ctx: ctx, cancel: cancel, focus: colChat, leftKey: "desk",
		transcripts: map[string][]Entry{}, now: time.Now, loc: time.Local,
		open: func(u string) error { return exec.Command("open", u).Run() },
	}
}

func (m *Model) Init() tea.Cmd {
	ch := make(chan any, 64)
	go follow(m.ctx, m.b, func(v any) {
		select {
		case ch <- v:
		case <-m.ctx.Done():
		}
	})
	return tea.Batch(func() tea.Msg { return eventsUp{ch} }, tick())
}

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *Model) waitEvent() tea.Cmd {
	ch := m.events
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return nil
		}
		return v
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	mm, cmd := m.update(msg)
	m.syncWords()
	return mm, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case eventsUp:
		m.events = msg.ch
		return m, m.waitEvent()
	case snapshotMsg:
		m.pinIfBusy()
		m.snap, m.have, m.live, m.conn = msg.snap, true, true, ""
		return m, tea.Batch(m.afterChange(""), m.waitEvent())
	case eventMsg:
		m.pinIfBusy()
		cmd := m.applyEvent(msg.ev)
		return m, tea.Batch(cmd, m.waitEvent())
	case connMsg:
		m.live = false
		if msg.err != nil {
			m.conn = msg.err.Error()
		} else {
			m.conn = msg.note
		}
		return m, m.waitEvent()
	case transcriptMsg:
		if msg.err != nil {
			m.setErr("could not read the transcript: " + msg.err.Error())
		} else {
			m.transcripts[msg.id] = msg.entries
		}
	case postedMsg:
		if msg.err != nil {
			m.setErr(postError(msg.path, msg.err))
			if msg.fail != nil {
				msg.fail(m, msg.err)
			}
		} else if msg.done != nil {
			msg.done(m, msg.body)
		}
	case tickMsg:
		return m, tea.Batch(m.sendDue(), tick())
	case tea.PasteMsg:
		if m.composing() {
			m.insert(strings.ReplaceAll(msg.Content, "\r\n", "\n"))
		}
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			m.scrollBy(3)
		} else if msg.Button == tea.MouseWheelDown {
			m.scrollBy(-3)
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "Prism"
	return v
}

func (m *Model) setErr(s string) { m.status, m.statusErr = s, true }
func (m *Model) say(s string)    { m.status, m.statusErr = s, false }

func postError(path string, err error) string {
	var he *httpError
	var fe fixtureErr
	if (errors.As(err, &he) && he.code == 409) || (errors.As(err, &fe) && fe.code == 409) {
		return "Already answered elsewhere"
	}
	return "Not sent (" + path + "): " + err.Error()
}

// applyEvent applies one change from the stream, and refetches the open transcript when its
// session changed.
func (m *Model) applyEvent(ev Event) tea.Cmd {
	if !m.have || ev.Seq <= m.snap.Seq {
		return nil
	}
	if err := m.snap.Apply(ev); err != nil {
		m.setErr(err.Error())
		return nil
	}
	refetch := ""
	if ev.Kind == "session" && ev.Op == "upsert" {
		var s struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(ev.Value, &s) == nil && m.view == "s:"+s.ID {
			refetch = s.ID
		}
	}
	return m.afterChange(refetch)
}

// afterChange keeps selections on the same things after the data changed.
func (m *Model) afterChange(refetch string) tea.Cmd {
	if q, ok := m.snap.question(m.selQ); ok {
		m.qCopy = q
	}
	m.pend = slices.DeleteFunc(m.pend, func(p *pending) bool {
		q, ok := m.snap.question(p.q.ID)
		if !ok || !q.Open() {
			m.say(p.q.ID + " was answered elsewhere; yours was not sent")
			return true
		}
		return false
	})
	if refetch != "" {
		return m.fetchTranscript(refetch)
	}
	if m.view != "" && strings.HasPrefix(m.view, "s:") {
		if _, ok := m.transcripts[m.view[2:]]; !ok {
			return m.fetchTranscript(m.view[2:])
		}
	}
	return nil
}

// pinIfBusy freezes the conversation where it is while Emil types or reads back, so what
// arrives shows as "N new" instead of moving the text under him.
func (m *Model) pinIfBusy() {
	if m.lastShown != "" || !m.have || len(m.snap.Conversation) == 0 {
		return
	}
	if len(m.text) > 0 || m.scroll > 0 {
		m.lastShown = m.snap.Conversation[len(m.snap.Conversation)-1].ID
	}
}

func (m *Model) unpin() { m.lastShown, m.scroll = "", 0 }

// shownConversation is the desk's conversation up to the pin, and how many entries wait below it.
func (m *Model) shownConversation() (shown []Entry, newer int) {
	conv := m.snap.Conversation
	if m.lastShown == "" {
		return conv, 0
	}
	i := slices.IndexFunc(conv, func(e Entry) bool { return e.ID == m.lastShown })
	if i < 0 {
		return conv, 0
	}
	return conv[:i+1], len(conv) - i - 1
}

func (m *Model) fetchTranscript(id string) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		es, err := b.Transcript(ctx, id)
		return transcriptMsg{id: id, entries: es, err: err}
	}
}

func (m *Model) post(path string, body any, done func(*Model, []byte), fail func(*Model, error)) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		resp, err := b.Post(ctx, path, body)
		return postedMsg{path: path, body: resp, err: err, done: done, fail: fail}
	}
}

func (m *Model) note(text string) {
	m.notes = append(m.notes, Entry{ID: "note" + strconv.Itoa(len(m.notes)), At: m.now(), Role: "note", Text: text})
}

// enterWords makes the text box the answer in words to the selected question.
func (m *Model) enterWords() {
	if m.words {
		return
	}
	m.words, m.deskDraft = true, m.text
	m.text, m.cur = nil, 0
}

// syncWords gives the desk line its text back once there is no question to answer in words.
func (m *Model) syncWords() {
	if !m.words || m.selQ != "" {
		return
	}
	m.words = false
	if len(m.text) == 0 {
		m.text, m.cur = m.deskDraft, len(m.deskDraft)
	}
	m.deskDraft = nil
}

// composing is whether keys typed in the middle column go to the composer.
func (m *Model) composing() bool { return m.focus == colChat && m.view == "" }

func (m *Model) insert(s string) {
	r := []rune(clean(s, true))
	m.text = slices.Insert(m.text, m.cur, r...)
	m.cur += len(r)
}

func (m *Model) scrollBy(d int) {
	if d < 0 && m.scroll == 0 {
		m.unpin()
		return
	}
	m.scroll = max(0, m.scroll+d)
	if m.scroll == 0 && d < 0 {
		m.unpin()
	}
}

// question is the question the composer answers, and whether it is still open.
func (m *Model) question() (Question, bool) {
	if m.selQ == "" {
		return Question{}, false
	}
	if q, ok := m.snap.question(m.selQ); ok {
		return q, q.Open()
	}
	return m.qCopy, false
}

func (m *Model) selectQuestion(id string) {
	m.selQ = id
	if q, ok := m.snap.question(id); ok {
		m.qCopy = q
	}
}

func (m *Model) pendingFor(id string) *pending {
	for _, p := range m.pend {
		if p.q.ID == id {
			return p
		}
	}
	return nil
}

// answer starts the countdown for an answer; the request goes out when it ends.
func (m *Model) answer(q Question, option int, text string) {
	if option >= len(q.Options) {
		m.setErr(fmt.Sprintf("%s has %d options", q.ID, len(q.Options)))
		return
	}
	m.pend = slices.DeleteFunc(m.pend, func(p *pending) bool { return p.q.ID == q.ID })
	m.pend = append(m.pend, &pending{q: q, option: option, text: text, deadline: m.now().Add(undoWindow)})
	m.selectQuestion(q.ID)
	m.status = ""
}

// undo takes back the newest answer still counting down; an answer in words goes back into the
// composer.
func (m *Model) undo() {
	if len(m.pend) == 0 {
		m.say("Nothing to undo")
		return
	}
	p := m.pend[len(m.pend)-1]
	m.pend = m.pend[:len(m.pend)-1]
	m.selectQuestion(p.q.ID)
	if p.option < 0 && len(m.text) == 0 {
		m.text, m.cur = []rune(p.text), len([]rune(p.text))
	}
	m.say("Took back the answer to " + p.q.ID)
}

func (m *Model) sendDue() tea.Cmd {
	now := m.now()
	var cmds []tea.Cmd
	keep := m.pend[:0]
	for _, p := range m.pend {
		if now.Before(p.deadline) {
			keep = append(keep, p)
			continue
		}
		cmds = append(cmds, m.sendAnswer(p))
		if m.selQ == p.q.ID {
			m.selQ = ""
		}
	}
	m.pend = keep
	return tea.Batch(cmds...)
}

func (m *Model) sendAnswer(p *pending) tea.Cmd {
	var body any = map[string]string{"text": p.text}
	if p.option >= 0 {
		body = map[string]int{"option": p.option}
	}
	label := p.label()
	id := p.q.ID
	return m.post("/v1/questions/"+id+"/answer", body,
		func(m *Model, resp []byte) {
			var reply struct {
				Delivery string `json:"delivery"`
			}
			if json.Unmarshal(resp, &reply) == nil && reply.Delivery == "queued" {
				m.note("Queued for " + m.trackName(p.q.Track) + ": sent when its agent stops")
				m.say("Queued " + id + ": " + label)
				return
			}
			m.note("You answered " + id + ": " + label)
		},
		func(m *Model, _ error) {
			if p.option < 0 && len(m.text) == 0 {
				m.text, m.cur = []rune(p.text), len([]rune(p.text))
			}
		})
}

func (m *Model) sendMessage() tea.Cmd {
	text := strings.TrimSpace(string(m.text))
	if text == "" {
		return nil
	}
	m.text, m.cur = nil, 0
	m.unpin()
	return m.post("/v1/desk/messages", map[string]string{"text": text},
		func(m *Model, body []byte) {
			var e Entry
			if json.Unmarshal(body, &e) == nil && e.ID != "" {
				m.snap.Conversation = upsert(m.snap.Conversation, e, func(x Entry) string { return x.ID })
			}
		},
		func(m *Model, _ error) {
			if len(m.text) == 0 {
				m.text, m.cur = []rune(text), len([]rune(text))
			}
		})
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.cancel()
	return m, tea.Quit
}

func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.help {
		m.help = false
		if k == "ctrl+c" {
			return m.quit()
		}
		return m, nil
	}
	if k != "z" && k != "ctrl+z" {
		m.status = ""
	}
	switch k {
	case "ctrl+c":
		return m.quit()
	case "tab", "ctrl+right":
		return m, m.setFocus((m.focus + 1) % 3)
	case "shift+tab", "ctrl+left":
		return m, m.setFocus((m.focus + 2) % 3)
	case "ctrl+z":
		m.undo()
		return m, nil
	}
	if m.composing() {
		return m, m.composerKey(msg)
	}
	switch k {
	case "q":
		return m.quit()
	case "?":
		m.help = true
		return m, nil
	case "z":
		m.undo()
		return m, nil
	}
	switch m.focus {
	case colSessions:
		return m, m.sessionsKey(k)
	case colBoard:
		return m, m.boardKeys(k)
	}
	return m, m.readOnlyKey(k)
}

func (m *Model) setFocus(c column) tea.Cmd {
	m.focus = c
	if c == colBoard {
		m.boardMoved()
	}
	return nil
}

func (m *Model) composerKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "enter":
		return m.submit()
	case "alt+enter", "shift+enter", "ctrl+j":
		m.insert("\n")
	case "backspace":
		if m.cur > 0 {
			m.text = slices.Delete(m.text, m.cur-1, m.cur)
			m.cur--
		}
	case "delete", "ctrl+d":
		if m.cur < len(m.text) {
			m.text = slices.Delete(m.text, m.cur, m.cur+1)
		}
	case "left", "ctrl+b":
		m.cur = max(0, m.cur-1)
	case "right", "ctrl+f":
		m.cur = min(len(m.text), m.cur+1)
	case "home", "ctrl+a":
		m.cur = 0
	case "end", "ctrl+e":
		m.cur = len(m.text)
	case "ctrl+u":
		m.text, m.cur = nil, 0
	case "up", "pgup":
		m.scrollBy(m.step(k))
	case "down", "pgdown":
		m.scrollBy(-m.step(k))
	case "esc":
		if m.selQ != "" {
			m.selQ = ""
		}
	case "z":
		if len(m.text) == 0 && len(m.pend) > 0 {
			m.undo()
			return nil
		}
		m.insert(msg.Text)
	default:
		if n, ok := pickKey(k); ok && m.selQ != "" {
			if q, open := m.question(); open {
				m.answer(q, n, "")
			} else {
				m.say(m.selQ + " is closed; esc goes back to Prism")
			}
			return nil
		}
		if msg.Text != "" {
			m.insert(msg.Text)
		}
	}
	return nil
}

// pickKey reads alt+N or ctrl+N, which pick option N from the composer.
func pickKey(k string) (int, bool) {
	for _, p := range []string{"alt+", "ctrl+"} {
		if d, ok := strings.CutPrefix(k, p); ok && len(d) == 1 && d[0] >= '1' && d[0] <= '9' {
			return int(d[0] - '1'), true
		}
	}
	return 0, false
}

func (m *Model) step(k string) int {
	if strings.HasPrefix(k, "pg") {
		return max(1, m.h/2)
	}
	return 1
}

// submit is enter in the box: with a question selected and nothing typed it takes the recommendation; typed text
// is the answer in words after r, otherwise a message to the desk.
func (m *Model) submit() tea.Cmd {
	if m.selQ == "" || (!m.words && strings.TrimSpace(string(m.text)) != "") {
		return m.sendMessage()
	}
	q, open := m.question()
	if !open {
		m.say(m.selQ + " is closed; esc goes back to Prism")
		return nil
	}
	text := strings.TrimSpace(string(m.text))
	if text == "" {
		m.answer(q, q.Recommended, "")
		return nil
	}
	m.text, m.cur = nil, 0
	m.answer(q, -1, text)
	return nil
}

func (m *Model) readOnlyKey(k string) tea.Cmd {
	switch k {
	case "esc":
		m.view, m.leftKey = "", "desk"
	case "up", "k", "pgup":
		m.scrollBy(m.step(k))
	case "down", "j", "pgdown":
		m.scrollBy(-m.step(k))
	case "t", "g", "o":
		return m.rowAction(k, m.viewSession(), m.viewTrack(), nil)
	}
	return nil
}

func (m *Model) viewSession() *Session {
	if id, ok := strings.CutPrefix(m.view, "s:"); ok {
		if s, ok := m.snap.session(id); ok {
			return &s
		}
	}
	return nil
}

func (m *Model) viewTrack() *Track {
	id := ""
	if s := m.viewSession(); s != nil {
		id = s.Track
	} else if t, ok := strings.CutPrefix(m.view, "t:"); ok {
		id = t
	}
	if t, ok := m.snap.track(id); ok {
		return &t
	}
	return nil
}

func (m *Model) sessionsKey(k string) tea.Cmd {
	rows := m.leftRows()
	i := slices.IndexFunc(rows, func(r leftRow) bool { return r.key == m.leftKey })
	switch k {
	case "up", "k", "down", "j":
		d := 1
		if k == "up" || k == "k" {
			d = -1
		}
		for j := i + d; j >= 0 && j < len(rows); j += d {
			if rows[j].selectable() {
				return m.selectLeft(rows[j])
			}
		}
	case "enter", "space":
		if i < 0 {
			return nil
		}
		if rows[i].kind == rowParked {
			m.parkedOpen = !m.parkedOpen
			return nil
		}
		if k == "enter" {
			m.focus = colChat
		}
	case "esc":
		return m.selectLeft(rows[0])
	case "t", "g", "o":
		if i >= 0 {
			return m.rowAction(k, rows[i].session(), rows[i].track, nil)
		}
	}
	return nil
}

// selectLeft moves the cursor in the sessions column and shows the row in the middle.
func (m *Model) selectLeft(r leftRow) tea.Cmd {
	m.leftKey = r.key
	return m.show(r.view())
}

func (m *Model) show(view string) tea.Cmd {
	if view == m.view {
		return nil
	}
	m.view, m.scroll = view, 0
	id, ok := strings.CutPrefix(view, "s:")
	if !ok {
		return nil
	}
	cmds := []tea.Cmd{m.fetchTranscript(id)}
	if s, ok := m.snap.session(id); ok && s.Status == "done" && s.SeenAt == nil {
		cmds = append(cmds, m.post("/v1/sessions/"+id+"/seen", nil, nil, nil))
	}
	return tea.Batch(cmds...)
}

func (m *Model) rowAction(k string, s *Session, t *Track, q *Question) tea.Cmd {
	switch k {
	case "t":
		if s == nil || s.ID == m.snap.Desk.Session {
			m.say("t takes over a session; select one")
			return nil
		}
		name := s.Name
		return m.post("/v1/sessions/"+s.ID+"/takeover", nil, func(m *Model, body []byte) {
			var r struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(body, &r)
			m.note("You took over " + name + ". It no longer reports here.")
			if r.Command != "" {
				m.say("Rex is not there: run " + r.Command)
			} else {
				m.say("Opened " + name + " in Rex")
			}
		}, nil)
	case "g":
		if s == nil || s.ID == m.snap.Desk.Session {
			m.say("g gives a session back to Prism; select one")
			return nil
		}
		name := s.Name
		return m.post("/v1/sessions/"+s.ID+"/giveback", nil, func(m *Model, _ []byte) {
			m.note("You gave " + name + " back to Prism. Its next stop reports here.")
		}, nil)
	case "o":
		var links []string
		if q != nil {
			links = q.allLinks()
		}
		if len(links) == 0 && t != nil {
			links = t.Links
		}
		if len(links) == 0 {
			m.say("No link here")
			return nil
		}
		u, open := links[0], m.open
		return func() tea.Msg {
			if err := open(u); err != nil {
				return postedMsg{path: u, err: err}
			}
			return nil
		}
	}
	return nil
}

func (m *Model) boardKeys(k string) tea.Cmd {
	rows := m.boardRows()
	i := slices.IndexFunc(rows, func(r boardRow) bool { return r.key == m.boardKey })
	if i < 0 && len(rows) > 0 {
		i = 0
		m.boardKey = rows[0].key
	}
	if i < 0 {
		return nil
	}
	r := rows[i]
	switch k {
	case "up", "k":
		if i > 0 {
			m.boardKey = rows[i-1].key
			m.boardMoved()
		}
	case "down", "j":
		if i < len(rows)-1 {
			m.boardKey = rows[i+1].key
			m.boardMoved()
		}
	case "enter":
		switch {
		case r.q != nil:
			if m.pendingFor(r.q.ID) == nil {
				m.answer(*r.q, r.q.Recommended, "")
			}
		case r.kind == brLanded:
			m.landedOpen = !m.landedOpen
		case r.s != nil:
			m.leftKey = "s:" + r.s.ID
			if lr, ok := m.leftRowFor(r.s.ID); ok {
				m.leftKey = lr.key
			}
			m.focus = colChat
			return m.show("s:" + r.s.ID)
		case r.t != nil:
			m.leftKey = "t:" + r.t.ID
			m.focus = colChat
			return m.show("t:" + r.t.ID)
		}
	case "space":
		if r.kind == brLanded {
			m.landedOpen = !m.landedOpen
		}
	case "r":
		if r.q != nil {
			m.selectQuestion(r.q.ID)
			m.enterWords()
			m.focus = colChat
			m.view, m.leftKey = "", "desk"
		}
	case "t", "g", "o":
		var t *Track
		id := ""
		if r.q != nil {
			id = r.q.Track
		} else if r.s != nil {
			id = r.s.Track
		} else if r.t != nil {
			id = r.t.ID
		}
		if tr, ok := m.snap.track(id); ok {
			t = &tr
		}
		s := r.s
		if s == nil && r.q != nil {
			if qs, ok := m.snap.session(r.q.Session); ok {
				s = &qs
			}
		}
		return m.rowAction(k, s, t, r.q)
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && r.q != nil {
			m.answer(*r.q, int(k[0]-'1'), "")
		}
	}
	return nil
}

// boardMoved makes the question under the board's cursor the composer's, or none.
func (m *Model) boardMoved() {
	rows := m.boardRows()
	if len(rows) > 0 && !slices.ContainsFunc(rows, func(r boardRow) bool { return r.key == m.boardKey }) {
		m.boardKey = rows[0].key
	}
	for _, r := range rows {
		if r.key == m.boardKey {
			if r.q != nil {
				m.selectQuestion(r.q.ID)
			} else if m.pendingFor(m.selQ) == nil {
				m.selQ = ""
			}
			return
		}
	}
}
