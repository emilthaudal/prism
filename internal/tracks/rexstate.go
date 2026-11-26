package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	rexEvery      = 2 * time.Second
	rexWorkers    = 4
	rexRescanWait = 10 * time.Second
	claudeApp     = "claude-code"
)

var rexTimeout = 3 * time.Second

// agentState is what Rex reports for a session's agent block.
type agentState struct {
	State string // idle, working, blocked, done, error
	Kind  string // permission or question, when blocked
	Msg   string
	At    time.Time
	Owner int // pid of the process that reported the state; 0 when Rex did not say
	// Exited: the owner is no longer the block's foreground process, which runs Foreground.
	Exited     bool
	Foreground string
}

type programRecord struct {
	ID           string    `json:"id"`
	State        string    `json:"state"`
	Kind         string    `json:"kind"`
	Msg          string    `json:"msg"`
	Title        string    `json:"title"`
	EffectiveApp string    `json:"effective_app"`
	UpdatedAt    time.Time `json:"updated_at"`
	Owner        struct {
		PID  int    `json:"pid"`
		Name string `json:"name"`
	} `json:"owner"`
}

func statePriority(s string) int {
	switch s {
	case "blocked":
		return 4
	case "error":
		return 3
	case "working":
		return 2
	case "done":
		return 1
	}
	return 0
}

// parseProgramStatus reduces a program_status reply to one state: the most urgent record wins,
// and the agent's own record (it has no id) wins a tie. ok is false when the block reports
// nothing from Claude Code.
func parseProgramStatus(b []byte) (s agentState, ok bool) {
	var r struct {
		Records []programRecord `json:"records"`
	}
	if json.Unmarshal(b, &r) != nil {
		return s, false
	}
	// A crashed agent leaves records behind (done and error survive it). When the newest
	// root record names an owner, keep only that owner's records (and owner-less ones), so a
	// leftover from an earlier process does not outrank the live agent's own records.
	var root *programRecord
	for i, rec := range r.Records {
		if rec.EffectiveApp == claudeApp && rec.ID == "" && rec.Owner.PID != 0 && (root == nil || rec.UpdatedAt.After(root.UpdatedAt)) {
			root = &r.Records[i]
		}
	}
	if root != nil {
		owner := root.Owner.PID
		r.Records = slices.DeleteFunc(r.Records, func(rec programRecord) bool { return rec.Owner.PID != 0 && rec.Owner.PID != owner })
	}
	var best *programRecord
	for i, rec := range r.Records {
		if rec.EffectiveApp != claudeApp || rec.State == "" {
			continue
		}
		if best == nil {
			best = &r.Records[i]
			continue
		}
		pb, pr := statePriority(best.State), statePriority(rec.State)
		if pr > pb || (pr == pb && best.ID != "" && rec.ID == "") {
			best = &r.Records[i]
		}
	}
	if best == nil {
		return s, false
	}
	// Claude Code puts msg and title on the subagent's record, not the main one.
	msg := ""
	for _, pick := range []func(programRecord) string{
		func(r programRecord) string { return r.Msg },
		func(r programRecord) string { return r.Title },
	} {
		for _, rec := range r.Records {
			if rec.EffectiveApp == claudeApp && strings.TrimSpace(pick(rec)) != "" {
				msg = strings.TrimSpace(pick(rec))
				break
			}
		}
		if msg != "" {
			break
		}
	}
	return agentState{State: best.State, Kind: best.Kind, Msg: msg, At: best.UpdatedAt, Owner: best.Owner.PID}, true
}

// rexResult: found has a state; failed means Rex did not answer, so the last state stands.
type rexResult struct {
	state  agentState
	block  string
	id     string
	found  bool
	failed bool
}

type rexMsg struct {
	at      time.Time
	results map[string]rexResult
}

// rexTracker holds the last Rex answer per session. Only the UI goroutine touches it.
type rexTracker struct {
	states   map[string]agentState
	blocks   map[string]string
	ids      map[string]string
	noBlock  map[string]time.Time
	lastRun  time.Time
	inFlight bool
}

func newRexTracker() *rexTracker {
	return &rexTracker{states: map[string]agentState{}, blocks: map[string]string{}, ids: map[string]string{}, noBlock: map[string]time.Time{}}
}

func (t *rexTracker) due(now time.Time) bool {
	return !t.inFlight && now.Sub(t.lastRun) >= rexEvery
}

func (t *rexTracker) apply(msg rexMsg) {
	t.inFlight = false
	for sess, r := range msg.results {
		switch {
		case r.failed:
			continue
		case r.found:
			t.states[sess], t.blocks[sess], t.ids[sess] = r.state, r.block, r.id
			delete(t.noBlock, sess)
			continue
		}
		delete(t.states, sess)
		delete(t.blocks, sess)
		delete(t.ids, sess)
		t.noBlock[sess] = msg.at
	}
}

