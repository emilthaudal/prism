package tracks

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const sessAgent1, sessAgent2 = "a2377ab21f2603674", "a9f00d11e22c33d44"

func activityHome(t *testing.T) func(name string, more map[string]any) string {
	t.Helper()
	setup(t)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	importNow(t)
	return func(name string, more map[string]any) string {
		t.Helper()
		f := map[string]any{"hook_event_name": name, "session_id": sessA, "cwd": "/work/tr"}
		for k, v := range more {
			f[k] = v
		}
		return hookJSON(t, f)
	}
}

func subagentsOf(t *testing.T, id string) []store.Subagent {
	t.Helper()
	var all []store.Subagent
	if err := withBareStore(func(st *store.Store) (err error) { all, err = st.Subagents(); return err }); err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(x store.Subagent) bool { return x.Session != id })
}

func TestHookRecordsTheActivityFromPreToolUseAndClearsItOnStop(t *testing.T) {
	ev := activityHome(t)
	ev("SessionStart", map[string]any{"source": "startup"})
	for _, c := range []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "go test ./...\n  -run X"}, "Bash: go test ./... -run X"},
		{"Edit", map[string]any{"file_path": "/work/tr/internal/tracks/serve.go"}, "Edit: serve.go"},
		{"Agent", map[string]any{"description": "PHP 8.5 delta rerun", "prompt": "long"}, "Agent: PHP 8.5 delta rerun"},
		{"Grep", map[string]any{"pattern": "TODO"}, "Grep: TODO"},
		{"mcp__linear__get_issue", map[string]any{"id": "EMI-1"}, "mcp__linear__get_issue"},
	} {
		if out := ev("PreToolUse", map[string]any{"tool_name": c.tool, "tool_input": c.input, "tool_use_id": "toolu_1"}); out != "" {
			t.Errorf("%s printed %q", c.tool, out)
		}
		s := sessionRow(t, sessA)
		if s.Activity != c.want || s.Status != "working" {
			t.Errorf("%s: activity %q status %q, want %q working", c.tool, s.Activity, s.Status, c.want)
		}
	}
	long := strings.Repeat("x", 300)
	ev("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": long}})
	if a := sessionRow(t, sessA).Activity; len([]rune(a)) != activityMax || !strings.HasSuffix(a, "…") {
		t.Errorf("a long command is cut to %d characters: %q", activityMax, a)
	}
	ev("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}, "agent_id": sessAgent1})
	if a := sessionRow(t, sessA).Activity; !strings.HasPrefix(a, "Bash: xxx") {
		t.Errorf("a subagent's tool call changed the session's activity: %q", a)
	}
	ev("Stop", map[string]any{"last_assistant_message": "done"})
	if s := sessionRow(t, sessA); s.Activity != "" || s.Status != "done" {
		t.Errorf("after Stop: %q %q", s.Activity, s.Status)
	}
}

