package tracks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const deskSess = "session:0020-desk-bbbb"

func readDesk(t *testing.T, args ...string) deskDoc {
	t.Helper()
	out, code := do(append([]string{"desk", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("desk --json: %d %s", code, out)
	}
	var doc deskDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("desk --json is not json: %v\n%s", err, out)
	}
	return doc
}

// deskHome is waitHome with every agent working, so no track stalls until a test says so.
func deskHome(t *testing.T) {
	t.Helper()
	waitHome(t)
	fakeAgents(t, func(string) rexResult { return agentAt("working", "", "") })
}

// asDesk makes the test run in the desk's Rex session, under the desk's agent: Rex names the
// test's parent process as that agent.
func asDesk(t *testing.T) {
	t.Helper()
	bindDesk(t, deskSess)
	t.Setenv("REX_SESSION", deskSess)
	deskAgentIs(t, os.Getppid(), nil)
}

func bindDesk(t *testing.T, sess string) {
	t.Helper()
	if err := withStore(func(st *store.Store) error { return st.BindDesk(sess, true) }); err != nil {
		t.Fatal(err)
	}
}

// deskAgentIs makes Rex report pid as the desk's agent, or fail with err.
func deskAgentIs(t *testing.T, pid int, err error) {
	t.Helper()
	old := deskAgentPID
	t.Cleanup(func() { deskAgentPID = old })
	deskAgentPID = func(sess string) (int, error) {
		if sess != deskSess {
			t.Errorf("asked Rex about %q, not the desk's session", sess)
		}
		return pid, err
	}
}

// A desk ask stored before the desk asked in chat stays readable in tracks desk.
func TestDeskWritesItsLogAndStillShowsStoredAsks(t *testing.T) {
	deskHome(t)
	asDesk(t)
	addAsk(t, "kid", "merge #212?", "yes", "CI green", "approved")
	for _, args := range [][]string{
		{"desk", "log", "kid2", "nudged", "lint is red on #212", "--asked", "idle 40m"},
		{"desk", "log", "kid", "noted", "raised merge #212 as ask 1"},
	} {
		if out, code := do(args...); code != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	doc := readDesk(t)
	if len(doc.Asks) != 1 || doc.Asks[0].Track != "kid" || doc.Asks[0].Question != "merge #212?" || doc.Asks[0].Recommendation != "yes" ||
		strings.Join(doc.Asks[0].Evidence, "|") != "CI green|approved" {
		t.Errorf("asks %+v", doc.Asks)
	}
	if len(doc.Handled) != 2 || doc.Handled[0].Kind != "nudged" || doc.Handled[0].Track != "kid2" || doc.Handled[0].Asked != "idle 40m" ||
		doc.Handled[0].Did != "lint is red on #212" || doc.Handled[1].Kind != "noted" || doc.Handled[0].At == "" {
		t.Errorf("handled %+v", doc.Handled)
	}
	out, code := do("desk")
	for _, want := range []string{"1 open asks, 2 handled today, 0 stalled", "[1] kid: merge #212?", "recommend: yes", "- CI green", "nudged kid2 (idle 40m): lint is red on #212"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("desk output lacks %q:\n%s", want, out)
		}
	}
}

func TestDeskJSONHasEveryKeyWhenTheDeskIsEmpty(t *testing.T) {
	deskHome(t)
	out, _ := do("desk", "--json")
	for _, want := range []string{`"asks": []`, `"handled": []`, `"stalls": []`, `"session": ""`, `"idle_after": "30m0s"`} {
		if !strings.Contains(out, want) {
			t.Errorf("desk --json lacks %s:\n%s", want, out)
		}
	}
}

func TestDeskLogShowsOnlyTodaysEntries(t *testing.T) {
	deskHome(t)
	asDesk(t)
	if out, code := do("desk", "log", "kid", "noted", "kept it"); code != 0 {
		t.Fatal(out)
	}
	if doc := readDesk(t); len(doc.Handled) != 1 {
		t.Fatalf("today's log: %+v", doc.Handled)
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	err := withStore(func(st *store.Store) error {
		_, err := st.LogDesk("kid", "noted", "", "yesterday")
		return err
	}, store.WithClock(func() time.Time { return yesterday }))
	if err != nil {
		t.Fatal(err)
	}
	doc := readDesk(t)
	if len(doc.Handled) != 1 || doc.Handled[0].Did != "kept it" {
		t.Errorf("an entry from yesterday is in today's log: %+v", doc.Handled)
	}
}

func TestOnlyTheDesksAgentWritesToTheDesk(t *testing.T) {
	deskHome(t)
	writes := [][]string{
		{"desk", "log", "kid", "noted", "x"},
	}
	refused := func(t *testing.T, want string) {
		t.Helper()
		for _, w := range writes {
			if out, code := do(w...); code == 0 || !strings.Contains(out, "refused: ") || !strings.Contains(out, want) {
				t.Errorf("%v: %d %s, want %q", w, code, out, want)
			}
		}
	}
	t.Run("no desk is running", func(t *testing.T) {
		t.Setenv("REX_SESSION", deskSess)
		refused(t, "no desk is running: tracks desk start opens one")
	})
	t.Run("outside Rex", func(t *testing.T) {
		asDesk(t)
		t.Setenv("REX_SESSION", "")
		refused(t, "this runs outside Rex")
	})
	t.Run("another Rex session", func(t *testing.T) {
		asDesk(t)
		for _, sess := range []string{leadSess, "sess-kid", "session:other"} {
			t.Setenv("REX_SESSION", sess)
			refused(t, "is not the desk's session")
		}
	})
	t.Run("the desk's session named by a process that does not run under its agent", func(t *testing.T) {
		asDesk(t)
		deskAgentIs(t, 999999, nil)
		refused(t, "does not run under the desk's agent (pid 999999)")
		deskAgentIs(t, 1, nil)
		refused(t, "does not run under the desk's agent (pid 1)")
	})
	t.Run("Rex reports no desk agent", func(t *testing.T) {
		asDesk(t)
		deskAgentIs(t, 0, errors.New("Rex reports no agent in the desk's session"))
		refused(t, "could not confirm the desk's agent: Rex reports no agent")
	})
	t.Run("--from is not a way in", func(t *testing.T) {
		if out, code := do("desk", "log", "kid", "noted", "x", "--from", "desk"); code == 0 || !strings.Contains(out, "usage: tracks desk") {
			t.Errorf("%d %s", code, out)
		}
	})
	if doc := readDesk(t); len(doc.Asks) != 0 || len(doc.Handled) != 0 {
		t.Errorf("a refused write landed: %+v", doc)
	}
	t.Run("the desk's agent writes", func(t *testing.T) {
		asDesk(t)
		for _, w := range writes {
			if out, code := do(w...); code != 0 {
				t.Errorf("%v: %d %s", w, code, out)
			}
		}
	})
}

// An ask left from the band closes once with tracks desk close, from the desk or from Emil.
func TestAStoredDeskAskClosesOnce(t *testing.T) {
	for _, c := range []struct {
		name string
		prep func(t *testing.T)
	}{
		{"the desk", func(t *testing.T) { asDesk(t) }},
		{"Emil", func(t *testing.T) { deskAgentIs(t, 999999, nil) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			deskHome(t)
			addAsk(t, "kid", "merge #212?", "yes")
			c.prep(t)
			if out, code := do("desk", "close", "1"); code != 0 || out != "closed desk ask 1\n" {
				t.Errorf("closing it: %d %q", code, out)
			}
			if doc := readDesk(t); len(doc.Asks) != 0 {
				t.Errorf("asks after the close: %+v", doc.Asks)
			}
			if out, code := do("desk", "close", "1"); code == 0 || !strings.Contains(out, "no such open desk ask") {
				t.Errorf("closing it twice: %d %q", code, out)
			}
		})
	}
}

func TestAnyoneMayReadTheDesk(t *testing.T) {
	deskHome(t)
	asDesk(t)
	t.Setenv("REX_SESSION", "sess-kid")
	if out, code := do("desk"); code != 0 {
		t.Errorf("a track reads the desk: %d %s", code, out)
	}
}

// deskStartHome is leadStubHome where the rex stub's known session, ansSess, can hold a desk,
// and the desk's folder, ~/Developer, exists and is trusted. dev is that folder.
func deskStartHome(t *testing.T) (th, bin, dev string) {
	t.Helper()
	th, bin = leadStubHome(t)
	t.Setenv("REX_SESSION", "")
	dev = filepath.Join(os.Getenv("HOME"), "Developer")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	trustDirs(t, th, dev)
	return th, bin, dev
}

// deskSettingsOf reads the settings rex new was given for the desk's agent.
func deskSettingsOf(t *testing.T, bin string) (raw string, s struct {
	Permissions struct {
		DefaultMode   string `json:"defaultMode"`
		DisableBypass string `json:"disableBypassPermissionsMode"`
		Allow, Deny   []string
	} `json:"permissions"`
}) {
	t.Helper()
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	argv := strings.Split(string(args), "\n")
	i := slices.Index(argv, "--settings")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("no --settings in %q", argv)
	}
	raw = argv[i+1]
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("--settings is not the settings JSON: %q: %v", raw, err)
	}
	return raw, s
}

