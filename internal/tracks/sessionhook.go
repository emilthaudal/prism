package tracks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// waitingForMax keeps a permission prompt's tool input to one short line.
const waitingForMax = 120

// activityMax keeps a session's activity to one short line.
const activityMax = 80

var errNoHookTime = errors.New("session not recorded: the hook's time ran out")

// sessionEventOf maps a hook payload to the write it makes on the session's row; ok is false
// for events that do not change a session.
func sessionEventOf(h hookInput) (store.SessionEvent, bool) {
	if !claudeSessionID.MatchString(h.SessionID) {
		return store.SessionEvent{}, false
	}
	e := store.SessionEvent{
		ID:         h.SessionID,
		Source:     h.Source,
		Background: os.Getenv("CLAUDE_JOB_DIR") != "",
		RexSession: os.Getenv("REX_SESSION"),
		Cwd:        h.Cwd,
		Model:      h.Model,
	}
	if p := os.Getenv(deskParentEnv); claudeSessionID.MatchString(p) {
		e.DeskParent, e.StartPrompt = p, os.Getenv(deskPromptEnv)
	}
	switch h.Event {
	case "PreToolUse":
		if h.ToolName == "" {
			return e, false
		}
		// A subagent's own tool calls are not the session's activity, but one means its prompt
		// was answered.
		if h.AgentID != "" {
			e.Event, e.AgentID = store.SessionAgentTool, h.AgentID
			return e, true
		}
		e.Event, e.Activity = store.SessionTool, activityOf(h.ToolName, h.ToolInput)
		if isAgentTool(h.ToolName) {
			e.Event, e.AgentID, e.AgentName = store.SessionAgentQueued, h.ToolUseID, toolInput(h.ToolInput)["description"]
		}
	case "SubagentStart":
		e.Event, e.AgentID, e.AgentName = store.SessionAgentStart, h.AgentID, h.AgentType
	case "SubagentStop":
		e.Event, e.AgentID, e.AgentName = store.SessionAgentStop, h.AgentID, h.AgentType
	case "SessionStart":
		e.Event = store.SessionStart
	case "UserPromptSubmit":
		e.Event, e.Prompt = store.SessionPrompt, h.Prompt
	case "Stop":
		e.Event, e.LastAnswer = store.SessionStop, h.LastAnswer
		var tasks []json.RawMessage
		_ = json.Unmarshal(h.BackgroundTasks, &tasks)
		e.BackgroundTasks = len(tasks)
	case "Notification":
		// Claude says it waits for input after a minute idle: that is not a block.
		if h.NotificationType == "idle_prompt" {
			return e, false
		}
		// Claude reports an MCP approval's answer as a notification: the session goes on.
		if h.NotificationType == "elicitation_response" || h.NotificationType == "elicitation_complete" {
			return e, false
		}
		e.Event, e.WaitingFor = store.SessionBlocked, h.Message
	case "PermissionRequest":
		e.Event, e.WaitingFor = store.SessionBlocked, permissionWaitingFor(h.ToolName, h.ToolInput)
	case "Elicitation":
		e.Event, e.WaitingFor = store.SessionBlocked, elicitationWaitingFor(h.MCPServer, h.Message)
	case "SessionEnd":
		e.Event = store.SessionEnd
	default:
		return e, false
	}
	return e, true
}

func isAgentTool(tool string) bool { return tool == "Agent" || tool == "Task" }

func toolInput(input json.RawMessage) map[string]string {
	var raw map[string]any
	_ = json.Unmarshal(input, &raw)
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// activityOf is "TOOL: what it works on" for a tool call, cut to one short line.
func activityOf(tool string, input json.RawMessage) string {
	f := toolInput(input)
	var what string
	switch tool {
	case "Bash":
		what = f["command"]
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		what = filepath.Base(f["file_path"] + f["notebook_path"])
		if what == "." {
			what = ""
		}
	case "Agent", "Task":
		what = f["description"]
	case "Grep", "Glob":
		what = f["pattern"]
	case "WebFetch":
		what = f["url"]
	case "WebSearch":
		what = f["query"]
	}
	return oneShortLine(tool, what, activityMax)
}

func oneShortLine(tool, what string, max int) string {
	line := strings.Join(strings.Fields(tool+": "+what), " ")
	if what == "" {
		line = tool
	}
	if r := []rune(line); len(r) > max {
		line = string(r[:max-1]) + "…"
	}
	return line
}

// permissionWaitingFor is "permission: TOOL INPUT" with the input cut to one short line.
func permissionWaitingFor(tool string, input json.RawMessage) string {
	return strings.TrimSpace("permission: " + tool + " " + permissionInput(input))
}

// permissionInput is a tool's input as one short line: a command or a file path when the tool
// has one, else the input as JSON.
func permissionInput(input json.RawMessage) string {
	short := permissionInputFull(input)
	if r := []rune(short); len(r) > waitingForMax {
		short = string(r[:waitingForMax-1]) + "…"
	}
	return short
}

// permissionInputFull is permissionInput on one line without the cut.
func permissionInputFull(input json.RawMessage) string {
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	short := ""
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if v, ok := fields[k].(string); ok && v != "" {
			short = v
			break
		}
	}
	if short == "" && len(input) > 0 && string(input) != "null" {
		short = string(input)
	}
	return strings.Join(strings.Fields(short), " ")
}

// sessionHook writes the session's row within what is left of the hook's time, without
// rendering a view.
func sessionHook(e store.SessionEvent, start time.Time) error {
	left := hookBudget - time.Since(start)
	if left <= 0 {
		return errNoHookTime
	}
	return withBareStore(func(st *store.Store) error { return st.SessionHook(e) }, store.WithBusyTimeout(min(hookLockWait, left)))
}
