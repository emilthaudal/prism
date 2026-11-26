package tracks

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// actsHome is a store with solo, a track whose agent runs idle in ansSess, with tier and asks
// as given, and the desk bound to deskSess. The test runs outside Rex.
func actsHome(t *testing.T, tier string, asks ...string) (th, bin string) {
	t.Helper()
	th, bin = leadStubHome(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	rawCard(t, "solo", nil)
	in := "where: working on it\n"
	if tier != "" {
		in += "tier: " + tier + "\n"
	}
	in += "asks:\n" + strings.Join(asks, "\n") + "\n"
	if out, code := cardIn(in, "card", "solo"); code != 0 {
		t.Fatalf("card solo: %d %s", code, out)
	}
	bindDesk(t, deskSess)
	return th, bin
}

func soloAsks(t *testing.T) []string {
	t.Helper()
	v, err := getView("solo")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range v.Asks {
		out = append(out, a.Text)
	}
	return out
}

const (
	versionAsk = "Which template-ci orb version? Recommend: 4.6.0"
	mergeAsk   = "PR 212 is green and approved. Merge it? Recommend: yes"
)

func TestTheDeskAnswersAQuestionThatIsNotEmilsAndLogsIt(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk, mergeAsk)
	asDesk(t)
	out, code := do("desk", "answer", "solo", "1", "4.6.0, the version shop-api runs")
	if code != 0 || out != "answered ask 1 on solo\n" {
		t.Fatalf("desk answer: %d %q", code, out)
	}
	want := `[desk] answer to your ask 1 ("Which template-ci orb version? Recommend: 4.6.0"): 4.6.0, the version shop-api runs`
	if got := sentText(bin); got != want {
		t.Errorf("typed %q\nwant  %q", got, want)
	}
	h := readDesk(t).Handled
	if len(h) != 1 || h[0].Kind != "answered" || h[0].Track != "solo" || h[0].Asked != versionAsk || h[0].Did != "4.6.0, the version shop-api runs" {
		t.Errorf("handled %+v", h)
	}
}

// TestTheDeskAnswersAnyAskOnAnyTier: the desk passes on Emil's answer to any ask, a decision
// on a tier-3 track included; the rules of the tier and Gatekeeper's confirm page still apply in
// the track that acts on it.
func TestTheDeskAnswersAnyAskOnAnyTier(t *testing.T) {
	cases := []struct {
		name, tier, ask, answer string
	}{
		{"a merge ask on a tier 3 track", "3: sends text to agents", mergeAsk, "yes, merge it: Emil said so in the desk's chat"},
		{"a question on a track with no tier", "", versionAsk, "4.6.0"},
		{"a merge ask on a tier 2 track", "2: product code", mergeAsk, "Emil merged it himself"},
		{"a close ask on a tier 1 track", "1: docs", "Goal met. Close this track? Recommend: yes", "yes"},
		{"a deploy ask", "2: product code", "Deploy to staging? Recommend: yes", "1. yes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, bin := actsHome(t, c.tier, c.ask)
			asDesk(t)
			if out, code := do("desk", "answer", "solo", "1", c.answer); code != 0 || out != "answered ask 1 on solo\n" {
				t.Fatalf("exit %d, output %q", code, out)
			}
			if got := sentText(bin); !strings.HasPrefix(got, "[desk] answer to your ask 1 (") || !strings.HasSuffix(got, "): "+c.answer) {
				t.Errorf("typed %q", got)
			}
			if h := readDesk(t).Handled; len(h) != 1 || h[0].Kind != "answered" || h[0].Asked != c.ask || h[0].Did != c.answer {
				t.Errorf("handled %+v", h)
			}
		})
	}
}

func TestTheDeskCannotAnswerAnAskThatIsNotThere(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk)
	asDesk(t)
	if out, code := do("desk", "answer", "solo", "3", "4.6.0"); code == 0 || !strings.Contains(out, "solo has no ask 3") {
		t.Errorf("exit %d, output %q", code, out)
	}
	nothingTyped(t, bin)
	if h := readDesk(t).Handled; len(h) != 0 {
		t.Errorf("a refused answer was logged: %+v", h)
	}
}

