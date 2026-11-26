package tracks

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCheck(t *testing.T) {
	valid := map[string]check{
		"pr 123 approved":           {"pr", "123", "approved"},
		"pr 7 merged":               {"pr", "7", "merged"},
		"pr 42 green":               {"pr", "42", "green"},
		"ci main green":             {"ci", "main", "green"},
		"ci feat/EMI-277_x.y green": {"ci", "feat/EMI-277_x.y", "green"},
		"ci release/2026.11 green":  {"ci", "release/2026.11", "green"},
	}
	for in, want := range valid {
		got, ok := parseCheck(in)
		if !ok || got != want {
			t.Errorf("parseCheck(%q) = %+v %v, want %+v", in, got, ok, want)
		}
		if got.kind == "ci" {
			if err := exec.Command("git", "check-ref-format", "--branch", got.arg).Run(); err != nil {
				t.Errorf("git refuses branch %q we accept: %v", got.arg, err)
			}
		}
	}
	hostile := []string{
		"", "pr 1; rm -rf ~", "pr $(x) merged", "ci --upload-pack=x green", "ci main green && x",
		"pr 1 approved; x", "pr -1 approved", "pr 1 approved\nx", "ci -x green", "ci main green\n",
		"ci $(x) green", "ci `x` green", "ci a b green", "pr 1  approved", "PR 1 approved", "pr 1 closed",
		"pr 1234567890 merged", " pr 1 merged", "ci main red",
		"ci ../x green", "ci a//b green", "ci x/ green", "ci /x green", "ci a..b green", "ci .x green",
		"ci a/.b green", "ci x.lock green", "ci a/x.lock/b green", "ci x. green", "ci HEAD green",
		"ci a@{1} green", "ci a?b green", "ci a%2e%2e green", "ci a/-b green",
	}
	for _, in := range hostile {
		if c, ok := parseCheck(in); ok {
			t.Errorf("parseCheck(%q) accepted: %+v", in, c)
		}
	}
}

func waitingCard(t *testing.T, th, name, chk string) string {
	t.Helper()
	txt := "---\nname: " + name + "\nmode: waiting   # Emil\ndir: " + th + "\ncheck: " + chk + "\nupdated: 2026-01-01T00:00:00Z\n---\n## Goal\nG\n\n## Needs me\n\n## Standing orders\n- x\n"
	foldBack(t)
	if err := writeAtomic(cardPath(name), []byte(txt)); err != nil {
		t.Fatal(err)
	}
	return txt
}

func checks(t *testing.T, dry bool, eval evaluator) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if err := runChecks(dry, eval, &out, &errb); err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String()
}

func readCard(name string) string { b, _ := os.ReadFile(cardPath(name)); return string(b) }

func TestHostileChecksNeverEvaluated(t *testing.T) {
	th, _ := setup(t)
	for i, chk := range []string{"pr 1; rm -rf ~", "pr $(x) merged", "ci --upload-pack=x green", "ci main green && x", "ci ../x green"} {
		waitingCard(t, th, string(rune('a'+i)), chk)
	}
	calls := 0
	_, errs := checks(t, false, func(string, check) (string, error) { calls++; return yes, nil })
	if calls != 0 || strings.Count(errs, "unknown check") != 5 {
		t.Errorf("calls=%d stderr:\n%s", calls, errs)
	}
}

