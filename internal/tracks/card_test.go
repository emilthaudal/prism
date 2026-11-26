package tracks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cardIn(stdin string, args ...string) (string, int) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return out.String() + errb.String(), code
}

const busyCard = `---
name: t
mode: waiting      # focus | background | waiting | parked (Emil sets this)
tier: 1
dir: /work/repo
rex_session: session:abc
claude_session: 1234-abcd
claude_cwd: /work/repo
answered: 2026-11-20T09:00:00Z
waiting_on: Anna
check: pr 7 approved
links: EMI-1
updated: 2026-11-19T10:00:00Z
---
## Goal
Ship the thing.

## Where it is
Old state.
Old detail.
Tier 1: docs only.

## Needs me
1. Old question? Recommend: no.
2. Check "pr 7 approved" is now yes. Recommend: set mode to focus.

## Standing orders
- Ask before merging.

## Later
- old idea
`

func TestCardWritesAgentFieldsAndKeepsTheRest(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	in := `where: PR 9 is open and waits on review; next I fix comments.
CI is green on abc123.
Staging e2e passed.
tier: 2: product code with tests
links: EMI-1, https://github.com/o/r/pull/9
check: pr 9 approved
waiting_on: review from Anna
asks:
1. Ship behind the flag first? Recommend: yes.
- Retry window 24h or 72h? Recommend: 72h.
later:
- new idea
`
	out, code := cardIn(in, "card", "t")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := "1. Ship behind the flag first? Recommend: yes.\n2. Retry window 24h or 72h? Recommend: 72h.\n3. Check \"pr 7 approved\" is now yes. Recommend: set mode to focus.\n"
	if out != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out, want)
	}
	c := loadCardMust(t, "t")
	for k, v := range map[string]string{
		"mode": "waiting", "tier": "2", "dir": "/work/repo", "rex_session": "session:abc",
		"claude_session": "1234-abcd", "claude_cwd": "/work/repo",
		"waiting_on": "review from Anna", "check": "pr 9 approved", "links": "EMI-1, https://github.com/o/r/pull/9",
	} {
		if c.Meta[k] != v {
			t.Errorf("%s = %q, want %q", k, c.Meta[k], v)
		}
	}
	if want := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC); !metaTime(c, "answered").Equal(want) {
		t.Errorf("answered = %q, want %v", c.Meta["answered"], want)
	}
	if at := cardUpdated(c); time.Since(at) > time.Minute {
		t.Errorf("updated not stamped: %q", c.Meta["updated"])
	}
	if c.Sections["Goal"] != "Ship the thing." || c.Sections["Standing orders"] != "- Ask before merging." {
		t.Errorf("goal or standing orders changed:\n%s", readCard("t"))
	}
	if w := c.Sections["Where it is"]; w != "PR 9 is open and waits on review; next I fix comments.\nCI is green on abc123.\nStaging e2e passed.\nTier 2: product code with tests" {
		t.Errorf("where:\n%s", w)
	}
	if c.Sections["Later"] != "- new idea" {
		t.Errorf("later: %q", c.Sections["Later"])
	}
	// With tracks.db the card is rendered whole, so only the files keep a line's own spacing.
	if !dbExists() && !strings.Contains(readCard("t"), "mode: waiting      # focus") {
		t.Errorf("mode line's comment lost:\n%s", readCard("t"))
	}
	entries, _ := os.ReadDir(filepath.Dir(cardPath("t")))
	if len(entries) != 1 {
		t.Errorf("leftover files next to the card: %v", entries)
	}
}

