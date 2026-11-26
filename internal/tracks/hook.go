package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var classifier = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"answers", regexp.MustCompile(`^\s*(1[\.\)]|d1|q1|go with your recommendation on |emil's answers?[: ])`)},
	{"status", regexp.MustCompile(`(status|where (did|we) |what do i have to do|forgot|what('| i)?s left|what was left|are .* live|did you|whats next|what's next)`)},
	{"go-ahead", regexp.MustCompile(`^(yes|yeah|ok|okay|go ahead|go|do it|merge|lets do|let's do|sounds good|approve|continue|fix it|push|ship|deploy)\b|merge it|fast forward`)},
	{"news", regexp.MustCompile(`(i (signed|posted|queued|merged|pushed|got|added|created|deployed|logged)|is now|has been|tokens again|github is running|waiting for review|\b(he|she|they) (merged|approved|replied)|reviewer|approved)`)},
	{"new-work", regexp.MustCompile(`^(implement|verify|add |fix |create |investigate|urgent|open the)`)},
}

func classify(prompt string) string {
	t := strings.TrimSpace(prompt)
	if strings.HasPrefix(t, "<task-notification>") || strings.HasPrefix(t, "<system-reminder>") || continueShape.MatchString(t) || leadMessage.MatchString(t) {
		return "system"
	}
	s := strings.ReplaceAll(strings.ToLower(t), "\n", " ")
	for _, c := range classifier {
		if c.re.MatchString(s) {
			return c.kind
		}
	}
	return "direction"
}

// runHook never fails the caller: Claude Code treats a non-zero exit as a hook error. It notes
// the outcome in hook.ok or hook.err, so a hook that stops working leaves a trace.
func runHook(in io.Reader) {
	defer func() { recover() }()
	if handled, err := guardedHook(in); handled || err != nil {
		recordHook(err)
	}
}

var processHook = hookEvent

// guardedHook turns a panic in the hook into an error. handled is false for runs that skip
// by design: a headless entrypoint or an event the hook does not use.
func guardedHook(in io.Reader) (handled bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			handled, err = true, fmt.Errorf("panic: %v", r)
		}
	}()
	return processHook(in)
}

