package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
	"github.com/emilthaudal/prism/internal/store"
)

func TestLoopNextContinuesPausesOrEnds(t *testing.T) {
	now := time.Date(2026, 11, 27, 2, 0, 0, 0, time.Local)
	reset := now.Add(90 * time.Minute)
	late := now.Add(10 * time.Hour)
	base := loopState{Track: "tuner", StartedAt: now.Add(-time.Hour), Until: now.Add(5 * time.Hour), Turns: 20, Budget: 70, Turn: 3, State: loopRunning}
	usage := func(five, seven float64, resets *time.Time, at time.Time) *deskui.Usage {
		return &deskui.Usage{FiveHour: &deskui.UsageLimit{UsedPercentage: five, ResetsAt: resets}, SevenDay: &deskui.UsageLimit{UsedPercentage: seven}, UpdatedAt: at}
	}
	with := func(f func(*loopState)) loopState { l := base; f(&l); return l }
	for _, c := range []struct {
		name string
		l    loopState
		in   loopInput
		want loopStep
	}{
		{"continue", base, loopInput{last: "Cut 4%.\nLOOP: continue - cache the parse", now: now}, loopStep{kind: loopRunning}},
		{"no marker continues on the cap", base, loopInput{last: "Did a thing.", now: now}, loopStep{kind: loopRunning}},
		{"the session says done", base, loopInput{last: "LOOP: continue - x\n…\nLOOP: done - nothing left above noise", now: now},
			loopStep{kind: loopEnded, why: `the session said "nothing left above noise"`}},
		{"stop asked", base, loopInput{last: "LOOP: continue - x", stop: true, now: now}, loopStep{kind: loopEnded, why: "stopped with tracks loop stop"}},
		{"turns used up", with(func(l *loopState) { l.Turn = 20 }), loopInput{last: "LOOP: continue - x", now: now}, loopStep{kind: loopEnded, why: "all 20 turns run"}},
		{"deadline passed", base, loopInput{last: "LOOP: continue - x", now: late}, loopStep{kind: loopEnded, why: "the deadline " + base.Until.Format("15:04") + " passed"}},
		{"over budget pauses until the reset", base, loopInput{now: now, usage: usage(70, 10, &reset, now)},
			loopStep{kind: loopPaused, why: "the five-hour usage is at 70%, at or over 70%", until: reset}},
		{"seven-day cap pauses", base, loopInput{now: now, usage: usage(10, 96, nil, now)},
			loopStep{kind: loopPaused, why: "the seven-day usage is at 96%, at or over 95%", until: now.Add(loopUnknownReset)}},
		{"reset after the deadline ends", with(func(l *loopState) { l.Until = now.Add(time.Hour) }), loopInput{now: now, usage: usage(80, 10, &reset, now)},
			loopStep{kind: loopEnded, why: "the five-hour usage is at 80%, at or over 70%, and it resets after the deadline"}},
		{"stale usage is unknown", base, loopInput{now: now, usage: usage(99, 99, &reset, now.Add(-2*time.Hour))}, loopStep{kind: loopRunning}},
		{"under budget", base, loopInput{now: now, usage: usage(69, 94, &reset, now)}, loopStep{kind: loopRunning}},
		{"an error is tried again past the turn cap", with(func(l *loopState) { l.Turn, l.Errors = 20, 2 }), loopInput{errored: true, now: now}, loopStep{kind: loopRunning}},
		{"the fourth error in a row ends it", with(func(l *loopState) { l.Errors = 3 }), loopInput{errored: true, now: now},
			loopStep{kind: loopEnded, why: "4 turns in a row ended in an error"}},
	} {
		if got := loopNext(c.l, c.in); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// loopStarted starts the desk's loop session tuner (aaaaaaa1) with the fake claude.
func loopStarted(t *testing.T, extra ...string) (id, bin string) {
	t.Helper()
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	args := append([]string{"start", "make the parser faster", "--dir", th, "--name", "tuner", "--loop"}, extra...)
	if out, code := do(args...); code != 0 || out != "started tuner in the background as aaaaaaa1\n" {
		t.Fatalf("start: %d %q", code, out)
	}
	loopSeen = map[string]deskSeen{}
	id = fullID("aaaaaaa1")
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": loopPrompt("tuner", mustLoop(t, "tuner"))})
	pollLines(t)
	return id, bin
}

// loopSeen is the watch's memory across one test's polls.
var loopSeen map[string]deskSeen

// loopTurn ends a turn of the loop session with answer and returns what the watch said.
func loopTurn(t *testing.T, id, answer string) []string {
	t.Helper()
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": answer})
	return pollLines(t)
}

// markError ends the session's turn as the reaper does when a turn ends without its Stop hook.
func markError(t *testing.T, id string) {
	t.Helper()
	err := withBareStore(func(st *store.Store) error {
		return st.MarkStalled([]store.Stall{{ID: id, Why: "its turn ended without a Stop hook"}}, time.Now().Add(time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
}

// lastNote is the last note claude --bg --resume got, and marks it submitted as the session's prompt.
func lastNote(t *testing.T, id, bin string) string {
	t.Helper()
	parts := strings.Split(strings.TrimSuffix(fileText(t, filepath.Join(bin, "claude.notes")), "\n<end>\n"), "\n<end>\n")
	n := parts[len(parts)-1]
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": n})
	return n
}

func TestALoopContinuesWithoutTheDeskAndReportsOnceWhenItEnds(t *testing.T) {
	id, bin := loopStarted(t, "--turns", "5", "--for", "6h")
	args := fileText(t, filepath.Join(bin, "claude.bgargs"))
	if !strings.Contains(args, "--model\nsonnet\n") || !strings.Contains(args, "/track tuner\n\nThis track runs as an unattended loop") || !strings.Contains(args, "`LOOP: done - <why nothing worth doing is left>`") {
		t.Errorf("start args:\n%s", args)
	}
	if lines := loopTurn(t, id, "Parse is 4% faster.\nLOOP: continue - cache the tokens"); len(lines) != 0 {
		t.Errorf("a continuing loop reported to the desk: %q", lines)
	}
	if n := lastNote(t, id, bin); !strings.HasPrefix(n, "Continue the loop: iteration 2 of 5, until ") || !strings.Contains(n, "never merge, push, deploy or install") {
		t.Errorf("note %q", n)
	}
	if l := mustLoop(t, "tuner"); l.Turn != 2 || l.State != loopRunning {
		t.Errorf("loop after a turn: %+v", l)
	}
	lines := loopTurn(t, id, "Tokens cached: 2% more.\nLOOP: done - what is left is noise")
	want := "tuner done: [loop ended after 2 of 5 turns: the session said \"what is left is noise\"] Tokens cached: 2% more. LOOP: done - what is left is noise"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("end lines:\n%q\nwant\n%q", lines, want)
	}
	if n := strings.Count(fileText(t, filepath.Join(bin, "claude.notes")), "<end>"); n != 1 {
		t.Errorf("%d notes delivered, want 1", n)
	}
	var loopLines []string
	for _, h := range readDesk(t).Handled {
		if h.Asked == "loop" {
			loopLines = append(loopLines, h.Did)
		}
	}
	if len(loopLines) != 2 || !strings.HasPrefix(loopLines[0], "loop started: 5 turns until ") || loopLines[1] != "loop ended after 2 of 5 turns: the session said \"what is left is noise\"" {
		t.Errorf("handled log loop entries: %q", loopLines)
	}
	if lines := loopTurn(t, id, "Something else."); len(lines) != 1 || !strings.HasPrefix(lines[0], "tuner done: Something else.") {
		t.Errorf("after the loop ended the session reports as usual: %q", lines)
	}
}

func pollLines(t *testing.T) []string {
	t.Helper()
	lines, err := pollDesk(loopSeen, false)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func mustLoop(t *testing.T, track string) loopState {
	t.Helper()
	l, ok := readLoop(track)
	if !ok {
		t.Fatalf("no loop on %s", track)
	}
	return l
}

func TestALoopOverBudgetPausesUntilTheReset(t *testing.T) {
	id, bin := loopStarted(t)
	now := time.Now()
	reset := now.Add(time.Hour).Truncate(time.Second)
	usage := map[string]any{"at": now.UTC().Format(time.RFC3339), "five_hour": map[string]any{"used_percentage": 75, "resets_at": reset.Unix()}}
	b, _ := json.Marshal(usage)
	if err := os.WriteFile(usagePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if lines := loopTurn(t, id, "LOOP: continue - more"); len(lines) != 0 {
		t.Errorf("a paused loop reported: %q", lines)
	}
	if l := mustLoop(t, "tuner"); l.State != loopPaused || l.PausedUntil == nil || !l.PausedUntil.Equal(reset) || l.Turn != 1 {
		t.Errorf("loop: %+v", l)
	}
	if exists(filepath.Join(bin, "claude.notes")) {
		t.Error("a paused loop got a note")
	}
	if out, _ := do("loop"); out != "tuner  turn 1/20  until "+loopClock(mustLoop(t, "tuner").Until, now)+"  paused until "+reset.Format("15:04")+"\n" {
		t.Errorf("tracks loop: %q", out)
	}
	old := loopNow
	t.Cleanup(func() { loopNow = old })
	loopNow = func() time.Time { return reset.Add(time.Minute) }
	if lines := pollLines(t); len(lines) != 0 {
		t.Errorf("resuming reported: %q", lines)
	}
	if l := mustLoop(t, "tuner"); l.State != loopRunning || l.Turn != 2 || l.PausedUntil != nil {
		t.Errorf("loop after the pause: %+v", l)
	}
	if n := lastNote(t, id, bin); !strings.HasPrefix(n, "Continue the loop: iteration 2 of 20") {
		t.Errorf("note %q", n)
	}
}

func TestALoopRetriesAnErrorThreeTimesThenEnds(t *testing.T) {
	id, bin := loopStarted(t)
	for i := range 4 {
		markError(t, id)
		lines := pollLines(t)
		if i < 3 && len(lines) != 0 {
			t.Fatalf("error %d reported: %q", i+1, lines)
		}
		if i == 3 && (len(lines) != 1 || !strings.Contains(lines[0], "[loop ended after 1 of 20 turns: 4 turns in a row ended in an error]")) {
			t.Fatalf("fourth error: %q", lines)
		}
		if i < 3 {
			if n := strings.Count(fileText(t, filepath.Join(bin, "claude.notes")), "<end>"); n != i+1 {
				t.Fatalf("error %d: %d notes delivered", i+1, n)
			}
			if n := lastNote(t, id, bin); !strings.HasPrefix(n, "Your last turn was cut off. Continue the loop: iteration 1 of 20") {
				t.Fatalf("note %q", n)
			}
			pollLines(t)
		}
	}
}

func TestTracksLoopListsAndStopsLoops(t *testing.T) {
	id, _ := loopStarted(t, "--turns", "3", "--until", time.Now().Add(3*time.Hour).Format("15:04"))
	until := mustLoop(t, "tuner").Until
	if out, _ := do("loop"); out != "tuner  turn 1/3  until "+loopClock(until, time.Now())+"  running\n" {
		t.Errorf("tracks loop: %q", out)
	}
	if out, code := do("loop", "stop", "tuner"); code != 0 || out != "tuner's loop ends after its current turn; its session is not stopped\n" {
		t.Errorf("stop: %d %q", code, out)
	}
	if out, _ := do("loop"); !strings.HasSuffix(out, "  running, stops after this turn\n") {
		t.Errorf("tracks loop after stop: %q", out)
	}
	if lines := loopTurn(t, id, "LOOP: continue - more"); len(lines) != 1 || !strings.Contains(lines[0], "[loop ended after 1 of 3 turns: stopped with tracks loop stop]") {
		t.Errorf("lines %q", lines)
	}
	if out, _ := do("loop"); !strings.HasSuffix(out, "  ended: stopped with tracks loop stop\n") {
		t.Errorf("tracks loop after the end: %q", out)
	}
	if out, code := do("loop", "stop", "tuner"); code == 0 || !strings.Contains(out, "has ended") {
		t.Errorf("stop an ended loop: %d %q", code, out)
	}
}

func TestALoopEndsWhenEmilTakesItOver(t *testing.T) {
	id, _ := loopStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "let me look at this"})
	pollLines(t)
	if l := mustLoop(t, "tuner"); l.State != loopEnded || l.Ended != "Emil took the session over" {
		t.Errorf("loop: %+v", l)
	}
}

func TestStartRefusesLoopFlagsOutsideTheDeskOrWithoutLoop(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--turns", "3"}, "--until, --for, --turns and --budget go with --loop"},
		{[]string{"--loop", "--until", "25:00"}, "--until must be HH:MM"},
		{[]string{"--loop", "--until", "06:00", "--for", "8h"}, "give --until or --for, not both"},
		{[]string{"--loop", "--budget", "0"}, "--budget must be a percent"},
	} {
		out, code := do(append([]string{"start", "x", "--dir", th}, c.args...)...)
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: %d %q", c.args, code, out)
		}
	}
	if ents, _ := os.ReadDir(loopsDir()); len(ents) != 0 {
		t.Errorf("refused starts left loops: %v", ents)
	}
}

func TestOnlyTheDeskStartsALoop(t *testing.T) {
	th, _ := startHome(t)
	if out, code := do("start", "make it faster", "--dir", th, "--loop"); code == 0 || !strings.Contains(out, "refused: --loop is the desk's") {
		t.Errorf("start --loop outside the desk: %d %q", code, out)
	}
	if ents, _ := os.ReadDir(loopsDir()); len(ents) != 0 {
		t.Errorf("a refused start left loops: %v", ents)
	}
}

func TestALoopsPermissionPromptIsDeniedAfterTheWait(t *testing.T) {
	id, _ := loopStarted(t)
	fastPermission(t, "1m")
	old := loopPermissionWait
	loopPermissionWait = 100 * time.Millisecond
	t.Cleanup(func() { loopPermissionWait = old })
	select {
	case out := <-askPermission(t, id, ghSuggestion):
		d := hookDecisionOf(t, out)
		if d["behavior"] != "deny" || d["message"] != loopDenyText {
			t.Errorf("decision %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop's permission prompt waited past the loop's wait")
	}
}

func TestTheSnapshotShowsALoopSessionsLoop(t *testing.T) {
	id, _ := loopStarted(t, "--turns", "7")
	snap, err := newLiveSource(nil).snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(snap)
	var doc struct {
		Sessions []struct {
			ID   string          `json:"id"`
			Loop json.RawMessage `json:"loop"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, s := range doc.Sessions {
		if s.ID != id {
			continue
		}
		var l map[string]any
		if json.Unmarshal(s.Loop, &l) != nil || l["turn"] != 1.0 || l["turns"] != 7.0 || l["state"] != "running" || l["paused_until"] != nil || l["until"] == nil {
			t.Errorf("loop %s", s.Loop)
		}
		return
	}
	t.Errorf("no session %s in %s", id, b)
}

func TestALoopsMarkerCountsOnlyOnTheLastNonEmptyLine(t *testing.T) {
	for _, c := range []struct{ text, verdict, why string }{
		{"LOOP: done - early\nbut more work followed", "", ""},
		{"I will say LOOP: done - later", "", ""},
		{"work\n\n  LOOP: continue - next  \n\n", "continue", "next"},
		{"work\n`LOOP: done - nothing left`", "done", "nothing left"},
		{"LOOP: continue - x\nLOOP: done - y", "done", "y"},
	} {
		if v, why := loopVerdict(c.text); v != c.verdict || why != c.why {
			t.Errorf("%q: %q %q, want %q %q", c.text, v, why, c.verdict, c.why)
		}
	}
}

func TestALoopQuotesTheSessionsReasonCutToTwoHundredCharacters(t *testing.T) {
	base := loopState{Track: "tuner", Until: time.Now().Add(time.Hour), Turns: 5, Turn: 1}
	long := strings.Repeat("é", 250)
	step := loopNext(base, loopInput{last: "LOOP: done - " + long, now: time.Now()})
	want := `the session said "` + strings.Repeat("é", 200) + `…"`
	if step.kind != loopEnded || step.why != want {
		t.Errorf("%q", step.why)
	}
	if step := loopNext(base, loopInput{last: "LOOP: done", now: time.Now()}); step.why != "the session said it is done" {
		t.Errorf("%q", step.why)
	}
}

func TestTheRouteStopsALoopAndAnswers404WithoutOne(t *testing.T) {
	_, c := liveHome(t)
	tr := c.snapshot().Tracks[2]
	path := "/v1/tracks/" + tr.ID + "/loop/stop"
	if code, _ := c.post(path, ""); code != http.StatusNotFound {
		t.Errorf("no loop: %d", code)
	}
	l := loopState{Track: tr.Name, StartedAt: time.Now(), Until: time.Now().Add(time.Hour), Turns: 5, Turn: 2, Budget: 70, State: loopRunning}
	if err := writeLoop(l); err != nil {
		t.Fatal(err)
	}
	code, v := c.post(path, "")
	if code != 200 || v["status"] != tr.Name+"'s loop ends after its current turn; its session is not stopped" {
		t.Errorf("stop: %d %v", code, v)
	}
	if !stopRequested(tr.Name) {
		t.Error("no stop request was written")
	}
	l.State, l.Ended = loopEnded, "done"
	if err := writeLoop(l); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.post(path, ""); code != http.StatusNotFound {
		t.Errorf("ended loop: %d", code)
	}
}

func TestTheMacStaysAwakeWhileALoopIsLive(t *testing.T) {
	startHome(t)
	started, stopped := 0, 0
	old := caffeinateStart
	caffeinateStart = func() (func(), error) { started++; return func() { stopped++ }, nil }
	t.Cleanup(func() { releaseLoopsAwake(); caffeinateStart = old })
	live := func(track string) loopState {
		return loopState{Track: track, StartedAt: time.Now(), Until: time.Now().Add(time.Hour), Turns: 5, Turn: 1, State: loopRunning}
	}
	keepLoopsAwake()
	if started != 0 {
		t.Fatalf("started with no loop: %d", started)
	}
	a, b := live("one"), live("two")
	for _, l := range []loopState{a, b} {
		if err := writeLoop(l); err != nil {
			t.Fatal(err)
		}
	}
	keepLoopsAwake()
	keepLoopsAwake()
	if started != 1 || stopped != 0 {
		t.Fatalf("two loops: started %d stopped %d", started, stopped)
	}
	a.State = loopEnded
	_ = writeLoop(a)
	keepLoopsAwake()
	if started != 1 || stopped != 0 {
		t.Fatalf("one loop left: started %d stopped %d", started, stopped)
	}
	b.State = loopPaused
	_ = writeLoop(b)
	keepLoopsAwake()
	if stopped != 0 {
		t.Fatalf("a paused loop is live: stopped %d", stopped)
	}
	b.State = loopEnded
	_ = writeLoop(b)
	keepLoopsAwake()
	if started != 1 || stopped != 1 {
		t.Fatalf("no loop left: started %d stopped %d", started, stopped)
	}
}

func TestALoopFileOnlyAppliesToTheSessionItStarted(t *testing.T) {
	id, bin := loopStarted(t, "--turns", "5")
	if l := mustLoop(t, "tuner"); l.Session != id {
		t.Fatalf("the loop recorded session %q, want %q", l.Session, id)
	}
	other := fullID("bbbbbbb2")
	err := withBareStore(func(st *store.Store) error {
		return st.StartDeskSession(store.DeskStart{ID: other, Parent: deskClaude, Track: "tuner", Name: "tuner", Prompt: "/track tuner"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isLoopSession(id) || isLoopSession(other) {
		t.Errorf("isLoopSession: first %v, second %v", isLoopSession(id), isLoopSession(other))
	}
	lines := loopTurn(t, other, "Did something.\nLOOP: continue - more")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "tuner done: Did something.") {
		t.Errorf("the second session's stop: %q", lines)
	}
	if exists(filepath.Join(bin, "claude.notes")) {
		t.Error("another session's loop file continued the second session")
	}
	if l := mustLoop(t, "tuner"); l.Turn != 1 || l.State != loopRunning {
		t.Errorf("loop moved: %+v", l)
	}
}

func TestClosingTheTrackOrAPlainStartRemovesAStaleLoopFile(t *testing.T) {
	loopStarted(t)
	if out, code := do("close", "--yes", "tuner"); code != 0 {
		t.Fatalf("close: %d %q", code, out)
	}
	if _, ok := readLoop("tuner"); ok {
		t.Error("closing the track left its loop file")
	}
}

func TestAPlainStartRemovesAStaleLoopFile(t *testing.T) {
	th, bin := startHome(t)
	asDeskInClaude(t, bin)
	stale := loopState{Track: "ghost", StartedAt: time.Now(), Until: time.Now().Add(time.Hour), Turns: 5, Turn: 1, State: loopRunning}
	if err := writeLoop(stale); err != nil {
		t.Fatal(err)
	}
	if out, code := do("start", "something else", "--dir", th, "--name", "ghost"); code != 0 {
		t.Fatalf("start: %d %q", code, out)
	}
	if _, ok := readLoop("ghost"); ok {
		t.Error("a plain start left the stale loop file")
	}
}

func TestAFailingNoteEndsTheLoopAndForwardsTheDone(t *testing.T) {
	id, _ := loopStarted(t)
	old := loopContinue
	loopContinue = func(store.Session, string) error { return errors.New("db is locked") }
	t.Cleanup(func() { loopContinue = old })
	lines := loopTurn(t, id, "Did a thing.\nLOOP: continue - more")
	if len(lines) != 1 || !strings.Contains(lines[0], "[loop ended after 2 of 20 turns: could not deliver the next note: db is locked]") {
		t.Errorf("lines %q", lines)
	}
	if l := mustLoop(t, "tuner"); l.State != loopEnded || l.Ended != "could not deliver the next note: db is locked" {
		t.Errorf("loop: %+v", l)
	}
}

func TestARestartPicksUpALoopWhoseTurnEndedWhileTheWatchWasDown(t *testing.T) {
	id, bin := loopStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Cut 3%.\nLOOP: continue - more"})
	lines, err := pollDesk(map[string]deskSeen{}, true)
	if err != nil || len(lines) != 0 {
		t.Fatalf("first poll: %q %v", lines, err)
	}
	if l := mustLoop(t, "tuner"); l.Turn != 2 || l.State != loopRunning {
		t.Errorf("loop: %+v", l)
	}
	if n := lastNote(t, id, bin); !strings.HasPrefix(n, "Continue the loop: iteration 2 of 20") {
		t.Errorf("note %q", n)
	}
}