func TestCheckTransitions(t *testing.T) {
	th, _ := setup(t)
	orig := waitingCard(t, th, "w", "pr 5 approved")
	card(t, th, "focus", "focus", "", "x", "")
	var ans string
	var evalErr error
	calls := 0
	eval := func(dir string, c check) (string, error) {
		calls++
		if dir != th || c != (check{"pr", "5", "approved"}) {
			t.Errorf("eval(%q, %+v)", dir, c)
		}
		return ans, evalErr
	}

	ans = no
	if out, _ := checks(t, false, eval); !strings.Contains(out, "unchanged") || readCard("w") != orig {
		t.Fatalf("first no fired:\n%s", out)
	}
	ans = pending
	if out, _ := checks(t, false, eval); !strings.Contains(out, "pending, not recorded") || readCard("w") != orig {
		t.Fatalf("pending fired:\n%s", out)
	}
	ans = yes
	if out, _ := checks(t, true, eval); !strings.Contains(out, "would add") || readCard("w") != orig {
		t.Fatalf("dry run wrote:\n%s", out)
	}
	if out, _ := checks(t, false, eval); !strings.Contains(out, "added an ask") ||
		!strings.Contains(readCard("w"), "## Needs me\n1. Check \"pr 5 approved\" is now yes. Recommend: open the track.\n") {
		t.Fatalf("yes did not fire:\n%s\n%s", out, readCard("w"))
	}
	after := readCard("w")
	if checks(t, false, eval); readCard("w") != after {
		t.Fatal("repeat fired")
	}
	evalErr = errors.New("boom")
	if _, errs := checks(t, false, eval); !strings.Contains(errs, "boom") || readCard("w") != after {
		t.Fatalf("error fired or not reported: %s", errs)
	}
	evalErr, ans = nil, pending
	if checks(t, false, eval); readCard("w") != after {
		t.Fatal("pending after yes fired")
	}
	ans = yes
	if checks(t, false, eval); readCard("w") != after {
		t.Fatal("error or pending overwrote stored answer")
	}
	ans = no
	if checks(t, false, eval); !strings.Contains(readCard("w"), "\n1. Check \"pr 5 approved\" is now no.") {
		t.Fatalf("yes to no did not fire:\n%s", readCard("w"))
	}
	if calls != 9 {
		t.Errorf("calls = %d", calls)
	}
	evs := readEvents()
	if len(evs) != 2 || evs[0].Event != "check" || evs[0].Track != "w" || evs[0].Kind != yes || evs[1].Kind != no {
		t.Errorf("events: %+v", evs)
	}
	m, err := loadChecks()
	if st := m["w: pr 5 approved"]; err != nil || len(m) != 1 || st.Answer || st.Asked == "" {
		t.Errorf("checks.json: %+v %v", m, err)
	}
}

func TestFlippedCheckSupersedesItsOldAsk(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 green")
	const yesAsk, noAsk = `Check "pr 5 green" is now yes.`, `Check "pr 5 green" is now no.`
	ans := yes
	eval := func(string, check) (string, error) { return ans, nil }
	checks(t, false, eval)
	if !strings.Contains(readCard("w"), yesAsk) {
		t.Fatalf("no yes ask:\n%s", readCard("w"))
	}
	ans = no
	checks(t, false, eval)
	if c := readCard("w"); strings.Contains(c, yesAsk) || strings.Count(c, noAsk) != 1 {
		t.Fatalf("want only the no ask:\n%s", c)
	}
	ans = yes
	checks(t, false, eval)
	if c := readCard("w"); strings.Contains(c, noAsk) || strings.Count(c, yesAsk) != 1 {
		t.Fatalf("want only the yes ask:\n%s", c)
	}
	if cardIn("asks:\n", "card", "w"); !strings.Contains(readCard("w"), yesAsk) {
		t.Errorf("an unseen ask was dropped by an asks: write:\n%s", readCard("w"))
	}
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "answers"})
	if cardIn("asks:\n", "card", "w"); strings.Contains(readCard("w"), yesAsk) {
		t.Errorf("a seen ask survived an asks: write:\n%s", readCard("w"))
	}
	ranOnTheStore(t)
}