type hookInput struct {
	Event            string          `json:"hook_event_name"`
	Cwd              string          `json:"cwd"`
	Prompt           string          `json:"prompt"`
	SessionID        string          `json:"session_id"`
	Transcript       string          `json:"transcript_path"`
	Source           string          `json:"source"`
	Model            string          `json:"model"`
	LastAnswer       string          `json:"last_assistant_message"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	ToolName         string          `json:"tool_name"`
	ToolUseID        string          `json:"tool_use_id"`
	ToolInput        json.RawMessage `json:"tool_input"`
	Suggestions      json.RawMessage `json:"permission_suggestions"`
	AgentID          string          `json:"agent_id"`
	AgentType        string          `json:"agent_type"`
	BackgroundTasks  json.RawMessage `json:"background_tasks"`
	// An MCP server's elicitation: an approval page to open, or a form.
	MCPServer string `json:"mcp_server_name"`
	Mode      string `json:"mode"`
	URL       string `json:"url"`
}

func hookEvent(in io.Reader) (handled bool, err error) {
	start := time.Now()
	// Headless runs (claude -p, the SDK) inherit REX_SESSION from the track's
	// session; their prompts are not Emil typing, so only the interactive CLI counts.
	// Background sessions (claude --bg) run as "cli" too.
	if ep := os.Getenv("CLAUDE_CODE_ENTRYPOINT"); ep != "" && ep != "cli" {
		return false, nil
	}
	var h hookInput
	b, _ := io.ReadAll(in)
	if err := json.Unmarshal(b, &h); err != nil {
		return true, fmt.Errorf("bad hook input: %w", err)
	}
	// A refused prompt starts no turn, so it changes neither the card nor the session's row.
	if h.Event == "UserPromptSubmit" && storeOn() && blockCopy(h, start) {
		return true, nil
	}
	handled, err = attentionHook(h)
	if se, ok := sessionEventOf(h); ok && storeOn() {
		return true, errors.Join(err, sessionHook(se, start))
	}
	return handled, err
}

// attentionHook logs the attention event and keeps the track's card current.
func attentionHook(h hookInput) (handled bool, err error) {
	e := Event{At: time.Now().UTC(), Cwd: h.Cwd, RexSession: os.Getenv("REX_SESSION"), RexBlock: os.Getenv("REX_BLOCK")}
	switch h.Event {
	case "UserPromptSubmit":
		e.Event, e.Len, e.Kind = "prompt", len(h.Prompt), classify(h.Prompt)
	case "Stop":
		e.Event = "stop"
	case "Notification":
		e.Event = "notify"
	case "SessionStart":
		if storeOn() {
			e.Event = "session_start"
			if moved, err := hookStore(e, h.SessionID, h.Transcript, h.Cwd); !moved {
				return true, err
			}
		}
		// A started or resumed session is not mid-answer: clear a stamp an interrupted turn left.
		if c := cardFor(e.RexSession); c != nil && c.Meta["answered"] != "" {
			defer withLockWait(hookLockWait)()
			err = setMetas(c.Name, clearAnswered(c))
		}
		return true, err
	default:
		return false, nil
	}
	if storeOn() {
		if moved, err := hookStore(e, h.SessionID, h.Transcript, h.Cwd); !moved {
			if err == nil && e.Event == "stop" && h.LastAnswer != "" {
				if c := cardFor(e.RexSession); c != nil {
					defer withLockWait(hookLockWait)()
					err = stopWhere(c, h.LastAnswer)
				}
			}
			return true, err
		}
	}
	c := cardFor(e.RexSession)
	if c != nil {
		e.Track = c.Name
	}
	// The event first: a card write can wait for the cards lock, and Claude Code kills a slow hook.
	restore := withLockWait(hookEventWait)
	err = appendEventFile(e)
	restore()
	if errors.Is(err, errSwitched) {
		// tracks migrate made tracks.db the source while the hook waited: all of it goes there.
		_, err = hookStore(e, h.SessionID, h.Transcript, h.Cwd)
		return true, err
	}
	if c != nil {
		defer withLockWait(hookLockWait)()
		err = errors.Join(err, recordClaudeSession(c, h.SessionID, h.Transcript, h.Cwd), markAnswered(c, e))
		if e.Event == "stop" && h.LastAnswer != "" {
			err = errors.Join(err, stopWhere(c, h.LastAnswer))
		}
	}
	return true, err
}

// markAnswered stamps `answered:` when Emil types into a track whose card has asks, so readers
// of `tracks ls --json` can stop counting those asks as waiting on him while the agent works.
// When the agent updates the card with tracks card, updated moves past answered; Stop clears
// the stamp either way, so it does not linger. The write also wakes anything watching the cards folder.
func markAnswered(c *Card, e Event) error {
	switch {
	case e.Event == "prompt" && e.Kind != "system" && len(c.askList()) > 0:
		return setMetas(c.Name, [][2]string{{"answered", e.At.Format(time.RFC3339)}})
	case e.Event == "stop" && c.Meta["answered"] != "":
		return setMetas(c.Name, clearAnswered(c))
	}
	return nil
}

// clearAnswered empties the answered stamp and, only where the card has it, the asks the pane recorded
// beside it, so a card never sent from the pane gains no empty line.
func clearAnswered(c *Card) [][2]string {
	kv := [][2]string{{"answered", ""}}
	if c.Meta["answered_asks"] != "" {
		kv = append(kv, [2]string{"answered_asks", ""})
	}
	return kv
}

func cardFor(session string) *Card {
	if session == "" {
		return nil
	}
	for _, c := range loadCards() {
		if c.Meta["rex_session"] == session {
			return c
		}
	}
	return nil
}
