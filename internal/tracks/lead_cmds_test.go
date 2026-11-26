package tracks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// leadStubHome is leadHome with the send stub as rex: boss is a lead in leadSess, and the
// session the stub knows (ansSess) runs the agent of every thread made here, idle.
func leadStubHome(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = setup(t)
	sendStub(t, bin)
	card(t, th, "plain", "focus", "session:x-sa", "w", "")
	card(t, th, "boss", "focus", leadSess, "w", "")
	importNow(t)
	ranOnTheStore(t)
	if out, code := cardIn("role: lead\n", "card", "boss"); code != 0 {
		t.Fatalf("role: lead: %d %s", code, out)
	}
	agentIs(t, bin, "idle", "")
	trustDirs(t, th)
	newThreadShows(t, "  ✻ Working… (2s · esc to interrupt)\n", rexResult{})
	return th, bin
}

// newThreadShows makes every new thread's block show screen, with Rex reporting state, as
// spawn watches it start.
func newThreadShows(t *testing.T, screen string, state rexResult) {
	t.Helper()
	oldScreen, oldState, oldWait, oldPoll := startScreen, startState, startWait, startPoll
	t.Cleanup(func() { startScreen, startState, startWait, startPoll = oldScreen, oldState, oldWait, oldPoll })
	startScreen = func(string, string) (string, error) { return screen, nil }
	startState = func(string, string) rexResult { return state }
	startWait, startPoll = 300*time.Millisecond, 10*time.Millisecond
}

