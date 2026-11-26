package tracks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// hookSilentAfter is how long a working focus track may go without a hook event before the
// header says so. A prompt starts every turn with an event and a Stop ends it, so silence
// that long beside a "working" agent means the hook is not running; most turns end within
// an hour, and 3 hours leaves room for the longest ones without crying wolf.
const hookSilentAfter = 3 * time.Hour

const hookReasonMax = 60

// hookRecord is what hook.ok and hook.err hold.
type hookRecord struct {
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	Error   string    `json:"error,omitempty"`
}

func hookOKPath() string  { return filepath.Join(home(), "hook.ok") }
func hookErrPath() string { return filepath.Join(home(), "hook.err") }

// buildID names the running binary: the VCS revision Go stamps into builds made inside a
// checkout, "-dirty" when the tree had changes, or "devel" when there is none.
func buildID() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	rev = rev[:min(len(rev), 12)]
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// recordHook notes the outcome of a hook run. It never fails the caller: the hook's exit code
// must stay 0, and a state dir that cannot be written is already the failure being reported.
func recordHook(err error) {
	defer func() { recover() }()
	rec := hookRecord{At: time.Now().UTC(), Version: buildID()}
	path := hookOKPath()
	if err != nil {
		rec.Error, path = err.Error(), hookErrPath()
	}
	if b, merr := json.Marshal(rec); merr == nil {
		_ = writeAtomic(path, append(b, '\n'))
	}
}

func readHookRecord(path string) (hookRecord, bool) {
	var r hookRecord
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &r) != nil || r.At.IsZero() {
		return r, false
	}
	return r, true
}

// hookFailure is hook.err's record while it is newer than hook.ok.
func hookFailure() (hookRecord, bool) {
	e, eok := readHookRecord(hookErrPath())
	if !eok {
		return e, false
	}
	if o, ook := readHookRecord(hookOKPath()); ook && !e.At.After(o.At) {
		return e, false
	}
	return e, true
}

// lastHookEvent is the hook's newest own event for the Rex session. Other writers log events
// under the same session (open, answer, need), which say nothing about the hook.
func lastHookEvent(evs []Event, sess string) (Event, bool) {
	for i := len(evs) - 1; i >= 0; i-- {
		switch e := evs[i]; {
		case e.RexSession != sess:
		case e.Event == "prompt", e.Event == "stop", e.Event == "notify":
			return e, true
		}
	}
	return Event{}, false
}

// hookWarning is the header's note that the hook is not reaching the tracks, or "" in the
// normal case. It concerns focus tracks Rex reports as working, except when the last hook
// event is a stop newer than Rex's state (the turn ended and Rex's reading is stale); a
// "working" that starts after a stop means the hook missed the next prompt. A
// recent failure (hook.err newer than hook.ok and under hookSilentAfter old) shows at once;
// otherwise it takes hookSilentAfter without an event, so an idle week, parked tracks and a
// Rex outage say nothing. A track with no hook event at all is never counted for silence.
func hookWarning(cards []*Card, states map[string]agentState, evs []Event, now time.Time, failure hookRecord, failing bool) string {
	var silent time.Duration
	affected := false
	for _, c := range cards {
		sess := c.Meta["rex_session"]
		if c.mode() != "focus" || sess == "" {
			continue
		}
		s, ok := states[sess]
		if !ok || s.State != "working" || s.Exited {
			continue
		}
		last, seen := lastHookEvent(evs, sess)
		if seen && last.Event == "stop" && !s.At.After(last.At) {
			continue // Rex's "working" predates the stop: a stale Rex reading, not a silent hook
		}
		affected = true
		if seen && now.Sub(last.At) > silent {
			silent = now.Sub(last.At)
		}
	}
	if affected && failing && now.Sub(failure.At) < hookSilentAfter {
		return "hook failing: " + shortReason(failure.Error)
	}
	if silent < hookSilentAfter {
		return ""
	}
	return fmt.Sprintf("hook silent for %s", shortDur(silent))
}

func shortReason(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > hookReasonMax {
		s = string(r[:hookReasonMax-1]) + "…"
	}
	return s
}
