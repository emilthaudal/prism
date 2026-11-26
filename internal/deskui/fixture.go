package deskui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fixture serves a snapshot file from memory and applies actions to it, as prism server
// --mock does: each action becomes changes on the event stream.
type Fixture struct {
	path        string
	mu          sync.Mutex
	snap        Snapshot
	transcripts map[string][]Entry
	log         []Event
	wake        chan struct{} // closed and replaced when the log grows
	now         func() time.Time
	posts       []string // every action path, in order; tests read it
}

// LoadFixture reads a snapshot file, and serve-transcripts.json beside it when there is one.
func LoadFixture(path string) (*Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &Fixture{path: path, wake: make(chan struct{}), now: time.Now, transcripts: map[string][]Entry{}}
	if err := json.Unmarshal(b, &f.snap); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if tb, err := os.ReadFile(filepath.Join(filepath.Dir(path), "serve-transcripts.json")); err == nil {
		var ts map[string]struct {
			Entries []Entry `json:"entries"`
		}
		if err := json.Unmarshal(tb, &ts); err != nil {
			return nil, fmt.Errorf("serve-transcripts.json: %w", err)
		}
		for id, t := range ts {
			f.transcripts[id] = t.Entries
		}
	}
	return f, nil
}

func (f *Fixture) Label() string { return "fixture · " + filepath.Base(f.path) }

func (f *Fixture) Snapshot(context.Context) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap.clone(), nil
}

func (f *Fixture) Transcript(_ context.Context, id string) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.transcripts[id]; ok {
		return t, nil
	}
	if s, ok := f.snap.session(id); ok && s.LastAnswer != "" {
		return []Entry{{Role: "assistant", Text: s.LastAnswer, At: s.UpdatedAt}}, nil
	}
	return nil, nil
}