// A check ask clears in the pane or when its check stops holding, so a typed answer would be
// logged as answered while the ask stays open: the desk is refused.
func TestTheDeskCannotAnswerACheckAsk(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk)
	if out, code := cardIn("check: pr 4 merged\n", "card", "solo"); code != 0 {
		t.Fatal(out)
	}
	var n int
	if err := withStore(func(st *store.Store) error {
		if err := st.SetMode("solo", "waiting"); err != nil {
			return err
		}
		if _, err := st.RecordCheck("solo", "pr 4 merged", false); err != nil {
			return err
		}
		_, err := st.RecordCheck("solo", "pr 4 merged", true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	v, err := getView("solo")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Asks {
		if a.Kind == "check" {
			n = a.N
		}
	}
	if n == 0 {
		t.Fatalf("no check ask on solo: %+v", v.Asks)
	}
	asDesk(t)
	if out, code := do("desk", "answer", "solo", strconv.Itoa(n), "yes"); code == 0 || !strings.Contains(out, "check ask") {
		t.Errorf("exit %d, output %q", code, out)
	}
	nothingTyped(t, bin)
	if h := readDesk(t).Handled; len(h) != 0 {
		t.Errorf("a refused answer was logged: %+v", h)
	}
}

// The desk answers a thread's ask too, but types nothing while Emil's own prompt is in flight.
func TestTheDeskAnswersAThreadsAskButNotWhileEmilAnswers(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk)
	threadIn(t, "kid")
	if out, code := cardIn("tier: 2: product code\nasks:\n"+versionAsk+"\n", "card", "kid"); code != 0 {
		t.Fatal(out)
	}
	asDesk(t)
	if out, code := do("desk", "answer", "kid", "1", "4.6.0"); code != 0 {
		t.Errorf("answering a thread: %d %s", code, out)
	}
	os.Remove(filepath.Join(bin, "rex.sent"))
	os.Remove(filepath.Join(bin, "rex.calls"))
	if err := withStore(func(st *store.Store) error {
		return st.SetMeta("solo", map[string]string{"answered": time.Now().UTC().Format(time.RFC3339)})
	}); err != nil {
		t.Fatal(err)
	}
	if out, code := do("desk", "answer", "solo", "1", "4.6.0"); code == 0 || !strings.Contains(out, "Emil is answering solo's asks") {
		t.Errorf("answering while Emil answers: %d %s", code, out)
	}
	nothingTyped(t, bin)
}

func TestOnlyTheDesksAgentAnswersAsTheDesk(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk)
	asDesk(t)
	deskAgentIs(t, 999999, nil)
	if out, code := do("desk", "answer", "solo", "1", "4.6.0"); code == 0 || !strings.Contains(out, "does not run under the desk's agent") {
		t.Errorf("%d %s", code, out)
	}
	nothingTyped(t, bin)
}

// The desk may tell a track that waits on its asks the fact it waits on; the [desk] mark keeps
// that from reading as Emil's answer.
func TestTheDeskSendsAFactToATrackThatWaitsOnAsks(t *testing.T) {
	_, bin := actsHome(t, "3: sends text to agents", mergeAsk)
	asDesk(t)
	if out, code := do("send", "solo", "v3.16.0 already exists"); code != 0 || out != "sent to solo\n" {
		t.Fatalf("send to a track with asks: %d %s", code, out)
	}
	if got := sentText(bin); got != "[desk] v3.16.0 already exists" {
		t.Errorf("typed %q", got)
	}
	if got := soloAsks(t); len(got) != 1 || got[0] != mergeAsk {
		t.Errorf("the send changed solo's asks: %q", got)
	}
}