func TestDeskStartOpensTheDeskOnOpusAndBindsIt(t *testing.T) {
	th, bin, dev := deskStartHome(t)
	t.Setenv(deskIdleEnv, "40m")
	out, code := do("desk", "start")
	if code != 0 || out != "the desk runs in Rex session session:new-0001\n" {
		t.Fatalf("desk start: %d %q", code, out)
	}
	exe, _ := os.Executable()
	settings, s := deskSettingsOf(t, bin)
	want := []string{"new", "desk", "--cwd=" + dev, "--keep-open", "--interactive", "--json", "--",
		"sh", "-c", `export PATH="$0:$PATH"; exec "$@"`, filepath.Dir(exe),
		"env", "TRACKS_HOME=" + th, deskIdleEnv + "=40m", "claude", "--model", "opus", "--name", "desk", "--settings", settings, "/desk", ""}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if got := strings.Split(string(args), "\n"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rex new argv:\n got %q\nwant %q", got, want)
	}
	if doc := readDesk(t); doc.Session != "session:new-0001" {
		t.Errorf("the desk is bound to %q", doc.Session)
	}
	split, _ := os.ReadFile(filepath.Join(bin, "rex.splitargs"))
	wantSplit := []string{"split", "-s", "session:new-0001", "-b", "block:n1", "--split=right", "--ratio=40", "--focus=false", "--interactive", "--label=tracks", "--json", "--",
		"sh", "-c", `export PATH="$0:$PATH"; exec "$@"`, filepath.Dir(exe), "env", "TRACKS_HOME=" + th, deskIdleEnv + "=40m", "tracks", "--home", ""}
	if got := strings.Split(string(split), "\n"); strings.Join(got, "|") != strings.Join(wantSplit, "|") {
		t.Errorf("rex split argv:\n got %q\nwant %q", got, wantSplit)
	}
	if s.Permissions.DefaultMode != "auto" {
		t.Errorf("the desk's defaultMode is %q, so it asks Emil before what auto mode would run", s.Permissions.DefaultMode)
	}
	if s.Permissions.DisableBypass != "disable" {
		t.Errorf("the desk can be switched to bypass (%q), which skips auto mode's review", s.Permissions.DisableBypass)
	}
	if strings.Join(s.Permissions.Allow, " ") != strings.Join(deskAllowed, " ") {
		t.Errorf("allowed %q", s.Permissions.Allow)
	}
	for _, rule := range s.Permissions.Allow {
		if rule == "Bash(tracks:*)" || strings.HasPrefix(rule, "Bash(rex") {
			t.Errorf("the desk may run %s without auto mode's review", rule)
		}
	}
	if want := []string{"Bash(tracks bind:*)", "Bash(tracks hook:*)"}; !slices.Equal(s.Permissions.Deny, want) {
		t.Errorf("the desk's settings deny %q, want only %q: the rest is as in any session", s.Permissions.Deny, want)
	}
}