func (f *Fixture) Events(ctx context.Context, since int64, fn func(Event) bool) error {
	for {
		f.mu.Lock()
		var todo []Event
		for _, ev := range f.log {
			if ev.Seq > since {
				todo = append(todo, ev)
			}
		}
		wake := f.wake
		f.mu.Unlock()
		for _, ev := range todo {
			if !fn(ev) {
				return errStopped
			}
			since = ev.Seq
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

// Emit applies a change as the server would and puts it on the stream.
func (f *Fixture) Emit(kind, op string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.emitLocked(kind, op, value)
}

func (f *Fixture) emitLocked(kind, op string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ev := Event{Seq: f.snap.Seq + 1, Op: op, Kind: kind, Value: b}
	if err := f.snap.Apply(ev); err != nil {
		return err
	}
	f.log = append(f.log, ev)
	close(f.wake)
	f.wake = make(chan struct{})
	return nil
}

// Posts lists the actions received, oldest first.
func (f *Fixture) Posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

type fixtureErr struct {
	code int
	msg  string
}

func (e fixtureErr) Error() string { return strconv.Itoa(e.code) + " " + e.msg }

// Status is the HTTP status the server answers with.
func (e fixtureErr) Status() int { return e.code }

func (f *Fixture) Post(_ context.Context, path string, body any) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, path)
	raw, _ := json.Marshal(body)
	var in struct {
		Text   string `json:"text"`
		Option *int   `json:"option"`
		Note   string `json:"note"`
		Mode   string `json:"mode"`

		ShowOptions bool `json:"show_options"`
		Answers     []struct {
			ID     string `json:"id"`
			Option *int   `json:"option"`
			Text   string `json:"text"`
		} `json:"answers"`
	}
	_ = json.Unmarshal(raw, &in)
	now := f.now().UTC()
	parts := strings.Split(strings.TrimPrefix(path, "/v1/"), "/")
	switch {
	case len(parts) == 2 && parts[0] == "desk" && parts[1] == "messages":
		e := Entry{ID: "m" + strconv.FormatInt(f.snap.Seq+1, 10) + "e", At: now, Role: "emil", Text: in.Text}
		if err := f.emitLocked("conversation", "upsert", e); err != nil {
			return nil, err
		}
		return json.Marshal(e)
	case len(parts) == 1 && parts[0] == "answers":
		if len(in.Answers) == 0 {
			return nil, fixtureErr{400, "no answers"}
		}
		var qs []Question
		for _, a := range in.Answers {
			q, ok := f.snap.question(a.ID)
			switch {
			case !ok:
				return nil, fixtureErr{404, "no question " + a.ID}
			case !q.Open():
				return nil, fixtureErr{409, q.ID + " is already answered"}
			case len(qs) > 0 && (q.Track != qs[0].Track || q.Session != qs[0].Session):
				return nil, fixtureErr{400, "a batch is one asker's"}
			}
			qs = append(qs, q)
		}
		queued := false
		ids := make([]string, len(qs))
		for i, q := range qs {
			ids[i] = q.ID
			ans := map[string]any{"by": "emil", "at": now}
			if a := in.Answers[i]; a.Option != nil {
				ans["option"] = *a.Option
			} else {
				ans["text"] = a.Text
			}
			q.Answer, _ = json.Marshal(ans)
			if s, ok := f.snap.session(q.Session); ok && q.From == "agent" && s.Status == "working" {
				q.Delivery, queued = "queued", true
			}
			if err := f.emitLocked("question", "upsert", q); err != nil {
				return nil, err
			}
		}
		delivery := "delivered"
		if queued {
			delivery = "queued"
		}
		return json.Marshal(map[string]string{"status": "answered " + strings.Join(ids, ", "), "delivery": delivery})
	case len(parts) == 3 && parts[0] == "questions":
		q, ok := f.snap.question(parts[1])
		if !ok {
			return nil, fixtureErr{404, "no question " + parts[1]}
		}
		if !q.Open() {
			return nil, fixtureErr{409, q.ID + " is already answered"}
		}
		if parts[2] == "ask" {
			if strings.TrimSpace(in.Text) == "" && !in.ShowOptions {
				return nil, fixtureErr{400, "the question is empty"}
			}
			delivery := "delivered"
			if s, ok := f.snap.session(q.Session); ok && s.Status == "working" {
				delivery = "queued"
			}
			return json.Marshal(map[string]string{"status": "asked about " + q.ID, "delivery": delivery})
		}
		ans := map[string]any{"by": "emil", "at": now}
		switch parts[2] {
		case "answer":
			if in.Option != nil {
				ans["option"] = *in.Option
			} else {
				ans["text"] = in.Text
			}
		case "close":
			ans["note"] = in.Note
		default:
			return nil, fixtureErr{404, path}
		}
		q.Answer, _ = json.Marshal(ans)
		resp := []byte("{}")
		if s, ok := f.snap.session(q.Session); ok && parts[2] == "answer" && q.From == "agent" && s.Status == "working" {
			q.Delivery = "queued"
			resp = []byte(`{"status":"queued ` + q.ID + `","delivery":"queued"}`)
		}
		return resp, f.emitLocked("question", "upsert", q)
	case len(parts) == 3 && parts[0] == "sessions":
		s, ok := f.snap.session(parts[1])
		if !ok {
			return nil, fixtureErr{404, "no session " + parts[1]}
		}
		resp := []byte("{}")
		switch parts[2] {
		case "takeover":
			s.Owner = "emil"
			resp, _ = json.Marshal(map[string]string{"command": "claude attach " + s.Short})
		case "giveback":
			s.Owner = "desk"
		case "seen":
			s.SeenAt = &now
		case "stop":
			s.Status, s.Host = "stopped", "stopped"
		default:
			return nil, fixtureErr{404, path}
		}
		return resp, f.emitLocked("session", "upsert", s)
	case len(parts) == 3 && parts[0] == "tracks":
		t, ok := f.snap.track(parts[1])
		if !ok {
			return nil, fixtureErr{404, "no track " + parts[1]}
		}
		switch parts[2] {
		case "mode":
			t.Mode = in.Mode
			return []byte("{}"), f.emitLocked("track", "upsert", t)
		case "open":
			return []byte("{}"), nil
		}
	}
	return nil, fixtureErr{404, path}
}
