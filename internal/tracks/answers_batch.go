package tracks

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	batchHeading = "Emil's answers:"
	// showOptionsText asks an agent for a picture per option; %s is its quoted ask.
	showOptionsText = "Render each option of your ask %s to an image and ask again with (see: PATH) after each option, but only if the options are visual (icons, colours, layouts, screenshots). If they are words or plans an image can't show, say so in one line and leave your ask as it is."
)

// batchAnswer is one answer of a batch: Option is 1-based, or Text is the answer in words.
type batchAnswer struct {
	ID     int64
	Option int
	Text   string
}

// batchText is the one message that carries several answers: a heading, then one answerText line
// each. Typed into an agent the lines share one line, so sep is a space there.
func batchText(lines []string, sep string) string {
	return batchHeading + sep + strings.Join(lines, sep)
}

// askerKey names who asked a question: the session that asked, else its track and kind (an
// agent's ask and a desk or check question on one track are different askers), else its kind.
func askerKey(q store.Question) string {
	switch {
	case q.SessionID != "":
		return "session " + q.SessionID
	case q.Track != "":
		return "track " + q.Track + " from " + q.From
	}
	return "from " + q.From
}

// deliverToAsker hands text to the agent that asked q: the desk session that asked it, by its own
// id, gets it as a note (held by tracks send until the session stops) while the desk owns it;
// anything else (a track's own agent, a session Emil took over) has it typed into the track's
// agent as one line. It never picks a session by the track's name: a track can hold an old,
// stopped desk session next to the agent that is actually asking. qs are the questions the
// text answers, for the card asks they mirror.
func deliverToAsker(q store.Question, qs []store.Question, text string) error {
	if q.SessionID != "" {
		s, ok, err := deskTarget(q.SessionID)
		if err != nil {
			return err
		}
		if ok && s.ID == q.SessionID && s.Owner == "desk" {
			var out bytes.Buffer
			return deskSendCmd(s, text, false, false, &out)
		}
	}
	return typeAnswer(q.Track, qs, sanitizeAnswer(text), "")
}

// answerBatch answers the questions of one asker as one message. Nothing is recorded when any
// answer is refused or the message cannot be delivered, except that an agent which is only busy
// takes the answers later: they are recorded as queued, together, and serve types them once.
func answerBatch(items []batchAnswer, by string) (status string, queued bool, err error) {
	if len(items) == 0 {
		return "", false, store.ErrEmptyAnswer
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) error {
		for _, it := range items {
			q, err := st.GetQuestion(it.ID)
			if err != nil {
				return err
			}
			qs = append(qs, q)
		}
		return nil
	}); err != nil {
		return "", false, err
	}
	ds := make([]store.Decision, len(items))
	lines := make([]string, len(items))
	labels := make([]string, len(items))
	for i, q := range qs {
		it := items[i]
		switch {
		case slices.ContainsFunc(items[:i], func(o batchAnswer) bool { return o.ID == it.ID }):
			return "", false, fmt.Errorf("%s is in the batch twice", decisionLabel(it.ID))
		case !q.Open():
			return "", false, fmt.Errorf("%s: %w", decisionLabel(q.ID), store.ErrQuestionClosed)
		case isHookPrompt(q):
			return "", false, fmt.Errorf("%s is a session's prompt: answer it on its own", decisionLabel(q.ID))
		case askerKey(q) != askerKey(qs[0]):
			return "", false, fmt.Errorf("%s and %s have different askers: a batch is one asker's", decisionLabel(qs[0].ID), decisionLabel(q.ID))
		case it.Option > len(q.Options):
			return "", false, fmt.Errorf("%s: %d: %w", decisionLabel(q.ID), it.Option, store.ErrBadOption)
		case (it.Option <= 0) == (strings.TrimSpace(it.Text) == ""):
			return "", false, fmt.Errorf("%s: %w", decisionLabel(q.ID), store.ErrEmptyAnswer)
		}
		ds[i] = store.Decision{ID: it.ID, Option: max(it.Option, 0), Text: it.Text}
		lines[i] = answerText(q, askNumber(q), ds[i].Option, strings.TrimSpace(it.Text))
		labels[i] = decisionLabel(q.ID)
	}
	typed := qs[0].From == "agent" && qs[0].Track != ""
	if typed {
		text := batchText(lines, " ")
		if len([]rune(text)) > maxAnswer {
			return "", false, fmt.Errorf("the answers are %d characters, more than the %d one message holds: send fewer at once", len([]rune(text)), maxAnswer)
		}
		if err := deliverToAsker(qs[0], qs, batchText(lines, "\n")); err != nil {
			var wait busyError
			if !errors.As(err, &wait) || by != "emil" {
				return "", false, err
			}
			queued = true
		}
	}
	if _, err := recordBatch(ds, by, typed && !queued); err != nil {
		return "", false, fmt.Errorf("answered %s, but could not record it: %w", strings.Join(labels, ", "), err)
	}
	if queued {
		return fmt.Sprintf("queued %s: typed into %s when its agent stops", strings.Join(labels, ", "), qs[0].Track), true, nil
	}
	return "answered " + strings.Join(labels, ", "), false, nil
}