// TestTheDeskWritesATracksCard: on Emil's word the desk writes a track's card like any session,
// from its own session or a process under its agent: it clears an ask, lowers a tier, parks it.
func TestTheDeskWritesATracksCard(t *testing.T) {
	writes := []struct{ name, in string }{
		{"clears the asks", "asks:\n"},
		{"drops one ask", "where: merged #212\nasks:\n" + versionAsk + "\n"},
		{"lowers the tier", "tier: 1: docs\n"},
	}
	callers := []struct {
		name string
		prep func(t *testing.T)
	}{
		{"from the desk's session", func(t *testing.T) { asDesk(t) }},
		{"under the desk's agent with another session", func(t *testing.T) {
			deskAgentIs(t, os.Getppid(), nil)
			t.Setenv("REX_SESSION", ansSess)
		}},
		{"while Rex cannot say who the desk's agent is", func(t *testing.T) {
			deskAgentIs(t, 0, errors.New("Rex did not answer"))
			t.Setenv("REX_SESSION", ansSess)
		}},
	}
	for _, c := range callers {
		for _, w := range writes {
			t.Run(c.name+": "+w.name, func(t *testing.T) {
				actsHome(t, "3: sends text to agents", mergeAsk)
				c.prep(t)
				if out, code := cardIn(w.in, "card", "solo"); code != 0 {
					t.Fatalf("exit %d, output %q", code, out)
				}
				v, _ := withView("solo")
				switch w.name {
				case "lowers the tier":
					if v.Track.Tier != "1" {
						t.Errorf("tier now %q", v.Track.Tier)
					}
				default:
					if got := soloAsks(t); slices.Contains(got, mergeAsk) {
						t.Errorf("asks now %q", got)
					}
				}
			})
		}
	}
	t.Run("parks and focuses it", func(t *testing.T) {
		actsHome(t, "2: product code")
		asDesk(t)
		if out, code := do("mode", "boss", "background"); code != 0 { // focus holds two: make room
			t.Fatalf("making room: %d %q", code, out)
		}
		for _, mode := range []string{"parked", "focus"} {
			if out, code := do("mode", "solo", mode); code != 0 || out != "solo is "+mode+"\n" {
				t.Fatalf("mode %s: %d %q", mode, code, out)
			}
			if v, _ := withView("solo"); v.Track.Mode != mode {
				t.Errorf("mode now %q, want %s", v.Track.Mode, mode)
			}
		}
	})
}

func TestModeRefusals(t *testing.T) {
	actsHome(t, "2: product code")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"mode", "solo"}, "usage: tracks mode NAME"},
		{[]string{"mode", "solo", "asleep"}, "invalid mode"},
		{[]string{"mode", "-x", "parked"}, "not a track name"},
		{[]string{"mode", "nobody", "parked"}, "nobody"},
	} {
		if out, code := do(c.args...); code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, output %q, want %q", c.args, code, out, c.want)
		}
	}
}

// The desk passes on Emil's yes: the answer may read as a go-ahead.
func TestTheDeskPassesOnAGoAhead(t *testing.T) {
	for _, answer := range []string{"yes", "Yes, merge it", "ok", "1. yes", "approve"} {
		t.Run(answer, func(t *testing.T) {
			_, bin := actsHome(t, "2: product code", mergeAsk)
			asDesk(t)
			if out, code := do("desk", "answer", "solo", "1", answer); code != 0 {
				t.Fatalf("exit %d, output %q", code, out)
			}
			if got := sentText(bin); !strings.HasSuffix(got, "): "+answer) {
				t.Errorf("typed %q", got)
			}
		})
	}
}

// tracks send from the desk types any text, one shaped like an answer included.
func TestTheDeskSendsTextShapedLikeAnAnswer(t *testing.T) {
	_, bin := actsHome(t, "3: sends text to agents", versionAsk)
	asDesk(t)
	if out, code := do("send", "solo", "answer to your ask 1: 4.6.0, Emil says"); code != 0 {
		t.Fatalf("exit %d, output %q", code, out)
	}
	if got := sentText(bin); got != "[desk] answer to your ask 1: 4.6.0, Emil says" {
		t.Errorf("typed %q", got)
	}
}

// u takes an answer back and leaves no lock: the desk may send to the track and answer it again.
func TestATakeBackLeavesNoLock(t *testing.T) {
	bin, e := answeredHome(t)
	if got := undoEntry(e, ""); !strings.HasPrefix(got, "Took back") {
		t.Fatalf("undo: %q", got)
	}
	os.Remove(filepath.Join(bin, "rex.sent"))
	os.Remove(filepath.Join(bin, "rex.calls"))
	asDesk(t)
	if out, code := do("send", "solo", "lint is red on #212"); code != 0 {
		t.Errorf("send after the take-back: %d %q", code, out)
	}
	if out, code := do("desk", "answer", "solo", "1", "4.5.0, as Emil said"); code != 0 {
		t.Errorf("answering again after the take-back: %d %q", code, out)
	}
}

// startHome is actsHome with th trusted, where new agents show they started.
func startHome(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = actsHome(t, "2: product code")
	trustDirs(t, th)
	return th, bin
}

