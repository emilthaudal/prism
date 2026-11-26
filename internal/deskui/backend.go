package deskui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Backend is what the window reads and writes: the server, or a fixture in memory.
type Backend interface {
	Snapshot(ctx context.Context) (Snapshot, error)
	// Events calls fn with each change after since, in order, until the stream ends, ctx is
	// done or fn returns false.
	Events(ctx context.Context, since int64, fn func(Event) bool) error
	Transcript(ctx context.Context, session string) ([]Entry, error)
	// Post sends an action (docs/serve-api.md, Actions) and returns the response body.
	Post(ctx context.Context, path string, body any) ([]byte, error)
	Label() string
}

type serveInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func tracksHome() string {
	if h := os.Getenv("TRACKS_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "state", "tracks")
}

// HTTP talks to prism server as $TRACKS_HOME/serve.json describes it.
type HTTP struct {
	URL, Token string
	Client     *http.Client
}

func NewHTTP() (*HTTP, error) {
	p := filepath.Join(tracksHome(), "serve.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("prism server is not running (%w); start it, or use --fixture FILE", err)
	}
	var si serveInfo
	if err := json.Unmarshal(b, &si); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if si.URL == "" {
		return nil, fmt.Errorf("%s has no url", p)
	}
	return &HTTP{URL: strings.TrimRight(si.URL, "/"), Token: si.Token, Client: &http.Client{}}, nil
}

func (h *HTTP) Label() string { return "Prism server" }

func (h *HTTP) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return nil, &httpError{resp.StatusCode, e.Error}
		}
		return nil, &httpError{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	return resp, nil
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s", e.code, e.msg) }

func (h *HTTP) getJSON(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := h.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

const requestTimeout = 10 * time.Second

func (h *HTTP) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	err := h.getJSON(ctx, "/v1/snapshot", &s)
	return s, err
}

func (h *HTTP) Transcript(ctx context.Context, session string) ([]Entry, error) {
	var t struct {
		Entries []Entry `json:"entries"`
	}
	err := h.getJSON(ctx, "/v1/sessions/"+url.PathEscape(session)+"/transcript", &t)
	return t.Entries, err
}

func (h *HTTP) Post(ctx context.Context, path string, body any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	resp, err := h.do(ctx, http.MethodPost, path, r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (h *HTTP) Events(ctx context.Context, since int64, fn func(Event) bool) error {
	resp, err := h.do(ctx, http.MethodGet, "/v1/events?since="+strconv.FormatInt(since, 10), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readSSE(resp.Body, fn)
}

// errStopped is readSSE's result when fn asked it to stop.
var errStopped = errors.New("stopped")

// readSSE reads a Server-Sent Events stream: data lines up to a blank line make one event;
// comment lines (the keep-alive) and other fields are skipped.
func readSSE(r io.Reader, fn func(Event) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var data []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(data) == 0 {
				continue
			}
			var ev Event
			err := json.Unmarshal([]byte(strings.Join(data, "\n")), &ev)
			data = data[:0]
			if err != nil {
				return fmt.Errorf("bad event: %w", err)
			}
			if !fn(ev) {
				return errStopped
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

const (
	retryMin = 500 * time.Millisecond
	retryMax = 10 * time.Second
)

// follow keeps the window in step with the backend: a snapshot, then the changes after it.
// A gap in seq or a dropped stream starts over with a fresh snapshot, after a backoff when
// it failed.
func follow(ctx context.Context, b Backend, send func(any)) {
	wait := retryMin
	for ctx.Err() == nil {
		snap, err := b.Snapshot(ctx)
		if err != nil {
			send(connMsg{err: err})
			if !sleepCtx(ctx, wait) {
				return
			}
			wait = min(wait*2, retryMax)
			continue
		}
		send(snapshotMsg{snap})
		last := snap.Seq
		gap := false
		err = b.Events(ctx, last, func(ev Event) bool {
			if ev.Seq <= last {
				return true
			}
			if ev.Seq != last+1 {
				gap = true
				return false
			}
			last = ev.Seq
			wait = retryMin
			send(eventMsg{ev})
			return true
		})
		if ctx.Err() != nil {
			return
		}
		if gap {
			send(connMsg{note: "missed changes; reloading"})
			continue
		}
		send(connMsg{err: fmt.Errorf("event stream ended: %w", err)})
		if !sleepCtx(ctx, wait) {
			return
		}
		wait = min(wait*2, retryMax)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
