package tracks

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
	"github.com/emilthaudal/prism/internal/store"
)

const (
	// conversationPage is how many entries of the desk's conversation a snapshot or a page holds.
	conversationPage = 200
	handledPage      = 100
	transcriptMax    = 300
	// serveRecent is how long a stopped session or a closed question stays in the snapshot.
	serveRecent = 24 * time.Hour
	// diffTTL is how long a session's diff stats are reused before git runs again.
	diffTTL   = time.Minute
	gitTimout = 10 * time.Second
	stopWait  = 30 * time.Second
)

type apiTrack struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Goal string `json:"goal"`
	Mode string `json:"mode"`
	// ShownMode is the mode the track is shown in: waiting is computed from check or
	// waiting_on, no open ask and no working session (EMI-423). Prism groups by it.
	ShownMode string `json:"shown_mode"`
	Tier      string `json:"tier"`
	Rank      int    `json:"rank"`
	Today     string `json:"today"`
	Where     string `json:"where"`
	WaitingOn string `json:"waiting_on"`
	// Check is the track's check: line and its last answer, null when it has none (EMI-431).
	Check     *apiCheck `json:"check"`
	Links     []string  `json:"links"`
	BlockedBy []string  `json:"blocked_by"`
	UpdatedAt time.Time `json:"updated_at"`
	// Milestones are the steps to the goal, in order (EMI-485), left out when the track has none.
	Milestones []apiMilestone `json:"milestones,omitempty"`
}

type apiMilestone struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

func apiMilestones(list []store.Milestone) []apiMilestone {
	var out []apiMilestone
	for _, m := range list {
		out = append(out, apiMilestone{Title: m.Title, Done: m.Done()})
	}
	return out
}

// apiCheck is a track's check: line as tracks check last answered it: answer is "yes" or "no",
// null with ran_at until it has an answer.
type apiCheck struct {
	Text   string     `json:"text"`
	Answer *string    `json:"answer"`
	RanAt  *time.Time `json:"ran_at"`
}

// trackCheck is a track's check text with the answer recorded for that exact text, if any.
func trackCheck(text string, rec store.Check, ok bool) *apiCheck {
	if text == "" {
		return nil
	}
	c := &apiCheck{Text: text}
	if ok && !rec.RanAt.IsZero() {
		a := "no"
		if rec.Answer {
			a = "yes"
		}
		c.Answer, c.RanAt = &a, apiTimePtr(rec.RanAt)
	}
	return c
}

type apiChanges struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Files   int    `json:"files"`
	Branch  string `json:"branch"`
	Merged  bool   `json:"merged"`
}