// Emil's own start, outside the desk, keeps a Rex session.
func TestStartMakesACardAndASessionAndLogsTheStart(t *testing.T) {
	th, bin := startHome(t)
	fakeClaude(t, bin)
	out, code := do("start", "investigate our scanner routines", "--dir", th, "--model", "sonnet")
	if code != 0 || !strings.HasPrefix(out, "started ") || !strings.HasSuffix(out, " in session:new-0001\n") {
		t.Fatalf("start: %d %q", code, out)
	}
	name := strings.Fields(out)[1]
	ls := lsTracks(t)[name]
	if ls.Goal != "investigate our scanner routines" || ls.Dir != th || ls.RexSession != "session:new-0001" || ls.Parent != nil {
		t.Errorf("the card: %+v", ls)
	}
	newArgs, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if !strings.Contains(string(newArgs), "claude\n--model\nsonnet\n--effort\nmedium\n--name\n"+name+"\n/track "+name) {
		t.Errorf("rex new args:\n%s", newArgs)
	}
	if strings.Contains(rexCalls(bin), "select") {
		t.Errorf("a start switched Emil's view:\n%s", rexCalls(bin))
	}
	if exists(filepath.Join(bin, "claude.bgargs")) {
		t.Error("Emil's start ran claude --bg")
	}
	h := readDesk(t).Handled
	if len(h) != 1 || h[0].Kind != "started" || h[0].Track != name || h[0].Asked != "investigate our scanner routines" {
		t.Errorf("handled %+v", h)
	}
}

func TestStartWithoutAModelStartsOpusAtMediumEffort(t *testing.T) {
	th, bin := startHome(t)
	fakeClaude(t, bin)
	out, code := do("start", "look at the scanner routines", "--dir", th)
	if code != 0 {
		t.Fatalf("start: %d %q", code, out)
	}
	newArgs, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if !strings.Contains(string(newArgs), "claude\n--model\nopus\n--effort\nmedium\n--name\n") {
		t.Errorf("rex new args:\n%s", newArgs)
	}
}

func TestStartRefusals(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, th string)
		args func(th string) []string
		want string
	}{
		{"from a track's session", func(t *testing.T, _ string) { t.Setenv("REX_SESSION", ansSess) },
			func(th string) []string { return []string{"start", "x y", "--dir", th} }, "a track does not start tracks"},
		{"from the desk's session but not its agent", func(t *testing.T, _ string) { asDesk(t); deskAgentIs(t, 999999, nil) },
			func(th string) []string { return []string{"start", "x y", "--dir", th} }, "does not run under the desk's agent"},
		{"a folder Claude has not trusted", nil,
			func(string) []string { return []string{"start", "x y", "--dir", os.TempDir()} }, "has not trusted"},
		{"a model it does not know", nil,
			func(th string) []string { return []string{"start", "x y", "--dir", th, "--model", "gpt"} }, "--model must be sonnet, opus or haiku"},
		{"an empty request", nil, func(th string) []string { return []string{"start", "  ", "--dir", th} }, "the request is empty"},
		{"no request", nil, func(string) []string { return []string{"start"} }, "usage: tracks start"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			th, bin := startHome(t)
			if c.prep != nil {
				c.prep(t, th)
			}
			out, code := do(c.args(th)...)
			if code == 0 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want %q", code, out, c.want)
			}
			if exists(filepath.Join(bin, "rex.newargs")) {
				t.Error("a refused start made a session")
			}
			if h := readDesk(t).Handled; len(h) != 0 {
				t.Errorf("a refused start was logged: %+v", h)
			}
		})
	}
}

func TestTheDeskMayRunTracksStartAndNothingWider(t *testing.T) {
	var starts []string
	for _, a := range deskAllowed {
		if strings.Contains(a, "start") || strings.Contains(a, "tracks:*") || a == "Bash" || strings.HasSuffix(a, "(*)") {
			starts = append(starts, a)
		}
	}
	if len(starts) != 1 || starts[0] != "Bash(tracks start:*)" {
		t.Errorf("start rules in deskAllowed: %q", starts)
	}
	s, err := deskSettings()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"defaultMode":"auto"`, `"disableBypassPermissionsMode":"disable"`, `"deny":["Bash(tracks bind:*)","Bash(tracks hook:*)"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("the desk's settings lack %s: %s", want, s)
		}
	}
}

