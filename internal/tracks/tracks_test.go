package tracks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("TRACKS_HOME", th)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "60")
	t.Setenv("REX_SESSION", "") // the Rex session running the tests is not the closing agent's
	// Tests run inside a claude --bg job must not look like the desk's own session.
	t.Setenv(deskParentEnv, "")
	t.Setenv(deskPromptEnv, "")
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("rex", "shift\n"+
		"if [ \"$1 $2\" = \"session ls\" ]; then echo '{\"sessions\":[{\"session_id\":\"session:x-sess9\",\"label\":\"a\"},{\"session_id\":\"session:x-sa\",\"label\":\"a\"},{\"session_id\":\"session:x-sb\",\"label\":\"b\"}]}'; exit 0; fi\n"+
		"echo \"$@\" >> \""+bin+"/rex.calls\"\n")
	stub("git", "echo 'abc123 a commit'\n")
	stub("gh", "echo '[{\"number\":7}]'\n")
	return
}

func card(t *testing.T, th, name, mode, sess, where, needs string) {
	t.Helper()
	txt := "---\nname: " + name + "\nmode: " + mode + "   # c\ndir: " + th + "\nrex_session: " + sess + "\n---\n## Goal\nG " + name + "\n\n## Where it is\n" + where + "\n\n## Needs me\n" + needs + "\n"
	foldBack(t)
	if err := writeAtomic(cardPath(name), []byte(txt)); err != nil {
		t.Fatal(err)
	}
}

func do(args ...string) (string, int) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return out.String() + errb.String(), code
}

func TestClassifier(t *testing.T) {
	cases := map[string]string{
		"1. yes 2. no": "answers",
		"Go with your recommendation on 1 and 2.":                                         "answers",
		"go with your recommendation on 3. Also merge it":                                 "answers",
		`Emil's answers: Emil's answer to your ask 1, "Q?": go with your recommendation.`: "answers",
		"go ahead":                    "go-ahead",
		"whats left?":                 "status",
		"merge it":                    "go-ahead",
		"yes please":                  "go-ahead",
		"i merged them":               "news",
		"they approved":               "news",
		"Implement PAN-213":           "new-work",
		"we should use the main repo": "direction",
		"<task-notification>x":        "system",
		"<system-reminder>\nstatus":   "system",
		"line one\nstatus please":     "status",
		" yes":                        "go-ahead",
		"\nmerge":                     "go-ahead",

		`Check "pr 1 merged" is now yes. Continue per the card.`: "system",
	}
	for in, want := range cases {
		if got := classify(in); got != want {
			t.Errorf("classify(%q)=%s want %s", in, got, want)
		}
	}
}

func TestListOrderAndWarning(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "bg", "background", "s1", "bg where", "")
	card(t, th, "wait", "waiting", "", "w where", "")
	card(t, th, "f1", "focus", "s2", "f1 where", "1. q? Recommend: y\n2. r?")
	card(t, th, "f2", "focus", "", "f2 where", "")
	card(t, th, "f3", "focus", "", "f3 where", "")
	card(t, th, "ask", "background", "", "ask where", "1. q")
	card(t, th, "pk", "parked", "", "parked", "")
	appendEvent(Event{At: time.Now().Add(-12*time.Minute - time.Second).UTC(), Event: "stop", RexSession: "s2"})
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", RexSession: "s1"})
	out, _ := do()
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		names = append(names, strings.Fields(l)[0])
	}
	if got := strings.Join(names, " "); got != "f1 f2 f3 ask bg wait pk warning:" {
		t.Fatalf("order: %s\n%s", got, out)
	}
	if !strings.Contains(out, "f1    focus") || !strings.Contains(out, " 2  idle 12m") {
		t.Errorf("asks/live missing:\n%s", out)
	}
	if !strings.Contains(out, "working") || !strings.Contains(out, "pk    parked") {
		t.Errorf("bad list:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if len([]rune(l)) > 60 {
			t.Errorf("line over width: %q", l)
		}
	}
}

