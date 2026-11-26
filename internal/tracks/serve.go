package tracks

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	serveVersion = 1
	// servePoll is how often serve looks for changes in tracks.db and the desk.
	servePoll = 500 * time.Millisecond
	// serveKeepAlive is how often an idle event stream gets a comment, so clients see it is alive.
	serveKeepAlive = 15 * time.Second
	// serveLogMax is how many changes serve keeps for clients that reconnect with ?since.
	serveLogMax  = 2000
	serveBodyMax = 1 << 20
	// deskCheckPath is where a command proves it is the headless desk's; it takes the desk's
	// secret instead of serve.json's token.
	deskCheckPath    = "/internal/desk"
	deskCheckTimeout = 3 * time.Second
)

const serveUsage = "usage: prism server [--force | --mock FILE | --detach]"

// serveKinds are the snapshot's lists, in the order changes to them go out.
var serveKinds = []struct{ kind, key string }{
	{"track", "tracks"}, {"session", "sessions"}, {"question", "questions"}, {"conversation", "conversation"}, {"handled", "handled"},
}

// appendOnly are the kinds whose snapshot is a window of the newest entries: an entry leaving
// the window is not a remove.
var appendOnly = map[string]bool{"conversation": true, "handled": true}

func serveInfoPath() string { return filepath.Join(home(), "serve.json") }
func serveLockPath() string { return filepath.Join(home(), "serve.lock") }

type serveInfo struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version int    `json:"version"`
	// Build is the binary serve started from (buildLabel); tracks desk prints it beside the installed
	// one. A restart on a new build keeps the pid and the start time, so only this tells them apart.
	Build string `json:"build,omitempty"`
}

// apiError is an error with the HTTP status it answers.
type apiError struct {
	code  int
	msg   string
	focus []string // with a refusal to fill focus: the tracks in it, older first
}

func (e apiError) Error() string { return e.msg }

func errorf(code int, format string, a ...any) error {
	return apiError{code: code, msg: fmt.Sprintf(format, a...)}
}

// serveSource is what serve serves: tracks.db with the headless desk, or a fixture.
type serveSource interface {
	// snapshot is the state as the snapshot's JSON, without seq and generated_at.
	snapshot(ctx context.Context) (any, error)
	// changed is a cheap check whether snapshot may differ from the last call.
	changed() bool
	transcript(ctx context.Context, session string) (any, error)
	conversation(ctx context.Context, before string) (any, error)
	// act runs an action: kind is desk, questions, sessions or tracks; id is "" for the desk.
	act(ctx context.Context, kind, id, action string, body json.RawMessage) (status int, reply any, err error)
}

type apiEvent struct {
	Seq   int64           `json:"seq"`
	Op    string          `json:"op"`
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

// rawSnap is a snapshot taken apart by kind and id, for diffing.
type rawSnap struct {
	desk  json.RawMessage
	lists map[string][]json.RawMessage
}

// hub turns snapshots into a numbered stream of changes: each refresh diffs the source's
// snapshot against the last one and logs an upsert or remove per entry that changed.
type hub struct {
	src serveSource
	// isDesk checks a headless desk's secret; nil when serve runs no desk.
	isDesk func(string) bool
	mu     sync.Mutex
	seq    int64
	cur    rawSnap
	prev   map[string]map[string]string
	log    []apiEvent
	wake   chan struct{}
}

// newHub starts seq at the clock in milliseconds, so a restarted serve never reuses a seq a
// client saw: its first change reads as a gap, and the client fetches the snapshot again.
func newHub(ctx context.Context, src serveSource) (*hub, error) {
	h := &hub{src: src, seq: time.Now().UnixMilli(), wake: make(chan struct{}), prev: map[string]map[string]string{}}
	_, err := h.refresh(ctx, true)
	h.log = nil
	return h, err
}

func idOf(raw json.RawMessage) string {
	var v struct {
		ID any `json:"id"`
	}
	_ = json.Unmarshal(raw, &v)
	switch id := v.ID.(type) {
	case string:
		return id
	case nil:
		return ""
	default:
		return fmt.Sprint(id)
	}
}

func splitSnap(v any) (rawSnap, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return rawSnap{}, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return rawSnap{}, err
	}
	s := rawSnap{desk: m["desk"], lists: map[string][]json.RawMessage{}}
	for _, k := range serveKinds {
		var list []json.RawMessage
		if raw := m[k.key]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &list); err != nil {
				return rawSnap{}, fmt.Errorf("%s: %w", k.key, err)
			}
		}
		s.lists[k.kind] = list
	}
	return s, nil
}