// answeredHome is actsHome after the desk answered solo's version ask; it returns the entry.
func answeredHome(t *testing.T) (bin string, e store.DeskLogEntry) {
	t.Helper()
	_, bin = actsHome(t, "2: product code", versionAsk)
	asDesk(t)
	if out, code := do("desk", "answer", "solo", "1", "4.6.0"); code != 0 {
		t.Fatalf("desk answer: %d %s", code, out)
	}
	t.Setenv("REX_SESSION", "")
	os.Remove(filepath.Join(bin, "rex.sent"))
	os.Remove(filepath.Join(bin, "rex.calls"))
	return bin, lastEntry(t)
}

func lastEntry(t *testing.T) store.DeskLogEntry {
	t.Helper()
	var es []store.DeskLogEntry
	onDesk(t, func(st *store.Store) {
		var err error
		if es, err = st.DeskLogRecent(1); err != nil {
			t.Fatal(err)
		}
	})
	if len(es) != 1 {
		t.Fatal("the desk's log is empty")
	}
	return es[0]
}

func TestUndoTakesBackAnAnswerTheTrackHasNotActedOn(t *testing.T) {
	for _, state := range []string{"idle", "working"} {
		t.Run("its agent is "+state, func(t *testing.T) {
			bin, e := answeredHome(t)
			agentIs(t, bin, state, "")
			got := undoEntry(e, "")
			if got != "Took back the desk's answer to solo: its ask waits for you on its card" {
				t.Fatalf("undo: %q", got)
			}
			want := `[desk] disregard my answer to your ask "Which template-ci orb version? Recommend: 4.6.0" (I said: 4.6.0). Emil answers it himself: keep the ask on your card and undo anything you did on my answer.`
			if s := sentText(bin); s != want {
				t.Errorf("typed %q", s)
			}
			if asks := readDesk(t).Asks; len(asks) != 0 {
				t.Errorf("the take-back raised desk asks: %+v", asks)
			}
			if asks := soloAsks(t); len(asks) != 1 || asks[0] != versionAsk {
				t.Errorf("solo's asks after the take-back: %q", asks)
			}
			if e := lastEntry(t); e.UndoneAt.IsZero() {
				t.Error("the entry is not marked taken back")
			}
			if again := undoEntry(e, ""); again != "That was taken back already" {
				t.Errorf("a second undo: %q", again)
			}
		})
	}
}

func TestUndoLeavesAnAnswerTheTrackActedOn(t *testing.T) {
	cases := []struct {
		name string
		act  func(t *testing.T)
		want string
	}{
		{"it wrote its card", func(t *testing.T) {
			deskAgentIs(t, 999999, nil) // the track's own agent, not the desk's
			t.Setenv("REX_SESSION", ansSess)
			if out, code := cardIn("where: bumping the orb to 4.6.0\nasks:\n", "card", "solo"); code != 0 {
				t.Fatal(out)
			}
		}, "solo wrote its card at"},
		{"it finished a turn", func(t *testing.T) {
			t.Setenv("REX_SESSION", ansSess)
			hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
		}, "solo finished a turn at"},
		{"it was closed", func(t *testing.T) {
			if err := withStore(func(st *store.Store) error { return st.CloseTrack("solo") }); err != nil {
				t.Fatal(err)
			}
		}, "solo was closed at"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin, e := answeredHome(t)
			c.act(t)
			got := undoEntry(e, "")
			if !strings.HasPrefix(got, "Not taken back: ") || !strings.Contains(got, c.want) {
				t.Errorf("undo: %q, want %q", got, c.want)
			}
			nothingTyped(t, bin)
			if asks := readDesk(t).Asks; len(asks) != 0 {
				t.Errorf("desk asks %+v", asks)
			}
		})
	}
	t.Run("its turn before the answer does not count", func(t *testing.T) {
		_, bin := actsHome(t, "2: product code", versionAsk)
		t.Setenv("REX_SESSION", ansSess)
		hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
		asDesk(t)
		if out, code := do("desk", "answer", "solo", "1", "4.6.0"); code != 0 {
			t.Fatal(out)
		}
		t.Setenv("REX_SESSION", "")
		if got := undoEntry(lastEntry(t), ""); !strings.HasPrefix(got, "Took back") {
			t.Errorf("undo: %q", got)
		}
		if !strings.Contains(sentText(bin), "disregard my answer") {
			t.Error("nothing typed")
		}
	})
	t.Run("its agent waits on a prompt", func(t *testing.T) {
		bin, e := answeredHome(t)
		agentIs(t, bin, "blocked", "permission")
		if got := undoEntry(e, ""); !strings.Contains(got, "Not sent: solo is waiting on a permission prompt") {
			t.Errorf("undo: %q", got)
		}
		nothingTyped(t, bin)
		if e := lastEntry(t); !e.UndoneAt.IsZero() {
			t.Error("an undo that was not sent marked the entry")
		}
	})
}

