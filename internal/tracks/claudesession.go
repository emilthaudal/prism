package tracks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	// The Rex context Claude Code writes at start sits in the first few records.
	transcriptHeadLines = 40
	transcriptMaxLine   = 16 * 1024 * 1024
)

var claudeSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// cardValueOK is whether p can sit on a card as a path and read back unchanged: parseCard
// cuts at a comment and trims quotes and spaces, and a control character would end the line.
func cardValueOK(p string) bool {
	return filepath.IsAbs(p) && p == strings.Trim(strings.TrimSpace(p), `"'`) &&
		!strings.ContainsFunc(p, unicode.IsControl) && !comment.MatchString(p)
}

// setMetas sets each key of the card's frontmatter in one write, adding a line for a key the
// card does not have yet.
func setMetas(name string, kv [][2]string) error {
	if storeOn() {
		return setMetasStore(name, kv)
	}
	err := withCardLock(name, func(text string) (string, error) {
		var err error
		for _, p := range kv {
			if text, err = upsertMetaText(name, text, p[0], p[1]); err != nil {
				return "", err
			}
		}
		return text, nil
	})
	if errors.Is(err, errSwitched) {
		return setMetasStore(name, kv)
	}
	return err
}

func upsertMetaText(name, text, key, value string) (string, error) {
	if t, err := setMetaText(name, text, key, value); err == nil {
		return t, nil
	}
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return "", fmt.Errorf("card %s has no frontmatter", name)
	}
	for i := 1; i < len(lines); i++ {
		if l := lines[i]; strings.TrimSpace(l) == "---" {
			add := strings.TrimRight(key+": "+value, " ")
			if strings.HasSuffix(l, "\r") {
				add += "\r"
			}
			return strings.Join(slices.Insert(lines, i, add), "\n"), nil
		}
	}
	return "", fmt.Errorf("card %s has no end to its frontmatter", name)
}

type transcriptRec struct {
	Cwd         string `json:"cwd"`
	Entrypoint  string `json:"entrypoint"`
	SessionID   string `json:"sessionId"`
	IsSidechain bool   `json:"isSidechain"`
}

// scanTranscriptHead calls fn with each of the transcript's first records until it returns true.
func scanTranscriptHead(path string, fn func(rec transcriptRec, raw []byte) bool) {
	scanTranscriptLines(path, transcriptHeadLines, fn)
}

// scanTranscriptLines is scanTranscriptHead over the first n records.
func scanTranscriptLines(path string, n int, fn func(rec transcriptRec, raw []byte) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), transcriptMaxLine)
	for i := 0; i < n && sc.Scan(); i++ {
		var rec transcriptRec
		if json.Unmarshal(sc.Bytes(), &rec) == nil && fn(rec, sc.Bytes()) {
			return
		}
	}
}

// transcriptCwd is the directory the transcript's session started in, "" when it does not say.
func transcriptCwd(path string) string {
	cwd := ""
	scanTranscriptHead(path, func(rec transcriptRec, _ []byte) bool {
		if rec.Cwd != "" && !rec.IsSidechain {
			cwd = rec.Cwd
			return true
		}
		return false
	})
	return cwd
}

func transcriptsDir() string { return filepath.Join(userHome(), ".claude", "projects") }

// newestFirst lists the top-level transcripts of every project, newest first. Subagent
// transcripts sit a level deeper, under <session>/subagents/, and are left out.
func newestFirst(pattern string) []string {
	files, _ := filepath.Glob(filepath.Join(transcriptsDir(), "*", pattern))
	files = slices.DeleteFunc(files, func(f string) bool { return strings.Contains(f, "/subagents/") })
	mod := map[string]int64{}
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			mod[f] = st.ModTime().UnixNano()
		}
	}
	slices.SortStableFunc(files, func(a, b string) int {
		switch {
		case mod[a] > mod[b]:
			return -1
		case mod[a] < mod[b]:
			return 1
		}
		return 0
	})
	return files
}

// cwdForClaudeSession is the start directory recorded in the session's own transcript.
func cwdForClaudeSession(id string) string {
	if !claudeSessionID.MatchString(id) {
		return ""
	}
	for _, f := range newestFirst(id + ".jsonl") {
		if cwd := transcriptCwd(f); cwd != "" {
			return cwd
		}
	}
	return ""
}

// findTranscript is the newest interactive Claude session that started in the Rex session:
// Claude Code writes REX_SESSION=<id> into its opening context. A headless run started from
// the session, or a conversation that only quotes the id, does not count.
func findTranscript(rexSession string) (id, cwd string, ok bool) {
	if rexSession == "" {
		return "", "", false
	}
	re := regexp.MustCompile(`REX_SESSION=` + regexp.QuoteMeta(rexSession) + `([^A-Za-z0-9_:.-]|$)`)
	for _, f := range newestFirst("*.jsonl") {
		want := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		scanTranscriptHead(f, func(rec transcriptRec, raw []byte) bool {
			if rec.Entrypoint != "cli" || rec.IsSidechain || rec.SessionID != want || !re.Match(raw) {
				return false
			}
			id, cwd, ok = rec.SessionID, rec.Cwd, true
			return true
		})
		if ok && claudeSessionID.MatchString(id) && cardValueOK(cwd) {
			return id, cwd, true
		}
		ok = false
	}
	return "", "", false
}

// recordClaudeSession keeps the card's claude_session and claude_cwd on the Claude session
// the hook fired in, so `tracks resume` can bring it back.
func recordClaudeSession(c *Card, id, transcript, hookCwd string) error {
	if !claudeSessionID.MatchString(id) || (c.Meta["claude_session"] == id && c.Meta["claude_cwd"] != "") {
		return nil
	}
	cwd := ""
	if strings.HasSuffix(transcript, ".jsonl") {
		cwd = transcriptCwd(transcript)
	}
	if cwd == "" {
		cwd = hookCwd
	}
	if !cardValueOK(cwd) {
		cwd = ""
	}
	if c.Meta["claude_session"] == id && cwd == "" {
		return nil
	}
	return setMetas(c.Name, [][2]string{{"claude_session", id}, {"claude_cwd", cwd}})
}