func TestOpen(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", "sess9", "where a", "1. ask")
	out, code := do("open", "a")
	if code != 0 || !strings.Contains(out, "G a") || !strings.Contains(out, "abc123") || !strings.Contains(out, "number") {
		t.Fatalf("open: %d\n%s", code, out)
	}
	calls, _ := os.ReadFile(filepath.Join(bin, "rex.calls"))
	if strings.TrimSpace(string(calls)) != "do session.select session_id=session:x-sess9" {
		t.Errorf("rex call: %q", calls)
	}
	if evs := readEvents(); len(evs) != 1 || evs[0].Event != "open" || evs[0].Track != "a" {
		t.Errorf("events: %+v", evs)
	}
}

func TestOpenRexFailsAndNoSession(t *testing.T) {
	th, bin := setup(t)
	os.WriteFile(filepath.Join(bin, "rex"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	card(t, th, "a", "focus", "s", "w", "")
	card(t, th, "b", "focus", "", "w", "")
	for n, want := range map[string]struct {
		code int
		note string
	}{"a": {3, "could not switch"}, "b": {2, "no rex_session"}} {
		out, code := do("open", n)
		if code != want.code || !strings.Contains(out, "Goal") || !strings.Contains(out, want.note) {
			t.Errorf("%s: %d\n%s", n, code, out)
		}
	}
}

func TestOpenSessionGoneExits2(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", "dead99", "w", "")
	out, code := do("open", "a")
	if code != 2 || !strings.Contains(out, "could not switch Rex to the session") || !strings.Contains(out, "Goal") {
		t.Errorf("%d\n%s", code, out)
	}
	if strings.Contains(rexCalls(bin), "session.select") {
		t.Errorf("selected a session that is gone:\n%s", rexCalls(bin))
	}
}

func TestNew(t *testing.T) {
	th, _ := setup(t)
	out, code := do("new", "x", "--dir", "/r")
	if code != 0 || out != "In the track's session, type: /track x\n" {
		t.Fatalf("%d %q", code, out)
	}
	c, err := loadCard("x")
	if err != nil || c.mode() != "background" || c.Meta["dir"] != "/r" || c.Sections["Where it is"] != "Not started." {
		t.Errorf("card: %+v %v", c, err)
	}
	if _, code := do("new", "x", "--dir", "/r"); code == 0 {
		t.Error("expected refusal for existing card")
	}
	if _, err := os.Stat(filepath.Join(th, "cards", "x.md")); err != nil {
		t.Error(err)
	}
}

func hook(t *testing.T, json string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"hook"}, strings.NewReader(json), &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	return out.String() + errb.String()
}

func TestStopHookWritesWhereFromTheReportUnlessTheAgentDid(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "")
	where := func() string { return whereFirstLine(loadCardMust(t, "tr")) }
	stop := func(report string) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "cwd": "/c", "last_assistant_message": report})
		hook(t, string(b))
	}
	stop("First report.")
	if got := where(); got != "w" {
		t.Errorf("the first Stop only records the card: where %q", got)
	}
	stop("Merged PR 12; next I wait on review.\nDetails below.")
	if got := where(); got != "Merged PR 12; next I wait on review." {
		t.Errorf("Stop did not write where from the report: %q", got)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"card", "tr"}, strings.NewReader("where: Goal changed: now the plan is B.\n"), &out, &errb); code != 0 {
		t.Fatal(out.String() + errb.String())
	}
	stop("Reworked the plan.")
	if got := where(); got != "Goal changed: now the plan is B." {
		t.Errorf("Stop overwrote a card the agent wrote this turn: %q", got)
	}
	stop("PR 13 is open; waiting on review.")
	if got := where(); got != "PR 13 is open; waiting on review." {
		t.Errorf("the next Stop did not resume: %q", got)
	}
}