// startedHome is startHome after the desk started a track; it returns the track and its entry.
// startedHome is a start logged as the desk's that made a Rex session: one run under the desk's
// agent outside its Rex session.
func startedHome(t *testing.T) (bin, name string, e store.DeskLogEntry) {
	t.Helper()
	th, bin := startHome(t)
	bindDesk(t, deskSess)
	deskAgentIs(t, os.Getppid(), nil)
	out, code := do("start", "investigate our scanner routines", "--dir", th)
	if code != 0 {
		t.Fatalf("start: %d %s", code, out)
	}
	t.Setenv("REX_SESSION", "")
	return bin, strings.Fields(out)[1], lastEntry(t)
}

func TestUndoClosesAStartedTrackThatNeverStartedWork(t *testing.T) {
	_, name, e := startedHome(t)
	got := undoEntry(e, "")
	if !strings.HasPrefix(got, "Closed "+name) {
		t.Errorf("undo: %q", got)
	}
	if v, err := withView(name); err != nil || !v.Closed {
		t.Errorf("%s is not closed: %v %+v", name, err, v.Track)
	}
	if e := lastEntry(t); e.UndoneAt.IsZero() {
		t.Error("the entry is not marked taken back")
	}
	if again := undoEntry(e, ""); again != "That was taken back already" {
		t.Errorf("a second undo: %q", again)
	}
}

func withView(name string) (store.View, error) {
	var v store.View
	err := withStore(func(st *store.Store) (err error) {
		v, err = st.Get(name)
		return err
	})
	return v, err
}

func TestUndoLeavesAStartedTrackThatStartedWork(t *testing.T) {
	cases := []struct {
		name string
		work func(t *testing.T, name string)
		want string
	}{
		{"it wrote its card", func(t *testing.T, name string) {
			deskAgentIs(t, 999999, nil) // the track's own agent writes, not the desk's
			if out, code := cardIn("where: looking at the routines\ntier: 2: product code\n", "card", name); code != 0 {
				t.Fatal(out)
			}
		}, "wrote its card at"},
		{"it finished a turn", func(t *testing.T, _ string) {
			t.Setenv("REX_SESSION", "session:new-0001")
			hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
		}, "finished a turn at"},
		{"it renamed itself", func(t *testing.T, name string) {
			if out, code := do("rename", name, "scanner-routines"); code != 0 {
				t.Fatal(out)
			}
		}, "renamed itself to scanner-routines"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, name, e := startedHome(t)
			c.work(t, name)
			got := undoEntry(e, "")
			if !strings.HasPrefix(got, "Not closed: ") || !strings.Contains(got, c.want) {
				t.Errorf("undo: %q, want %q", got, c.want)
			}
			if v, _ := withView(name); v.Closed {
				t.Errorf("%s was closed", name)
			}
		})
	}
	t.Run("a started entry for a track that start did not make", func(t *testing.T) {
		actsHome(t, "2: product code")
		onDesk(t, func(st *store.Store) {
			if _, err := st.LogDesk("solo", "started", "", "started solo"); err != nil {
				t.Fatal(err)
			}
		}, store.WithClock(func() time.Time { return time.Now().Add(time.Hour) }))
		if got := undoEntry(lastEntry(t), ""); !strings.Contains(got, "solo was not made by this start") {
			t.Errorf("undo: %q", got)
		}
		if v, _ := withView("solo"); v.Closed {
			t.Error("solo was closed")
		}
	})
}