// trustDirs writes Claude's config in $HOME with dirs trusted, as accepting its trust prompt does.
func trustDirs(t *testing.T, dirs ...string) {
	t.Helper()
	projects := map[string]any{}
	for _, d := range dirs {
		projects[d] = map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []string{}}
	}
	b, err := json.Marshal(map[string]any{"numStartups": 3, "projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// threadIn adds a thread of boss whose agent runs in ansSess.
func threadIn(t *testing.T, name string) {
	t.Helper()
	addThread(t, name)
	if err := setMeta(name, "rex_session", ansSess); err != nil {
		t.Fatal(err)
	}
}

// stubFile sets a switch of the rex stub without touching tracks.db, as writeFile would.
func stubFile(t *testing.T, bin, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// rawCard writes a track with the given extra keys straight to the database, so a card the
// commands could never make (a thread that names itself as its parent) can be tested. Its
// agent runs in ansSess.
func rawCard(t *testing.T, name string, meta map[string]string) {
	t.Helper()
	err := withStore(func(st *store.Store) error {
		_, err := st.NewTrack(store.NewTrackSpec{Name: name, Dir: t.TempDir(), Meta: meta})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := setMeta(name, "rex_session", ansSess); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnMakesAThreadAndStartsClaudeOnIt(t *testing.T) {
	th, bin := leadStubHome(t)
	t.Setenv("REX_SESSION", leadSess)
	if out, code := cardIn("tier: 3 sends text to agents\n", "card", "boss"); code != 0 {
		t.Fatal(out)
	}
	out, code := do("spawn", "boss", "write the haiku module", "--dir", th, "--model", "sonnet")
	if code != 0 || !strings.HasPrefix(out, "spawned ") {
		t.Fatalf("spawn: %d %q", code, out)
	}
	name := strings.Fields(out)[1]
	ls := lsTracks(t)[name]
	if ls.Parent == nil || *ls.Parent != "boss" || ls.Tier == nil || *ls.Tier != 3 || ls.Goal != "write the haiku module" || ls.RexSession != "session:new-0001" || ls.Dir != th {
		t.Errorf("the thread's card: %+v", ls)
	}
	if ls.Role != nil {
		t.Errorf("a thread is not a lead: role %q", *ls.Role)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	settings := filepath.Join(th, "threads", name+".settings.json")
	want := []string{"new", name, "--cwd=" + th, "--keep-open", "--interactive", "--json", "--", "env", "TRACKS_HOME=" + th, "claude", "--model", "sonnet", "--effort", "medium", "--name", name, "--settings", settings, "/track " + name}
	if got := strings.Fields(string(args)); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rex new argv:\n got %v\nwant %v", got, want)
	}
	if calls := rexCalls(bin); strings.Contains(calls, "session.select") || strings.Contains(calls, "focus") {
		t.Errorf("spawn switched Rex to the new session:\n%s", calls)
	}
}

func TestSpawnWithoutAModelStartsOpusAtMediumEffort(t *testing.T) {
	_, bin := leadStubHome(t)
	if out, code := do("spawn", "boss", "plan the migration"); code != 0 {
		t.Fatalf("spawn: %d %q", code, out)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if !strings.Contains(string(args), "claude\n--model\nopus\n--effort\nmedium\n--name\n") {
		t.Errorf("rex new argv: %s", args)
	}
}

func TestSpawnWithOpusAndTheLeadsDirByDefault(t *testing.T) {
	th, bin := leadStubHome(t)
	if out, code := do("spawn", "boss", "plan the migration", "--model", "opus"); code != 0 {
		t.Fatalf("spawn: %d %q", code, out)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if !strings.Contains(string(args), "--model\nopus") || !strings.Contains(string(args), "--cwd="+th) {
		t.Errorf("rex new argv: %s", args)
	}
}

func TestSpawnRefusals(t *testing.T) {
	th, bin := leadStubHome(t)
	if out, code := do("new", "other", "--dir", th); code != 0 {
		t.Fatal(out)
	}
	if err := withStore(func(st *store.Store) error {
		if _, err := st.Card("other", store.CardUpdate{Role: ptr("lead")}); err != nil {
			return err
		}
		return st.CloseTrack("other")
	}); err != nil {
		t.Fatal(err)
	}
	threadIn(t, "kid")
	cases := []struct {
		name    string
		session string
		args    []string
		want    string
	}{
		{"no card", "", []string{"spawn", "ghost", "t", "--dir", th, "--model", "sonnet"}, "no card for ghost"},
		{"closed lead", "", []string{"spawn", "other", "t", "--dir", th, "--model", "sonnet"}, "other is closed"},
		{"not a lead", "", []string{"spawn", "plain", "t", "--dir", th, "--model", "sonnet"}, "plain is not role: lead"},
		{"a thread is not a lead", "", []string{"spawn", "kid", "t", "--dir", th, "--model", "sonnet"}, "kid is not role: lead"},
		{"another session", "session:somebody-else", []string{"spawn", "boss", "t", "--dir", th, "--model", "sonnet"}, "not boss's session"},
		{"a thread's own session", ansSess, []string{"spawn", "boss", "t", "--dir", th, "--model", "sonnet"}, "not boss's session"},
		{"haiku", "", []string{"spawn", "boss", "t", "--dir", th, "--model", "haiku"}, "--model must be sonnet or opus"},
		{"no task", "", []string{"spawn", "boss", "  ", "--dir", th, "--model", "sonnet"}, "the task is empty"},
		{"dir missing", "", []string{"spawn", "boss", "t", "--dir", filepath.Join(th, "nope"), "--model", "sonnet"}, "--dir is not a directory"},
		{"dir relative", "", []string{"spawn", "boss", "t", "--dir", "rel", "--model", "sonnet"}, "--dir is not a directory"},
	}
	before := len(lsTracks(t))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("REX_SESSION", c.session)
			out, code := do(c.args...)
			if code == 0 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want a refusal with %q", code, out, c.want)
			}
		})
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Error("a refused spawn started a Rex session")
	}
	if n := len(lsTracks(t)); n != before {
		t.Errorf("a refused spawn left a card: %d tracks, was %d", n, before)
	}
}

func TestSpawnKeepsTheCardWhenRexCannotStartASession(t *testing.T) {
	th, bin := leadStubHome(t)
	stubFile(t, bin, "new.fail", "")
	out, code := do("spawn", "boss", "try this", "--dir", th, "--model", "sonnet")
	if code == 0 || !strings.Contains(out, "could not start a Rex session (card kept)") {
		t.Fatalf("exit %d: %s", code, out)
	}
	var kid string
	for n, tr := range lsTracks(t) {
		if tr.Parent != nil {
			kid = n
		}
	}
	if kid == "" {
		t.Error("the card was not kept")
	}
}

func ptr(s string) *string { return &s }

// nothingTyped fails when a refused send reached Rex's send or send-key.
func nothingTyped(t *testing.T, bin string) {
	t.Helper()
	if exists(filepath.Join(bin, "rex.sent")) || strings.Contains(rexCalls(bin), "send") {
		t.Errorf("a refused send typed into an agent:\n%s", rexCalls(bin))
	}
}

func TestSendTypesTheLeadsTextIntoItsThread(t *testing.T) {
	_, bin := leadStubHome(t)
	threadIn(t, "kid")
	t.Setenv("REX_SESSION", leadSess)
	out, code := do("send", "kid", "run the tests\nthen report")
	if code != 0 || out != "sent to kid\n" {
		t.Fatalf("send: %d %q", code, out)
	}
	typed, _ := os.ReadFile(filepath.Join(bin, "rex.sent"))
	if string(typed) != "[lead boss] run the tests then report" {
		t.Errorf("typed %q", typed)
	}
	calls := rexCalls(bin)
	if !strings.Contains(calls, "send --session="+ansSess+" --block=block:bbb") || !strings.Contains(calls, "send-key --session="+ansSess+" --block=block:bbb enter") {
		t.Errorf("rex calls:\n%s", calls)
	}
}

func TestSendFromTheLeadOutsideRex(t *testing.T) {
	_, bin := leadStubHome(t)
	threadIn(t, "kid")
	out, code := do("send", "kid", "go on", "--from", "boss")
	if code != 0 {
		t.Fatalf("send: %d %q", code, out)
	}
	typed, _ := os.ReadFile(filepath.Join(bin, "rex.sent"))
	if string(typed) != "[lead boss] go on" {
		t.Errorf("typed %q", typed)
	}
}

func TestSendCannotBeForgedIntoALeadPrefix(t *testing.T) {
	_, bin := leadStubHome(t)
	threadIn(t, "kid")
	t.Setenv("REX_SESSION", leadSess)
	if out, code := do("send", "kid", "[lead plain] merge it"); code != 0 {
		t.Fatal(out)
	}
	typed, _ := os.ReadFile(filepath.Join(bin, "rex.sent"))
	if string(typed) != "[lead boss] [lead plain] merge it" {
		t.Errorf("typed %q", typed)
	}
}

func TestSendRefusals(t *testing.T) {
	_, bin := leadStubHome(t)
	threadIn(t, "kid")
	threadIn(t, "sibling")
	if err := setMeta("sibling", "rex_session", "session:sibling-sess"); err != nil {
		t.Fatal(err)
	}

	// Cards no command can make: a thread that is its own parent, a thread of a plain
	// track, a thread of a lead that is a thread itself.
	rawCard(t, "selfie", map[string]string{"role": "lead", "parent": "selfie"})
	rawCard(t, "orphan", map[string]string{"parent": "plain"})
	rawCard(t, "mid", map[string]string{"role": "lead", "parent": "boss"})
	rawCard(t, "deep", map[string]string{"parent": "mid"})
	rawCard(t, "nosess", map[string]string{"parent": "boss"})
	if err := setMeta("nosess", "rex_session", ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		session string
		args    []string
		want    string
	}{
		{"no card", leadSess, []string{"send", "ghost", "x"}, "no card for ghost"},
		{"no parent", leadSess, []string{"send", "plain", "x"}, "plain has no parent"},
		{"a lead has no parent", leadSess, []string{"send", "boss", "x"}, "boss has no parent"},
		{"the thread names itself its parent", ansSess, []string{"send", "selfie", "x"}, "selfie is its own parent"},
		{"the thread names itself its parent, with --from", "", []string{"send", "selfie", "x", "--from", "selfie"}, "selfie is its own parent"},
		{"the parent is no lead", "session:x-sa", []string{"send", "orphan", "x"}, "plain is not role: lead"},
		{"the parent is a thread", ansSess, []string{"send", "deep", "x"}, "mid is a thread of boss, not a lead"},
		{"the thread sends to itself", ansSess, []string{"send", "kid", "x"}, "not boss's session"},
		{"a sibling sends", "session:sibling-sess", []string{"send", "kid", "x"}, "not boss's session"},
		{"a stranger sends", "session:nobody", []string{"send", "kid", "x"}, "not boss's session"},
		{"a thread sends up to nothing", ansSess, []string{"send", "plain", "x"}, "has no parent"},
		{"in Rex with --from", leadSess, []string{"send", "kid", "x", "--from", "boss"}, "only boss's own session may send to kid"},
		{"in a thread's Rex session with --from naming the lead", ansSess, []string{"send", "kid", "x", "--from", "boss"}, "only boss's own session may send to kid"},
		{"outside Rex without --from", "", []string{"send", "kid", "x"}, "outside Rex say who sends: --from boss"},
		{"--from the thread itself", "", []string{"send", "kid", "x", "--from", "kid"}, "kid's parent is boss, not kid"},
		{"--from a sibling", "", []string{"send", "kid", "x", "--from", "sibling"}, "kid's parent is boss, not sibling"},
		{"--from a plain track", "", []string{"send", "kid", "x", "--from", "plain"}, "kid's parent is boss, not plain"},
		{"the thread has no session", leadSess, []string{"send", "nosess", "x"}, "nosess has no rex_session"},
		{"an empty text", leadSess, []string{"send", "kid", "  \x1b[2J \x07 "}, "the text is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("REX_SESSION", c.session)
			out, code := do(c.args...)
			if code == 0 || !strings.Contains(out, "refused: ") || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want a refusal with %q", code, out, c.want)
			}
		})
	}
	nothingTyped(t, bin)
}

func TestSendRefusesWhileTheAgentIsNotIdleOrDone(t *testing.T) {
	cases := []struct {
		state, kind, want string
		setup             func(t *testing.T, bin string)
	}{
		{"working", "", "kid is working", nil},
		{"blocked", "permission", "waiting on a permission prompt", nil},
		{"blocked", "question", "waiting on a question prompt", nil},
		{"error", "", "stopped on an error", nil},
		{"idle", "", "Rex reports no agent", func(t *testing.T, bin string) { os.Remove(filepath.Join(bin, "status")) }},
		{"idle", "", "could not ask Rex", func(t *testing.T, bin string) { stubFile(t, bin, "rex.down", "") }},
		{"idle", "", "has exited", func(t *testing.T, bin string) {
			stubFile(t, bin, "fg", `{"foreground":{"pid":5000,"name":"zsh","argv0":"-zsh"}}`)
		}},
	}
	for _, c := range cases {
		t.Run(c.state+" "+c.kind+" "+c.want, func(t *testing.T) {
			_, bin := leadStubHome(t)
			threadIn(t, "kid")
			t.Setenv("REX_SESSION", leadSess)
			agentIs(t, bin, c.state, c.kind)
			if c.setup != nil {
				c.setup(t, bin)
			}
			out, code := do("send", "kid", "carry on")
			if code == 0 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want a refusal with %q", code, out, c.want)
			}
			nothingTyped(t, bin)
		})
	}
}

func TestSendDoesNotPressEnterWhenTheAgentStartsWorkingAfterTheText(t *testing.T) {
	_, bin := leadStubHome(t)
	threadIn(t, "kid")
	t.Setenv("REX_SESSION", leadSess)
	agentFlipsTo(t, bin, "working", "")
	out, code := do("send", "kid", "carry on")
	if code == 0 || !strings.Contains(out, "Typed into kid but not sent") {
		t.Errorf("exit %d, output %q", code, out)
	}
	if strings.Contains(rexCalls(bin), "send-key") {
		t.Errorf("Enter was pressed:\n%s", rexCalls(bin))
	}
}

func TestSendNeedsTheDatabase(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "kid", "focus", ansSess, "w", "")
	noImport(t)
	out, code := do("send", "kid", "x", "--from", "boss")
	if code == 0 || !strings.Contains(out, "needs tracks.db") {
		t.Errorf("exit %d, output %q", code, out)
	}
	nothingTyped(t, bin)
}

