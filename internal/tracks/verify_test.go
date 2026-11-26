package tracks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	stopHook       = `{"session_id":"s1","hook_event_name":"Stop","stop_hook_active":false}`
	stopHookActive = `{"session_id":"s1","hook_event_name":"Stop","stop_hook_active":true}`
	prePushYML     = "pre-push:\n  commands:\n    test:\n      run: ./test.sh\n"
)

// verifyThread is a thread of boss working in a fresh dir, with verifyCmd as its verify: command.
func verifyThread(t *testing.T, verifyCmd string) (dir string) {
	t.Helper()
	leadHome(t)
	dir = t.TempDir()
	err := withStore(func(st *store.Store) error {
		_, err := st.Spawn("boss", store.SpawnSpec{Name: "kid", Dir: dir, Goal: "do kid", Verify: verifyCmd})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeLefthook puts a lefthook in dir's node_modules/.bin that runs body as sh.
func fakeLefthook(t *testing.T, dir, body string) {
	t.Helper()
	bin := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "lefthook"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runVerify runs tracks verify kid with the hook input and returns stdout, stderr and the exit code.
func runVerify(hookInput string, args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := Run(append([]string{"verify", "kid"}, args...), strings.NewReader(hookInput), &out, &errb)
	return out.String(), errb.String(), code
}

func verifiedOf(t *testing.T, name string) string {
	t.Helper()
	v := lsTracks(t)[name].Verified
	if v == nil {
		return ""
	}
	return *v
}

func TestVerifyWritesWhereFromTheReportOfADeskStartedSession(t *testing.T) {
	verifyThread(t, "")
	t.Setenv("REX_SESSION", "")
	stop := func(report string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"session_id": "s1", "hook_event_name": "Stop", "last_assistant_message": report})
		if _, errs, code := runVerify(string(b)); code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
	}
	stop("First report.")
	stop("Committed abc123; the lead checks it next.\nMore.")
	if got := whereFirstLine(loadCardMust(t, "kid")); got != "Committed abc123; the lead checks it next." {
		t.Errorf("where %q", got)
	}
}

func TestVerifyWithNothingToRunPassesAndSaysSo(t *testing.T) {
	verifyThread(t, "")
	out, errs, code := runVerify(stopHook)
	if code != 0 || !strings.Contains(out, "nothing to verify") {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
	if v := verifiedOf(t, "kid"); !strings.HasPrefix(v, "pass ") || !strings.Contains(v, "nothing to run") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyIgnoresALefthookConfigWithoutPrePush(t *testing.T) {
	dir := verifyThread(t, "")
	writeIn(t, dir, "lefthook.yml", "pre-commit:\n  commands:\n    lint:\n      run: exit 1\n")
	fakeLefthook(t, dir, "exit 1\n")
	if _, errs, code := runVerify(stopHook); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if v := verifiedOf(t, "kid"); !strings.Contains(v, "nothing to run") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyRunsLefthookPrePushThenTheVerifyCommand(t *testing.T) {
	dir := verifyThread(t, "echo ran > verify.ran")
	writeIn(t, dir, "lefthook.yml", prePushYML)
	fakeLefthook(t, dir, `echo "$@" > lefthook.args; pwd > lefthook.pwd`+"\n")
	out, errs, code := runVerify(stopHook)
	if code != 0 {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "lefthook.args")); strings.TrimSpace(string(b)) != "run pre-push" {
		t.Errorf("lefthook ran with %q, want run pre-push", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "lefthook.pwd")); !sameDir(strings.TrimSpace(string(b)), dir) {
		t.Errorf("lefthook ran in %q, want the card's dir %q", b, dir)
	}
	if !exists(filepath.Join(dir, "verify.ran")) {
		t.Error("the verify: command did not run in the card's dir")
	}
	v := verifiedOf(t, "kid")
	if !strings.HasPrefix(v, "pass ") || !strings.Contains(v, "lefthook pre-push, verify: echo ran > verify.ran") {
		t.Errorf("verified: %q", v)
	}
	f := strings.Fields(v)
	if ts, err := time.Parse(time.RFC3339, f[1]); err != nil || time.Since(ts) > time.Minute {
		t.Errorf("verified has no fresh time: %q", v)
	}
	if f[2] != "in" || f[3] != dir+":" {
		t.Errorf("verified does not name the dir it ran in: %q", v)
	}
}

func TestVerifyFindsLefthookAtTheRepoRootOfASubdir(t *testing.T) {
	_, bin := setup(t)
	if err := os.Remove(filepath.Join(bin, "git")); err != nil { // the real git finds the root
		t.Fatal(err)
	}
	card(t, t.TempDir(), "boss", "focus", leadSess, "w", "")
	importNow(t)
	ranOnTheStore(t)
	if out, code := cardIn("role: lead\n", "card", "boss"); code != 0 {
		t.Fatal(out)
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sub := filepath.Join(root, "pkg", "calc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIn(t, root, "lefthook.yml", prePushYML)
	fakeLefthook(t, root, "pwd > lefthook.pwd\n")
	if err := withStore(func(st *store.Store) error {
		_, err := st.Spawn("boss", store.SpawnSpec{Name: "kid", Dir: sub, Goal: "g"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := runVerify(stopHook); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "lefthook.pwd")); !sameDir(strings.TrimSpace(string(b)), root) {
		t.Errorf("lefthook ran in %q, want the repo root %q", b, root)
	}
	if v := verifiedOf(t, "kid"); !strings.Contains(v, " in "+sub+": lefthook pre-push") {
		t.Errorf("verified: %q", v)
	}
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestVerifyFailureExits2WithTheLast40LinesAndKeepsTheAgentWorking(t *testing.T) {
	dir := verifyThread(t, "echo should not run > verify.ran")
	writeIn(t, dir, "lefthook.yml", prePushYML)
	fakeLefthook(t, dir, "i=1; while [ $i -le 100 ]; do echo \"line $i\"; i=$((i+1)); done; exit 1\n")
	out, errs, code := runVerify(stopHook)
	if code != 2 {
		t.Fatalf("exit %d, want 2; out %q err %q", code, out, errs)
	}
	if !strings.Contains(errs, "lefthook pre-push failed") || !strings.Contains(errs, "line 61\n") || !strings.Contains(errs, "line 100") {
		t.Errorf("stderr lacks the failure or the last 40 lines:\n%s", errs)
	}
	if strings.Contains(errs, "line 60\n") {
		t.Errorf("stderr has more than the last 40 lines:\n%s", errs)
	}
	if exists(filepath.Join(dir, "verify.ran")) {
		t.Error("the verify: command ran after lefthook failed")
	}
	if v := verifiedOf(t, "kid"); !strings.HasPrefix(v, "fail ") || !strings.Contains(v, "lefthook pre-push failed") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyHandsBackOutputWithoutTerminalEscapes(t *testing.T) {
	verifyThread(t, `printf '\033[31mFAIL: add\033[0m NO_COLOR=%s\n' "$NO_COLOR"; exit 1`)
	_, errs, code := runVerify(stopHook)
	if code != 2 || !strings.Contains(errs, "FAIL: add NO_COLOR=1") || strings.Contains(errs, "\x1b") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
}

func TestVerifyFailsOnTheVerifyCommandAlone(t *testing.T) {
	verifyThread(t, "echo broken; exit 3")
	_, errs, code := runVerify(stopHook)
	if code != 2 || !strings.Contains(errs, "verify: echo broken; exit 3 failed (exit status 3)") || !strings.Contains(errs, "broken") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
	if v := verifiedOf(t, "kid"); !strings.HasPrefix(v, "fail ") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyLetsTheAgentStopWhenStopHookIsActive(t *testing.T) {
	verifyThread(t, "exit 1")
	out, errs, code := runVerify(stopHookActive)
	if code != 0 {
		t.Fatalf("exit %d with stop_hook_active, want 0: out %q err %q", code, out, errs)
	}
	if v := verifiedOf(t, "kid"); !strings.HasPrefix(v, "fail ") || !strings.Contains(v, "already sent back once") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyTimesOutAndKillsTheCheck(t *testing.T) {
	dir := verifyThread(t, "")
	writeIn(t, dir, "lefthook.yml", prePushYML)
	fakeLefthook(t, dir, "sleep 30 & wait\n")
	start := time.Now()
	_, errs, code := runVerify(stopHook, "--timeout", "500ms")
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("verify took %s with a 500ms timeout", took)
	}
	if code != 2 || !strings.Contains(errs, "did not finish within 500ms") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
	if v := verifiedOf(t, "kid"); !strings.HasPrefix(v, "fail ") || !strings.Contains(v, "did not finish") {
		t.Errorf("verified: %q", v)
	}
}

func TestVerifyFailsWhenPrePushHasNoLefthookToRunIt(t *testing.T) {
	dir := verifyThread(t, "")
	writeIn(t, dir, "lefthook.yml", prePushYML)
	_, errs, code := runVerify(stopHook)
	if code != 2 || !strings.Contains(errs, "lefthook is in neither node_modules/.bin nor PATH") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
}

func TestVerifyUsesLefthookOnPath(t *testing.T) {
	dir := verifyThread(t, "")
	writeIn(t, dir, ".lefthook.toml", "[pre-push.commands.test]\nrun = \"x\"\n")
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	if err := os.WriteFile(filepath.Join(bin, "lefthook"), []byte("#!/bin/sh\necho on-path; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errs, code := runVerify(stopHook)
	if code != 2 || !strings.Contains(errs, "on-path") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
}

func TestVerifyFindsPrePushInEachConfigFormat(t *testing.T) {
	cases := map[string]string{
		"lefthook.yml":       prePushYML,
		".lefthook.yaml":     "'pre-push':\n  jobs: []\n",
		"lefthook.toml":      "[pre-push]\nparallel = true\n",
		"lefthook.json":      `{"pre-push": {"commands": {}}}`,
		"lefthook-local.yml": prePushYML,
	}
	for f, body := range cases {
		dir := t.TempDir()
		fakeLefthook(t, dir, "")
		writeIn(t, dir, f, body)
		if checks, why := verifyChecks(dir, ""); len(checks) != 1 || why != "" {
			t.Errorf("%s: %v %q", f, checks, why)
		}
	}
	dir := t.TempDir()
	writeIn(t, dir, "lefthook.yml", "pre-commit:\n  commands:\n    pre-push-ish:\n      run: x\n")
	if checks, _ := verifyChecks(dir, ""); len(checks) != 0 {
		t.Errorf("a nested pre-push key counted as the hook: %v", checks)
	}
}

func TestVerifyPassesWhenTheCardIsGone(t *testing.T) {
	verifyThread(t, "exit 1")
	if err := withStore(func(st *store.Store) error { return st.CloseTrack("kid") }); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := runVerify(stopHook); code != 0 || !strings.Contains(errs, "nothing checked") {
		t.Fatalf("exit %d, err %q", code, errs)
	}
}

func TestSpawnGivesOnlyTheThreadAStopHook(t *testing.T) {
	th, bin := leadStubHome(t)
	out, code := do("spawn", "boss", "write the haiku module", "--dir", th, "--model", "sonnet", "--verify", "go test ./...")
	if code != 0 {
		t.Fatalf("spawn: %d %q", code, out)
	}
	name := strings.Fields(out)[1]
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	settings := filepath.Join(th, "threads", name+".settings.json")
	if !strings.Contains(string(args), "--settings\n"+settings+"\n") {
		t.Fatalf("the thread was not started with --settings %s:\n%s", settings, args)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	exe, _ := os.Executable()
	stop := doc.Hooks["Stop"]
	if len(doc.Hooks) != 1 || len(stop) != 1 || len(stop[0].Hooks) != 1 {
		t.Fatalf("hooks: %s", b)
	}
	h := stop[0].Hooks[0]
	if h.Type != "command" || h.Command != shellJoin([]string{exe, "verify", name}) || h.Timeout != 660 {
		t.Errorf("the Stop hook: %+v", h)
	}
	if exists(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")) {
		t.Error("spawn wrote Claude's global settings")
	}
	c, err := loadCard(name)
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta["verify"] != "go test ./..." {
		t.Errorf("verify: %q", c.Meta["verify"])
	}
	if got := threadSettingsArgs(c, false); strings.Join(got, " ") != "--settings "+settings {
		t.Errorf("a resumed thread gets %v", got)
	}
	plain, _ := loadCard("boss")
	if got := threadSettingsArgs(plain, false); got != nil {
		t.Errorf("a lead gets the thread hook on resume: %v", got)
	}
}

func TestSpawnRefusesAFolderClaudeHasNotTrusted(t *testing.T) {
	th, bin := leadStubHome(t)
	parent := t.TempDir()
	inside := filepath.Join(parent, "repo")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	before := len(lsTracks(t))
	out, code := do("spawn", "boss", "t", "--dir", inside, "--model", "sonnet")
	if code == 0 || !strings.Contains(out, "refused: Claude has not trusted "+inside) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if n := len(lsTracks(t)); n != before || exists(filepath.Join(bin, "rex.newargs")) {
		t.Fatalf("a refused spawn left a card or a session")
	}
	// Trust is inherited from a folder above, and matched on the real path too.
	real, _ := filepath.EvalSymlinks(parent)
	trustDirs(t, th, real)
	if out, code := do("spawn", "boss", "t", "--dir", inside, "--model", "sonnet"); code != 0 {
		t.Fatalf("a dir under a trusted folder: %d %s", code, out)
	}
	if err := os.Remove(filepath.Join(os.Getenv("HOME"), ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if out, code := do("spawn", "boss", "t", "--dir", th, "--model", "sonnet"); code == 0 || !strings.Contains(out, "could not read Claude's trusted folders") {
		t.Fatalf("no Claude config: %d %s", code, out)
	}
}

// Screens Claude Code 2.1.330 showed at start, captured with rex capture --trim.
const (
	trustScreen = `────────────────────────────────────────────────────────────────────────────────
 Accessing workspace:

 /Users/me/Developer/worktrees/tracks-probe-adddir

 Quick safety check: Is this a project you created or one you trust? (Like your
 own code, a well-known open source project, or work from your team). If not,
 take a moment to review what's in this folder first.

 ⚠ This folder adds 1 directory to the workspace in .claude/settings.json:
   /tmp

 ❯ No, continue without these permissions
   Yes, I trust this folder

 Enter to confirm · Esc to cancel
`
	mcpScreen = `────────────────────────────────────────────────────────────────────────────────
  New MCP server found in this project: probe
  MCP servers may execute code or access system resources. All tool calls
  require approval. Learn more in the MCP documentation.

    Use this MCP server
    Use this and all future MCP servers in this project
  ❯ Continue without using this MCP server

  Enter to confirm · Esc to cancel
`
)

func TestDialogTextReadsTheQuestion(t *testing.T) {
	if d := dialogText(trustScreen); d != "Claude asks whether to trust the folder: ⚠ This folder adds 1 directory to the workspace in .claude/settings.json: /tmp" {
		t.Errorf("trust: %q", d)
	}
	if d := dialogText(mcpScreen); !strings.HasPrefix(d, "New MCP server found in this project: probe") {
		t.Errorf("mcp: %q", d)
	}
	if d := dialogText("❯ \n  ✻ Working… (esc to interrupt)\n"); d != "" {
		t.Errorf("a working agent read as a dialog: %q", d)
	}
}

func TestSpawnTakesBackAThreadStoppedAtAStartDialog(t *testing.T) {
	for name, screen := range map[string]string{"trust": trustScreen, "mcp": mcpScreen} {
		t.Run(name, func(t *testing.T) {
			th, bin := leadStubHome(t)
			newThreadShows(t, screen, rexResult{found: true, state: agentState{State: "blocked", Kind: "permission", Msg: "trust this folder"}})
			before := len(lsTracks(t))
			out, code := do("spawn", "boss", "fix the parser", "--dir", th, "--model", "sonnet")
			first := strings.SplitN(dialogText(screen), " ", 4)[:3]
			if code == 0 || !strings.Contains(out, "refused: Claude stopped the new thread in "+th+" at a dialog nobody can answer") ||
				!strings.Contains(out, strings.Join(first, " ")) || !strings.Contains(out, "Open claude in "+th+" once") {
				t.Fatalf("exit %d: %s", code, out)
			}
			if !strings.Contains(rexCalls(bin), "session kill session:new-0001") {
				t.Errorf("the stalled session was not ended:\n%s", rexCalls(bin))
			}
			if n := len(lsTracks(t)); n != before {
				t.Errorf("the thread's card was kept: %d tracks, was %d", n, before)
			}
			if ents, _ := os.ReadDir(filepath.Join(th, "threads")); len(ents) != 0 {
				t.Errorf("the thread's settings were kept: %v", ents)
			}
			newThreadShows(t, "  ✻ Working… (esc to interrupt)\n", rexResult{})
			if out, code := do("spawn", "boss", "fix the parser", "--dir", th, "--model", "sonnet"); code != 0 || !strings.HasPrefix(out, "spawned ") {
				t.Errorf("spawning again after the dialog was answered: %d %s", code, out)
			}
		})
	}
}

func TestSpawnTakesRexsWorkingStateAsStarted(t *testing.T) {
	th, _ := leadStubHome(t)
	newThreadShows(t, "", rexResult{found: true, state: agentState{State: "working"}})
	if out, code := do("spawn", "boss", "t", "--dir", th, "--model", "sonnet"); code != 0 || strings.Contains(out, "did not show") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestSpawnSaysSoWhenTheAgentDoesNotShowItStarted(t *testing.T) {
	th, _ := leadStubHome(t)
	newThreadShows(t, "", rexResult{})
	out, code := do("spawn", "boss", "t", "--dir", th, "--model", "sonnet")
	if code != 0 || !strings.Contains(out, "but its agent did not show it had started within") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if n := len(lsTracks(t)); n != 3 {
		t.Errorf("the thread's card is gone: %d tracks", n)
	}
}

func TestARenamedThreadKeepsItsStopHookThroughResume(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "boss", "focus", "rex_session: "+leadSess)
	metaCard(t, "kid", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th, "parent: boss")
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	importNow(t)
	ranOnTheStore(t)
	if _, err := writeThreadSettings("kid"); err != nil {
		t.Fatal(err)
	}
	if out, code := do("rename", "kid", "kid-two"); code != 0 {
		t.Fatalf("rename: %d %s", code, out)
	}
	moved := threadSettingsPath("kid-two")
	if exists(threadSettingsPath("kid")) || !exists(moved) {
		t.Fatalf("the settings did not follow the rename")
	}
	if b, _ := os.ReadFile(moved); !strings.Contains(string(b), "verify kid-two") {
		t.Errorf("the moved settings verify the old name:\n%s", b)
	}
	out, _ := resumeDo("kid-two", "--dry-run")
	if !strings.Contains(out, "--settings "+moved) {
		t.Errorf("the resumed thread has no Stop hook: %s", out)
	}
	// A settings file lost some other way is written again for the current name on resume.
	if err := os.Remove(moved); err != nil {
		t.Fatal(err)
	}
	c, err := loadCard("kid-two")
	if err != nil {
		t.Fatal(err)
	}
	if got := threadSettingsArgs(c, false); strings.Join(got, " ") != "--settings "+moved || !exists(moved) {
		t.Errorf("a missing settings file was not written again: %v", got)
	}
}

func TestSpawnCapsTheLeadsOpenThreads(t *testing.T) {
	th, _ := leadStubHome(t)
	spawn := func() (string, int) { return do("spawn", "boss", "a task", "--dir", th, "--model", "sonnet") }
	var names []string
	for i := 0; i < defaultMaxThreads; i++ {
		out, code := spawn()
		if code != 0 {
			t.Fatalf("thread %d: %d %s", i+1, code, out)
		}
		names = append(names, strings.Fields(out)[1])
	}
	out, code := spawn()
	if code == 0 || !strings.Contains(out, fmt.Sprintf("boss already has %d open threads", defaultMaxThreads)) {
		t.Fatalf("thread %d: %d %s", defaultMaxThreads+1, code, out)
	}
	if err := withStore(func(st *store.Store) error { return st.CloseTrack(names[0]) }); err != nil {
		t.Fatal(err)
	}
	if out, code := spawn(); code != 0 {
		t.Fatalf("after a close: %d %s", code, out)
	}
	t.Setenv(maxThreadsEnv, "6")
	if out, code := spawn(); code != 0 {
		t.Fatalf("with %s=6: %d %s", maxThreadsEnv, code, out)
	}
	t.Setenv(maxThreadsEnv, "2")
	if out, code := spawn(); code == 0 || !strings.Contains(out, "boss already has 5 open threads and may have 2") {
		t.Fatalf("the refusal counts the open threads: %d %s", code, out)
	}
	t.Setenv(maxThreadsEnv, "zero")
	if out, code := spawn(); code == 0 || !strings.Contains(out, maxThreadsEnv+" must be") {
		t.Fatalf("a bad cap: %d %s", code, out)
	}
}

func TestSpawnRefusesAVerifyCommandTheCardCannotHold(t *testing.T) {
	th, _ := leadStubHome(t)
	for _, v := range []string{"make test # all", "a\nb"} {
		if out, code := do("spawn", "boss", "t", "--dir", th, "--model", "sonnet", "--verify", v); code == 0 || !strings.Contains(out, "--verify must be one line") {
			t.Errorf("%q: %d %s", v, code, out)
		}
	}
}