func TestCardLeavesOutFieldsAsTheyAre(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	if out, code := cardIn("tier: 3 runs headless\n", "card", "t"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	c := loadCardMust(t, "t")
	if w := c.Sections["Where it is"]; w != "Old state.\nOld detail.\nTier 3: runs headless" {
		t.Errorf("where:\n%s", w)
	}
	if len(c.askList()) != 2 || c.Meta["links"] != "EMI-1" || c.Sections["Later"] != "- old idea" || c.Meta["check"] != "pr 7 approved" {
		t.Errorf("a field left out changed:\n%s", readCard("t"))
	}

	// An empty asks: clears the agent's asks and keeps the check's.
	out, code := cardIn("where: Waiting on review.\nasks:\n", "card", "t")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if out != "1. Check \"pr 7 approved\" is now yes. Recommend: set mode to focus.\n" {
		t.Errorf("printed %q", out)
	}
	if w := loadCardMust(t, "t").Sections["Where it is"]; w != "Waiting on review.\nTier 3: runs headless" {
		t.Errorf("tier line not kept: %q", w)
	}
}

// A check ask stays until Emil has seen it, then an agent's asks: list may drop it, and tracks
// check does not put it back.
func TestCardDropsACheckAskOnlyOnceAnswered(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 merged")
	eval := func(string, check) (string, error) { return yes, nil }
	const ask = `Check "pr 5 merged" is now yes.`
	checks(t, false, eval)
	if !strings.Contains(readCard("w"), ask) {
		t.Fatalf("no check ask:\n%s", readCard("w"))
	}

	out, code := cardIn("asks:\n", "card", "w")
	if code != 0 || !strings.Contains(out, ask) || !strings.Contains(readCard("w"), ask) {
		t.Fatalf("unanswered check ask dropped: exit %d %q\n%s", code, out, readCard("w"))
	}
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "system"})
	if cardIn("asks:\n", "card", "w"); !strings.Contains(readCard("w"), ask) {
		t.Fatal("a system prompt counted as Emil answering the check ask")
	}

	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "answers"})
	out, code = cardIn("asks:\nNext? Recommend: yes.\n", "card", "w")
	if code != 0 || out != "1. Next? Recommend: yes.\n" || strings.Contains(readCard("w"), ask) {
		t.Fatalf("answered check ask kept: exit %d %q\n%s", code, out, readCard("w"))
	}
	if checks(t, false, eval); strings.Contains(readCard("w"), ask) {
		t.Errorf("tracks check put the answered ask back:\n%s", readCard("w"))
	}
}

func TestCardPrintsNoAsksAndFillsAFreshCard(t *testing.T) {
	setup(t)
	if out, code := cardIn("", "bind", "fresh"); code != 0 {
		t.Fatalf("bind: exit %d: %s", code, out)
	}
	in := "goal: The pane shows tiers.\ndir: /Users/x/Developer/tracks\nwhere: Started.\ntier: 1\n"
	out, code := cardIn(in, "card", "fresh")
	if code != 0 || out != "no asks\n" {
		t.Fatalf("exit %d: %q", code, out)
	}
	c := loadCardMust(t, "fresh")
	if c.Sections["Goal"] != "The pane shows tiers." || c.Meta["dir"] != "/Users/x/Developer/tracks" || c.tier() != "1" || c.Sections["Where it is"] != "Started." {
		t.Errorf("card:\n%s", readCard("fresh"))
	}
	if strings.Contains(readCard("fresh"), "Commit, push") {
		t.Errorf("template still has the default standing order:\n%s", readCard("fresh"))
	}
}

func TestCardRefusesBadInput(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	for in, why := range map[string]string{
		"":                            "no fields",
		"mode: focus\n":               "not a field",
		"where: a\nb\nc\nd\n":         "at most 2",
		"where: a\n## Needs me\n":     "##",
		"asks:\n## x\n":               "##",
		"tier: 4\n":                   "tier must be",
		"where: a\nwhere: b\n":        "twice",
		"waiting_on: Anna # urgent\n": "comment",
		"dir: relative/path\n":        "absolute",
		"hello\nwhere: a\n":           "not a field",
	} {
		out, code := cardIn(in, "card", "t")
		if code == 0 || !strings.Contains(out, why) {
			t.Errorf("%q: exit %d, %q; want an error with %q", in, code, out, why)
		}
	}
	if readCard("t") != busyCard {
		t.Errorf("a refused input changed the card:\n%s", readCard("t"))
	}
	if out, code := cardIn("where: x\n", "card", "nobody"); code == 0 || !strings.Contains(out, "tracks bind nobody") {
		t.Errorf("missing card: exit %d, %q", code, out)
	}
}