type apiSubagent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type apiSession struct {
	ID         string        `json:"id"`
	Short      string        `json:"short"`
	Track      *string       `json:"track"`
	Parent     *string       `json:"parent"`
	Owner      string        `json:"owner"`
	Host       string        `json:"host"`
	Name       string        `json:"name"`
	Status     string        `json:"status"`
	WaitingFor string        `json:"waiting_for"`
	Activity   string        `json:"activity"`
	Subagents  []apiSubagent `json:"subagents"`
	LastAnswer string        `json:"last_answer"`
	Changes    *apiChanges   `json:"changes"`
	SeenAt     *time.Time    `json:"seen_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	// Loop is the session's loop (EMI-466), left out when it runs none.
	Loop *apiLoop `json:"loop,omitempty"`
}

type apiQuestion struct {
	ID             string             `json:"id"`
	Track          *string            `json:"track"`
	Session        *string            `json:"session"`
	From           string             `json:"from"`
	Question       string             `json:"question"`
	Options        []store.QOption    `json:"options"`
	Recommended    *int               `json:"recommended"`
	Recommendation *string            `json:"recommendation"`
	Evidence       []store.Evidence   `json:"evidence"`
	Links          []string           `json:"links"`
	Attachments    []store.Attachment `json:"attachments"`
	CreatedAt      time.Time          `json:"created_at"`
	Answer         map[string]any     `json:"answer"`
	// Delivery is "queued" while Emil's answer to a track agent's question waits to be typed into
	// the agent, "delivered" once it was, null for everything else.
	Delivery *string `json:"delivery"`
}

type apiEntry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Role    string    `json:"role"`
	Text    string    `json:"text"`
	Steps   int       `json:"steps,omitempty"`
	Session string    `json:"session,omitempty"`
	Outcome string    `json:"outcome,omitempty"`
}

type apiHandled struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Track string    `json:"track"`
	Kind  string    `json:"kind"`
	Did   string    `json:"did"`
	// Undone is true once Emil took the entry back; undoable kinds are answered, started, closed.
	Undone bool `json:"undone"`
}

type apiDesk struct {
	Session      string        `json:"session"`
	Status       string        `json:"status"`
	QueuedEvents int           `json:"queued_events"`
	Usage        *deskui.Usage `json:"usage"`
}

type apiSnapshot struct {
	Desk         apiDesk       `json:"desk"`
	Tracks       []apiTrack    `json:"tracks"`
	Sessions     []apiSession  `json:"sessions"`
	Questions    []apiQuestion `json:"questions"`
	Conversation []apiEntry    `json:"conversation"`
	Handled      []apiHandled  `json:"handled"`
}

// apiTime is t in UTC to the second, or the zero time.
func apiTime(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func apiTimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := apiTime(t)
	return &v
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func entryOf(e store.Entry) apiEntry {
	return apiEntry{ID: "m" + strconv.FormatInt(e.ID, 10), At: apiTime(e.At), Role: e.Role, Text: e.Text, Steps: e.Steps, Session: e.Session, Outcome: e.Outcome}
}

func apiQuestionOf(q store.Question, trackID map[string]string) apiQuestion {
	a := apiQuestion{ID: decisionLabel(q.ID), Track: strPtr(trackID[q.Track]), Session: strPtr(q.SessionID), From: q.From, Question: q.Question,
		Options: nonNil(q.Options), Evidence: nonNil(q.Evidence), Links: nonNil(q.Links), Attachments: nonNil(q.Attachments), CreatedAt: apiTime(q.CreatedAt), Recommendation: strPtr(q.Recommendation)}
	if q.Recommended > 0 {
		r := q.Recommended - 1
		a.Recommended = &r
	}
	switch {
	case !q.AnsweredAt.IsZero():
		a.Answer = map[string]any{"option": nil, "text": nil, "by": q.AnsweredBy, "at": apiTime(q.AnsweredAt)}
		if q.AnswerOption > 0 {
			a.Answer["option"] = q.AnswerOption - 1
		} else {
			a.Answer["text"] = q.AnswerText
		}
	case !q.ClosedAt.IsZero():
		a.Answer = map[string]any{"note": q.AnswerText, "by": "", "at": apiTime(q.ClosedAt)}
	}
	if q.From == "agent" && !q.AnsweredAt.IsZero() && !isHookPrompt(q) {
		d := "delivered"
		if q.DeliveredAt.IsZero() {
			d = "queued"
		}
		a.Delivery = &d
	}
	return a
}

// liveSource serves tracks.db and the headless desk.
type liveSource struct {
	desk  *deskRunner
	diffs *diffCache
	stamp string
	dirty atomic.Bool
}

func newLiveSource(desk *deskRunner) *liveSource {
	l := &liveSource{desk: desk}
	l.diffs = &diffCache{m: map[string]diffEntry{}, changed: func() { l.dirty.Store(true) }}
	return l
}

func (l *liveSource) changed() bool {
	s := storeStamp() + usageStamp() + loopsStamp()
	if l.desk != nil {
		s += l.desk.stamp()
	}
	moved := s != l.stamp || l.dirty.Swap(false)
	l.stamp = s
	return moved
}

func (l *liveSource) snapshot(ctx context.Context) (any, error) {
	var (
		views   []store.View
		list    []store.Session
		agents  []store.Subagent
		qs      []store.Question
		conv    []store.Entry
		handled []store.DeskLogEntry
		deskID  string
		checks  []store.NamedCheck
		miles   map[int64][]store.Milestone
	)
	now := time.Now()
	err := withBareStore(func(st *store.Store) (err error) {
		if views, err = st.LiveTracks(); err != nil {
			return err
		}
		if checks, err = st.AllChecks(); err != nil {
			return err
		}
		if miles, err = st.AllMilestones(); err != nil {
			return err
		}
		if list, err = st.Sessions(); err != nil {
			return err
		}
		if agents, err = st.Subagents(); err != nil {
			return err
		}
		if qs, err = st.RecentQuestions(now.Add(-serveRecent)); err != nil {
			return err
		}
		if conv, err = st.Conversation(0, conversationPage); err != nil {
			return err
		}
		if handled, err = st.DeskLogRecent(handledPage); err != nil {
			return err
		}
		deskID, err = st.DeskClaude()
		return err
	})
	if err != nil {
		return nil, err
	}
	snap := apiSnapshot{Tracks: []apiTrack{}, Sessions: []apiSession{}, Questions: []apiQuestion{}, Conversation: []apiEntry{}, Handled: []apiHandled{}}
	trackID := map[string]string{}
	working := map[string]bool{}
	loops := snapshotLoops()
	for _, s := range list {
		if s.Track != "" && s.Status == "working" && s.Host != "stopped" {
			working[s.Track] = true
		}
	}
	checkOf := map[string]store.Check{}
	for _, c := range checks {
		checkOf[c.Track+": "+c.Text] = c.Check
	}
	for _, v := range views {
		t := v.Track
		id := strconv.FormatInt(t.ID, 10)
		rec, recOK := checkOf[t.Name+": "+t.Check]
		trackID[t.Name] = id
		shown := shownMode(t.Mode, idle(t.Check, t.WaitingOn, len(v.Asks), working[t.Name]))
		snap.Tracks = append(snap.Tracks, apiTrack{ID: id, Name: t.Name, Goal: t.Goal, Mode: t.Mode, ShownMode: shown, Tier: t.Tier, Rank: t.Rank, Today: t.Today,
			Where: t.Where, WaitingOn: t.WaitingOn, Check: trackCheck(t.Check, rec, recOK), Links: nonNil(splitLinks(t.Links)), BlockedBy: nonNil(t.BlockedBy), UpdatedAt: apiTime(t.UpdatedAt), Milestones: apiMilestones(miles[t.ID])})
	}
	for _, s := range list {
		if s.Host == "stopped" && now.Sub(s.UpdatedAt) > serveRecent {
			continue
		}
		// The desk shows as the desk, and a session of Emil's with no track (any Claude he runs
		// outside tracks) is nothing the desk or the board has to show.
		if s.ID == deskID || (s.Owner == "emil" && trackID[s.Track] == "") {
			continue
		}
		a := apiSession{ID: s.ID, Short: shortID(s.ID), Track: strPtr(trackID[s.Track]), Parent: strPtr(s.ParentID), Owner: s.Owner, Host: s.Host,
			Name: s.Name, Status: s.Status, WaitingFor: s.WaitingFor, Activity: s.Activity, Subagents: []apiSubagent{}, LastAnswer: s.LastAnswer, SeenAt: apiTimePtr(s.SeenAt), UpdatedAt: apiTime(s.UpdatedAt)}
		if s.Host == "stopped" {
			a.Status = "stopped"
		}
		if s.ParentID != "" {
			a.Loop = loops[s.Track]
		}
		if a.Name == "" {
			a.Name = s.Track
		}
		running := 0
		for _, x := range agents {
			if x.Session == s.ID {
				a.Subagents = append(a.Subagents, apiSubagent{Name: x.Name, Status: x.Status})
				if x.Status == "working" {
					running++
				}
			}
		}
		// A turn that ended while its subagents run is not the session's end: it waits on them
		// (EMI-426).
		if running > 0 && (a.Status == "done" || a.Status == "idle") {
			a.Status, a.Activity = "working", waitingOnAgents(running)
		}
		// A usage limit holds the session blocked so its notes wait; it asks nothing of Emil.
		if a.Status == "blocked" && store.UsageLimited(s.WaitingFor) {
			a.Status = "error"
		}
		if s.ID != deskID && s.Cwd != "" {
			a.Changes = l.diffs.get(s.Cwd)
		}
		snap.Sessions = append(snap.Sessions, a)
	}
	for _, q := range qs {
		snap.Questions = append(snap.Questions, apiQuestionOf(q, trackID))
	}
	for _, e := range conv {
		snap.Conversation = append(snap.Conversation, entryOf(e))
	}
	slices.Reverse(handled)
	for _, e := range handled {
		tr := e.Track
		if id, ok := trackID[tr]; ok {
			tr = id
		}
		snap.Handled = append(snap.Handled, apiHandled{ID: "e" + strconv.FormatInt(e.ID, 10), At: apiTime(e.At), Track: tr, Kind: e.Kind, Did: e.Did, Undone: !e.UndoneAt.IsZero()})
	}
	snap.Desk = apiDesk{Session: deskID, Status: "idle", Usage: readUsage()}
	if l.desk != nil {
		snap.Desk.Status, snap.Desk.QueuedEvents = l.desk.state()
	}
	return snap, nil
}

func splitLinks(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (l *liveSource) conversation(ctx context.Context, before string) (any, error) {
	var id int64
	if before != "" {
		n, err := strconv.ParseInt(strings.TrimPrefix(before, "m"), 10, 64)
		if err != nil {
			return nil, errorf(http.StatusBadRequest, "not a conversation id: %q", before)
		}
		id = n
	}
	var conv []store.Entry
	if err := withBareStore(func(st *store.Store) (err error) { conv, err = st.Conversation(id, conversationPage); return err }); err != nil {
		return nil, err
	}
	out := []apiEntry{}
	for _, e := range conv {
		out = append(out, entryOf(e))
	}
	return map[string]any{"conversation": out}, nil
}

func (l *liveSource) act(ctx context.Context, kind, id, action string, body json.RawMessage) (int, any, error) {
	var in struct {
		Text   string `json:"text"`
		Option *int   `json:"option"`
		Note   string `json:"note"`
		Mode   string `json:"mode"`
		Swap   string `json:"swap"`
		// DeskToken is the restarting desk's own secret: prism desk restart run by the desk.
		DeskToken string `json:"desk_token"`

		ShowOptions bool          `json:"show_options"`
		Answers     []answerInput `json:"answers"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			return 0, nil, errorf(http.StatusBadRequest, "bad body: %v", err)
		}
	}
	switch kind {
	case "desk":
		if action == "restart" {
			if l.desk == nil {
				return 0, nil, errorf(http.StatusConflict, "prism server runs no desk")
			}
			msg, err := l.desk.restart(in.DeskToken != "" && l.desk.isDesk(in.DeskToken))
			if err != nil {
				return 0, nil, err
			}
			return http.StatusAccepted, map[string]string{"status": msg}, nil
		}
		if strings.TrimSpace(in.Text) == "" {
			return 0, nil, errorf(http.StatusBadRequest, "the message is empty")
		}
		e, err := l.desk.post(in.Text)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusAccepted, entryOf(e), nil
	case "answers":
		return l.answers(in.Answers)
	case "questions":
		if action == "ask" {
			return l.ask(id, in.Text, in.ShowOptions)
		}
		return l.question(id, action, in.Option, in.Text, in.Note)
	case "sessions":
		return l.session(ctx, id, action)
	case "tracks":
		return l.track(id, action, in.Mode, in.Swap)
	case "handled":
		return l.undoHandled(id, action)
	}
	return 0, nil, errorf(http.StatusNotFound, "no such action")
}

func storeErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNoQuestion), errors.Is(err, store.ErrNoSession), errors.Is(err, store.ErrNoTrack):
		return errorf(http.StatusNotFound, "%s", err.Error())
	case errors.Is(err, store.ErrQuestionClosed):
		return errorf(http.StatusConflict, "%s", err.Error())
	case errors.Is(err, store.ErrBadOption), errors.Is(err, store.ErrEmptyAnswer):
		return errorf(http.StatusBadRequest, "%s", err.Error())
	}
	return err
}

// answerInput is one answer of POST /v1/answers; option counts from 0.
type answerInput struct {
	ID     string `json:"id"`
	Option *int   `json:"option"`
	Text   string `json:"text"`
}

func deliveryReply(status string, queued bool) map[string]string {
	delivery := "delivered"
	if queued {
		delivery = "queued"
	}
	return map[string]string{"status": status, "delivery": delivery}
}

// answers is POST /v1/answers: one asker's answers, recorded and delivered as one message.
func (l *liveSource) answers(in []answerInput) (int, any, error) {
	items := make([]batchAnswer, len(in))
	for i, a := range in {
		qid, err := questionID(a.ID)
		if err != nil {
			return 0, nil, errorf(http.StatusNotFound, "%s", err.Error())
		}
		items[i] = batchAnswer{ID: qid, Text: a.Text}
		if a.Option != nil {
			if *a.Option < 0 {
				return 0, nil, errorf(http.StatusBadRequest, "option %d: %v", *a.Option, store.ErrBadOption)
			}
			items[i].Option, items[i].Text = *a.Option+1, ""
		}
	}
	status, queued, err := answerBatch(items, "emil")
	if err != nil {
		return 0, nil, storeErr(err)
	}
	return http.StatusOK, deliveryReply(status, queued), nil
}

