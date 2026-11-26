package tracks

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	// stallAfter is how long a session's row may say working while its process sits idle
	// between turns before the turn counts as cut off: no hook fired since, and the Stop hook
	// never came (EMI-426: a usage limit ends a turn that way).
	stallAfter = 3 * time.Minute
	// reapEvery is how often the desk's watch checks its sessions' processes.
	reapEvery = 30 * time.Second
	// transcriptTail is how much of a transcript's end the reaper reads for the turn's last record.
	transcriptTail = 256 * 1024
)

// watchReaps is whether the desk's watch checks the sessions' processes; tests turn it off, so
// that no real claude agents is asked about their sessions.
var watchReaps = true

var reapMu struct {
	sync.Mutex
	last time.Time
}

// reapDue runs reapSessions at most once per reapEvery, for the desk's watch. A failure only
// leaves the rows as the hooks wrote them.
func reapDue() {
	if !watchReaps {
		return
	}
	reapMu.Lock()
	if time.Since(reapMu.last) < reapEvery {
		reapMu.Unlock()
		return
	}
	reapMu.last = time.Now()
	reapMu.Unlock()
	_ = withBareStore(func(st *store.Store) error { _, _, err := readSessions(st); return err })
}

// waitingOnAgents is the activity of a session whose turn ended while n subagents run.
func waitingOnAgents(n int) string {
	if n == 1 {
		return "waiting on 1 agent"
	}
	return "waiting on " + strconv.Itoa(n) + " agents"
}

// workingAgents counts each session's subagents that still run.
func workingAgents(st *store.Store) (map[string]int, error) {
	list, err := st.Subagents()
	out := map[string]int{}
	for _, a := range list {
		if a.Status == "working" {
			out[a.Session]++
		}
	}
	return out, err
}

// turnCut is why a session's turn ended without its Stop hook: a usage limit, with the time it
// resets when the transcript says it, else nothing known.
type turnCut struct {
	usageLimit bool
	resets     time.Time
}

// stalledSessions are the sessions whose row says working while claude agents lists their
// process idle, and no hook has touched the row for stallAfter: their turn ended without a Stop.
// Usage-limited sessions whose limit has reset since come back too, to be marked for a resume.
func stalledSessions(list []store.Session, agents map[string]claudeAgent, now time.Time) []store.Stall {
	var out []store.Stall
	for _, s := range list {
		a, ok := agents[s.ID]
		if !ok || a.PID == 0 || a.Status != "idle" || s.Host == "stopped" || now.Sub(s.UpdatedAt) < stallAfter {
			continue
		}
		limited := s.Status == "blocked" && store.UsageLimited(s.WaitingFor)
		if s.Status != "working" && !limited {
			continue
		}
		cut := lastTurnCut(s.ID)
		switch {
		case cut.usageLimit && cut.resets.After(now):
			if !limited {
				out = append(out, store.Stall{ID: s.ID, Hold: true, Why: store.UsageLimitWhy(cut.resets.Local().Format("15:04"))})
			}
		case cut.usageLimit || limited:
			out = append(out, store.Stall{ID: s.ID, Why: "the usage limit reset: resume it, its turn was cut off"})
		default:
			out = append(out, store.Stall{ID: s.ID, Why: "its turn ended without a Stop hook"})
		}
	}
	return out
}

// lastTurnCut reads the end of the session's transcript for the turn's last assistant record:
// Claude Code writes a usage-limit stop as a synthetic one with apiError usage_limit_reached.
func lastTurnCut(id string) turnCut {
	var cut turnCut
	if !claudeSessionID.MatchString(id) {
		return cut
	}
	files := newestFirst(id + ".jsonl")
	if len(files) == 0 {
		return cut
	}
	f, err := os.Open(files[0])
	if err != nil {
		return cut
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > transcriptTail {
		_, _ = f.Seek(st.Size()-transcriptTail, io.SeekStart)
	}
	b, _ := io.ReadAll(f)
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var rec struct {
			Type        string `json:"type"`
			APIError    string `json:"apiError"`
			QuotaLimits struct {
				ResetsAt int64 `json:"resetsAt"`
			} `json:"quotaLimits"`
		}
		if json.Unmarshal(lines[i], &rec) != nil || rec.Type != "assistant" {
			continue
		}
		if rec.APIError == "usage_limit_reached" {
			cut.usageLimit = true
			if rec.QuotaLimits.ResetsAt > 0 {
				cut.resets = time.Unix(rec.QuotaLimits.ResetsAt, 0)
			}
		}
		return cut
	}
	return cut
}