// A flapping check on a waiting card, where no agent write renumbers the asks, must keep its
// one ask within the pane's 1-9 keys; and an agent's own question that starts like a check ask
// is not a check ask.
func TestFlappingCheckKeepsAskOneAndAgentAsks(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "ci main green")
	cardIn("asks:\nCheck \"ci main green\" is now yes. Should I merge? Recommend: merge.\n", "card", "w")
	const own = `Check "ci main green" is now yes. Should I merge?`
	ans := yes
	eval := func(string, check) (string, error) { return ans, nil }
	for i := range 10 {
		want, other := `Check "ci main green" is now yes. Recommend`, `Check "ci main green" is now no. Recommend`
		ans = yes
		if i%2 == 1 {
			ans, want, other = no, other, want
		}
		checks(t, false, eval)
		// Every flip leaves exactly the current check ask, whatever the agent's ask says.
		if c := readCard("w"); strings.Count(c, want) != 1 || strings.Contains(c, other) || !strings.Contains(c, own) {
			t.Fatalf("flip %d (%s):\n%s", i+1, ans, c)
		}
	}
	c := readCard("w")
	if !strings.Contains(c, "\n2. Check \"ci main green\" is now no.") || strings.Contains(c, "is now yes. Recommend") {
		t.Errorf("after 10 flips want only the no ask as 2:\n%s", c)
	}
	if !strings.Contains(c, own) {
		t.Errorf("the agent's own ask was dropped:\n%s", c)
	}
}

func TestCheckReaddsAskLostToAgentRewrite(t *testing.T) {
	th, _ := setup(t)
	noImport(t) // an agent's file rewrite cannot drop an ask from tracks.db; it is kept as a hand edit
	orig := waitingCard(t, th, "w", "pr 5 merged")
	eval := func(string, check) (string, error) { return yes, nil }
	const ask = "Check \"pr 5 merged\" is now yes."
	checks(t, false, eval)
	waitingCard(t, th, "w", "pr 5 merged")
	if out, _ := checks(t, false, eval); !strings.Contains(out, "re-added an ask") || strings.Count(readCard("w"), ask) != 1 {
		t.Fatalf("lost ask not re-added:\n%s\n%s", out, readCard("w"))
	}
	if out, _ := checks(t, false, eval); !strings.Contains(out, "unchanged") || strings.Count(readCard("w"), ask) != 1 {
		t.Fatalf("ask added twice:\n%s\n%s", out, readCard("w"))
	}
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "system"})
	waitingCard(t, th, "w", "pr 5 merged")
	if checks(t, false, eval); strings.Count(readCard("w"), ask) != 1 {
		t.Fatal("a system prompt counted as Emil handling the ask")
	}
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "answers"})
	waitingCard(t, th, "w", "pr 5 merged")
	if checks(t, false, eval); readCard("w") != orig {
		t.Fatalf("re-added an ask Emil already handled:\n%s", readCard("w"))
	}
	if evs := readEvents(); len(evs) != 3 || evs[0].Event != "check" {
		t.Errorf("events: %+v", evs)
	}
}

func TestCheckSkipsCardChangedMidCheck(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 merged")
	var rewritten string
	_, errs := checks(t, false, func(string, check) (string, error) {
		if dbExists() {
			// The card file is a view: the agent changes check: with tracks card.
			if out, code := cardIn("check: pr 6 merged\n", "card", "w"); code != 0 {
				t.Fatal(out)
			}
			rewritten = readCard("w")
		} else {
			rewritten = waitingCard(t, th, "w", "pr 6 merged")
		}
		return yes, nil
	})
	m, _ := loadChecks()
	if readCard("w") != rewritten || !strings.Contains(errs, "card changed") || len(m) != 0 || len(readEvents()) != 0 {
		t.Errorf("stderr %q checks %v card:\n%s", errs, m, readCard("w"))
	}
}

