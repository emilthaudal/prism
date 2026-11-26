package tracks

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// deskClaude is the desk's own Claude session in these tests.
const deskClaude = "dddddddd-0000-4a4a-8888-dddddddddddd"

// fakeClaude puts a claude on PATH that starts background sessions as claude --bg does: it
// prints "backgrounded · ID · NAME" and lists the session in claude agents --json, with a pid
// that claude stop sets to 0 and claude --bg --resume gives back. With
// claude.untrusted in bin it refuses the folder, as claude does. claude --bg --resume ID -- TEXT
// logs "resume ID -- ARGC" in claude.calls and appends TEXT to claude.notes; with
// claude.resumefail in bin it fails.
func fakeClaude(t *testing.T, bin string) {
	t.Helper()
	body := `d=` + shellQuote(bin) + `
case "$1" in
agents) printf '['; paste -sd, "$d/claude.agents" 2>/dev/null | tr -d '\n'; printf ']\n'; exit 0;;
stop|rm) echo "$@" >> "$d/claude.calls"
  [ "$1" = stop ] && [ -f "$d/claude.agents" ] && sed "/\"id\":\"$2\"/s/\"pid\":[0-9]*/\"pid\":0/" "$d/claude.agents" > "$d/claude.agents.tmp" && mv "$d/claude.agents.tmp" "$d/claude.agents"
  exit 0;;
esac
if [ "$1" = --bg ] && [ "$2" = --resume ]; then
  [ -f "$d/claude.resumefail" ] && { echo "No conversation found with session ID: $3" >&2; exit 1; }
  echo "resume $3 $4 $#" >> "$d/claude.calls"
  [ -f "$d/claude.agents" ] && sed "/\"sessionId\":\"$3\"/s/\"pid\":0/\"pid\":4999/" "$d/claude.agents" > "$d/claude.agents.tmp" && mv "$d/claude.agents.tmp" "$d/claude.agents"
  printf '%s\n<end>\n' "$5" >> "$d/claude.notes"
  exit 0
fi
printf '%s\n' "$@" > "$d/claude.bgargs"
env | grep -E '^(REX_SESSION|REX_BLOCK|REX_SERVER|CLAUDE_CODE_SESSION_ID)=' > "$d/claude.env"
pwd -P > "$d/claude.cwd"
if [ -f "$d/claude.untrusted" ]; then echo "Workspace not trusted: run claude in $(pwd) once and accept the trust dialog" >&2; exit 1; fi
n=$(( $(cat "$d/claude.n" 2>/dev/null || echo 0) + 1 )); echo $n > "$d/claude.n"
short="aaaaaaa$n"
while [ $# -gt 0 ]; do [ "$1" = -n ] && name=$2; shift; done
echo "{\"id\":\"$short\",\"sessionId\":\"$short-1111-4a4a-8888-aaaaaaaaaaaa\",\"kind\":\"background\",\"pid\":$((4000 + n))}" >> "$d/claude.agents"
echo "Starting background service…"
echo "backgrounded · $short · $name"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fullID(short string) string { return short + "-1111-4a4a-8888-aaaaaaaaaaaa" }

// asDeskInClaude runs the test as the desk's agent, in the desk's Claude session, with the fake claude.
func asDeskInClaude(t *testing.T, bin string) {
	t.Helper()
	asDesk(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", deskClaude)
	fakeClaude(t, bin)
}

// bgHook runs tracks hook as a background session's hook.
func bgHook(t *testing.T, fields map[string]any) {
	t.Helper()
	t.Setenv("CLAUDE_JOB_DIR", "/jobs/x")
	t.Setenv("REX_SESSION", "")
	defer t.Setenv("REX_SESSION", deskSess)
	hookJSON(t, fields)
}

func deskSessionRow(t *testing.T, id string) (store.Session, bool) {
	t.Helper()
	var list []store.Session
	if err := withBareStore(func(st *store.Store) (err error) { list, err = st.DeskSessions(); return err }); err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return store.Session{}, false
}

func waitDesk(t *testing.T) string {
	t.Helper()
	old := waitPoll
	waitPoll = 10 * time.Millisecond
	defer func() { waitPoll = old }()
	out, _ := do("wait", "--desk", "--timeout", "50ms")
	return out
}

func TestTheDesksStartRunsClaudeInTheBackground(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	out, code := do("start", "make the refund retries work again", "--dir", th, "--name", "refund-retries", "--model", "sonnet")
	if code != 0 || out != "started refund-retries in the background as aaaaaaa1\n" {
		t.Fatalf("start: %d %q", code, out)
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Error("the desk's start made a Rex session")
	}
	settings := threadSettingsPath("refund-retries")
	args, _ := os.ReadFile(filepath.Join(bin, "claude.bgargs"))
	if want := "--bg\n-n\nrefund-retries\n--settings\n" + settings + "\n--model\nsonnet\n--effort\nmedium\n/track refund-retries\n"; string(args) != want {
		t.Errorf("claude args:\n%s\nwant:\n%s", args, want)
	}
	cwd, _ := os.ReadFile(filepath.Join(bin, "claude.cwd"))
	if real, _ := filepath.EvalSymlinks(th); strings.TrimSpace(string(cwd)) != real {
		t.Errorf("claude ran in %s, not %s", cwd, real)
	}
	var doc struct {
		Env   map[string]string
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile(settings)
	if err != nil || json.Unmarshal(b, &doc) != nil {
		t.Fatalf("settings: %v %s", err, b)
	}
	exe, _ := os.Executable()
	if stop := doc.Hooks["Stop"]; len(stop) != 2 || stop[0].Hooks[0].Command != shellJoin([]string{exe, "verify", "refund-retries"}) ||
		stop[1].Hooks[0].Command != shellJoin([]string{exe, "hook"}) {
		t.Errorf("Stop hooks: %+v", stop)
	}
	if e := doc.Env; e["TRACKS_HOME"] != th || e["REX_SESSION"] != "" || e["REX_BLOCK"] != "" || !strings.HasPrefix(e["PATH"], filepath.Dir(exe)+":") {
		t.Errorf("the session's environment: %+v", e)
	}
	if doc.Env[deskParentEnv] != deskClaude || doc.Env[deskPromptEnv] != "/track refund-retries" {
		t.Errorf("the session's hook does not know its parent and start prompt: %+v", doc.Env)
	}
	if e, _ := os.ReadFile(filepath.Join(bin, "claude.env")); len(e) != 0 {
		t.Errorf("claude --bg got the desk's own session in its environment:\n%s", e)
	}
	if _, ok := doc.Env["REX_SESSION"]; !ok {
		t.Error("the session may keep the Rex session of whoever started Claude's background service")
	}
	s, ok := deskSessionRow(t, fullID("aaaaaaa1"))
	if !ok || s.Owner != "desk" || s.Host != "background" || s.ParentID != deskClaude || s.Track != "refund-retries" || s.Status != "working" || s.Model != "sonnet" {
		t.Errorf("the session row: %+v", s)
	}
	if h := readDesk(t).Handled; len(h) != 1 || h[0].Kind != "started" || h[0].Track != "refund-retries" {
		t.Errorf("handled %+v", h)
	}
	// The start prompt reaching the session is the desk's, not Emil's.
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": fullID("aaaaaaa1"), "prompt": "/track refund-retries"})
	if s, _ := deskSessionRow(t, fullID("aaaaaaa1")); s.Owner != "desk" {
		t.Errorf("the start prompt handed the session to Emil: %+v", s)
	}
}

func TestTheDesksStartTakesItsModeFromTheFlagAndDefaultsToBackground(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("mode", "boss", "background"); code != 0 { // focus holds two: make room
		t.Fatalf("making room: %d %q", code, out)
	}
	for _, c := range []struct{ name, flag, want string }{
		{"no-mode", "", "background"},
		{"in-focus", "focus", "focus"},
		{"put-away", "parked", "parked"},
	} {
		args := []string{"start", "work on " + c.name, "--dir", th, "--name", c.name}
		if c.flag != "" {
			args = append(args, "--mode", c.flag)
		}
		if out, code := do(args...); code != 0 {
			t.Fatalf("start %s: %d %q", c.name, code, out)
		}
		if got := loadCardMust(t, c.name).mode(); got != c.want {
			t.Errorf("%s started in %q, want %q", c.name, got, c.want)
		}
	}
	if out, code := do("start", "x y", "--dir", th, "--name", "bad-mode", "--mode", "waiting"); code == 0 || !strings.Contains(out, "--mode must be") {
		t.Errorf("a waiting start was not refused: %d %q", code, out)
	}
	if storeNameTaken("bad-mode") {
		t.Error("the refused start made a track")
	}
}

func TestTheDesksStartRefusesAFolderClaudeDoesNotTrust(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if err := os.WriteFile(filepath.Join(bin, "claude.untrusted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := do("start", "make the refund retries work again", "--dir", th, "--name", "refund-retries")
	if code == 0 || !strings.Contains(out, "refused: Workspace not trusted") {
		t.Fatalf("start: %d %q", code, out)
	}
	if _, ok := lsTracks(t)["refund-retries"]; ok || exists(cardPath("refund-retries")) || exists(threadSettingsPath("refund-retries")) {
		t.Error("the refused start left its card or settings")
	}
	if storeNameTaken("refund-retries") {
		t.Error("the refused start kept the name")
	}
	if h := readDesk(t).Handled; len(h) != 0 {
		t.Errorf("a refused start was logged: %+v", h)
	}
	if out, code := do("start", "again", "--dir", th, "--name", "refund-retries"); code == 0 || strings.Contains(out, "taken") {
		t.Errorf("the name is not free again: %d %q", code, out)
	}
}

func TestTheDeskQueuesStartsPastItsCapAndTheWatchStartsThemInOrder(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	t.Setenv(maxDeskEnv, "1")
	for _, c := range []struct{ name, want string }{
		{"first", "started first in the background as aaaaaaa1\n"},
		{"second", "queued second (0 ahead)\n"},
		{"third", "queued third (1 ahead)\n"},
	} {
		if out, code := do("start", "work on "+c.name, "--dir", th, "--name", c.name); code != 0 || out != c.want {
			t.Fatalf("start %s: %d %q, want %q", c.name, code, out, c.want)
		}
	}
	if _, ok := lsTracks(t)["third"]; !ok {
		t.Error("a queued start has no card")
	}
	if out := waitDesk(t); strings.Contains(out, "started") {
		t.Fatalf("the watch started a queued session while the desk was at its cap:\n%s", out)
	}
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": fullID("aaaaaaa1"), "last_assistant_message": "Opened PR 12; CI is green."})
	out := waitDesk(t)
	if out != "first done: Opened PR 12; CI is green.\nsecond started from the queue as aaaaaaa2\n" {
		t.Fatalf("wait --desk:\n%s", out)
	}
	if s, ok := deskSessionRow(t, fullID("aaaaaaa2")); !ok || s.Track != "second" || s.ParentID != deskClaude || s.Owner != "desk" {
		t.Errorf("the queued session: %+v", s)
	}
	if out := waitDesk(t); strings.Contains(out, "first") || strings.Contains(out, "third") {
		t.Errorf("a delivered report came again, or the cap broke:\n%s", out)
	}
	if out, _ := do("start", "work on fourth", "--dir", th, "--name", "fourth"); out != "queued fourth (1 ahead)\n" {
		t.Errorf("a start behind the queue: %q", out)
	}
}

func TestTheStopHookReportsToTheParentAndTheWatchPrintsIt(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	id := fullID("aaaaaaa1")
	bgHook(t, map[string]any{"hook_event_name": "PermissionRequest", "session_id": id, "tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf build"}})
	bgHook(t, map[string]any{"hook_event_name": "Notification", "session_id": id, "message": "Claude needs your permission to use Bash"})
	var msgs []store.Report
	if err := withBareStore(func(st *store.Store) (err error) { msgs, err = st.DeskReports(); return err }); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "permission: Bash rm -rf build" || msgs[0].From != id {
		t.Errorf("reports: %+v", msgs)
	}
	if out := waitDesk(t); out != "cleanup blocked: permission: Bash rm -rf build\n" {
		t.Errorf("wait --desk:\n%s", out)
	}
}

func TestTheWatchLeavesEmilsSessionsAlone(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	id := fullID("aaaaaaa1")
	// One of Emil's own sessions, in Rex, works and stops.
	hookJSON(t, map[string]any{"hook_event_name": "Stop", "session_id": sessA, "last_assistant_message": "mine"})
	seen := map[string]deskSeen{}
	if lines, err := pollDesk(seen, true); err != nil || len(lines) != 0 {
		t.Fatalf("first poll: %v %q", err, lines)
	}
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "let me look at this myself"})
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "done for Emil"})
	lines, err := pollDesk(seen, false)
	if err != nil || len(lines) != 1 || lines[0] != "cleanup is Emil's now: he typed into it, so leave it to him" {
		t.Errorf("after Emil typed into it: %v %q", err, lines)
	}
	if lines, _ := pollDesk(seen, false); len(lines) != 0 {
		t.Errorf("said it again, or printed Emil's session: %q", lines)
	}
	if s, _ := deskSessionRow(t, id); s.Owner != "emil" {
		t.Errorf("owner %q", s.Owner)
	}
}

func TestADeskSessionThatStopsIsPrinted(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	seen := map[string]deskSeen{}
	if _, err := pollDesk(seen, true); err != nil {
		t.Fatal(err)
	}
	bgHook(t, map[string]any{"hook_event_name": "SessionEnd", "session_id": fullID("aaaaaaa1")})
	if lines, err := pollDesk(seen, false); err != nil || len(lines) != 1 || lines[0] != "cleanup stopped" {
		t.Errorf("%v %q", err, lines)
	}
}

func TestTheDesksStartRefusesAFolderClaudeHasNotTrusted(t *testing.T) {
	_, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "x y", "--dir", os.TempDir()); code == 0 || !strings.Contains(out, "has not trusted") {
		t.Errorf("start: %d %q", code, out)
	}
	if exists(filepath.Join(bin, "claude.bgargs")) {
		t.Error("claude --bg ran in a folder Claude has not trusted")
	}
}

func TestTheQueueDropsATrackEmilPickedUp(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	t.Setenv(maxDeskEnv, "1")
	for _, n := range []string{"first", "q-three"} {
		if out, code := do("start", "work on "+n, "--dir", th, "--name", n); code != 0 {
			t.Fatal(out)
		}
	}
	if err := setMeta("q-three", "rex_session", "session:emil-0001"); err != nil {
		t.Fatal(err)
	}
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": fullID("aaaaaaa1"), "last_assistant_message": "done"})
	if out := waitDesk(t); out != "first done: done\nq-three not started from the queue: Emil has it\n" {
		t.Fatalf("wait --desk:\n%s", out)
	}
	if n, _ := os.ReadFile(filepath.Join(bin, "claude.n")); strings.TrimSpace(string(n)) != "1" {
		t.Errorf("claude --bg ran again: %s", n)
	}
	if out := waitDesk(t); strings.Contains(out, "q-three") {
		t.Errorf("the dropped start came back:\n%s", out)
	}
}

// A session Emil typed into before the desk recorded its start stays his.
func TestRecordingADeskStartKeepsASessionEmilTookFirst(t *testing.T) {
	startHome(t)
	id := fullID("aaaaaaa9")
	t.Setenv(deskParentEnv, deskClaude)
	t.Setenv(deskPromptEnv, "/track solo")
	bgHook(t, map[string]any{"hook_event_name": "SessionStart", "session_id": id, "source": "startup"})
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "/track solo"})
	if s, _ := deskSessionRow(t, id); s.Owner != "desk" || s.ParentID != deskClaude {
		t.Fatalf("the start prompt: %+v", s)
	}
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "I'll take this one"})
	err := withBareStore(func(st *store.Store) error {
		return st.StartDeskSession(store.DeskStart{ID: id, Parent: deskClaude, Track: "solo", Name: "solo", Prompt: "/track solo"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := deskSessionRow(t, id); s.Owner != "emil" {
		t.Errorf("recording the start took the session back from Emil: %+v", s)
	}
}

func loggedByDesk(t *testing.T) []store.DeskLogEntry {
	t.Helper()
	var v store.DeskView
	if err := withBareStore(func(st *store.Store) (err error) { v, err = st.Desk(time.Now().Add(-time.Hour)); return err }); err != nil {
		t.Fatal(err)
	}
	var noted []store.DeskLogEntry
	for _, e := range v.Log {
		if e.Kind == "noted" {
			noted = append(noted, e)
		}
	}
	return noted
}

func TestAnInterimReportIsLoggedByTheServerWithoutWakingTheDesk(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	id := fullID("aaaaaaa1")
	desk := newDeskRunner(io.Discard)
	if lines, _ := desk.watchOnce(); len(lines) != 0 {
		t.Fatalf("first watch: %q", lines)
	}
	old := interimWake
	interimWake = time.Hour
	defer func() { interimWake = old }()

	bgHook(t, map[string]any{"hook_event_name": "SubagentStart", "session_id": id, "agent_id": "ag1", "agent_type": "Explore"})
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Explorer is still reading.\nMore detail.", "background_tasks": []any{map[string]any{"id": "b1"}}})
	lines, reports := desk.watchOnce()
	if len(lines) != 0 || len(reports) != 1 {
		t.Fatalf("an interim report woke the desk: %q %d", lines, len(reports))
	}
	log := loggedByDesk(t)
	if len(log) != 1 || log[0].Track != "cleanup" || log[0].Kind != "noted" || log[0].Did != "cleanup reported, still working: Explorer is still reading." {
		t.Fatalf("handled log: %+v", log)
	}

	bgHook(t, map[string]any{"hook_event_name": "PermissionRequest", "session_id": id, "tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf build"}})
	bgHook(t, map[string]any{"hook_event_name": "Notification", "session_id": id, "message": "Claude needs your permission to use Bash"})
	if lines, _ := desk.watchOnce(); len(lines) != 1 || lines[0] != "cleanup blocked: permission: Bash rm -rf build" {
		t.Errorf("an ask did not wake the desk: %q", lines)
	}

	bgHook(t, map[string]any{"hook_event_name": "SubagentStop", "session_id": id, "agent_id": "ag1", "agent_type": "Explore", "background_tasks": []any{}})
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Opened PR 12.", "background_tasks": []any{}})
	if lines, _ := desk.watchOnce(); len(lines) != 1 || lines[0] != "cleanup done: Opened PR 12." {
		t.Errorf("a finished track did not wake the desk: %q", lines)
	}
	if n := len(loggedByDesk(t)); n != 1 {
		t.Errorf("a wake was also logged by the server: %d entries", n)
	}
}

func TestInterimReportsPastTheWaitWakeTheDeskOnce(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	if out, code := do("start", "clean up", "--dir", th, "--name", "cleanup"); code != 0 {
		t.Fatal(out)
	}
	id := fullID("aaaaaaa1")
	desk := newDeskRunner(io.Discard)
	desk.watchOnce()
	old := interimWake
	interimWake = 50 * time.Millisecond
	defer func() { interimWake = old }()

	for _, msg := range []string{"first look", "second look"} {
		bgHook(t, map[string]any{"hook_event_name": "SubagentStart", "session_id": id, "agent_id": "ag1", "agent_type": "Explore"})
		bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": msg, "background_tasks": []any{map[string]any{"id": "b1"}}})
		if lines, _ := desk.watchOnce(); len(lines) != 0 {
			t.Fatalf("%s woke the desk: %q", msg, lines)
		}
	}
	if n := len(loggedByDesk(t)); n != 2 {
		t.Fatalf("handled log has %d entries", n)
	}
	time.Sleep(60 * time.Millisecond)
	lines, _ := desk.watchOnce()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "2 interim reports logged in the handled log") || !strings.HasSuffix(lines[0], "cleanup, cleanup") {
		t.Fatalf("the batch line: %q", lines)
	}
	if lines, _ := desk.watchOnce(); len(lines) != 0 {
		t.Errorf("the batch woke the desk twice: %q", lines)
	}
}