func TestStopHookKeepsTheDetailLinesUnderWhere(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w\nCI is green on the head commit.", "")
	stop := func(report string) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "cwd": "/c", "last_assistant_message": report})
		hook(t, string(b))
	}
	stop("First.")
	stop("PR 14 is open.")
	if got := strings.TrimSpace(loadCardMust(t, "tr").Sections["Where it is"]); got != "PR 14 is open.\nCI is green on the head commit." {
		t.Errorf("where %q", got)
	}
}

func TestHook(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "")
	if o := hook(t, `{"hook_event_name":"UserPromptSubmit","cwd":"/c","prompt":"merge it SECRET"}`); o != "" {
		t.Errorf("output %q", o)
	}
	hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
	hook(t, `{"hook_event_name":"Notification"}`)
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"<system-reminder>x"}`)
	if o := hook(t, `not json`); o != "" {
		t.Errorf("output %q", o)
	}
	raw, _ := os.ReadFile(logPath())
	if strings.Contains(string(raw), "SECRET") {
		t.Error("prompt text stored")
	}
	evs := readEvents()
	if len(evs) != 4 || evs[0].Kind != "go-ahead" || evs[0].Len != 15 || evs[0].Track != "tr" || evs[0].RexSession != "s5" ||
		evs[1].Event != "stop" || evs[2].Event != "notify" || evs[3].Kind != "system" {
		t.Errorf("events: %+v", evs)
	}
}

func TestHookSkipsHeadless(t *testing.T) {
	setup(t)
	t.Setenv("REX_SESSION", "s5")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-cli")
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"merge it"}`)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	hook(t, `{"hook_event_name":"Stop"}`)
	if evs := readEvents(); len(evs) != 1 || evs[0].Event != "stop" {
		t.Errorf("events: %+v", evs)
	}
}

func TestLiveStateNeedsYou(t *testing.T) {
	now := time.Now()
	evs := []Event{{At: now, Event: "prompt", RexSession: "s"}, {At: now, Event: "notify", RexSession: "s"}}
	if got := liveState(evs, "s", now); got != "needs you" {
		t.Errorf("got %q", got)
	}
}

func TestOpenRejectsPath(t *testing.T) {
	setup(t)
	if _, code := do("open", "../../x"); code == 0 {
		t.Error("path accepted")
	}
}

func TestStats(t *testing.T) {
	setup(t)
	now := time.Now().UTC()
	appendEvent(Event{At: now, Event: "open", Track: "a"})
	for _, k := range []string{"go-ahead", "direction", "system", "news", "direction"} {
		appendEvent(Event{At: now, Event: "prompt", Kind: k})
	}
	appendEvent(Event{At: now.AddDate(0, 0, -30), Event: "prompt", Kind: "news"})
	out, _ := do("stats")
	if !strings.Contains(out, "total") || !strings.Contains(out, "50%") || !strings.Contains(out, "4.0") ||
		!strings.HasSuffix(out, "baseline (orch 2-16 Nov): 35% avoidable\n") {
		t.Errorf("stats:\n%s", out)
	}
}

func TestParseCardComments(t *testing.T) {
	c := parseCard("n", "---\nmode: waiting # x\nwaiting_on: bob # how\n---\n## Needs me\n1. a\n  2) b\nnot 3.\n")
	if c.mode() != "waiting" || c.Meta["waiting_on"] != "bob" || c.asks() != 2 {
		t.Errorf("%+v asks=%d", c.Meta, c.asks())
	}
	c = parseCard("n", "---\nwaiting_on: PR #42 review\ndir: /x/C#proj\nmode: \"focus\"\n---\n")
	if c.Meta["waiting_on"] != "PR #42 review" || c.Meta["dir"] != "/x/C#proj" || c.mode() != "focus" {
		t.Errorf("%+v", c.Meta)
	}
}
