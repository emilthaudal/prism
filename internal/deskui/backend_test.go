package deskui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sse(ev Event) string {
	b, _ := json.Marshal(ev)
	return fmt.Sprintf("id: %d\ndata: %s\n\n", ev.Seq, b)
}

func ev(seq int64, op, kind string, v any) Event {
	b, _ := json.Marshal(v)
	return Event{Seq: seq, Op: op, Kind: kind, Value: b}
}

// fakeServe is prism server on the fixture's snapshot: each /v1/events stream sends the next
// script from streams and then closes.
type fakeServe struct {
	t         *testing.T
	snap      Snapshot
	streams   [][]Event
	snapshots atomic.Int32
	mu        sync.Mutex
	sinces    []string
	posts     []string
}

func (f *fakeServe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"no token"}`)
		return
	}
	switch {
	case r.URL.Path == "/v1/snapshot":
		f.snapshots.Add(1)
		_ = json.NewEncoder(w).Encode(f.snap)
	case r.URL.Path == "/v1/events":
		f.mu.Lock()
		f.sinces = append(f.sinces, r.URL.Query().Get("since"))
		var script []Event
		if len(f.streams) > 0 {
			script, f.streams = f.streams[0], f.streams[1:]
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		for _, e := range script {
			fmt.Fprint(w, sse(e))
		}
		w.(http.Flusher).Flush()
		if script == nil {
			<-r.Context().Done()
		}
	case r.Method == http.MethodPost:
		f.mu.Lock()
		f.posts = append(f.posts, r.URL.Path)
		f.mu.Unlock()
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"m99","role":"emil","text":"hi"}`)
	default:
		w.WriteHeader(404)
	}
}

func startServe(t *testing.T, streams ...[]Event) (*fakeServe, *HTTP) {
	t.Helper()
	fx, err := LoadFixture(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeServe{t: t, snap: fx.snap, streams: streams}
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("TRACKS_HOME", home)
	info, _ := json.Marshal(map[string]any{"url": srv.URL, "token": "tok", "pid": 1, "version": 1})
	if err := os.WriteFile(filepath.Join(home, "serve.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP()
	if err != nil {
		t.Fatal(err)
	}
	return fs, h
}

// collect runs follow until it has sent n messages.
func collect(t *testing.T, b Backend, n int) []any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []any
	done := make(chan struct{})
	go follow(ctx, b, func(v any) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, v)
		if len(got) == n {
			close(done)
		}
	})
	select {
	case <-done:
	case <-ctx.Done():
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("follow sent %d of %d messages: %T", len(got), n, got)
	}
	cancel()
	mu.Lock()
	defer mu.Unlock()
	return append([]any(nil), got[:n]...)
}

func TestSSEOpsAppliedInOrder(t *testing.T) {
	d16 := Question{ID: "D16", Track: "121", Question: "Open the PR?", Options: []Option{{Label: "Yes"}}, CreatedAt: fixtureNow}
	stream := []Event{
		ev(1043, "upsert", "question", d16),
		ev(1044, "upsert", "conversation", Entry{ID: "m90", Role: "desk", Text: "first", At: fixtureNow}),
		ev(1045, "upsert", "conversation", Entry{ID: "m90", Role: "desk", Text: "first, edited", At: fixtureNow}),
		ev(1046, "remove", "question", map[string]string{"id": "D16"}),
		ev(1047, "remove", "question", map[string]string{"id": "D15"}),
		ev(1048, "upsert", "desk", Desk{Session: "c45dc3eb-5f0e-4d6a-9a8e-1b2c3d4e5f60", Status: "working"}),
	}
	fs, h := startServe(t, stream)
	msgs := collect(t, h, 1+len(stream))

	tm := newTest(t, 200, 50)
	for _, msg := range msgs {
		tm.send(msg)
	}
	if tm.snap.Seq != 1048 {
		t.Fatalf("seq %d, want 1048", tm.snap.Seq)
	}
	if _, ok := tm.snap.question("D16"); ok {
		t.Error("D16 was upserted then removed, but is still there")
	}
	if _, ok := tm.snap.question("D15"); ok {
		t.Error("D15 was removed, but is still there")
	}
	last := tm.snap.Conversation[len(tm.snap.Conversation)-1]
	if last.ID != "m90" || last.Text != "first, edited" || len(tm.snap.Conversation) != 7 {
		t.Errorf("conversation ends %+v (len %d), want m90 edited once", last, len(tm.snap.Conversation))
	}
	if tm.snap.Desk.Status != "working" {
		t.Errorf("desk status %q", tm.snap.Desk.Status)
	}
	s := tm.screen()
	if !strings.Contains(s, "first, edited") || !strings.Contains(s, "1 need you") || !strings.Contains(s, "◐ working") {
		t.Errorf("screen does not show the applied changes\n%s", s)
	}
	if fs.sinces[0] != "1042" {
		t.Errorf("events asked since %q, want the snapshot's seq", fs.sinces[0])
	}
}