// ask is POST /v1/questions/{id}/ask: Emil asks the agent about its question without answering it.
func (l *liveSource) ask(id, text string, showOptions bool) (int, any, error) {
	qid, err := questionID(id)
	if err != nil {
		return 0, nil, errorf(http.StatusNotFound, "%s", err.Error())
	}
	var toDesk func(string) error
	if l.desk != nil {
		toDesk = func(s string) error { _, err := l.desk.post(s); return err }
	}
	status, queued, err := askQuestion(qid, text, showOptions, toDesk)
	if err != nil {
		return 0, nil, storeErr(err)
	}
	return http.StatusOK, deliveryReply(status, queued), nil
}

// question answers or closes a question as Emil. Options count from 0 here and from 1 in the store.
func (l *liveSource) question(id, action string, option *int, text, note string) (int, any, error) {
	qid, err := questionID(id)
	if err != nil {
		return 0, nil, errorf(http.StatusNotFound, "%s", err.Error())
	}
	switch action {
	case "answer":
		opt := 0
		if option != nil {
			if *option < 0 {
				return 0, nil, errorf(http.StatusBadRequest, "option %d: %v", *option, store.ErrBadOption)
			}
			opt, text = *option+1, ""
		}
		status, queued, err := answerQuestionQueued(qid, opt, text, "emil", "")
		if err != nil {
			return 0, nil, storeErr(err)
		}
		delivery := "delivered"
		if queued {
			delivery = "queued"
		}
		return http.StatusOK, map[string]string{"status": status, "delivery": delivery}, nil
	case "close":
		if err := withBareStore(func(st *store.Store) error { return st.CloseQuestion(qid, note) }); err != nil {
			return 0, nil, storeErr(err)
		}
	default:
		return 0, nil, errorf(http.StatusNotFound, "no action %s on a question", action)
	}
	return http.StatusOK, nil, nil
}