func TestUOnTheHandledLogTakesBackTheSelectedEntry(t *testing.T) {
	_, name, _ := startedHome(t)
	m := newPane()
	plain(m, 120, 30)
	press(m, "h")
	if m.logv == nil {
		t.Fatal("h did not open the handled log")
	}
	if v, _ := withView(name); v.Closed {
		t.Fatal("opening the log closed the track")
	}
	picked := m.logv.entries[m.logv.idx].ID
	logEntry(t, "solo", "noted", "", "logged while the log was open")
	cmd := press(m, "u")
	if cmd == nil || !m.undoing {
		t.Fatalf("u gave no command (status %q)", m.status)
	}
	if again := press(m, "u"); again != nil {
		t.Error("a second u while the first runs gave a command")
	}
	m.Update(cmd())
	if v, _ := withView(name); !v.Closed {
		t.Errorf("u did not close %s: %q", name, m.status)
	}
	if m.logv == nil || len(m.logv.entries) != 2 || m.logv.entries[m.logv.idx].ID != picked || m.logv.entries[m.logv.idx].UndoneAt.IsZero() {
		t.Errorf("the cursor is not on the entry taken back: %+v", m.logv)
	}
}

func TestAStartOutsideTheDesksAgentIsNotLoggedAsTheDesks(t *testing.T) {
	for _, c := range []struct {
		name string
		prep func(t *testing.T)
		want string
	}{
		{"no Rex session", func(t *testing.T) { deskAgentIs(t, 999999, nil) }, "not the desk"},
		{"a session no card names", func(t *testing.T) { deskAgentIs(t, 999999, nil); t.Setenv("REX_SESSION", "session:not-a-card") }, "not the desk"},
		{"Rex cannot say", func(t *testing.T) { deskAgentIs(t, 0, errors.New("Rex did not answer")) }, "caller unknown"},
		{"under the desk's agent without its session", func(t *testing.T) { deskAgentIs(t, os.Getppid(), nil) }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, _ := startHome(t)
			c.prep(t)
			if out, code := do("start", "a request", "--dir", th); code != 0 {
				t.Fatalf("start: %d %s", code, out)
			}
			h := readDesk(t).Handled
			if len(h) != 1 || h[0].Kind != "started" {
				t.Fatalf("handled %+v", h)
			}
			if c.want == "" && strings.Contains(h[0].Did, "(") || c.want != "" && !strings.Contains(h[0].Did, c.want) {
				t.Errorf("did %q, want %q", h[0].Did, c.want)
			}
		})
	}
}

// A card write never asks Rex who the desk's agent is, whatever it changes.
func TestACardWriteNeverAsksRex(t *testing.T) {
	for _, c := range []struct{ name, start, write string }{
		{"the first tier on a new track", "", "where: starting\ntier: 2: product code\n"},
		{"the same tier again", "2: product code", "tier: 2: another reason\n"},
		{"raised to 3", "2: product code", "tier: 3: touches prod\n"},
		{"lowered to 2", "3: sends text to agents", "tier: 2: product code\n"},
		{"every ask dropped", "2: product code", "asks:\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			actsHome(t, c.start, mergeAsk)
			asked := 0
			old := deskAgentPID
			t.Cleanup(func() { deskAgentPID = old })
			deskAgentPID = func(string) (int, error) { asked++; return 0, errors.New("Rex did not answer") }
			t.Setenv("REX_SESSION", ansSess)
			if out, code := cardIn(c.write, "card", "solo"); code != 0 || asked != 0 {
				t.Errorf("exit %d, Rex asked %d times: %s", code, asked, out)
			}
		})
	}
}

// TestTheDeskClosesATrack: on Emil's word the desk closes a track, from its own session or a
// process under its agent.
func TestTheDeskClosesATrack(t *testing.T) {
	t.Run("from the desk's session", func(t *testing.T) {
		actsHome(t, "2: product code", versionAsk)
		asDesk(t)
		if out, code := do("close", "--yes", "solo"); code != 0 || strings.Contains(out, "refused") {
			t.Errorf("exit %d, output %q", code, out)
		}
		if v, err := withView("solo"); err != nil || !v.Closed {
			t.Errorf("solo is not closed: %v %+v", err, v.Track)
		}
	})
	t.Run("under the desk's agent with another session", func(t *testing.T) {
		actsHome(t, "3: sends text to agents", versionAsk)
		deskAgentIs(t, os.Getppid(), nil)
		t.Setenv("REX_SESSION", ansSess)
		if out, code := do("close", "--yes", "solo"); code != 0 || strings.Contains(out, "refused") {
			t.Errorf("exit %d, output %q", code, out)
		}
		if v, err := withView("solo"); err != nil || !v.Closed {
			t.Errorf("solo is not closed: %v %+v", err, v.Track)
		}
	})
}