func checkCardInMode(t *testing.T, th, name, mode, chk string) {
	t.Helper()
	txt := "---\nname: " + name + "\nmode: " + mode + "   # Emil\ndir: " + th + "\ncheck: " + chk + "\nupdated: 2026-01-01T00:00:00Z\n---\n## Goal\nG\n\n## Needs me\n\n## Standing orders\n- x\n"
	foldBack(t)
	if err := writeAtomic(cardPath(name), []byte(txt)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRunsInAnyModeButParked(t *testing.T) {
	th, _ := setup(t)
	checkCardInMode(t, th, "f", "focus", "pr 5 approved")
	eval := func(string, check) (string, error) { return yes, nil }
	if out, _ := checks(t, false, eval); !strings.Contains(out, "added an ask") ||
		!strings.Contains(readCard("f"), "1. Check \"pr 5 approved\" is now yes. Recommend: open the track.\n") {
		t.Fatalf("focus card did not fire:\n%s\n%s", out, readCard("f"))
	}
	after := readCard("f")
	if checks(t, false, eval); readCard("f") != after || strings.Count(readCard("f"), "is now yes") != 1 {
		t.Fatalf("focus card fired twice:\n%s", readCard("f"))
	}
	if !isCheckAsk(`Check "pr 5 approved" is now yes. Recommend: open the track.`) {
		t.Error("new ask shape is not a check ask")
	}

	checkCardInMode(t, th, "p", "parked", "pr 6 approved")
	calls := 0
	checks(t, false, func(string, check) (string, error) { calls++; return yes, nil })
	if calls != 1 || strings.Contains(readCard("p"), "is now") {
		t.Errorf("parked card checked: calls=%d\n%s", calls, readCard("p"))
	}
}

func TestCheckAnswerSurvivesModeChanges(t *testing.T) {
	th, _ := setup(t)
	eval := func(string, check) (string, error) { return yes, nil }
	const ask = "Check \"pr 5 approved\" is now yes."
	checkCardInMode(t, th, "w", "focus", "pr 5 approved")
	checks(t, false, eval)
	for _, mode := range []string{"waiting", "parked", "waiting", "focus"} {
		txt := readCard("w")
		txt = strings.Replace(txt, "mode: focus", "mode: "+mode, 1)
		txt = strings.Replace(txt, "mode: waiting", "mode: "+mode, 1)
		txt = strings.Replace(txt, "mode: parked", "mode: "+mode, 1)
		foldBack(t)
		if err := writeAtomic(cardPath("w"), []byte(txt)); err != nil {
			t.Fatal(err)
		}
		checks(t, false, eval)
		if strings.Count(readCard("w"), ask) != 1 {
			t.Fatalf("re-fired in %s:\n%s", mode, readCard("w"))
		}
		if m, _ := loadChecks(); len(m) != 1 {
			t.Fatalf("answer pruned in %s: %v", mode, m)
		}
	}
	foldBack(t)
	if err := os.Remove(cardPath("w")); err != nil {
		t.Fatal(err)
	}
	checks(t, false, eval)
	if m, _ := loadChecks(); len(m) != 0 {
		t.Errorf("answer kept for a card that is gone: %v", m)
	}
}

func TestInsertAsk(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	const fm = "---\nname: n\nmode: waiting # c\nupdated: old\n---\n"
	const stamped = "---\nname: n\nmode: waiting # c\nupdated: 2026-11-16T10:00:00Z\n---\n"
	cases := []struct{ name, in, want string }{
		{"existing asks",
			fm + "## Needs me\n1. a? Recommend: y\n  3) b\n\n## Standing orders\n- s\n",
			stamped + "## Needs me\n1. a? Recommend: y\n  3) b\n4. ASK\n\n## Standing orders\n- s\n"},
		{"empty section",
			fm + "## Where it is\nw\n\n## Needs me\n\n## Standing orders\n- s\n",
			stamped + "## Where it is\nw\n\n## Needs me\n1. ASK\n\n## Standing orders\n- s\n"},
		{"section at end without newline",
			fm + "## Needs me\n1. a",
			stamped + "## Needs me\n1. a\n2. ASK\n"},
		{"last heading wins, as in parseCard",
			fm + "## Needs me\n5. old\n\n## Later\n## Needs me\n1. b\n",
			stamped + "## Needs me\n5. old\n\n## Later\n## Needs me\n1. b\n2. ASK\n"},
		{"missing, before standing orders",
			fm + "## Goal\ng\n\n## Standing orders\n- s\n",
			stamped + "## Goal\ng\n\n## Needs me\n1. ASK\n\n## Standing orders\n- s\n"},
		{"missing, at end",
			fm + "## Goal\ng\n",
			stamped + "## Goal\ng\n\n## Needs me\n1. ASK\n"},
		{"no updated key",
			"---\nmode: waiting\n---\n## Needs me\n",
			"---\nmode: waiting\nupdated: 2026-11-16T10:00:00Z\n---\n## Needs me\n1. ASK\n"},
	}
	for _, c := range cases {
		got := insertAsk(c.in, "ASK", now)
		if got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
		if !strings.Contains(parseCard("n", got).Sections["Needs me"], "ASK") {
			t.Errorf("%s: parseCard does not see the ask", c.name)
		}
	}
}

const holdGate = `{"__typename":"StatusContext","context":"ci/circleci: build-branch/hold-staging","state":"PENDING"}`

func TestJudgeGreen(t *testing.T) {
	c := check{"pr", "1", "green"}
	for in, want := range map[string]string{
		`[` + holdGate + `,{"name":"Scanner","status":"COMPLETED","conclusion":"SUCCESS"},{"context":"ci/circleci: test","state":"SUCCESS"}]`: yes,
		`[{"name":"hold-test","status":"QUEUED"},{"name":"build","status":"COMPLETED","conclusion":"SKIPPED"}]`:                               yes,
		`[` + holdGate + `,{"context":"ci/circleci: test","state":"PENDING"},{"name":"a","status":"COMPLETED","conclusion":"SUCCESS"}]`:       pending,
		`[{"name":"a","status":"IN_PROGRESS"}]`:                                                   pending,
		`[{"name":"a","status":"IN_PROGRESS"},{"context":"ci/circleci: lint","state":"FAILURE"}]`: no,
		`[{"name":"a","status":"COMPLETED","conclusion":"FAILURE"}]`:                              no,
		`[` + holdGate + `]`: pending,
		`[]`:                 pending,
		`[{"context":"ci/circleci: build-branch/hold_staging_feature","state":"FAILURE"},{"context":"ci/circleci: hold","state":"PENDING"},{"name":"approve-prod","status":"QUEUED"},{"name":"b","status":"COMPLETED","conclusion":"SUCCESS"}]`: yes,
		`[{"name":"threshold-check","status":"COMPLETED","conclusion":"FAILURE"},{"name":"b","status":"COMPLETED","conclusion":"SUCCESS"}]`:                                                                                                     no,
		`[{"name":"holdout","status":"COMPLETED","conclusion":"FAILURE"},{"name":"b","status":"COMPLETED","conclusion":"SUCCESS"}]`:                                                                                                             no,
	} {
		if got, err := c.answer([]byte(`{"statusCheckRollup":` + in + `}`)); err != nil || got != want {
			t.Errorf("%s: %v %v, want %s", in, got, err, want)
		}
	}
}

func TestCIAnswer(t *testing.T) {
	c := check{"ci", "main", "green"}
	wrap := func(rollup string) string {
		return `{"data":{"repository":{"ref":{"target":{"statusCheckRollup":` + rollup + `}}}}}`
	}
	for in, want := range map[string]string{
		wrap(`{"contexts":{"nodes":[{"name":"build","status":"COMPLETED","conclusion":"SUCCESS"},` + holdGate + `]}}`): yes,
		wrap(`{"contexts":{"nodes":[{"name":"build","status":"COMPLETED","conclusion":"FAILURE"}]}}`):                  no,
		wrap(`{"contexts":{"nodes":[{"name":"build","status":"IN_PROGRESS"}]}}`):                                       pending,
		wrap(`null`): pending,
	} {
		if got, err := c.answer([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if _, err := c.answer([]byte(`{"data":{"repository":{"ref":null}}}`)); err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Errorf("missing branch: %v", err)
	}
}

func TestGhEval(t *testing.T) {
	th, bin := setup(t)
	t.Setenv("TRACKS_GH_ACCOUNTS", "acme=me-at-acme")
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho git@github.com:acme/svc.git\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo "tok-$4"; exit 0; fi
printf '%s|' "$GH_TOKEN" "$@" >> "`+bin+`/gh.calls"; echo >> "`+bin+`/gh.calls"
if [ "$1" = api ]; then echo '{"data":{"repository":{"ref":{"target":{"statusCheckRollup":{"contexts":{"nodes":[{"context":"ci/circleci: test","state":"SUCCESS"}]}}}}}}}'; exit 0; fi
echo '{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},`+holdGate+`]}'
`), 0o755)
	for _, chk := range []check{{"pr", "9", "approved"}, {"pr", "9", "green"}, {"ci", "feat/x", "green"}} {
		if got, err := ghEval(th, chk); err != nil || got != yes {
			t.Errorf("%+v: %v %v", chk, got, err)
		}
	}
	calls, _ := os.ReadFile(filepath.Join(bin, "gh.calls"))
	lines := strings.Split(string(calls), "\n")
	if lines[0] != "tok-me-at-acme|pr|view|9|--json|state,reviewDecision,statusCheckRollup|" {
		t.Errorf("gh pr call: %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], "tok-me-at-acme|api|graphql|-F|owner={owner}|-F|repo={repo}|-f|ref=refs/heads/feat/x|-f|query=query(") {
		t.Errorf("gh ci call: %q", lines[2])
	}
	if _, err := ghEval("relative", check{"pr", "9", "approved"}); err == nil {
		t.Error("relative dir accepted")
	}
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho 'no oauth token found for me-at-acme' >&2\nexit 1\n"), 0o755)
	if _, err := ghEval(th, check{"pr", "9", "approved"}); err == nil || !strings.Contains(err.Error(), "no oauth token found") {
		t.Errorf("auth error lacks gh stderr: %v", err)
	}
	if ghAccount("https://github.com/someone/tracks.git") != "" || ghAccount("") != "" || ghAccount("https://github.com/ACME/x") != "me-at-acme" {
		t.Error("owner mapping")
	}
}

func TestTheStarLoginIsTheFallbackAfterOwnersAndDirs(t *testing.T) {
	t.Setenv("TRACKS_GH_ACCOUNTS", "acme=work, /w=work, *=personal")
	for _, tc := range []struct {
		dir, remote string
		c           check
		want        string
	}{
		{"/x/svc", "git@github.com:acme/svc.git", check{"ci", "main", "green"}, "work"},
		{"/w/svc", "git@github.com:someone/svc.git", check{"ci", "main", "green"}, "work"},
		{"/x/svc", "git@github.com:someone/svc.git", check{"ci", "main", "green"}, "personal"},
		{"/x/svc", "", check{"pr", "acme/svc#4", "approved"}, "work"},
		{"/w/svc", "", check{"pr", "someone/svc#4", "approved"}, "personal"},
	} {
		if got := ghAccountFor(tc.dir, tc.c, tc.remote); got != tc.want {
			t.Errorf("%s %s %+v: %q, want %q", tc.dir, tc.remote, tc.c, got, tc.want)
		}
	}
}

func TestGhEvalWithoutAMappingUsesGhsOwnLogin(t *testing.T) {
	th, bin := setup(t)
	t.Setenv("TRACKS_GH_ACCOUNTS", "")
	t.Setenv("GH_TOKEN", "")
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho git@github.com:someone/svc.git\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo "auth called" >&2; exit 1; fi
printf '[%s]' "$GH_TOKEN" >> "`+bin+`/gh.calls"
echo '{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[]}'
`), 0o755)
	if got, err := ghEval(th, check{"pr", "9", "approved"}); err != nil || got != yes {
		t.Fatalf("%v %v", got, err)
	}
	if calls, _ := os.ReadFile(filepath.Join(bin, "gh.calls")); string(calls) != "[]" {
		t.Errorf("GH_TOKEN set without a mapping: %q", calls)
	}
}
