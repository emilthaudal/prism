// Package deskui is the three-column desk window (prism tui): a client of the prism server API
// in docs/serve-api.md, or of a fixture file with the same snapshot.
package deskui

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type Snapshot struct {
	Seq          int64      `json:"seq"`
	GeneratedAt  time.Time  `json:"generated_at"`
	Desk         Desk       `json:"desk"`
	Tracks       []Track    `json:"tracks"`
	Sessions     []Session  `json:"sessions"`
	Questions    []Question `json:"questions"`
	Conversation []Entry    `json:"conversation"`
	Handled      []Handled  `json:"handled"`
}

type Desk struct {
	Session      string `json:"session"`
	Status       string `json:"status"`
	QueuedEvents int    `json:"queued_events"`
	Usage        *Usage `json:"usage"`
}

// Usage is Emil's Claude usage limits as his status line last saw them; null until it has written any.
type Usage struct {
	FiveHour  *UsageLimit `json:"five_hour"`
	SevenDay  *UsageLimit `json:"seven_day"`
	UpdatedAt time.Time   `json:"updated_at"`
}

type UsageLimit struct {
	UsedPercentage float64    `json:"used_percentage"`
	ResetsAt       *time.Time `json:"resets_at"`
}

type Track struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Goal      string    `json:"goal"`
	Mode      string    `json:"mode"`
	Tier      string    `json:"tier"`
	Rank      int       `json:"rank"`
	Today     string    `json:"today"`
	Where     string    `json:"where"`
	WaitingOn string    `json:"waiting_on"`
	Links     []string  `json:"links"`
	BlockedBy []string  `json:"blocked_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Changes struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Files   int    `json:"files"`
	Branch  string `json:"branch"`
	Merged  bool   `json:"merged"`
}

type Session struct {
	ID         string     `json:"id"`
	Short      string     `json:"short"`
	Track      string     `json:"track"`
	Parent     string     `json:"parent"`
	Owner      string     `json:"owner"`
	Host       string     `json:"host"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	WaitingFor string     `json:"waiting_for"`
	Activity   string     `json:"activity"`
	LastAnswer string     `json:"last_answer"`
	Changes    *Changes   `json:"changes"`
	SeenAt     *time.Time `json:"seen_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type Option struct {
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Attachment  *Attachment `json:"attachment"`
}

// Attachment is a picture, page or file a question points at; Kind is image, page or file.
type Attachment struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

type Evidence struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

type Question struct {
	ID             string          `json:"id"`
	Track          string          `json:"track"`
	Session        string          `json:"session"`
	From           string          `json:"from"`
	Question       string          `json:"question"`
	Options        []Option        `json:"options"`
	Recommended    int             `json:"recommended"`
	Recommendation string          `json:"recommendation"`
	Evidence       []Evidence      `json:"evidence"`
	Links          []string        `json:"links"`
	Attachments    []Attachment    `json:"attachments"`
	CreatedAt      time.Time       `json:"created_at"`
	Answer         json.RawMessage `json:"answer"`
	Delivery       string          `json:"delivery"`
}

// allLinks are the question's links, then its attachments' URLs, the question's own first, then each option's.
func (q Question) allLinks() []string {
	out := slices.Clone(q.Links)
	for _, a := range q.Attachments {
		out = append(out, a.URL)
	}
	for _, o := range q.Options {
		if o.Attachment != nil {
			out = append(out, o.Attachment.URL)
		}
	}
	return out
}

// attachmentLinks are the attachments' URLs, the question's own first, then each option's.
func (q Question) attachmentLinks() []string {
	return q.allLinks()[len(q.Links):]
}

func (a Attachment) glyph() string {
	switch a.Kind {
	case "image":
		return "▣"
	case "page":
		return "◎"
	}
	return "▫"
}

// Queued is whether Emil's answer is recorded and waits to be typed into the track's agent.
func (q Question) Queued() bool { return q.Delivery == "queued" && !q.Open() }

// AnswerLabel is the answer in words: the option's label, or the text given.
func (q Question) AnswerLabel() string {
	var a struct {
		Option *int    `json:"option"`
		Text   *string `json:"text"`
	}
	if json.Unmarshal(q.Answer, &a) != nil {
		return ""
	}
	switch {
	case a.Option != nil && *a.Option >= 0 && *a.Option < len(q.Options):
		return q.Options[*a.Option].Label
	case a.Text != nil:
		return *a.Text
	}
	return ""
}

func (q Question) Open() bool { return len(q.Answer) == 0 || string(q.Answer) == "null" }

// Entry is one line of the desk's conversation, or of a session's transcript.
type Entry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Role    string    `json:"role"`
	Text    string    `json:"text"`
	Steps   int       `json:"steps"`
	Session string    `json:"session"`
	Outcome string    `json:"outcome"`
}

type Handled struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Track string    `json:"track"`
	Kind  string    `json:"kind"`
	Did   string    `json:"did"`
}

// Event is one change on /v1/events.
type Event struct {
	Seq   int64           `json:"seq"`
	Op    string          `json:"op"`
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

func upsert[T any](list []T, v T, id func(T) string) []T {
	if i := slices.IndexFunc(list, func(x T) bool { return id(x) == id(v) }); i >= 0 {
		list[i] = v
		return list
	}
	return append(list, v)
}

func remove[T any](list []T, gone string, id func(T) string) []T {
	return slices.DeleteFunc(list, func(x T) bool { return id(x) == gone })
}

func change[T any](list []T, ev Event, id func(T) string) ([]T, error) {
	if ev.Op == "remove" {
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(ev.Value, &r); err != nil {
			return list, err
		}
		return remove(list, r.ID, id), nil
	}
	var v T
	if err := json.Unmarshal(ev.Value, &v); err != nil {
		return list, err
	}
	return upsert(list, v, id), nil
}

// Apply applies one change to the snapshot and moves its seq on.
func (s *Snapshot) Apply(ev Event) error {
	if ev.Op != "upsert" && ev.Op != "remove" {
		return fmt.Errorf("event %d: unknown op %q", ev.Seq, ev.Op)
	}
	var err error
	switch ev.Kind {
	case "track":
		s.Tracks, err = change(s.Tracks, ev, func(t Track) string { return t.ID })
	case "session":
		s.Sessions, err = change(s.Sessions, ev, func(x Session) string { return x.ID })
	case "question":
		s.Questions, err = change(s.Questions, ev, func(q Question) string { return q.ID })
	case "conversation":
		s.Conversation, err = change(s.Conversation, ev, func(e Entry) string { return e.ID })
	case "handled":
		s.Handled, err = change(s.Handled, ev, func(h Handled) string { return h.ID })
	case "desk":
		if ev.Op == "upsert" {
			err = json.Unmarshal(ev.Value, &s.Desk)
		}
	default:
		err = fmt.Errorf("unknown kind %q", ev.Kind)
	}
	if err != nil {
		return fmt.Errorf("event %d: %w", ev.Seq, err)
	}
	s.Seq = ev.Seq
	return nil
}

func (s *Snapshot) track(id string) (Track, bool) {
	i := slices.IndexFunc(s.Tracks, func(t Track) bool { return t.ID == id })
	if i < 0 {
		return Track{}, false
	}
	return s.Tracks[i], true
}

func (s *Snapshot) session(id string) (Session, bool) {
	i := slices.IndexFunc(s.Sessions, func(x Session) bool { return x.ID == id })
	if i < 0 {
		return Session{}, false
	}
	return s.Sessions[i], true
}

func (s *Snapshot) question(id string) (Question, bool) {
	i := slices.IndexFunc(s.Questions, func(q Question) bool { return q.ID == id })
	if i < 0 {
		return Question{}, false
	}
	return s.Questions[i], true
}

// clone copies the slices, so a snapshot handed to the model never shares them with a backend.
func (s Snapshot) clone() Snapshot {
	s.Tracks = slices.Clone(s.Tracks)
	s.Sessions = slices.Clone(s.Sessions)
	s.Questions = slices.Clone(s.Questions)
	s.Conversation = slices.Clone(s.Conversation)
	s.Handled = slices.Clone(s.Handled)
	return s
}
