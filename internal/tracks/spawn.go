package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	defaultMaxThreads = 4
	maxThreadsEnv     = "TRACKS_MAX_THREADS"

	// dialogFooter ends every Claude Code start-up dialog (folder trust, a project's MCP
	// servers); a thread has nobody to answer one.
	dialogFooter = "Enter to confirm · Esc to cancel"
	// workingMark is on Claude's screen while it works on a turn.
	workingMark = "esc to interrupt"
)

var (
	// startWait is how long spawn watches a new thread for its agent to take its prompt or
	// stop at a dialog; startPoll is how often it looks.
	startWait = 10 * time.Second
	startPoll = 500 * time.Millisecond
	// startScreen and startState read the new thread's block; tests replace them.
	startScreen = func(sid, block string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
		defer cancel()
		b, err := rexRun(ctx, "capture", "-s", sid, "-b", block, "--trim")
		return string(b), err
	}
	startState = func(sid, block string) rexResult { return fetchSession(sid, block, "") }
)

// verifyCmdOK is whether cmd can sit on one card line and read back unchanged.
func verifyCmdOK(cmd string) bool {
	return !strings.ContainsFunc(cmd, unicode.IsControl) && !comment.MatchString(cmd)
}

// maxThreads is how many open threads a lead may have: TRACKS_MAX_THREADS, else 4.
func maxThreads() (int, error) {
	v := os.Getenv(maxThreadsEnv)
	if v == "" {
		return defaultMaxThreads, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a whole number above 0: %q", maxThreadsEnv, v)
	}
	return n, nil
}

// threadSettingsPath is the Claude Code settings file tracks spawn passes to a thread's agent.
func threadSettingsPath(name string) string {
	return filepath.Join(home(), "threads", name+".settings.json")
}

// writeThreadSettings writes the settings that make tracks verify the thread's Stop hook. They
// go to the thread's own session with --settings, so no other session gets the hook. The
// command names this binary by its path, so the thread is checked by the tracks that made it.
func writeThreadSettings(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	hook := map[string]any{
		"type":    "command",
		"command": shellJoin([]string{exe, "verify", name}),
		"timeout": int((defaultVerifyTimeout + hookSlack).Seconds()),
	}
	doc := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{hook}}}}}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	path := threadSettingsPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, writeAtomic(path, append(b, '\n'))
}

// threadSettingsArgs are the --settings flags that keep a resumed thread under its Stop hook.
// A thread whose settings file is missing gets one written for its current name, except on a
// dry run, which only says where it would be.
func threadSettingsArgs(c *Card, dry bool) []string {
	if c.Meta["parent"] == "" {
		return nil
	}
	path := threadSettingsPath(c.Name)
	if _, err := os.Stat(path); err != nil && !dry {
		if path, err = writeThreadSettings(c.Name); err != nil {
			return nil
		}
	}
	return []string{"--settings", path}
}

// moveThreadSettings gives a renamed thread settings under its new name, so the Stop hook
// follows it through a resume; the running agent keeps the file it started with in memory.
func moveThreadSettings(old, name string) error {
	if _, err := os.Stat(threadSettingsPath(old)); err != nil {
		return nil
	}
	if _, err := writeThreadSettings(name); err != nil {
		return err
	}
	return os.Remove(threadSettingsPath(old))
}

// claudeConfigPath is the file Claude Code keeps its per-folder trust in.
func claudeConfigPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(userHome(), ".claude.json")
}

// noTrustedFolder says why Claude would surely stop a new session in dir at its trust prompt:
// neither dir nor a folder above it is trusted. Whether a trusted folder still prompts (for
// its own settings or MCP servers) is seen only once the session runs, by watchStart.
func noTrustedFolder(dir string) string {
	path := claudeConfigPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return "could not read Claude's trusted folders in " + path + ": " + err.Error()
	}
	var doc struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "could not read Claude's trusted folders in " + path + ": " + err.Error()
	}
	dirs := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		dirs = append(dirs, real)
	}
	for _, d := range dirs {
		for p := d; ; p = filepath.Dir(p) {
			if doc.Projects[p].HasTrustDialogAccepted {
				return ""
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	return "Claude has not trusted " + dir + " or a folder above it, so the thread would wait at the trust prompt with nobody to answer: open claude in " + dir + " once and trust it, or spawn into a folder Claude already trusts"
}

// dialogText is the question of the dialog on screen: the lines above its footer, back to the
// rule Claude draws above it, made one line.
func dialogText(screen string) string {
	lines := strings.Split(ansi.Strip(screen), "\n")
	end := -1
	for i, l := range lines {
		if strings.Contains(l, dialogFooter) {
			end = i
		}
	}
	if end < 0 {
		return ""
	}
	start := 0
	for i := end - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && strings.Trim(t, "─") == "" {
			start = i + 1
			break
		}
	}
	body := lines[start:end]
	if !strings.Contains(strings.Join(body, " "), "Quick safety check") {
		return oneLine(strings.Join(body, " "))
	}
	// The trust dialog's own paragraph is the same every time; its reason sits under the ⚠.
	q := "Claude asks whether to trust the folder"
	for i, l := range body {
		if !strings.HasPrefix(strings.TrimSpace(l), "⚠") {
			continue
		}
		j := i
		for j < len(body) && strings.TrimSpace(body[j]) != "" {
			j++
		}
		q += ": " + oneLine(strings.Join(body[i:j], " "))
		break
	}
	return q
}

// watchStart watches a new thread's block until its agent takes its prompt (Rex says it is
// working, or Claude's screen says so) or stops at a start-up dialog. dialog is that dialog's
// text; started is false when neither showed within startWait.
func watchStart(sid, block string) (dialog string, started bool) {
	deadline := time.Now().Add(startWait)
	for {
		screen, err := startScreen(sid, block)
		if err == nil {
			if d := dialogText(screen); d != "" {
				return d, false
			}
			if strings.Contains(screen, workingMark) {
				return "", true
			}
		}
		if r := startState(sid, block); !r.failed && r.found && !r.state.Exited && r.state.State == "working" {
			return "", true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(startPoll)
	}
}

// unspawn takes back a thread whose agent stopped at a start-up dialog: its session ends, and
// its card and settings go, so nothing is left that looks like a running thread.
func unspawn(name, sid string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
	defer cancel()
	_, kerr := rexRun(ctx, "session", "kill", sid)
	derr := withStore(func(st *store.Store) error { return st.DeleteThread(name) })
	if derr == nil {
		if err := os.Remove(cardPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			derr = err
		}
	}
	os.Remove(threadSettingsPath(name))
	return errors.Join(kerr, derr)
}
