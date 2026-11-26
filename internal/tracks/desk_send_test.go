package tracks

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// deskStarted starts the desk session cleanup (aaaaaaa1) with the fake claude, as the desk.
func deskStarted(t *testing.T) (id, bin string) {
	t.Helper()
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	return fullID("aaaaaaa1"), bin
}

type noteRow struct {
	From, Text string
	Held       bool
	Delivered  bool
}

// notes are the notes sent to a session, oldest first, without the start prompt.
func notes(t *testing.T, session string) []noteRow {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT from_kind, text, held, delivered_at IS NOT NULL FROM messages
		WHERE session_id = ? AND kind = 'note' AND text NOT LIKE '/track %' ORDER BY id`, session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []noteRow
	for rows.Next() {
		var n noteRow
		if err := rows.Scan(&n.From, &n.Text, &n.Held, &n.Delivered); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestSendDeliversANoteToAnIdleDeskSessionAtOnce(t *testing.T) {
	id, bin := deskStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Opened PR 12."})
	if out, code := do("send", "cleanup", "  - also run the linter"); code != 0 || out != "delivered to cleanup\n" {
		t.Fatalf("send: %d %q", code, out)
	}
	if calls := fileText(t, filepath.Join(bin, "claude.calls")); calls != "stop aaaaaaa1\nresume "+id+" -- 5\n" {
		t.Errorf("claude calls:\n%s", calls)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != "- also run the linter\n<end>\n" {
		t.Errorf("delivered text %q", n)
	}
	if n := notes(t, id); len(n) != 1 || n[0].From != "desk" || n[0].Held || !n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
	// The resumed prompt is the note, so the session stays the desk's.
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "- also run the linter"})
	if s, _ := deskSessionRow(t, id); s.Owner != "desk" {
		t.Errorf("the delivered note handed the session to Emil: %+v", s)
	}
	// Emil sends too, by short id, from outside the desk.
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Linted."})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if out, code := do("send", "aaaaaaa1", "thanks"); code != 0 || out != "delivered to cleanup\n" {
		t.Fatalf("send by short id: %d %q", code, out)
	}
	if n := notes(t, id); len(n) != 2 || n[1].From != "emil" || n[1].Text != "thanks" {
		t.Errorf("notes %+v", n)
	}
}

func TestSendHoldsOneNoteWhileTheSessionWorksAndTheWatchDeliversItAtStop(t *testing.T) {
	id, bin := deskStarted(t)
	for _, c := range []struct{ args []string }{
		{[]string{"send", "cleanup", "first thing"}},
		{[]string{"send", "cleanup", "second thing"}},
	} {
		if out, code := do(c.args...); code != 0 || out != "held for cleanup: delivered when it stops\n" {
			t.Fatalf("%v: %d %q", c.args, code, out)
		}
	}
	if n := notes(t, id); len(n) != 1 || n[0].Text != "first thing\n\nsecond thing" || !n[0].Held || n[0].Delivered {
		t.Fatalf("appended: %+v", n)
	}
	if out, code := do("send", id, "only this", "--replace"); code != 0 || out != "held for cleanup: delivered when it stops\n" {
		t.Fatalf("replace: %d %q", code, out)
	}
	if n := notes(t, id); len(n) != 1 || n[0].Text != "only this" {
		t.Fatalf("replaced: %+v", n)
	}
	// Blocked holds too.
	bgHook(t, map[string]any{"hook_event_name": "Notification", "session_id": id, "message": "Claude needs your permission to use Bash"})
	if out := waitDesk(t); strings.Contains(out, "delivered") || exists(filepath.Join(bin, "claude.calls")) {
		t.Fatalf("delivered to a blocked session:\n%s", out)
	}
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "/track cleanup"})
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Done with the first part."})
	if out := waitDesk(t); out != "delivered note to cleanup\ncleanup done: Done with the first part.\n" {
		t.Fatalf("wait --desk:\n%s", out)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != "only this\n<end>\n" {
		t.Errorf("delivered text %q", n)
	}
	if n := notes(t, id); len(n) != 1 || n[0].Held || !n[0].Delivered {
		t.Errorf("after delivery: %+v", n)
	}
	// Delivering stops the session for a moment: the watch does not report that.
	bgHook(t, map[string]any{"hook_event_name": "SessionEnd", "session_id": id})
	if out := waitDesk(t); strings.Contains(out, "stopped") || strings.Contains(out, "delivered") {
		t.Errorf("after delivery:\n%s", out)
	}
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "only this"})
	if s, _ := deskSessionRow(t, id); s.Owner != "desk" {
		t.Errorf("the delivered note handed the session to Emil: %+v", s)
	}
}

func TestAFailedDeliveryKeepsTheNoteHeld(t *testing.T) {
	id, bin := deskStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "ok"})
	if err := os.WriteFile(filepath.Join(bin, "claude.resumefail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := do("send", "cleanup", "go on"); code == 0 || !strings.Contains(out, "No conversation found") {
		t.Fatalf("send: %d %q", code, out)
	}
	if n := notes(t, id); len(n) != 1 || !n[0].Held || n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
}

func TestSendRefusesASessionEmilTookOver(t *testing.T) {
	id, bin := deskStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "I'll do this myself"})
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "ok"})
	if out, code := do("send", "cleanup", "go on"); code != 1 || out != "tracks: refused: cleanup is Emil's\n" {
		t.Fatalf("send: %d %q", code, out)
	}
	if n := notes(t, id); len(n) != 0 || exists(filepath.Join(bin, "claude.calls")) {
		t.Errorf("a refused send left %+v", n)
	}
	if out, code := do("send", "cleanup", "go on", "--replace"); code != 1 || !strings.Contains(out, "Emil's") {
		t.Errorf("send --replace: %d %q", code, out)
	}
}

// permissionHook runs tracks permission with payload, as Claude Code runs it, and returns what it printed.
func permissionHook(t *testing.T, payload map[string]any) <-chan string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		var out, errb bytes.Buffer
		Run([]string{"permission"}, bytes.NewReader(b), &out, &errb)
		done <- out.String() + errb.String()
	}()
	return done
}

func fastPermission(t *testing.T, wait string) {
	t.Helper()
	old := permissionPoll
	permissionPoll = 10 * time.Millisecond
	t.Cleanup(func() { permissionPoll = old })
	t.Setenv(permissionWaitEnv, wait)
}

func askPermission(t *testing.T, id string, suggestions any) <-chan string {
	t.Helper()
	p := map[string]any{"hook_event_name": "PermissionRequest", "session_id": id, "tool_name": "Bash",
		"tool_input": map[string]any{"command": "gh pr create --fill"}}
	if suggestions != nil {
		p["permission_suggestions"] = suggestions
	}
	return permissionHook(t, p)
}

// questionOf waits for the watch to print a question and returns the line.
func questionOf(t *testing.T, seen map[string]deskSeen) string {
	t.Helper()
	for range 200 {
		lines, err := pollDesk(seen, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if strings.Contains(l, " asks: ") {
				return l
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no question")
	return ""
}

func hookDecisionOf(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc struct {
		HookSpecificOutput struct {
			HookEventName string
			Decision      map[string]any
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.HookSpecificOutput.HookEventName != "PermissionRequest" {
		t.Fatalf("not a PermissionRequest decision: %v %q", err, out)
	}
	return doc.HookSpecificOutput.Decision
}

// setTier sets a track's tier as its agent would, with tracks card.
func setTier(t *testing.T, track, tier string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"card", track}, strings.NewReader("tier: "+tier+"\n"), &out, &errb); code != 0 {
		t.Fatalf("card: %s%s", out.String(), errb.String())
	}
}

var ghSuggestion = []map[string]any{{"type": "addRules", "rules": []map[string]any{{"toolName": "Bash", "ruleContent": "gh pr create:*"}},
	"behavior": "allow", "destination": "localSettings"}}

func TestAPermissionPromptOfADeskSessionBecomesAQuestionTheDeskAnswers(t *testing.T) {
	id, _ := deskStarted(t)
	setTier(t, "cleanup", "2: product code")
	fastPermission(t, "1m")
	settings := fileText(t, threadSettingsPath("cleanup"))
	exe, _ := os.Executable()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string
				Timeout int
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &doc); err != nil {
		t.Fatal(err)
	}
	if p := doc.Hooks["PermissionRequest"]; len(p) == 0 || p[0].Hooks[0].Command != shellJoin([]string{exe, "permission"}) || p[0].Hooks[0].Timeout != 3600 {
		t.Errorf("PermissionRequest hooks: %+v", p)
	}
	seen := map[string]deskSeen{}
	if _, err := pollDesk(seen, true); err != nil {
		t.Fatal(err)
	}
	got := askPermission(t, id, ghSuggestion)
	line := questionOf(t, seen)
	if line != "cleanup asks: Allow Bash: gh pr create --fill? D1" {
		t.Fatalf("question line %q", line)
	}
	if out, _ := do("decisions", "--json"); !strings.Contains(out, `"label": "Allow for this session: Bash(gh pr create:*)"`) {
		t.Errorf("the session option does not show its rule:\n%s", out)
	}
	// The session's own hook reports the block: the question already says it.
	bgHook(t, map[string]any{"hook_event_name": "PermissionRequest", "session_id": id, "tool_name": "Bash", "tool_input": map[string]any{"command": "gh pr create --fill"}})
	if lines, _ := pollDesk(seen, false); len(lines) != 0 {
		t.Errorf("the block came twice: %q", lines)
	}
	select {
	case out := <-got:
		t.Fatalf("the hook printed before an answer: %q", out)
	case <-time.After(50 * time.Millisecond):
	}
	if out, code := do("permission-answer", "D1", "session"); code != 1 || !strings.Contains(out, "Allow once or Deny only") {
		t.Errorf("the desk allowed for a whole session: %d %q", code, out)
	}
	if out, code := do("permission-answer", "D1", "allow"); code != 0 || out != "answered D1: Allow once\n" {
		t.Fatalf("answer: %d %q", code, out)
	}
	out := <-got
	if d := hookDecisionOf(t, out); d["behavior"] != "allow" || len(d) != 1 {
		t.Errorf("decision %v", d)
	}
	if h := readDesk(t).Handled; len(h) != 2 || h[1].Kind != "answered" || h[1].Did != "answered D1: Allow once" {
		t.Errorf("the desk's answer was not logged: %+v", h)
	}
	if out, code := do("permission-answer", "D1", "deny"); code == 0 || !strings.Contains(out, "closed") {
		t.Errorf("answered twice: %d %q", code, out)
	}
}

func TestTheDeskLeavesPermissionsOnTier3AndOnEmilsSessionsToEmil(t *testing.T) {
	id, _ := deskStarted(t)
	setTier(t, "cleanup", "3: sends text to agents")
	fastPermission(t, "1m")
	seen := map[string]deskSeen{}
	got := askPermission(t, id, nil)
	questionOf(t, seen)
	for _, a := range []string{"allow", "deny"} {
		if out, code := do("permission-answer", "D1", a); code != 1 || !strings.Contains(out, "not tier 1 or 2") {
			t.Errorf("the desk answered %s on tier 3: %d %q", a, code, out)
		}
	}
	if out, code := do("decide", "D1", "1", "--by", "desk"); code != 1 || !strings.Contains(out, "not tier 1 or 2") {
		t.Errorf("tracks decide let the desk answer on tier 3: %d %q", code, out)
	}
	setTier(t, "cleanup", "2: product code")
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "I'll take this one"})
	// Refused either way: as Emil's, or as closed when the hook let go first.
	if out, code := do("permission-answer", "D1", "allow"); code != 1 || !strings.Contains(out, "Emil's now") && !strings.Contains(out, "closed") {
		t.Errorf("the desk answered Emil's session: %d %q", code, out)
	}
	// He has the session in front of him now: the hook lets go and closes the question, so
	// the prompt is his to answer in the session.
	if out := <-got; out != "" {
		t.Errorf("the hook answered a session Emil took over: %q", out)
	}
	t.Setenv("REX_SESSION", "") // Emil answers from outside the desk's session
	if out, code := do("decide", "D1", "1"); code == 0 || !strings.Contains(out, "closed") {
		t.Errorf("answered a prompt of a session Emil took over: %d %q", code, out)
	}
}

func TestTwoHooksOnOnePromptShareOneQuestion(t *testing.T) {
	id, _ := deskStarted(t)
	fastPermission(t, "1m")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("REX_SESSION", "")
	seen := map[string]deskSeen{}
	// The session's own hook and Emil's global one both run tracks permission.
	first := askPermission(t, id, nil)
	questionOf(t, seen)
	second := askPermission(t, id, nil)
	time.Sleep(50 * time.Millisecond)
	if out, _ := do("decisions", "--json"); strings.Count(out, "Allow Bash: gh pr create --fill?") != 1 {
		t.Fatalf("one prompt asked twice:\n%s", out)
	}
	if out, code := do("decide", "D1", "1"); code != 0 {
		t.Fatalf("decide: %d %q", code, out)
	}
	for _, got := range []<-chan string{first, second} {
		if d := hookDecisionOf(t, <-got); d["behavior"] != "allow" {
			t.Errorf("decision %v", d)
		}
	}
	// The same command asked again later is a new question.
	third := askPermission(t, id, nil)
	questionOf(t, seen)
	if out, code := do("decide", "D2", "2"); code != 0 || out != "answered D2: Deny\n" {
		t.Fatalf("second prompt: %d %q", code, out)
	}
	if d := hookDecisionOf(t, <-third); d["behavior"] != "deny" {
		t.Errorf("decision %v", d)
	}
}

func TestEmilAnswersAPermissionForTheSessionOrDeniesIt(t *testing.T) {
	id, _ := deskStarted(t)
	fastPermission(t, "1m")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	seen := map[string]deskSeen{}
	got := askPermission(t, id, ghSuggestion)
	questionOf(t, seen)
	if out, code := do("permission-answer", "1", "session"); code != 0 || out != "answered D1: Allow for this session: Bash(gh pr create:*)\n" {
		t.Fatalf("answer: %d %q", code, out)
	}
	d := hookDecisionOf(t, <-got)
	perms, _ := d["updatedPermissions"].([]any)
	if d["behavior"] != "allow" || len(perms) != 1 || perms[0].(map[string]any)["destination"] != "session" ||
		perms[0].(map[string]any)["type"] != "addRules" {
		t.Errorf("decision %v", d)
	}
	got = askPermission(t, id, ghSuggestion)
	questionOf(t, seen)
	if out, code := do("permission-answer", "D2", "deny", "--text", "open it as a draft"); code != 0 || out != "answered D2: open it as a draft\n" {
		t.Fatalf("deny: %d %q", code, out)
	}
	if d := hookDecisionOf(t, <-got); d["behavior"] != "deny" || d["message"] != "open it as a draft" {
		t.Errorf("decision %v", d)
	}
	got = askPermission(t, id, nil)
	questionOf(t, seen)
	t.Setenv("REX_SESSION", "") // Emil answers from outside the desk's session
	if out, code := do("decide", "D3", "2"); code != 0 || out != "answered D3: Deny\n" {
		t.Fatalf("decide: %d %q", code, out)
	}
	if d := hookDecisionOf(t, <-got); d["behavior"] != "deny" || d["message"] != "denied by emil" {
		t.Errorf("decision %v", d)
	}
	if h := readDesk(t).Handled; len(h) != 1 {
		t.Errorf("Emil's answers were logged as the desk's: %+v", h)
	}
}

func TestAnUnansweredPermissionTimesOutAndLeavesThePrompt(t *testing.T) {
	id, _ := deskStarted(t)
	fastPermission(t, "150ms")
	seen := map[string]deskSeen{}
	got := askPermission(t, id, nil)
	questionOf(t, seen)
	if out := <-got; out != "" {
		t.Errorf("the timed-out hook printed %q", out)
	}
	lines, err := pollDesk(seen, false)
	if err != nil || len(lines) != 1 || lines[0] != "cleanup D1 closed: timed out: answer with claude attach aaaaaaa1" {
		t.Errorf("after the timeout: %v %q", err, lines)
	}
	if out, code := do("permission-answer", "D1", "allow"); code == 0 || !strings.Contains(out, "closed") {
		t.Errorf("answered a closed question: %d %q", code, out)
	}
}

func TestThePermissionHookLeavesEmilsSessionsAlone(t *testing.T) {
	startHome(t)
	fastPermission(t, "1m")
	hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": sessA, "source": "startup"})
	select {
	case out := <-askPermission(t, sessA, nil):
		if out != "" {
			t.Errorf("printed %q", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hook waits on a session the desk does not own")
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) { qs, err = st.DeskQuestions(time.Time{}); return err }); err != nil || len(qs) != 0 {
		t.Errorf("questions %v %+v", err, qs)
	}
}