func (h *hub) emit(kind, op string, value json.RawMessage) {
	h.seq++
	h.log = append(h.log, apiEvent{Seq: h.seq, Op: op, Kind: kind, Value: value})
	if len(h.log) > serveLogMax {
		h.log = h.log[len(h.log)-serveLogMax:]
	}
}

// refresh reads the source when it may have changed, or always with force, and logs the changes.
func (h *hub) refresh(ctx context.Context, force bool) (rawSnap, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !force && !h.src.changed() {
		return h.cur, nil
	}
	v, err := h.src.snapshot(ctx)
	if err != nil {
		return h.cur, err
	}
	s, err := splitSnap(v)
	if err != nil {
		return h.cur, err
	}
	before := h.seq
	for _, k := range serveKinds {
		old := h.prev[k.kind]
		now := map[string]string{}
		for _, raw := range s.lists[k.kind] {
			id := idOf(raw)
			now[id] = string(raw)
			if old[id] != string(raw) {
				h.emit(k.kind, "upsert", raw)
			}
		}
		if !appendOnly[k.kind] {
			for _, raw := range h.cur.lists[k.kind] {
				if id := idOf(raw); id != "" {
					if _, ok := now[id]; !ok {
						b, _ := json.Marshal(map[string]string{"id": id})
						h.emit(k.kind, "remove", b)
					}
				}
			}
		}
		h.prev[k.kind] = now
	}
	if string(s.desk) != string(h.cur.desk) && len(s.desk) > 0 {
		h.emit("desk", "upsert", s.desk)
	}
	h.cur = s
	if h.seq != before {
		close(h.wake)
		h.wake = make(chan struct{})
	}
	return s, nil
}

// snapshotJSON is the snapshot with the seq its content matches.
func (h *hub) snapshotJSON(ctx context.Context) ([]byte, error) {
	if _, err := h.refresh(ctx, true); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]any{"seq": h.seq, "generated_at": time.Now().UTC().Truncate(time.Second), "desk": h.cur.desk}
	for _, k := range serveKinds {
		list := h.cur.lists[k.kind]
		if list == nil {
			list = []json.RawMessage{}
		}
		out[k.key] = list
	}
	return json.Marshal(out)
}

// since is the changes after seq. ok is false when the log no longer reaches back to seq, or
// seq is ahead of this serve: the client must fetch the snapshot again.
func (h *hub) since(seq int64) (evs []apiEvent, wake chan struct{}, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if seq > h.seq || (seq < h.seq && (len(h.log) == 0 || h.log[0].Seq > seq+1)) {
		return nil, nil, false
	}
	for _, ev := range h.log {
		if ev.Seq > seq {
			evs = append(evs, ev)
		}
	}
	return evs, h.wake, true
}