func TestHookRecordsSubagentsFromTheSubagentEvents(t *testing.T) {
	ev := activityHome(t)
	ev("SessionStart", map[string]any{"source": "startup"})
	ev("UserPromptSubmit", map[string]any{"prompt": "go"})
	ev("PreToolUse", map[string]any{"tool_name": "Agent", "tool_use_id": "t1", "tool_input": map[string]any{"description": "PHP 8.5 delta rerun"}})
	ev("PreToolUse", map[string]any{"tool_name": "Agent", "tool_use_id": "t2", "tool_input": map[string]any{"description": "lint sweep"}})
	if got := subagentsOf(t, sessA); len(got) != 0 {
		t.Errorf("a subagent that has not started is not listed: %+v", got)
	}
	ev("SubagentStart", map[string]any{"agent_id": sessAgent1, "agent_type": "general-purpose"})
	ev("SubagentStart", map[string]any{"agent_id": sessAgent2, "agent_type": "general-purpose"})
	want := func(label string, w ...[2]string) {
		t.Helper()
		var got [][2]string
		for _, x := range subagentsOf(t, sessA) {
			got = append(got, [2]string{x.Name, x.Status})
		}
		if !slices.Equal(got, w) {
			t.Errorf("%s: %v, want %v", label, got, w)
		}
	}
	want("both started", [2]string{"PHP 8.5 delta rerun", "working"}, [2]string{"lint sweep", "working"})
	ev("SubagentStop", map[string]any{"agent_id": sessAgent1, "agent_type": "general-purpose", "background_tasks": []any{}})
	want("one stopped", [2]string{"PHP 8.5 delta rerun", "done"}, [2]string{"lint sweep", "working"})
	ev("SubagentStart", map[string]any{"agent_id": "a3", "agent_type": "Explore"})
	want("one with no Agent call is named by its type", [2]string{"PHP 8.5 delta rerun", "done"}, [2]string{"lint sweep", "working"}, [2]string{"Explore", "working"})
	ev("Stop", map[string]any{"last_assistant_message": "x", "background_tasks": []any{map[string]any{"id": "b1"}}})
	want("a Stop with background tasks keeps the working ones", [2]string{"PHP 8.5 delta rerun", "done"}, [2]string{"lint sweep", "working"}, [2]string{"Explore", "working"})
	ev("Stop", map[string]any{"last_assistant_message": "x", "background_tasks": []any{}})
	want("a Stop with none ends them", [2]string{"PHP 8.5 delta rerun", "done"}, [2]string{"lint sweep", "done"}, [2]string{"Explore", "done"})
	ev("UserPromptSubmit", map[string]any{"prompt": "next"})
	want("a new prompt clears the finished ones")
}

func TestSnapshotCarriesActivityAndSubagents(t *testing.T) {
	_, c := liveHome(t)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv(deskParentEnv, sessB)
	ev := func(name string, more map[string]any) {
		f := map[string]any{"hook_event_name": name, "session_id": sessA, "cwd": "/work/tr"}
		for k, v := range more {
			f[k] = v
		}
		hookJSON(t, f)
	}
	ev("SessionStart", map[string]any{"source": "startup"})
	ev("PreToolUse", map[string]any{"tool_name": "Agent", "tool_use_id": "t1", "tool_input": map[string]any{"description": "rerun"}})
	ev("SubagentStart", map[string]any{"agent_id": sessAgent1, "agent_type": "general-purpose"})
	ev("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "go test ./..."}})
	_, b := c.req(http.MethodGet, "/v1/snapshot", serveToken, "")
	var raw struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(raw.Sessions, func(s map[string]any) bool { return s["id"] == sessA })
	if i < 0 {
		t.Fatalf("session missing: %s", b)
	}
	s := raw.Sessions[i]
	if s["activity"] != "Bash: go test ./..." {
		t.Errorf("activity %v", s["activity"])
	}
	agents, _ := s["subagents"].([]any)
	if len(agents) != 1 || agents[0].(map[string]any)["name"] != "rerun" || agents[0].(map[string]any)["status"] != "working" {
		t.Errorf("subagents %v", s["subagents"])
	}
	ev("Stop", map[string]any{"last_assistant_message": "ok"})
	_, b = c.req(http.MethodGet, "/v1/snapshot", serveToken, "")
	_ = json.Unmarshal(b, &raw)
	s = raw.Sessions[slices.IndexFunc(raw.Sessions, func(s map[string]any) bool { return s["id"] == sessA })]
	if s["activity"] != "" || s["subagents"] == nil {
		t.Errorf("after Stop: activity %v, subagents %v", s["activity"], s["subagents"])
	}
}

// A PreToolUse hook runs before every tool call, so its whole processing must stay well inside
// the hook's time.
func TestPreToolUseHookStaysUnderFiftyMilliseconds(t *testing.T) {
	ev := activityHome(t)
	ev("SessionStart", map[string]any{"source": "startup"})
	const runs = 30
	var slowest, total time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		ev("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "go test ./..."}})
		d := time.Since(start)
		total += d
		slowest = max(slowest, d)
	}
	t.Logf("PreToolUse hook: mean %v, slowest %v over %d runs", total/runs, slowest, runs)
	if total/runs > 50*time.Millisecond {
		t.Errorf("mean %v is over 50ms", total/runs)
	}
}