func TestCardOnAClosedTrackWritesNothing(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	if _, _, err := closeTrack("t", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	out, code := cardIn("where: still going\n", "card", "t")
	if code != 0 || out != "closed\n" {
		t.Errorf("exit %d: %q", code, out)
	}
	gone(t, "t")
	if out, code := cardIn("", "bind", "t"); code != 0 || out != "closed\n" {
		t.Errorf("bind: exit %d: %q", code, out)
	}
	gone(t, "t")
}

func TestCardFollowsARename(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("old"), strings.Replace(busyCard, "name: t", "name: old", 1))
	if _, err := renameTrack("old", "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, code := cardIn("where: Renamed and going.\n", "card", "old")
	if code != 0 || !strings.HasPrefix(out, "renamed to new\n1. Old question?") {
		t.Errorf("exit %d: %q", code, out)
	}
	gone(t, "old")
	if w := loadCardMust(t, "new").firstLine("Where it is"); w != "Renamed and going." {
		t.Errorf("new card's where: %q", w)
	}
	if out, code := cardIn("", "bind", "old"); code != 0 || out != "renamed to new\n" {
		t.Errorf("bind: exit %d: %q", code, out)
	}
	gone(t, "old")
}

// The pane sets the mode while tracks card holds the card: the lock makes the pane wait, so
// the agent's write and the mode both survive.
func TestCardAndAConcurrentSetModeBothSurvive(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	done := make(chan error, 1)
	t.Cleanup(func() { afterCardRead = func() {} })
	afterCardRead = func() {
		afterCardRead = func() {}
		go func() { done <- setMode("t", "parked") }()
		time.Sleep(300 * time.Millisecond) // the pane's write is waiting by now
	}
	if out, code := cardIn("where: New state.\n", "card", "t"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ranOnTheStore(t)
	c := loadCardMust(t, "t")
	if c.mode() != "parked" || c.firstLine("Where it is") != "New state." {
		t.Errorf("lost a write: mode %q, where %q", c.mode(), c.firstLine("Where it is"))
	}
	var logged bool
	for _, e := range readEvents() {
		logged = logged || e.Event == "mode" && e.Track == "t" && e.Kind == "parked"
	}
	if !logged {
		t.Error("the mode change was not logged")
	}
}

// Many writers at once: every frontmatter key a writer adds survives every tracks card write.
func TestCardWritersInterleave(t *testing.T) {
	setup(t)
	writeFile(t, cardPath("t"), busyCard)
	const n = 20
	errs := make(chan error, 2*n)
	for i := range n {
		go func() {
			if out, code := cardIn(fmt.Sprintf("where: round %d\n", i), "card", "t"); code != 0 {
				errs <- errors.New(out)
				return
			}
			errs <- nil
		}()
		go func() {
			errs <- setMetas("t", [][2]string{{fmt.Sprintf("k%d", i), "v"}})
		}()
	}
	for range 2 * n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	ranOnTheStore(t)
	c := loadCardMust(t, "t")
	for i := range n {
		if c.Meta[fmt.Sprintf("k%d", i)] != "v" {
			t.Errorf("k%d lost:\n%s", i, readCard("t"))
		}
	}
	if !strings.HasPrefix(c.firstLine("Where it is"), "round ") || len(c.askList()) != 2 {
		t.Errorf("card after the writers:\n%s", readCard("t"))
	}
}

func TestBindMakesTheCardAndRecordsTheSession(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("REX_SESSION", "session:new-1")
	out, code := cardIn("", "bind", "b")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	c := loadCardMust(t, "b")
	if c.Meta["rex_session"] != "session:new-1" || c.Meta["dir"] != dir || c.mode() != "background" || c.firstLine("Where it is") != "Not started." {
		t.Errorf("card:\n%s", readCard("b"))
	}
	if out != readCard("b") {
		t.Errorf("printed %q, want the card", out)
	}

	writeFile(t, cardPath("x"), busyCard)
	t.Setenv("REX_SESSION", "session:later")
	if out, code := cardIn("", "bind", "x"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if want := strings.Replace(busyCard, "rex_session: session:abc", "rex_session: session:later", 1); readCard("x") != want && !(dbExists() && sameCard(readCard("x"), strings.Replace(want, "name: t", "name: x", 1))) {
		t.Errorf("bind changed more than rex_session:\n%s", readCard("x"))
	}
}