func (l *liveSource) findSession(id string) (store.Session, error) {
	var list []store.Session
	if err := withBareStore(func(st *store.Store) (err error) { list, err = st.Sessions(); return err }); err != nil {
		return store.Session{}, err
	}
	i := slices.IndexFunc(list, func(s store.Session) bool { return s.ID == id })
	if i < 0 {
		return store.Session{}, errorf(http.StatusNotFound, "no session %s", id)
	}
	return list[i], nil
}

func (l *liveSource) session(ctx context.Context, id, action string) (int, any, error) {
	s, err := l.findSession(id)
	if err != nil {
		return 0, nil, err
	}
	short := shortID(s.ID)
	label := sessionLabel(s.Track, s.Name, s.ID)
	switch action {
	case "takeover":
		var deskID string
		if err := withBareStore(func(st *store.Store) (err error) { deskID, err = st.DeskClaude(); return err }); err != nil {
			return 0, nil, err
		}
		if s.ID == deskID {
			return 0, nil, errorf(http.StatusConflict, "the desk's own session is not taken over: write to it in the conversation")
		}
		if err := withBareStore(func(st *store.Store) error { return st.SetOwner(s.ID, "emil") }); err != nil {
			return 0, nil, storeErr(err)
		}
		if err := attachInRex(ctx, label, s.Cwd, short); err != nil {
			return http.StatusOK, map[string]string{"command": "claude attach " + short}, nil
		}
	case "giveback":
		if err := withBareStore(func(st *store.Store) error { return st.SetOwner(s.ID, "desk") }); err != nil {
			return 0, nil, storeErr(err)
		}
		if s.Owner != "desk" && l.desk != nil {
			l.desk.event(label + " is yours again: Emil gave it back (" + short + ")")
		}
	case "seen":
		if err := withBareStore(func(st *store.Store) error { return st.MarkSeen(s.ID) }); err != nil {
			return 0, nil, storeErr(err)
		}
	case "stop":
		ctx, cancel := context.WithTimeout(ctx, stopWait)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "claude", "stop", short).CombinedOutput(); err != nil {
			return 0, nil, fmt.Errorf("claude stop %s: %s", short, oneLine(string(out)+" "+err.Error()))
		}
	default:
		return 0, nil, errorf(http.StatusNotFound, "no action %s on a session", action)
	}
	return http.StatusOK, nil, nil
}

