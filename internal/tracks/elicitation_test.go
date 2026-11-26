package tracks

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const gatekeeperURL = "https://gatekeeper.example/confirm/abc123"

// elicitationHook runs tracks elicitation with payload, as Claude Code runs it, and returns what it printed.
func elicitationHook(t *testing.T, payload map[string]any) <-chan string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		var out, errb bytes.Buffer
		Run([]string{"elicitation"}, bytes.NewReader(b), &out, &errb)
		done <- out.String() + errb.String()
	}()
	return done
}

func askApproval(t *testing.T, id, mode string) <-chan string {
	t.Helper()
	t.Setenv(elicitationWaitEnv, "1m")
	return elicitationHook(t, map[string]any{"hook_event_name": "Elicitation", "session_id": id, "mcp_server_name": "gatekeeper",
		"message": "Approve merging acme/shop-api#42", "mode": mode, "url": gatekeeperURL, "elicitation_id": "e1"})
}

type elicitationOutput struct {
	Reason             string
	HookSpecificOutput struct {
		HookEventName string
		Action        string
	}
}

func elicitationOf(t *testing.T, out string) elicitationOutput {
	t.Helper()
	var doc elicitationOutput
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.HookSpecificOutput.HookEventName != "Elicitation" {
		t.Fatalf("not an Elicitation answer: %v %q", err, out)
	}
	return doc
}

func sessionWaitingFor(t *testing.T, id string) (status, waiting string) {
	t.Helper()
	var list []store.Session
	if err := withBareStore(func(st *store.Store) (err error) { list, err = st.DeskSessions(); return err }); err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s.Status, s.WaitingFor
		}
	}
	t.Fatalf("no session %s", id)
	return "", ""
}

func TestAnMCPApprovalOfADeskSessionBecomesEmilsQuestionWithItsPage(t *testing.T) {
	id, _ := deskStarted(t)
	setTier(t, "cleanup", "1: docs")
	fastPermission(t, "1m")
	exe, _ := os.Executable()
	if s := fileText(t, threadSettingsPath("cleanup")); !strings.Contains(s, `"command": "`+shellJoin([]string{exe, "elicitation"})+`"`) || !strings.Contains(s, `"timeout": 3600`) {
		t.Errorf("no Elicitation hook in the settings:\n%s", s)
	}
	seen := map[string]deskSeen{}
	if _, err := pollDesk(seen, true); err != nil {
		t.Fatal(err)
	}
	got := askApproval(t, id, "url")
	if line := questionOf(t, seen); line != "cleanup asks: gatekeeper: Approve merging acme/shop-api#42 D1" {
		t.Fatalf("question line %q", line)
	}
	if status, w := sessionWaitingFor(t, id); status != "blocked" || w != "approval in gatekeeper: Approve merging acme/shop-api#42" {
		t.Errorf("session %s %q", status, w)
	}
	if out, _ := do("decisions", "--json"); !strings.Contains(out, gatekeeperURL) || !strings.Contains(out, `"label": "Done, continue"`) {
		t.Errorf("the question lacks its page or options:\n%s", out)
	}
	// Even on a tier-1 track, approving is Emil's.
	if out, code := do("decide", "D1", "1", "--by", "desk"); code != 1 || !strings.Contains(out, "MCP approval is Emil's") {
		t.Errorf("the desk answered an approval: %d %q", code, out)
	}
	if out, code := do("permission-answer", "D1", "allow"); code != 1 || !strings.Contains(out, "not a permission prompt") {
		t.Errorf("permission-answer took an approval: %d %q", code, out)
	}
	select {
	case out := <-got:
		t.Fatalf("the hook printed before Emil answered: %q", out)
	case <-time.After(50 * time.Millisecond):
	}
	t.Setenv("REX_SESSION", "") // Emil answers from outside the desk's session
	if out, code := do("decide", "D1", "1"); code != 0 || out != "answered D1: Done, continue\n" {
		t.Fatalf("Emil's answer: %d %q", code, out)
	}
	if doc := elicitationOf(t, <-got); doc.HookSpecificOutput.Action != "accept" {
		t.Errorf("answer %+v", doc)
	}
	// Claude reports the answer as a notification; the session is not blocked by it.
	bgHook(t, map[string]any{"hook_event_name": "PreToolUse", "session_id": id, "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})
	bgHook(t, map[string]any{"hook_event_name": "Notification", "session_id": id, "notification_type": "elicitation_response", "message": "Elicitation response for server \"gatekeeper\": accept"})
	if status, _ := sessionWaitingFor(t, id); status != "working" {
		t.Errorf("the session after the approval: %s", status)
	}
}

func TestEmilDeclinesAnMCPApprovalWithHisWords(t *testing.T) {
	id, _ := deskStarted(t)
	fastPermission(t, "1m")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("REX_SESSION", "")
	seen := map[string]deskSeen{}
	got := askApproval(t, id, "url")
	questionOf(t, seen)
	if out, code := do("decide", "D1", "--text", "not before the freeze ends"); code != 0 {
		t.Fatalf("decline: %d %q", code, out)
	}
	if doc := elicitationOf(t, <-got); doc.HookSpecificOutput.Action != "decline" || doc.Reason != "not before the freeze ends" {
		t.Errorf("answer %+v", doc)
	}
	got = askApproval(t, id, "url")
	questionOf(t, seen)
	if out, code := do("decide", "D2", "2"); code != 0 || out != "answered D2: Decline\n" {
		t.Fatalf("decline: %d %q", code, out)
	}
	if doc := elicitationOf(t, <-got); doc.HookSpecificOutput.Action != "decline" || doc.Reason != "declined by emil" {
		t.Errorf("answer %+v", doc)
	}
}

func TestAFormElicitationIsLeftToClaudesOwnPrompt(t *testing.T) {
	id, _ := deskStarted(t)
	fastPermission(t, "1m")
	select {
	case out := <-askApproval(t, id, "form"):
		if out != "" {
			t.Errorf("printed %q", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hook waits on a form")
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) { qs, err = st.DeskQuestions(time.Time{}); return err }); err != nil || len(qs) != 0 {
		t.Errorf("questions %v %+v", err, qs)
	}
}

func TestAPromptQuestionKeepsTheWholeTextOrSaysItIsCut(t *testing.T) {
	long := "Merge #192 into main: the PR is approved and green; staging e2e passed on the head commit and the release notes are attached for the deploy that follows it"
	if got := elicitationQuestion("gatekeeper", long); got != "gatekeeper: "+long {
		t.Fatalf("a long approval message was cut: %q", got)
	}
	input := json.RawMessage(`{"command":"` + strings.Repeat("a ", 100) + `"}`)
	if got := permissionQuestion("Bash", input); !strings.Contains(got, strings.Repeat("a ", 99)+"a?") {
		t.Fatalf("a long permission input was cut: %q", got)
	}
	huge := strings.Repeat("x", promptQuestionMax+50)
	if got := wholePrompt(huge); !strings.HasSuffix(got, "(cut: 50 more characters)") {
		t.Fatalf("a cut prompt must say so: %q", got[len(got)-40:])
	}
	// The session's waiting_for line stays one short line.
	if got := elicitationWaitingFor("gatekeeper", long); len([]rune(got)) > waitingForMax {
		t.Fatalf("waiting_for grew: %d", len([]rune(got)))
	}
}