func (t *rexTracker) state(sess string) (agentState, bool) {
	s, ok := t.states[sess]
	return s, ok
}

// cmd starts one bounded fetch for the sessions. It returns nil when none is needed.
func (t *rexTracker) cmd(now time.Time, sessions []string, evs []Event) tea.Cmd {
	if len(sessions) == 0 || !t.due(now) {
		return nil
	}
	t.inFlight, t.lastRun = true, now
	type job struct{ sess, hint, id string }
	var jobs []job
	for _, s := range sessions {
		if at, ok := t.noBlock[s]; ok && now.Sub(at) < rexRescanWait {
			continue
		}
		hint := agentBlock(evs, s)
		if b := t.blocks[s]; b != "" {
			hint = b
		}
		jobs = append(jobs, job{s, hint, t.ids[s]})
	}
	return func() tea.Msg {
		res := map[string]rexResult{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, rexWorkers)
		for _, j := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				r := fetchSession(j.sess, j.hint, j.id)
				mu.Lock()
				res[j.sess] = r
				mu.Unlock()
			}()
		}
		wg.Wait()
		return rexMsg{time.Now(), res}
	}
}

// rexRun never starts a server and never waits on a stuck one: the context ends the process,
// and WaitDelay ends the wait when a child still holds its output open.
func rexRun(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "rex", append([]string{rexNoStart}, args...)...)
	cmd.WaitDelay = waitDelay
	return cmd.Output()
}

func programStatus(ctx context.Context, sess, block string) (s agentState, ok bool, err error) {
	b, err := rexRun(ctx, "block", "call", "-s", sess, "-b", block, "program_status")
	if err != nil {
		return s, false, err
	}
	s, ok = parseProgramStatus(b)
	return s, ok, nil
}

// fetchSession reads the agent's state: from the hinted block when it still reports one,
// else by scanning the session's blocks for Claude Code. It reports failed unless Rex
// answered that the session has no agent, so an outage keeps the last known state.
func fetchSession(sess, hint, id string) rexResult {
	ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
	defer cancel()
	notFound := func(err error) rexResult {
		if errors.Is(err, errSessionNone) || errors.Is(err, errSessionAmbiguous) {
			return rexResult{}
		}
		return rexResult{failed: true}
	}
	if id == "" {
		var err error
		if id, _, err = resolveSessionCtx(ctx, sess); err != nil {
			return notFound(err)
		}
	}
	if hint != "" {
		if s, ok, _ := programStatus(ctx, id, hint); ok {
			return withLiveness(ctx, rexResult{state: s, block: hint, id: id, found: true})
		}
	}
	b, err := rexRun(ctx, "block", "ls", "-s", id, "--json")
	if err != nil {
		if ctx.Err() != nil {
			return rexResult{failed: true}
		}
		// The cached id may be stale: a session that is gone has no agent.
		if _, _, rerr := resolveSessionCtx(ctx, sess); errors.Is(rerr, errSessionNone) {
			return rexResult{}
		}
		return rexResult{failed: true}
	}
	var l struct {
		Blocks []struct {
			ID string `json:"block_id"`
		} `json:"blocks"`
	}
	if json.Unmarshal(b, &l) != nil {
		return rexResult{failed: true}
	}
	for _, blk := range l.Blocks {
		if blk.ID == hint {
			continue
		}
		s, ok, err := programStatus(ctx, id, blk.ID)
		switch {
		case err != nil && ctx.Err() != nil:
			return rexResult{failed: true}
		case err != nil:
			continue // a block without program_status (not a terminal) is not the agent
		case ok:
			return withLiveness(ctx, rexResult{state: s, block: blk.ID, id: id, found: true})
		}
	}
	return rexResult{}
}

// withLiveness marks the state exited when the process that reported it is no longer the
// block's foreground process: Rex keeps an idle record after Claude Code exits, and typing
// into that block would reach a shell. A record without an owner cannot be checked.
func withLiveness(ctx context.Context, r rexResult) rexResult {
	if r.state.Owner == 0 {
		return r
	}
	b, err := rexRun(ctx, "block", "call", "-s", r.id, "-b", r.block, "process")
	var p struct {
		Foreground *struct {
			PID   int    `json:"pid"`
			Name  string `json:"name"`
			Argv0 string `json:"argv0"`
		} `json:"foreground"`
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return rexResult{failed: true}
	case err != nil, json.Unmarshal(b, &p) != nil, p.Foreground == nil:
		r.state.Exited = true
	case p.Foreground.PID != r.state.Owner, isShell(p.Foreground.Name), isShell(p.Foreground.Argv0):
		// Rex names the foreground process as a report's owner, so a status written into
		// the terminal while a shell is in front still counts as the shell's.
		r.state.Exited, r.state.Foreground = true, p.Foreground.Name
	}
	return r
}

func isShell(name string) bool {
	switch strings.TrimPrefix(filepath.Base(name), "-") {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "tcsh", "csh", "nu", "login":
		return true
	}
	return false
}