func TestSSEGapRefetchesSnapshot(t *testing.T) {
	fs, h := startServe(t,
		[]Event{ev(1043, "upsert", "desk", Desk{Status: "working"}), ev(1045, "upsert", "desk", Desk{Status: "idle"})},
	)
	msgs := collect(t, h, 4) // snapshot, 1043, the gap, a fresh snapshot
	if _, ok := msgs[0].(snapshotMsg); !ok {
		t.Fatalf("first message %#v", msgs[0])
	}
	if e, ok := msgs[1].(eventMsg); !ok || e.ev.Seq != 1043 {
		t.Fatalf("second message %#v, want event 1043", msgs[1])
	}
	if c, ok := msgs[2].(connMsg); !ok || c.note == "" {
		t.Fatalf("third message %#v, want the gap note", msgs[2])
	}
	if _, ok := msgs[3].(snapshotMsg); !ok {
		t.Fatalf("after a gap %#v, want a fresh snapshot", msgs[3])
	}
	if n := fs.snapshots.Load(); n < 2 {
		t.Fatalf("snapshot fetched %d times, want a refetch", n)
	}
}

func TestSSEReconnectsAfterDrop(t *testing.T) {
	fs, h := startServe(t,
		[]Event{ev(1043, "upsert", "desk", Desk{Status: "working"})}, // then the server closes the stream
	)
	msgs := collect(t, h, 4)
	if c, ok := msgs[2].(connMsg); !ok || c.err == nil {
		t.Fatalf("after the drop %#v, want a connection error", msgs[2])
	}
	if _, ok := msgs[3].(snapshotMsg); !ok {
		t.Fatalf("after the drop %#v, want a fresh snapshot", msgs[3])
	}
	tm := newTest(t, 200, 50)
	tm.b = h
	tm.send(msgs[2])
	if !strings.Contains(tm.screen(), "○ reconnecting") {
		t.Error("a dropped stream does not show reconnecting")
	}
	tm.send(msgs[3])
	if !strings.Contains(tm.screen(), "● Prism server") {
		t.Error("a fresh snapshot does not show live")
	}
	_ = fs
}

func TestHTTPActionsCarryToken(t *testing.T) {
	fs, h := startServe(t)
	tm := newTest(t, 200, 50)
	tm.b = h
	tm.typeText("hi")
	tm.press("enter")
	if len(fs.posts) != 1 || fs.posts[0] != "/v1/desk/messages" {
		t.Fatalf("posts %v", fs.posts)
	}
	if !strings.Contains(tm.screen(), "hi") {
		t.Error("the sent message does not show")
	}
	bad := &HTTP{URL: h.URL, Token: "wrong", Client: http.DefaultClient}
	if _, err := bad.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "401 no token") {
		t.Fatalf("bad token: %v", err)
	}
}

func TestReadSSE(t *testing.T) {
	in := ": hello\n\nid: 1\nevent: change\ndata: {\"seq\":1,\ndata: \"op\":\"upsert\",\"kind\":\"desk\",\"value\":{}}\n\n"
	var got []Event
	err := readSSE(strings.NewReader(in), func(e Event) bool { got = append(got, e); return true })
	if len(got) != 1 || got[0].Seq != 1 || got[0].Kind != "desk" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
