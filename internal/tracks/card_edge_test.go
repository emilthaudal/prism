package tracks

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A tracks card write that lands while a rename is moving the card follows the rename: no
// writer is told there is no card.
func TestCardWritersFollowARenameInProgress(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "x")
	const rounds, writers = 40, 8
	for r := range rounds {
		old, name := fmt.Sprintf("old%d", r), fmt.Sprintf("new%d", r)
		card(t, th, old, "focus", renSess, "w", "")
		errs := make(chan string, writers+1)
		start := make(chan struct{})
		for i := range writers {
			go func() {
				<-start
				time.Sleep(time.Duration(i) * 200 * time.Microsecond)
				if out, code := cardIn("where: writing\n", "card", old); code != 0 {
					errs <- out
					return
				}
				errs <- ""
			}()
		}
		go func() {
			<-start
			if _, err := renameTrack(old, name, time.Now()); err != nil {
				errs <- err.Error()
				return
			}
			errs <- ""
		}()
		close(start)
		for range writers + 1 {
			if msg := <-errs; msg != "" {
				t.Errorf("round %d: %s", r, msg)
			}
		}
		gone(t, old)
		if w := loadCardMust(t, name).firstLine("Where it is"); w != "writing" {
			t.Errorf("round %d: new card's where: %q", r, w)
		}
	}
}

// tracks bind that lands while a rename moves the card never brings the old name back as a
// second card: it prints the card or says the track was renamed.
func TestBindDuringARenameMakesNoSecondCard(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "x")
	const rounds, binders = 60, 8
	for r := range rounds {
		old, name := fmt.Sprintf("old%d", r), fmt.Sprintf("new%d", r)
		card(t, th, old, "focus", renSess, "w", "")
		outs := make(chan string, binders+1)
		start := make(chan struct{})
		for i := range binders {
			go func() {
				<-start
				time.Sleep(time.Duration(i) * 200 * time.Microsecond)
				out, code := cardIn("", "bind", old)
				if code != 0 {
					outs <- "exit: " + out
					return
				}
				outs <- out
			}()
		}
		go func() {
			<-start
			if _, err := renameTrack(old, name, time.Now()); err != nil {
				outs <- "rename: " + err.Error()
				return
			}
			outs <- ""
		}()
		close(start)
		for range binders + 1 {
			out := <-outs
			if out != "" && out != "renamed to "+name+"\n" && !strings.HasPrefix(out, "---\nname: ") {
				t.Errorf("round %d: bind printed %q", r, out)
			}
		}
		ranOnTheStore(t)
		gone(t, old)
		loadCardMust(t, name)
	}
}

// The agent changes check: without writing asks:. The old check's ask stays while Emil has not
// seen it and goes with the next asks: write once he has; the new check's unseen ask stays.
func TestChangedCheckKeepsUnseenAskAndDropsSeenOne(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 merged")
	eval := func(string, check) (string, error) { return yes, nil }
	const oldAsk, newAsk = `Check "pr 5 merged" is now yes.`, `Check "pr 6 merged" is now yes.`
	checks(t, false, eval)

	if out, code := cardIn("check: pr 6 merged\n", "card", "w"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	checks(t, false, eval)
	if cardIn("asks:\n", "card", "w"); !strings.Contains(readCard("w"), oldAsk) || !strings.Contains(readCard("w"), newAsk) {
		t.Fatalf("an ask Emil has not seen vanished:\n%s", readCard("w"))
	}

	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "answers"})
	if cardIn("asks:\n", "card", "w"); strings.Contains(readCard("w"), oldAsk) || strings.Contains(readCard("w"), newAsk) {
		t.Errorf("after Emil saw both, an asks: write should clear them as answered:\n%s", readCard("w"))
	}
}

// The old check's ask Emil has seen is dropped by an asks: write; the new check's ask he has not
// seen stays.
func TestSeenStaleCheckAskDropsButUnseenNewOneStays(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w", "pr 5 merged")
	eval := func(string, check) (string, error) { return yes, nil }
	const oldAsk, newAsk = `Check "pr 5 merged" is now yes.`, `Check "pr 6 merged" is now yes.`
	checks(t, false, eval)
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", Track: "w", Kind: "answers"})
	time.Sleep(2 * time.Millisecond)

	if out, code := cardIn("check: pr 6 merged\n", "card", "w"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	checks(t, false, eval)
	if out, code := cardIn("asks:\n", "card", "w"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	c := readCard("w")
	if strings.Contains(c, oldAsk) || !strings.Contains(c, newAsk) {
		t.Errorf("want the seen old ask dropped and the unseen new one kept:\n%s", c)
	}
}