func recordBatch(ds []store.Decision, by string, delivered bool) (got []store.Question, err error) {
	err = withBareStore(func(st *store.Store) (err error) { got, err = st.DecideMany(ds, by, delivered); return err })
	return got, err
}

// askText is what the agent is told when Emil asks about an ask instead of answering it.
func askText(q store.Question, words string, showOptions bool) string {
	if showOptions {
		return fmt.Sprintf(showOptionsText, quoteAsk(q.Question))
	}
	return fmt.Sprintf("Emil asks about your ask %s: %s", quoteAsk(q.Question), words)
}

// askQuestion tells whoever asked question id what Emil wants to know, without answering it: a
// track's agent, or for the desk's and a check's questions the desk, whose queue holds it while
// it works. An agent that is only busy gets it later: serve holds it in memory.
func askQuestion(id int64, words string, showOptions bool, toDesk func(string) error) (status string, queued bool, err error) {
	var q store.Question
	if err := withBareStore(func(st *store.Store) (err error) { q, err = st.GetQuestion(id); return err }); err != nil {
		return "", false, err
	}
	words = strings.TrimSpace(words)
	switch {
	case !q.Open():
		return "", false, store.ErrQuestionClosed
	case isHookPrompt(q):
		return "", false, fmt.Errorf("%s is a session's prompt: there is no one to ask", decisionLabel(id))
	case showOptions && len(q.Options) == 0:
		return "", false, fmt.Errorf("%s has no options to show", decisionLabel(id))
	case !showOptions && words == "":
		return "", false, store.ErrEmptyAnswer
	}
	text := askText(q, words, showOptions)
	if q.From != "agent" || q.Track == "" {
		if toDesk == nil {
			return "", false, fmt.Errorf("%s is not a track agent's question and the desk is not running: there is no one to ask", decisionLabel(id))
		}
		if err := toDesk(text); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("asked the desk about %s", decisionLabel(id)), false, nil
	}
	if err := deliverToAsker(q, nil, text); err != nil {
		var wait busyError
		if !errors.As(err, &wait) {
			return "", false, err
		}
		pendingAsks.add(heldAsk{q: q, text: text, at: time.Now()})
		return fmt.Sprintf("queued ask on %s: typed into %s when its agent stops", decisionLabel(id), q.Track), true, nil
	}
	return fmt.Sprintf("asked about %s", decisionLabel(id)), false, nil
}

// heldAsk is a question Emil asked while the agent was busy.
type heldAsk struct {
	q    store.Question
	text string
	at   time.Time
}

// askQueue holds those in serve's memory: a restart drops them, and Emil asks again.
type askQueue struct {
	mu   sync.Mutex
	list []heldAsk
}

var pendingAsks askQueue

func (a *askQueue) add(h heldAsk) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, h)
}

func (a *askQueue) all() []heldAsk {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.list)
}

func (a *askQueue) drop(h heldAsk) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = slices.DeleteFunc(a.list, func(x heldAsk) bool { return x.q.ID == h.q.ID && x.at.Equal(h.at) })
}