// attachInRex opens claude attach SHORT in a new Rex session and focuses it; it fails when Rex
// is not running.
var attachInRex = func(ctx context.Context, label, cwd, short string) error {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	if strings.HasPrefix(label, "-") {
		label = short
	}
	args := []string{"new", label, "--json"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	b, err := rexRun(ctx, append(args, "--", claude, "attach", short)...)
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &created) == nil && created.ID != "" {
		_, _ = rexRun(ctx, "do", "session.select", "session_id="+created.ID)
	}
	return nil
}

func (l *liveSource) track(id, action, mode, swap string) (int, any, error) {
	var views []store.View
	if err := withBareStore(func(st *store.Store) (err error) { views, err = st.LiveTracks(); return err }); err != nil {
		return 0, nil, err
	}
	i := slices.IndexFunc(views, func(v store.View) bool { return strconv.FormatInt(v.Track.ID, 10) == id })
	if i < 0 {
		return 0, nil, errorf(http.StatusNotFound, "no open track %s", id)
	}
	name := views[i].Track.Name
	switch action {
	case "mode":
		if !slices.Contains(cardModes, mode) {
			return 0, nil, errorf(http.StatusBadRequest, "mode must be one of %s", strings.Join(cardModes, ", "))
		}
		// A third focus track answers 409 with the tracks in focus, older first, so Prism asks
		// which one moves to background and sends the answer back as swap (EMI-423).
		if err := setModeChecked(name, mode, swap); err != nil {
			var full *focusFullError
			if errors.As(err, &full) {
				return 0, nil, apiError{code: http.StatusConflict, msg: err.Error(), focus: full.focus}
			}
			return 0, nil, err
		}
	case "open":
		if err := open([]string{name}, io.Discard, palette{}); err != nil {
			return 0, nil, err
		}
	case "loop/stop":
		msg, err := requestLoopStop(name)
		if errors.Is(err, errNoLoop) {
			return 0, nil, errorf(http.StatusNotFound, "%s", err.Error())
		}
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]string{"status": msg}, nil
	default:
		return 0, nil, errorf(http.StatusNotFound, "no action %s on a track", action)
	}
	return http.StatusOK, nil, nil
}

// transcript reads the session's Claude JSONL: Emil's prompts, the assistant's text with its
// tool calls folded into steps, the newest transcriptMax entries.
func (l *liveSource) transcript(ctx context.Context, id string) (any, error) {
	if !claudeSessionID.MatchString(id) {
		return nil, errorf(http.StatusNotFound, "not a session id: %q", id)
	}
	paths, _ := filepath.Glob(filepath.Join(transcriptsDir(), "*", id+".jsonl"))
	entries := []apiTranscriptEntry{}
	if len(paths) == 0 {
		if s, err := l.findSession(id); err == nil && s.LastAnswer != "" {
			entries = append(entries, apiTranscriptEntry{Role: "assistant", Text: s.LastAnswer, At: apiTime(s.UpdatedAt)})
		}
		return map[string]any{"entries": entries}, nil
	}
	f, err := os.Open(paths[0])
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries = foldTranscript(f)
	if len(entries) > transcriptMax {
		entries = entries[len(entries)-transcriptMax:]
	}
	return map[string]any{"entries": entries}, nil
}

type apiTranscriptEntry struct {
	Role  string    `json:"role"`
	Text  string    `json:"text"`
	Steps int       `json:"steps,omitempty"`
	At    time.Time `json:"at"`
}

