package tracks

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// compactPrompt is Claude Code's own compaction: the session keeps its id, card and reports,
// and goes on from Claude Code's summary of what it did.
const compactPrompt = "/compact"

// Past these, prism server compacts a session at its next quiet moment: the desk once its turn
// ends, a session the desk started before the next note it gets.
var (
	deskCompactAt  = 150_000
	trackCompactAt = 250_000
)

const (
	// compactWait is how long a note waits for its session's compaction to show in the
	// transcript before it goes in anyway.
	compactWait = 10 * time.Minute
	// contextTail is how much of the end of a transcript is read for its context size.
	contextTail = 8 << 20
)

// callUsage is the usage of one model call, which together is the context it sent.
type callUsage struct {
	Input       int `json:"input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
}

func (u callUsage) context() int { return u.Input + u.CacheCreate + u.CacheRead }

// transcriptContext is the context of session id's last call, 0 after a compaction, and when
// the transcript's newest compaction happened; zero values without a transcript.
func transcriptContext(id string) (tokens int, compacted time.Time) {
	files := newestFirst(id + ".jsonl")
	if len(files) == 0 {
		return 0, time.Time{}
	}
	f, err := os.Open(files[0])
	if err != nil {
		return 0, time.Time{}
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > contextTail {
		if _, err := f.Seek(st.Size()-contextTail, io.SeekStart); err != nil {
			return 0, time.Time{}
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), transcriptMaxLine)
	for sc.Scan() {
		var rec struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				Usage *callUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.IsSidechain {
			continue
		}
		switch {
		case rec.Type == "system" && rec.Subtype == "compact_boundary":
			tokens = 0
			if at, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
				compacted = at
			}
		case rec.Type == "assistant" && rec.Message.Usage != nil:
			tokens = rec.Message.Usage.context()
		}
	}
	return tokens, compacted
}

// compactingPath marks a compaction prism server started for session id; its time is when.
func compactingPath(id string) string { return filepath.Join(home(), "compacting", id) }

// compactMarked is whether a compaction of session id was started and not yet seen to end.
func compactMarked(id string) bool { _, err := os.Stat(compactingPath(id)); return err == nil }

// compactState is where a compaction prism server started for session id stands: running
// while it is not in the transcript yet and started less than compactWait ago, ended (and
// unmarked) once it is in the transcript or that wait ran out.
func compactState(id string) (running, ended bool) {
	st, err := os.Stat(compactingPath(id))
	if err != nil {
		return false, false
	}
	if _, done := transcriptContext(id); done.Before(st.ModTime()) && time.Since(st.ModTime()) < compactWait {
		return true, false
	}
	os.Remove(compactingPath(id))
	return false, true
}

// holdForCompaction is whether a note for the idle session s waits for the watch to compact
// it first: it is being compacted, or past trackCompactAt.
func holdForCompaction(s store.Session) bool {
	running, ended := compactState(s.ID)
	if running {
		return true
	}
	tokens, _ := transcriptContext(s.ID)
	return !ended && tokens >= trackCompactAt
}

// compactSession resumes session s, which stopped working, with /compact and marks it compacting.
func compactSession(s store.Session) error {
	path := compactingPath(s.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return err
	}
	if err := resumeWith(s, compactPrompt); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