// watch refreshes every servePoll until ctx ends.
func (h *hub) watch(ctx context.Context) {
	t := time.NewTicker(servePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := h.refresh(ctx, false); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "prism server:", oneLine(err.Error()))
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae apiError
	if errors.As(err, &ae) {
		if ae.focus != nil {
			writeJSON(w, ae.code, map[string]any{"error": ae.msg, "focus": ae.focus})
			return
		}
		writeJSON(w, ae.code, map[string]string{"error": ae.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// handler is the API: every route behind the bearer token.
func (h *hub) handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		b, err := h.snapshotJSON(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("GET /v1/events", h.events)
	mux.HandleFunc("GET /v1/conversation", func(w http.ResponseWriter, r *http.Request) {
		v, err := h.src.conversation(r.Context(), r.URL.Query().Get("before"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("GET /v1/sessions/{id}/transcript", func(w http.ResponseWriter, r *http.Request) {
		v, err := h.src.transcript(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	act := func(kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, serveBodyMax))
			if err != nil {
				writeErr(w, errorf(http.StatusBadRequest, "reading the body: %v", err))
				return
			}
			if len(strings.TrimSpace(string(body))) > 0 && !json.Valid(body) {
				writeErr(w, errorf(http.StatusBadRequest, "the body is not JSON"))
				return
			}
			action := r.PathValue("action")
			if kind == "desk" {
				action = path.Base(r.URL.Path) // messages or restart
			}
			code, reply, err := h.src.act(r.Context(), kind, r.PathValue("id"), action, body)
			if err != nil {
				writeErr(w, err)
				return
			}
			if _, err := h.refresh(r.Context(), true); err != nil {
				fmt.Fprintln(os.Stderr, "prism server:", oneLine(err.Error()))
			}
			if reply == nil {
				reply = map[string]any{}
			}
			writeJSON(w, code, reply)
		}
	}
	mux.HandleFunc("POST /v1/desk/messages", act("desk"))
	mux.HandleFunc("POST /v1/desk/restart", act("desk"))
	mux.HandleFunc("POST /v1/answers", act("answers"))
	mux.HandleFunc("POST /v1/questions/{id}/{action}", act("questions"))
	mux.HandleFunc("POST /v1/sessions/{id}/{action}", act("sessions"))
	mux.HandleFunc("POST /v1/tracks/{id}/{action}", act("tracks"))
	mux.HandleFunc("POST /v1/handled/{id}/{action}", act("handled"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, errorf(http.StatusNotFound, "no route %s %s", r.Method, r.URL.Path))
	})
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == deskCheckPath {
			h.deskCheck(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeErr(w, errorf(http.StatusUnauthorized, "a bearer token from serve.json is needed"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// deskCheck answers 204 when the request presents the running desk's secret, else 403: how a
// command proves it runs for the headless desk.
func (h *hub) deskCheck(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Desk ")
	if r.Method != http.MethodPost || !ok || h.isDesk == nil || !h.isDesk(token) {
		writeErr(w, errorf(http.StatusForbidden, "not the desk"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// proveDesk asks the running serve whether this process carries the headless desk's secret.
func proveDesk() error {
	token := os.Getenv(deskTokenEnv)
	if token == "" {
		return errors.New("this process has no desk token")
	}
	si, err := readServeInfo()
	if err != nil {
		return errors.New("prism server is not running")
	}
	if !strings.HasPrefix(si.URL, "http://127.0.0.1:") {
		return fmt.Errorf("serve.json names %q, not a local serve", si.URL)
	}
	req, err := http.NewRequest(http.MethodPost, si.URL+deskCheckPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Desk "+token)
	resp, err := (&http.Client{Timeout: deskCheckTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("could not ask prism server: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("prism server does not know this desk token")
	}
	return nil
}

// events is the SSE stream. A client whose since the log no longer reaches gets the stream
// closed at once, and fetches the snapshot again.
func (h *hub) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming is not supported"))
		return
	}
	h.mu.Lock()
	last := h.seq
	h.mu.Unlock()
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, errorf(http.StatusBadRequest, "since is not a number: %q", v))
			return
		}
		last = n
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	keep := time.NewTicker(serveKeepAlive)
	defer keep.Stop()
	for {
		evs, wake, ok := h.since(last)
		if !ok {
			return
		}
		for _, ev := range evs {
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, b); err != nil {
				return
			}
			last = ev.Seq
		}
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-keep.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// lockServe takes the one-serve-at-a-time lock in the home.
func lockServe() (unlock func(), err error) {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(serveLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			msg := "prism server runs already for " + home()
			if si, err := readServeInfo(); err == nil {
				msg += fmt.Sprintf(" (pid %d, %s)", si.PID, si.URL)
			}
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func readServeInfo() (serveInfo, error) {
	var si serveInfo
	b, err := os.ReadFile(serveInfoPath())
	if err == nil {
		err = json.Unmarshal(b, &si)
	}
	return si, err
}

func writeServeInfo(si serveInfo) error {
	b, err := json.MarshalIndent(si, "", "  ")
	if err != nil {
		return err
	}
	tmp := serveInfoPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, serveInfoPath())
}

// adoptLoginPath puts the login shell's PATH in front of serve's own when claude isn't on it. A serve the Desk app starts
// inherits the GUI's PATH (/usr/bin:/bin:…), where neither claude nor gh nor tracks is found, and
// the desk and everything it runs inherit serve's.
func adoptLoginPath(errw io.Writer) {
	defer adoptRexPath()
	if _, err := exec.LookPath("claude"); err == nil {
		return
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, sh, "-lic", `printf '\n__TRACKS_PATH__%s__TRACKS_PATH__\n' "$PATH"`).Output()
	login := ""
	if s := string(out); err == nil {
		if i := strings.Index(s, "__TRACKS_PATH__"); i >= 0 {
			rest := s[i+len("__TRACKS_PATH__"):]
			if j := strings.Index(rest, "__TRACKS_PATH__"); j >= 0 {
				login = rest[:j]
			}
		}
	}
	if login == "" {
		fmt.Fprintf(errw, "prism server: could not read the login shell's PATH (%v); adding ~/.local/bin\n", err)
		if h, herr := os.UserHomeDir(); herr == nil {
			login = filepath.Join(h, ".local", "bin")
		}
	}
	seen := map[string]bool{}
	var parts []string
	for _, p := range append(filepath.SplitList(login), filepath.SplitList(os.Getenv("PATH"))...) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

// adoptRexPath adds the Rex CLI's folder to PATH when rex isn't on it: Rex puts it on PATH only
// in its own terminals, and an app-started serve (and the desk under it) asks Rex for agent states.
func adoptRexPath() {
	if _, err := exec.LookPath("rex"); err == nil {
		return
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(h, "Library", "Application Support", "com.superlogical.rex", "RexServer", "Versions", "Current", "Rex Server.app", "Contents", "MacOS")
	if _, err := os.Stat(filepath.Join(dir, "rex")); err != nil {
		return
	}
	os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
}

// interactiveDeskRuns says when the desk is bound to a Rex session whose agent still runs: serve
// would take the desk from it.
func interactiveDeskRuns() error {
	sess, err := deskSessionNow()
	if err != nil || sess == "" || sess == headlessDesk {
		return err
	}
	if _, err := deskAgentPID(sess); err != nil {
		return nil
	}
	return fmt.Errorf("the desk runs in Rex session %s: stop it, or start serve with --force to take the desk from it", sess)
}

func serveCmd(args []string, out, errw io.Writer) error {
	if len(args) == 1 && args[0] == "--detach" {
		return detachServe()
	}
	var restart bool
	if err := serveOnce(args, out, errw, &restart); err != nil || !restart {
		return err
	}
	return reexecServe(args)
}

// serveOnce runs serve until it is stopped, or until its binary was replaced (restart is then
// set); the lock and serve.json are released before it returns.
func serveOnce(args []string, out, errw io.Writer, restart *bool) error {
	mock, force := "", false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--force":
		force = true
	case len(args) == 2 && args[0] == "--mock":
		mock = args[1]
	default:
		return errors.New(serveUsage)
	}
	unlock, err := lockServe()
	if err != nil {
		return err
	}
	defer unlock()
	adoptLoginPath(errw)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, replaced := context.WithCancel(ctx)
	defer replaced()

	var src serveSource
	var desk *deskRunner
	if mock != "" {
		if src, err = newMockSource(mock); err != nil {
			return err
		}
	} else {
		if !storeOn() {
			return errors.New("prism server " + needsStore)
		}
		if err := interactiveDeskRuns(); err != nil && !force {
			return err
		}
		if err := withBareStore(func(st *store.Store) error { return st.RefreshQuestions() }); err != nil {
			fmt.Fprintf(errw, "prism server: refreshing questions from asks: %v\n", err)
		}
		if err := withBareStore(func(st *store.Store) error { return st.BindDesk(headlessDesk, true) }); err != nil {
			return fmt.Errorf("binding the desk to prism server: %w", err)
		}
		desk = newDeskRunner(errw)
		src = newLiveSource(desk)
	}
	h, err := newHub(ctx, src)
	if err != nil {
		return err
	}
	if desk != nil {
		desk.changed = func() { go h.refresh(ctx, true) }
		h.isDesk = desk.isDesk
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	si := serveInfo{URL: "http://" + ln.Addr().String(), Token: token, PID: os.Getpid(), Version: serveVersion}
	if exe, err := os.Executable(); err == nil {
		si.Build = buildLabel(exe)
	}
	if err := writeServeInfo(si); err != nil {
		return err
	}
	defer func() {
		if cur, err := readServeInfo(); err == nil && cur.PID == si.PID {
			os.Remove(serveInfoPath())
		}
	}()
	srv := &http.Server{Handler: h.handler(token), ReadHeaderTimeout: 10 * time.Second}
	go h.watch(ctx)
	var wg sync.WaitGroup
	if mock == "" {
		// Waited on, so a restart never lands between typing an answer and marking it delivered.
		wg.Add(1)
		go func() { defer wg.Done(); newDeliverer(errw).run(ctx) }()
	}
	if desk != nil {
		wg.Add(1)
		go func() { defer wg.Done(); desk.run(ctx) }()
	}
	if exe, err := os.Executable(); err == nil {
		go func() {
			// A stop that came first (SIGTERM, Ctrl-C) stays a stop.
			if watchBinary(ctx, exe, serveBinaryPoll, desk.between, serveRestartMaxWait) && ctx.Err() == nil {
				fmt.Fprintf(errw, "prism server: %s was replaced; restarting on the new build\n", exe)
				*restart = true
				replaced()
			}
		}()
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	what := "tracks.db"
	if mock != "" {
		what = mock
	}
	fmt.Fprintf(out, "prism server: %s on %s (%s)\n", what, si.URL, serveInfoPath())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	wg.Wait()
	return nil
}