// foldTranscript turns transcript lines into entries: a user prompt is one entry, and the
// assistant's messages up to the next prompt are one, their tool calls counted as steps.
func foldTranscript(r io.Reader) []apiTranscriptEntry {
	var out []apiTranscriptEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var rec struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			IsMeta    bool      `json:"isMeta"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.IsMeta {
			continue
		}
		text, steps, prompt := contentOf(rec.Message.Content)
		switch rec.Type {
		case "user":
			if prompt && strings.TrimSpace(text) != "" {
				out = append(out, apiTranscriptEntry{Role: "user", Text: text, At: apiTime(rec.Timestamp)})
			}
		case "assistant":
			if text == "" && steps == 0 {
				continue
			}
			if n := len(out); n > 0 && out[n-1].Role == "assistant" {
				out[n-1].Text = joinText(out[n-1].Text, text)
				out[n-1].Steps += steps
				out[n-1].At = apiTime(rec.Timestamp)
				continue
			}
			out = append(out, apiTranscriptEntry{Role: "assistant", Text: text, Steps: steps, At: apiTime(rec.Timestamp)})
		}
	}
	return out
}

// contentOf reads a message's content: its text, its tool calls, and whether it is a prompt
// (text a person or a delivered message wrote) rather than tool results.
func contentOf(raw json.RawMessage) (text string, steps int, prompt bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, 0, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	prompt = len(blocks) > 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text = joinText(text, b.Text)
		case "tool_use":
			steps++
		case "tool_result":
			prompt = false
		}
	}
	return text, steps, prompt
}

func joinText(a, b string) string {
	switch {
	case strings.TrimSpace(b) == "":
		return a
	case a == "":
		return b
	}
	return a + "\n\n" + b
}

type diffEntry struct {
	at      time.Time
	changes *apiChanges
	running bool
}

// diffCache keeps each folder's diff stats against main for diffTTL, computing them off the
// request path: a snapshot shows what is cached and a later change brings the fresh stats.
type diffCache struct {
	mu      sync.Mutex
	m       map[string]diffEntry
	changed func()
}

func (d *diffCache) get(cwd string) *apiChanges {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.m[cwd]
	if !e.running && time.Since(e.at) > diffTTL {
		e.running = true
		d.m[cwd] = e
		go func() {
			c := gitChanges(cwd)
			d.mu.Lock()
			old := d.m[cwd].changes
			d.m[cwd] = diffEntry{at: time.Now(), changes: c}
			d.mu.Unlock()
			if fmt.Sprint(old) != fmt.Sprint(c) {
				d.changed()
			}
		}()
	}
	return e.changes
}

// gitOut runs git in a session's folder without anything the folder's config can run: no
// fsmonitor, no hooks, no external diff or textconv, and only PATH and HOME from serve's
// environment, so no secret of serve's reaches it.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", dir}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"}
	b, err := cmd.Output()
	return strings.TrimSpace(string(b)), err
}

// gitChanges is the committed work in dir against main (or master) since they parted; nil
// outside a repository or on the base branch itself. Comparing two commits never reads the
// working tree, so no clean filter of the folder's runs.
func gitChanges(dir string) *apiChanges {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimout)
	defer cancel()
	branch, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil
	}
	base := ""
	for _, b := range []string{"main", "master"} {
		if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "-q", b); err == nil {
			base = b
			break
		}
	}
	if base == "" || branch == base {
		return nil
	}
	fork, err := gitOut(ctx, dir, "merge-base", base, "HEAD")
	if err != nil {
		return nil
	}
	out, err := gitOut(ctx, dir, "diff", "--numstat", "--no-ext-diff", "--no-textconv", fork, "HEAD")
	if err != nil {
		return nil
	}
	c := &apiChanges{Branch: branch}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		c.Files++
		a, _ := strconv.Atoi(f[0])
		r, _ := strconv.Atoi(f[1])
		c.Added += a
		c.Removed += r
	}
	if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", "HEAD", base); err == nil && c.Files == 0 {
		c.Merged = true
	}
	return c
}

// undoHandled is u in the pane's handled log: it takes back the desk's entry id ("e501"), which
// opens again a track the desk closed, or disregards an answer, or ends a start. Only this route
// and the pane's keypress reach undoEntry, so only Emil's click reopens a track from the app.
func (l *liveSource) undoHandled(id, action string) (int, any, error) {
	if action != "undo" {
		return 0, nil, errorf(http.StatusNotFound, "no action %s on a handled entry", action)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "e"), 10, 64)
	if err != nil {
		return 0, nil, errorf(http.StatusNotFound, "no handled entry %s", id)
	}
	var e store.DeskLogEntry
	if err := withBareStore(func(st *store.Store) (err error) { e, err = st.DeskEntry(n); return err }); err != nil {
		if errors.Is(err, store.ErrNoEntry) {
			return 0, nil, errorf(http.StatusNotFound, "no handled entry %s", id)
		}
		return 0, nil, err
	}
	switch {
	case e.Kind != "answered" && e.Kind != "started" && e.Kind != "closed":
		return 0, nil, errorf(http.StatusBadRequest, "only the desk's answers, starts and closes can be taken back")
	case !e.UndoneAt.IsZero():
		return 0, nil, errorf(http.StatusConflict, "that was taken back already")
	}
	return http.StatusOK, map[string]any{"status": undoEntry(cleanDeskEntry(e), "")}, nil
}
