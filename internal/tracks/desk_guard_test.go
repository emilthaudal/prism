package tracks

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

// The shapes of the desk's replies on 25 Nov, reworded: the "Open:" lists Emil answered by
// typing must fail, and plain reports and D-id pointers must pass.
func TestNumbersQuestionsOnTheDesksRealShapes(t *testing.T) {
	fail := map[string]string{
		"open list": "The rename is done.\n\nOpen:\n1. **Merge the rename into main?** I recommend yes. The tests pass.\n2. **Close prism-redesign?** I recommend yes.",
		"one item":  "Open:\n1. **Close emi-296-close-keeps-asks?** I recommend yes.",
		"paren":     "Open:\n6) **prism-cli-followups** is yours. I recommend giving it back.",
		"mixed":     "Open:\n1. **D31, start the next part?** I recommend yes.\n2. **EMI-404, sessions flipping:** start a track for it? I recommend yes.",
		"indented":  "Two things:\n  3. Install tracks and restart serve?",
	}
	pass := map[string]string{
		"nothing open": "emi-397 has finished.\n\nOpen: nothing for you right now.",
		"facts":        "Done today:\n1. merged EMI-400\n2. installed tracks\n3. closed emi-296",
		"d pointers":   "Open:\n1. **D25, what next for the icon?** I recommend 1.\n2. **D26, merge the polished icon?** I recommend yes.",
		"bullets":      "- **prism-redesign**: waits on your answer to D31.\n- **pantry-api-arcade**: one question open?",
		"imperative":   "Open:\n1. **Merge EMI-400 yourself.** Run the merge in the main checkout.",
		"prose":        "I recommend we wait until 3. Is that ok?",
	}
	for name, text := range fail {
		if !numbersQuestions(text) {
			t.Errorf("%s passed:\n%s", name, text)
		}
	}
	for name, text := range pass {
		if numbersQuestions(text) {
			t.Errorf("%s failed:\n%s", name, text)
		}
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func deskTurn(r *deskRunner, text string) {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
	r.handle(r.gen, b)
	r.handle(r.gen, []byte(`{"type":"result","subtype":"success"}`))
}

func TestServerHidesANumberedReplyAndReasksOnceOnly(t *testing.T) {
	decisionHome(t, "alpha")
	var stdin bytes.Buffer
	r := newDeskRunner(io.Discard)
	r.stdin, r.alive, r.busy = nopCloser{&stdin}, true, true
	lastDesk := func() store.Entry {
		var es []store.Entry
		withBareStore(func(st *store.Store) (err error) { es, err = st.Conversation(0, 1); return err })
		return es[len(es)-1]
	}

	deskTurn(r, "Open:\n1. **Merge #212?** I recommend yes.")
	if e := lastDesk(); e.Outcome != outcomeReasked {
		t.Fatalf("numbered reply not hidden: %+v", e)
	}
	if !r.busy || !strings.Contains(stdin.String(), "Prism: your last reply numbered questions") {
		t.Fatalf("no reask turn: busy %v, stdin %s", r.busy, stdin.String())
	}

	stdin.Reset()
	deskTurn(r, "Open:\n1. **Merge #212?** I recommend yes.")
	if e := lastDesk(); e.Outcome != "" {
		t.Errorf("second failing reply hidden too: %+v", e)
	}
	if r.busy || stdin.Len() != 0 {
		t.Errorf("reasked twice: %s", stdin.String())
	}

	r.busy = true
	deskTurn(r, "Raised D1 for the merge.")
	if e := lastDesk(); e.Outcome != "" {
		t.Errorf("plain reply hidden: %+v", e)
	}
	r.busy = true
	deskTurn(r, "Open:\n1. **Install main?** I recommend tonight.")
	if e := lastDesk(); e.Outcome != outcomeReasked {
		t.Errorf("guard did not rearm after a passing turn: %+v", e)
	}
}

func TestBatchPutsTheServersNoticeFirst(t *testing.T) {
	got := batch(reaskNotice, []store.Entry{{Text: "merge it"}}, []string{"solo done: ok"})
	if !strings.HasPrefix(got, reaskNotice+"\nEmil: merge it\nEvents:\n- solo done: ok") {
		t.Errorf("batch:\n%s", got)
	}
}
