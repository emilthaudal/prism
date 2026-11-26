package tracks

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
)

// Claude Code titles its window from the session's first message, "/track NAME", and keeps
// that title after the track is renamed. A new agent starts with --name NAME instead, and a
// rename retitles the window: the running Claude's terminal title now, and a custom-title
// record in its transcript, which /rename also writes, for /resume and a resumed window.

// claudeTTY is claudeTTYOf; tests point it at a file.
var claudeTTY = claudeTTYOf

// claudeTTYOf is the terminal of the Claude process this command runs under, when that Claude
// is session id; Claude Code puts CLAUDE_PID and CLAUDE_CODE_SESSION_ID in its tools'
// environment. A rename run anywhere else ("" here) leaves running windows alone.
func claudeTTYOf(id string) string {
	pid := os.Getenv("CLAUDE_PID")
	if id == "" || os.Getenv("CLAUDE_CODE_SESSION_ID") != id || pid == "" || strings.Trim(pid, "0123456789") != "" {
		return ""
	}
	out, err := run("", "ps", "-o", "tty=", "-p", pid)
	tty := strings.TrimSpace(out)
	if err != nil || tty == "" || tty == "??" || tty == "-" {
		return ""
	}
	return "/dev/" + tty
}

// retitleClaude names the track's Claude window name and returns what it could not do.
// name is a valid track name, so it holds no control characters.
func retitleClaude(id, name string) (notes []string) {
	if tty := claudeTTY(id); tty != "" {
		if err := writeTitle(tty, name); err != nil {
			notes = append(notes, "renamed, but could not retitle the Claude window: "+err.Error())
		}
	}
	if !claudeSessionID.MatchString(id) {
		return notes
	}
	files := newestFirst(id + ".jsonl")
	if len(files) == 0 {
		return notes
	}
	if err := appendTitleRecord(files[0], id, name); err != nil {
		notes = append(notes, "renamed, but /resume keeps the old Claude title: "+err.Error())
	}
	return notes
}

// writeTitle sets a terminal's title with the OSC 0 escape. Non-blocking, so a terminal
// nobody reads fails the write instead of hanging the rename.
func writeTitle(tty, name string) error {
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\x1b]0;" + name + "\x07")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// appendTitleRecord adds the record /rename writes to an existing transcript. One small
// append is one write, so it does not interleave with Claude's own.
func appendTitleRecord(path, id, name string) error {
	rec, err := json.Marshal(struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		SessionID   string `json:"sessionId"`
	}{"custom-title", name, id})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(append(rec, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