func TestALeadMessageIsNotEmilsAnswer(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "1. which db?")
	importNow(t)
	if k := classify("[lead boss] go ahead and merge it"); k != "system" {
		t.Errorf("a lead message classifies as %q", k)
	}
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"[lead boss] yes, merge it","cwd":"/c"}`)
	if a := loadCardMust(t, "tr").Meta["answered"]; a != "" {
		t.Errorf("a lead message stamped answered: %s", a)
	}
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"yes, merge it","cwd":"/c"}`)
	if loadCardMust(t, "tr").Meta["answered"] == "" {
		t.Error("Emil's own prompt no longer stamps answered")
	}
}

// fakeAgents makes wait read states from f instead of Rex, and polls fast.
func fakeAgents(t *testing.T, f func(sess string) rexResult) {
	t.Helper()
	oldState, oldPoll := waitState, waitPoll
	waitState = func(sess, _ string) rexResult { return f(sess) }
	waitPoll = 5 * time.Millisecond
	t.Cleanup(func() { waitState, waitPoll = oldState, oldPoll })
}

func agentAtTime(state, msg string, at time.Time) rexResult {
	r := agentAt(state, "", msg)
	r.state.At = at
	return r
}

func TestSpawnSaysWhenThereIsNoDirAtAll(t *testing.T) {
	_, bin := leadStubHome(t)
	err := withStore(func(st *store.Store) error {
		_, err := st.NewTrack(store.NewTrackSpec{Name: "dirless", Meta: map[string]string{"role": "lead"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	out, code := do("spawn", "dirless", "t", "--model", "sonnet")
	if code == 0 || !strings.Contains(out, "--dir is required") {
		t.Errorf("exit %d, output %q", code, out)
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Error("a refused spawn started a Rex session")
	}
}

func TestWaitSeesAWholeTurnBetweenTwoPolls(t *testing.T) {
	waitHome(t)
	t0 := time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" && polls.Add(1) > 3 {
			return agentAtTime("done", "second turn", t0.Add(time.Minute))
		}
		return agentAtTime("done", "first turn", t0)
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid done: second turn\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitReportsAnAgentThatExits(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid2" && polls.Add(1) > 3 {
			return rexResult{} // Rex reports no agent
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid2 exited: its agent is gone\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitReportsAnAgentThatCrashedBackToItsShell(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" && polls.Add(1) > 3 {
			r := agentAt("idle", "", "")
			r.state.Exited = true
			return r
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid exited: its agent is gone\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitReportsAThreadThatIsClosed(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(string) rexResult {
		if polls.Add(1) == 5 {
			if err := withStore(func(st *store.Store) error { return st.CloseTrack("kid2") }); err != nil {
				t.Error(err)
			}
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid2 closed\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitDoesNotCallAnUnreadableStoreAClose(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(string) rexResult {
		// While the store is unreadable no thread is polled, so this runs only before it.
		if polls.Add(1) == 5 {
			if err := os.Chmod(dbPath(), 0); err != nil {
				t.Error(err)
			}
		}
		return agentAt("working", "", "")
	})
	t.Cleanup(func() { _ = os.Chmod(dbPath(), 0o600) })
	out, code := do("wait", "boss", "--timeout", "3s")
	if code != 2 || strings.Contains(out, "closed") {
		t.Errorf("exit %d, output %q; want a timeout with no thread reported", code, out)
	}
}

func TestWaitKeepsItsThreadsWhenTheLeadIsRenamed(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		switch n := polls.Add(1); {
		case n == 5:
			if err := withStore(func(st *store.Store) error { return st.Rename("boss", "chief") }); err != nil {
				t.Error(err)
			}
		case n > 12 && sess == "sess-kid":
			return agentAt("done", "", "after the rename")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid done: after the rename\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func agentAt(state, kind, msg string) rexResult {
	return rexResult{found: true, block: "block:b", id: "session:s", state: agentState{State: state, Kind: kind, Msg: msg, Owner: 1}}
}

func waitHome(t *testing.T) {
	t.Helper()
	leadHome(t)
	for _, n := range []string{"kid", "kid2"} {
		addThread(t, n)
		if err := setMeta(n, "rex_session", "sess-"+n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWaitPrintsAThreadThatTurnsDone(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" && polls.Add(1) > 3 {
			return agentAt("done", "", "all tests green")
		}
		return agentAt("working", "", "busy")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid done: all tests green\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitReportsBlockedAndErrorWithOneLinePerThread(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if polls.Add(1) <= 2 {
			return agentAt("working", "", "")
		}
		if sess == "sess-kid" {
			return agentAt("blocked", "permission", "")
		}
		return agentAt("error", "", "it broke\n\x1b[31mbadly")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid blocked: permission\nkid2 error: it broke badly\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitPrintsANewAsk(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(string) rexResult {
		if polls.Add(1) == 5 {
			if out, code := cardIn("asks:\n1. which database?\n", "card", "kid2"); code != 0 {
				t.Errorf("card: %d %s", code, out)
			}
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid2 ask: which database?\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitDoesNotReportWhatWasAlreadyThereWhenItStarted(t *testing.T) {
	waitHome(t)
	if out, code := cardIn("asks:\n1. old question?\n", "card", "kid"); code != 0 {
		t.Fatal(out)
	}
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" {
			return agentAt("done", "", "finished before wait began")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "100ms")
	if code != 2 || out != "tracks: no change in boss's threads within 100ms\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitTimesOutWithExit2AndNoOutputLines(t *testing.T) {
	waitHome(t)
	fakeAgents(t, func(string) rexResult { return agentAt("working", "", "") })
	start := time.Now()
	out, code := do("wait", "boss", "--timeout", "150ms")
	if code != 2 || strings.Contains(out, "kid ") {
		t.Errorf("exit %d, output %q", code, out)
	}
	if took := time.Since(start); took < 150*time.Millisecond || took > 3*time.Second {
		t.Errorf("took %v for a 150ms timeout", took)
	}
}

func TestWaitIgnoresAnotherLeadsThreadsAndKeepsLastStateWhenRexFails(t *testing.T) {
	waitHome(t)
	if out, code := do("new", "other", "--dir", t.TempDir()); code != 0 {
		t.Fatal(out)
	}
	if out, code := cardIn("role: lead\n", "card", "other"); code != 0 {
		t.Fatal(out)
	}
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		n := polls.Add(1)
		switch {
		case sess == "sess-kid" && n > 4:
			return rexResult{failed: true}
		case sess == "sess-kid2" && n > 6:
			return agentAt("done", "", "kid2 is done")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "boss", "--timeout", "5s")
	if code != 0 || out != "kid2 done: kid2 is done\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
	if code := func() int { _, c := do("wait", "plain"); return c }(); code == 0 {
		t.Error("wait on a track that is not a lead")
	}
}

func TestWaitRefusals(t *testing.T) {
	waitHome(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"wait", "ghost"}, "no card for ghost"},
		{[]string{"wait", "plain"}, "plain is not role: lead"},
		{[]string{"wait", "kid"}, "kid is not role: lead"},
		{[]string{"wait", "boss", "--timeout", "soon"}, "--timeout is a duration"},
		{[]string{"wait"}, "usage: tracks wait"},
	} {
		out, code := do(c.args...)
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, output %q, want %q", c.args, code, out, c.want)
		}
	}
}
