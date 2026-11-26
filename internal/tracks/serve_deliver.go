package tracks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	// deliverEvery is how often serve looks for answers waiting for their agent to stop.
	deliverEvery = 5 * time.Second
	// deliverRetry is the least time between two tries at an answer that failed for a reason other
	// than the agent being busy.
	deliverRetry = time.Minute
	// deliverGiveUp is how long after the answer a failing delivery is kept before the question
	// goes back to Emil.
	deliverGiveUp = 30 * time.Minute
)

// deliverer types the answers Emil gave while a track's agent was busy, one at a time per track,
// once Rex says the agent has stopped.
type deliverer struct {
	now  func() time.Time
	logf func(format string, a ...any)
	// sentAt is when an answer was last typed into a track's agent; the next waits out answeredGrace
	// so it never lands in the turn the first one started.
	sentAt   map[string]time.Time
	failedAt map[int64]time.Time
}

func newDeliverer(errw io.Writer) *deliverer {
	return &deliverer{
		now:      time.Now,
		logf:     func(format string, a ...any) { fmt.Fprintf(errw, "prism server: "+format+"\n", a...) },
		sentAt:   map[string]time.Time{},
		failedAt: map[int64]time.Time{},
	}
}

func (d *deliverer) run(ctx context.Context) {
	t := time.NewTicker(deliverEvery)
	defer t.Stop()
	for {
		d.once()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// typedAnswer is what is typed into the agent for a recorded answer, as answering it with the
// agent idle types it.
func typedAnswer(q store.Question) string {
	if q.AnswerOption > 0 {
		return answerText(q, askNumber(q), q.AnswerOption, "")
	}
	return q.AnswerText
}

// once makes one pass over the queued answers: the oldest of each track gets a try.
func (d *deliverer) once() {
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) { qs, err = st.QueuedAnswers(); return err }); err != nil {
		d.logf("reading queued answers: %v", err)
		return
	}
	seen := map[string]bool{}
	for _, q := range qs {
		if isHookPrompt(q) || seen[q.Track] {
			continue
		}
		seen[q.Track] = true
		group := batchOf(qs, q)
		now := d.now()
		if at, ok := d.sentAt[q.Track]; ok && now.Sub(at) < answeredGrace {
			continue
		}
		if at, ok := d.failedAt[q.ID]; ok && now.Sub(at) < deliverRetry {
			continue
		}
		var err error
		if len(group) == 1 {
			err = deliverAnswer(q, typedAnswer(q), "")
		} else {
			err = deliverToAsker(q, group, batchText(typedLines(group), " "))
		}
		var wait busyError
		switch {
		case err == nil:
			d.sentAt[q.Track] = d.now()
			delete(d.failedAt, q.ID)
			ids := make([]int64, len(group))
			for i, g := range group {
				ids[i] = g.ID
			}
			if err := withBareStore(func(st *store.Store) error { return st.MarkDelivered(ids) }); err != nil {
				d.logf("marking %s delivered: %v", decisionLabel(q.ID), err)
			}
		case errors.As(err, &wait):
			delete(d.failedAt, q.ID)
		default:
			d.failedAt[q.ID] = now
			if now.Sub(q.AnsweredAt) >= deliverGiveUp {
				d.giveUp(group, err)
			}
		}
	}
	d.heldAsks(seen)
}

// batchOf is the queued answers recorded in the same write as q: one batch is typed as one message.
func batchOf(qs []store.Question, q store.Question) []store.Question {
	return slices.DeleteFunc(slices.Clone(qs), func(o store.Question) bool {
		return o.Track != q.Track || !o.AnsweredAt.Equal(q.AnsweredAt) || isHookPrompt(o)
	})
}

// typedLines is one answerText line for each of qs.
func typedLines(qs []store.Question) []string {
	lines := make([]string, len(qs))
	for i, q := range qs {
		lines[i] = answerText(q, askNumber(q), q.AnswerOption, q.AnswerText)
	}
	return lines
}

// heldAsks types the questions Emil asked while an agent was busy, once its answers had their
// turn; an ask for a question that has since closed is dropped.
func (d *deliverer) heldAsks(seen map[string]bool) {
	for _, h := range pendingAsks.all() {
		var q store.Question
		if err := withBareStore(func(st *store.Store) (err error) { q, err = st.GetQuestion(h.q.ID); return err }); err != nil || !q.Open() {
			pendingAsks.drop(h)
			continue
		}
		now := d.now()
		if seen[h.q.Track] {
			continue
		}
		if at, ok := d.sentAt[h.q.Track]; ok && now.Sub(at) < answeredGrace {
			continue
		}
		seen[h.q.Track] = true
		err := deliverToAsker(h.q, nil, h.text)
		var wait busyError
		switch {
		case err == nil:
			d.sentAt[h.q.Track] = d.now()
			pendingAsks.drop(h)
		case errors.As(err, &wait):
		case now.Sub(h.at) >= deliverGiveUp:
			d.logf("giving up the ask about %s: %v", decisionLabel(h.q.ID), err)
			pendingAsks.drop(h)
		}
	}
}

// giveUp puts the questions back in Needs you, with why their answer never arrived.
func (d *deliverer) giveUp(qs []store.Question, err error) {
	for _, q := range qs {
		note := fmt.Sprintf("Your answer %q was not delivered to %s in %d minutes: %s", clipRunes(oneLine(q.Answer()), 80), q.Track,
			int(deliverGiveUp/time.Minute), strings.TrimPrefix(err.Error(), "Not sent: "))
		if e := withBareStore(func(st *store.Store) error { return st.ReopenQuestion(q.ID, note) }); e != nil {
			d.logf("putting %s back: %v", decisionLabel(q.ID), e)
			continue
		}
		delete(d.failedAt, q.ID)
	}
}