func TestADeskWhoseTracksPaneDidNotOpenStillRunsAndSaysSo(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	stubFile(t, bin, "split.fail", "")
	out, code := do("desk", "start")
	if code != 0 || !strings.Contains(out, "the desk runs in Rex session session:new-0001\n") || !strings.Contains(out, "the tracks pane did not open beside it") {
		t.Errorf("desk start: %d %q", code, out)
	}
	if doc := readDesk(t); doc.Session != "session:new-0001" {
		t.Errorf("the desk is bound to %q", doc.Session)
	}
	m := newPane()
	plain(m, 120, 30)
	bindDesk(t, "session:gone")
	if msg := pressD(t, m); !strings.Contains(msg.status, "the tracks pane did not open beside it") {
		t.Errorf("d: %+v", msg)
	}
}

func TestTheDesksSettingsComeFromTheBinaryNotTheStateFolder(t *testing.T) {
	th, bin, _ := deskStartHome(t)
	planted := filepath.Join(th, "desk.settings.json")
	if err := os.WriteFile(planted, []byte(`{"permissions":{"defaultMode":"bypassPermissions","allow":["Bash(*)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := do("desk", "start"); code != 0 {
		t.Fatalf("desk start: %d %s", code, out)
	}
	raw, s := deskSettingsOf(t, bin)
	if strings.Contains(raw, "bypassPermissions") || s.Permissions.DefaultMode != "auto" || len(s.Permissions.Deny) == 0 {
		t.Errorf("the desk runs with settings %s", raw)
	}
}

func TestTheDeskStartsInItsOwnFolderWhateverTheCallerSays(t *testing.T) {
	th, bin, dev := deskStartHome(t)
	worktree := filepath.Join(th, "kid-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	trustDirs(t, th, dev, worktree)
	if out, code := do("desk", "start", "--dir", worktree); code == 0 || !strings.Contains(out, "usage: tracks desk") {
		t.Errorf("--dir: %d %s", code, out)
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Fatal("desk start --dir opened a Rex session")
	}
	t.Setenv("TRACKS_DEFAULT_DIR", worktree)
	if out, code := do("desk", "start"); code != 0 {
		t.Fatalf("desk start: %d %s", code, out)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if !strings.Contains(string(args), "--cwd="+dev+"\n") {
		t.Errorf("the desk did not start in %s:\n%s", dev, args)
	}
}

func TestOnlyOneDeskRuns(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	bindDesk(t, ansSess)
	out, code := do("desk", "start")
	if code == 0 || !strings.Contains(out, "the desk already runs in Rex session "+ansSess) {
		t.Errorf("a second desk: %d %s", code, out)
	}
	stubFile(t, bin, "rex.down", "")
	if out, code := do("desk", "start"); code == 0 || !strings.Contains(out, "could not ask Rex whether the desk") {
		t.Errorf("Rex down: %d %s", code, out)
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Error("a refused desk start opened a Rex session")
	}
	if doc := readDesk(t); doc.Session != ansSess {
		t.Errorf("a refused start moved the desk to %q", doc.Session)
	}
}

func TestDeskStartReplacesADeskWhoseAgentIsGone(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	bindDesk(t, ansSess)
	os.Remove(filepath.Join(bin, "status")) // Rex reports no agent in ansSess
	if out, code := do("desk", "start"); code != 0 {
		t.Fatalf("desk start: %d %s", code, out)
	}
	if doc := readDesk(t); doc.Session != "session:new-0001" {
		t.Errorf("the desk is bound to %q", doc.Session)
	}
}

func TestDeskStartRefusals(t *testing.T) {
	th, bin, _ := deskStartHome(t)
	t.Setenv("REX_SESSION", "session:x-sa")
	if out, code := do("desk", "start"); code == 0 || !strings.Contains(out, "runs the agent of plain, and a track does not start the desk") {
		t.Errorf("from a track's session: %d %s", code, out)
	}
	t.Setenv("REX_SESSION", "")
	trustDirs(t, th)
	if out, code := do("desk", "start"); code == 0 || !strings.Contains(out, "Claude has not trusted") {
		t.Errorf("an untrusted desk folder: %d %s", code, out)
	}
	if out, code := do("desk", "start", "extra"); code == 0 || !strings.Contains(out, "usage: tracks desk") {
		t.Errorf("an argument: %d %s", code, out)
	}
	if exists(filepath.Join(bin, "rex.newargs")) {
		t.Error("a refused desk start opened a Rex session")
	}
	if doc := readDesk(t); doc.Session != "" {
		t.Errorf("a refused start bound the desk to %q", doc.Session)
	}
}

func TestDeskStartEndsADeskStoppedAtADialog(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	newThreadShows(t, "──────\n Do you trust the files in this folder?\n Enter to confirm · Esc to cancel\n", rexResult{})
	out, code := do("desk", "start")
	if code == 0 || !strings.Contains(out, "Claude stopped the desk") {
		t.Errorf("exit %d: %s", code, out)
	}
	if !strings.Contains(rexCalls(bin), "session kill session:new-0001") {
		t.Errorf("the desk's session was not ended:\n%s", rexCalls(bin))
	}
}

func TestDeskRefusals(t *testing.T) {
	deskHome(t)
	asDesk(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"desk", "ask", "kid", "q?"}, deskAsksGone},
		{[]string{"desk", "close", "1"}, "no such open desk ask"},
		{[]string{"desk", "pickup", "1"}, deskAsksGone},
		{[]string{"desk", "log", "kid", "merged", "x"}, "kind must be nudged or noted"},
		{[]string{"desk", "log", "kid", "answered", "x"}, "tracks desk answer logs answers"},
		{[]string{"desk", "log", "kid", "started", "x"}, "tracks start logs starts"},
		{[]string{"desk", "log", "kid", "noted", " "}, "what the desk did is empty"},
		{[]string{"desk", "frob"}, "usage: tracks desk"},
		{[]string{"desk", "--idle", "soon"}, "--idle is a duration"},
		{[]string{"desk", "--nope"}, "usage: tracks desk"},
	} {
		if out, code := do(c.args...); code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, output %q, want %q", c.args, code, out, c.want)
		}
	}
}

func TestDeskNeedsTheDatabase(t *testing.T) {
	setup(t)
	noImport(t) // the store run would import a database, and wait --all would then wait for a timeout
	for _, args := range [][]string{{"desk"}, {"desk", "ask", "a", "q"}, {"wait", "--all"}} {
		if out, code := do(args...); code == 0 || !strings.Contains(out, needsStore) {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
}

func TestDetectStallsAgainstFakeStatuses(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	idle := 30 * time.Minute
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	probe := func(f func(*stallProbe)) stallProbe {
		p := stallProbe{Name: "t", Mode: "focus", Session: true, Answered: true, State: "working", At: ago(time.Minute)}
		f(&p)
		return p
	}
	for _, c := range []struct {
		name   string
		p      stallProbe
		reason string
		detail string
	}{
		{"working is not a stall", probe(func(p *stallProbe) { p.At = ago(5 * time.Hour) }), "", ""},
		{"idle over the limit with no ask", probe(func(p *stallProbe) { p.State, p.At = "idle", ago(31*time.Minute) }), stallIdle, "idle 31m with no ask"},
		{"done over the limit with no ask", probe(func(p *stallProbe) { p.State, p.At = "done", ago(2*time.Hour) }), stallIdle, "idle 2h with no ask"},
		{"idle exactly at the limit", probe(func(p *stallProbe) { p.State, p.At = "idle", ago(idle) }), "", ""},
		{"idle under the limit", probe(func(p *stallProbe) { p.State, p.At = "idle", ago(29*time.Minute) }), "", ""},
		{"idle with an ask is Emil's turn", probe(func(p *stallProbe) { p.State, p.At, p.Asks = "idle", ago(time.Hour), 1 }), "", ""},
		{"idle with no time known", probe(func(p *stallProbe) { p.State, p.At = "idle", time.Time{} }), "", ""},
		{"idle but waiting on something", probe(func(p *stallProbe) { p.State, p.At, p.Mode = "idle", ago(time.Hour), "waiting" }), "", ""},
		{"idle but parked", probe(func(p *stallProbe) { p.State, p.At, p.Mode = "idle", ago(time.Hour), "parked" }), "", ""},
		{"blocked on a permission prompt", probe(func(p *stallProbe) { p.State, p.Kind = "blocked", "permission" }), stallBlocked, "blocked on a permission prompt"},
		{"blocked on a question is an ask", probe(func(p *stallProbe) { p.State, p.Kind = "blocked", "question" }), "", ""},
		{"error", probe(func(p *stallProbe) { p.State, p.Msg = "error", "it broke\n\x1b[31mbadly" }), stallError, "error: it broke badly"},
		{"error without a message", probe(func(p *stallProbe) { p.State = "error" }), stallError, "error: no message"},
		{"exited with the card open", probe(func(p *stallProbe) { p.State, p.At = "", time.Time{} }), stallExited, "its agent has exited and the card is still open"},
		{"parked and exited", probe(func(p *stallProbe) { p.State, p.Mode = "", "parked" }), "", ""},
		{"no Rex session on the card", probe(func(p *stallProbe) { p.State, p.Session = "", false }), "", ""},
		{"Rex has not answered yet", probe(func(p *stallProbe) { p.State, p.Answered = "", false }), "", ""},
	} {
		got := detectStalls(now, idle, []stallProbe{c.p})
		switch {
		case c.reason == "" && len(got) != 0:
			t.Errorf("%s: stalled %+v", c.name, got)
		case c.reason != "" && (len(got) != 1 || got[0].Reason != c.reason || got[0].Detail != c.detail || got[0].Track != "t"):
			t.Errorf("%s: got %+v, want %s %q", c.name, got, c.reason, c.detail)
		}
	}
	got := detectStalls(now, idle, []stallProbe{probe(func(p *stallProbe) { p.State, p.At = "idle", ago(time.Hour) })})
	if len(got) != 1 || got[0].Since == nil || *got[0].Since != "2026-11-23T11:00:00Z" {
		t.Errorf("since: %+v", got)
	}
}

func TestDeskJSONListsStallsFromRexAndTheCards(t *testing.T) {
	waitHome(t)
	stuckSince := time.Now().Add(-45 * time.Minute)
	fakeAgents(t, func(sess string) rexResult {
		switch sess {
		case "sess-kid":
			return agentAtTime("idle", "", stuckSince)
		case "sess-kid2":
			return agentAt("blocked", "permission", "")
		}
		return agentAt("working", "", "")
	})
	doc := readDesk(t)
	if len(doc.Stalls) != 2 || doc.Stalls[0].Track != "kid" || doc.Stalls[0].Reason != stallIdle || doc.Stalls[1].Track != "kid2" || doc.Stalls[1].Reason != stallBlocked {
		t.Fatalf("stalls %+v", doc.Stalls)
	}
	if out, _ := do("desk"); !strings.Contains(out, "2 stalled") || !strings.Contains(out, "kid: idle 45m with no ask") {
		t.Errorf("desk output:\n%s", out)
	}
	if doc := readDesk(t, "--idle", "2h"); len(doc.Stalls) != 1 || doc.Stalls[0].Track != "kid2" || doc.IdleAfter != "2h0m0s" {
		t.Errorf("with --idle 2h: %+v", doc)
	}
	t.Setenv(deskIdleEnv, "3h")
	if doc := readDesk(t); len(doc.Stalls) != 1 || doc.IdleAfter != "3h0m0s" {
		t.Errorf("with %s: %+v", deskIdleEnv, doc)
	}
}

// An ask on the card is in front of Emil, so its track is not idle; a desk ask stored from the
// band is not, since the pane no longer shows it.
func TestACardAskIsNotAnIdleStallAndAStoredDeskAskDoesNotHideOne(t *testing.T) {
	waitHome(t)
	old := time.Now().Add(-2 * time.Hour)
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" || sess == "sess-kid2" {
			return agentAtTime("idle", "", old)
		}
		return agentAt("working", "", "")
	})
	if out, code := cardIn("asks:\n1. which database?\n", "card", "kid"); code != 0 {
		t.Fatal(out)
	}
	addAsk(t, "kid2", "merge?", "yes")
	if doc := readDesk(t); len(doc.Stalls) != 1 || doc.Stalls[0].Track != "kid2" || doc.Stalls[0].Reason != stallIdle {
		t.Errorf("stalls %+v, want kid2 idle only", doc.Stalls)
	}
}

func TestDeskIdleMustBeADuration(t *testing.T) {
	deskHome(t)
	t.Setenv(deskIdleEnv, "soon")
	if out, code := do("desk"); code == 0 || !strings.Contains(out, deskIdleEnv+" must be a duration") {
		t.Errorf("%d %s", code, out)
	}
}

func TestWaitAllPrintsAChangeInAnyTrack(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid2" && polls.Add(1) > 12 {
			return agentAt("done", "", "kid2 finished")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "5s")
	if code != 0 || out != "kid2 done: kid2 finished\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllSeesAnAskOnATrackThatIsNotAThread(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(string) rexResult {
		if polls.Add(1) == 12 {
			if out, code := cardIn("asks:\n1. which version?\n", "card", "plain"); code != 0 {
				t.Errorf("card: %d %s", code, out)
			}
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "5s")
	if code != 0 || out != "plain ask: which version?\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

// A store that cannot be read while --follow watches says so once, and the watch goes on.
func TestWaitAllFollowSaysWhenTheStoreCannotBeRead(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		n := polls.Add(1)
		if n == 12 {
			if err := os.Chmod(dbPath(), 0); err != nil {
				t.Error(err)
			}
			go func() {
				time.Sleep(300 * time.Millisecond)
				os.Chmod(dbPath(), 0o644)
			}()
		}
		if sess == "sess-kid2" && n > 12 {
			return agentAt("done", "", "kid2 finished")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--follow", "--timeout", "2s")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 2 || len(lines) != 3 || !strings.HasPrefix(lines[0], "tracks wait: ") || !strings.HasSuffix(lines[0], ", still watching") ||
		lines[1] != "kid2 done: kid2 finished" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

// With --follow, wait --all keeps watching after a change and prints each one as it comes, so
// the desk can run it under Monitor; an answer to a stored desk ask no longer wakes it.
func TestWaitAllFollowPrintsEveryChangeUntilItsTimeout(t *testing.T) {
	waitHome(t)
	addAsk(t, "kid", "merge #212?", "yes")
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		n := polls.Add(1)
		if n == 12 {
			answerAsk(t, 1, "accepted", "yes")
		}
		switch {
		case sess == "sess-kid2" && n > 12:
			return agentAt("done", "", "kid2 finished")
		case sess == "sess-kid" && n > 40:
			return agentAt("done", "", "kid finished")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--follow", "--timeout", "2s")
	if code != 2 || out != "kid2 done: kid2 finished\nkid done: kid finished\ntracks: stopped watching after 2s\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllPrintsAStallWhenAnIdleAgentCrossesTheLimit(t *testing.T) {
	waitHome(t)
	since := time.Now()
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" {
			return agentAtTime("idle", "", since)
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--idle", "300ms", "--timeout", "5s")
	if code != 0 || out != "kid stalled: idle 0m with no ask\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllPrintsAStallForAnAgentThatTurnsBlockedOnAPermission(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid2" && polls.Add(1) > 12 {
			return agentAt("blocked", "permission", "")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "5s")
	if code != 0 || out != "kid2 blocked: permission\nkid2 stalled: blocked on a permission prompt\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllPrintsAStallForAnAgentThatExits(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" && polls.Add(1) > 12 {
			return rexResult{}
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "5s")
	if code != 0 || out != "kid exited: its agent is gone\nkid stalled: its agent has exited and the card is still open\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllDoesNotReportAStallThatWasThereWhenItStarted(t *testing.T) {
	waitHome(t)
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" {
			return agentAt("error", "", "broke before wait began")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "200ms")
	if code != 2 || out != "tracks: no change in any track within 200ms\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestWaitAllReportsAStallAgainAfterItClears(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess != "sess-kid" {
			return agentAt("working", "", "")
		}
		switch n := polls.Add(1); {
		case n <= 12:
			return agentAt("working", "", "")
		case n <= 14:
			return agentAt("blocked", "permission", "")
		}
		return agentAt("working", "", "")
	})
	out, code := do("wait", "--all", "--timeout", "5s")
	if code != 0 || !strings.Contains(out, "kid stalled: blocked on a permission prompt") {
		t.Fatalf("first: exit %d, output %q", code, out)
	}
	polls.Store(0)
	out, code = do("wait", "--all", "--timeout", "5s")
	if code != 0 || !strings.Contains(out, "kid stalled: blocked on a permission prompt") {
		t.Errorf("second: exit %d, output %q", code, out)
	}
}

func TestWaitAllTimesOutWithExit2(t *testing.T) {
	waitHome(t)
	fakeAgents(t, func(string) rexResult { return agentAt("working", "", "") })
	start := time.Now()
	out, code := do("wait", "--all", "--timeout", "150ms")
	if code != 2 || out != "tracks: no change in any track within 150ms\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
	if took := time.Since(start); took < 150*time.Millisecond || took > 3*time.Second {
		t.Errorf("took %v for a 150ms timeout", took)
	}
}

func TestWaitAllKeepsTheLastStateWhenRexFails(t *testing.T) {
	waitHome(t)
	var polls atomic.Int32
	fakeAgents(t, func(sess string) rexResult {
		if sess == "sess-kid" && polls.Add(1) > 6 {
			return rexResult{failed: true}
		}
		return agentAt("working", "", "")
	})
	if out, code := do("wait", "--all", "--timeout", "300ms"); code != 2 {
		t.Errorf("a Rex outage looked like a change: exit %d, output %q", code, out)
	}
}

func TestWaitAllRefusals(t *testing.T) {
	deskHome(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"wait", "--all", "boss"}, "usage: tracks wait"},
		{[]string{"wait", "--all", "--timeout", "soon"}, "--timeout is a duration"},
		{[]string{"wait", "--all", "--idle", "soon"}, "--idle is a duration"},
		{[]string{"wait", "boss", "--idle", "5m"}, "usage: tracks wait"},
	} {
		if out, code := do(c.args...); code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, output %q, want %q", c.args, code, out, c.want)
		}
	}
}
