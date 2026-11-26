package tracks

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	// answerDelay is how long an answer waits before it is sent, so z can take it back.
	answerDelay    = 5 * time.Second
	countdownCells = 5
	expandMaxLines = 10
)

// pendingAnswer is an answer chosen in the band and not sent yet.
type pendingAnswer struct {
	id     int64
	track  string
	option int
	words  string
	label  string
	due    time.Time
}

// decidedMsg is the outcome of sending a pending answer.
type decidedMsg struct {
	id     int64
	status string
	sent   bool
}

// timeNow is the pane's clock; a test replaces clockFn.
func (m *paneModel) timeNow() time.Time {
	if m.clockFn != nil {
		return m.clockFn()
	}
	return time.Now()
}

// loadQuestions reads the open questions, nil without tracks.db or when they cannot be read.
func loadQuestions() []store.Question {
	if !storeOn() {
		return nil
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) {
		qs, err = st.OpenQuestions()
		return err
	}); err != nil {
		return nil
	}
	return qs
}

// questionsOf are the open questions of a track, oldest first.
func (m *paneModel) questionsOf(track string) []store.Question {
	var out []store.Question
	for _, q := range m.questions {
		if q.Track == track {
			out = append(out, q)
		}
	}
	return out
}

// selectedQuestion is the question the band keys act on: the one chosen with [ and ] on the
// selected track, else its oldest. nil when the track has none or the band is not in use.
func (m *paneModel) selectedQuestion() *store.Question {
	c := m.selected()
	if c == nil || !m.useQuestions {
		return nil
	}
	qs := m.questionsOf(c.Name)
	if len(qs) == 0 {
		return nil
	}
	if i := slices.IndexFunc(qs, func(q store.Question) bool { return q.ID == m.selQ }); i >= 0 {
		return &qs[i]
	}
	return &qs[0]
}

func (m *paneModel) pendingFor(id int64) *pendingAnswer {
	for _, p := range m.pending {
		if p.id == id {
			return p
		}
	}
	return nil
}

// stepQuestion moves the selection d questions along the selected track.
func (m *paneModel) stepQuestion(d int) {
	q := m.selectedQuestion()
	if q == nil {
		return
	}
	qs := m.questionsOf(q.Track)
	i := slices.IndexFunc(qs, func(x store.Question) bool { return x.ID == q.ID })
	m.selQ = qs[((i+d)%len(qs)+len(qs))%len(qs)].ID
}

// startPending queues an answer to send after answerDelay and shows its countdown on the row.
func (m *paneModel) startPending(q store.Question, option int, words string) {
	if m.pendingFor(q.ID) != nil {
		m.status = decisionLabel(q.ID) + " is waiting to be sent: z takes it back"
		return
	}
	label := words
	if option > 0 {
		label = q.Options[option-1].Label
	}
	label = strings.Join(strings.Fields(cleanText(label)), " ")
	m.pending = append(m.pending, &pendingAnswer{id: q.ID, track: q.Track, option: option, words: words, label: label, due: m.clock().Add(answerDelay)})
}

// answerRecommended is enter on a question: it takes the recommendation.
func (m *paneModel) answerRecommended(q store.Question) {
	switch {
	case q.Recommended >= 1 && q.Recommended <= len(q.Options):
		m.startPending(q, q.Recommended, "")
	default:
		m.status = decisionLabel(q.ID) + " has no recommended option: pick 1-9 or r answers in words"
	}
}

// answerInWords queues the words typed with r as the answer to question id.
func (m *paneModel) answerInWords(id int64, words string) {
	if i := slices.IndexFunc(m.questions, func(q store.Question) bool { return q.ID == id }); i >= 0 {
		m.startPending(m.questions[i], 0, words)
	}
}

// pickOption is 1-9 on a question.
func (m *paneModel) pickOption(q store.Question, n int) {
	switch {
	case len(q.Options) == 0:
		m.status = decisionLabel(q.ID) + " has no options: r answers in words"
	case n > len(q.Options):
		m.status = fmt.Sprintf("%s has %d options", decisionLabel(q.ID), len(q.Options))
	default:
		m.startPending(q, n, "")
	}
}

// undoPending is z: it takes back the selected question's pending answer, else the newest.
func (m *paneModel) undoPending() {
	if len(m.pending) == 0 {
		m.status = "Nothing waiting to be sent"
		return
	}
	i := len(m.pending) - 1
	if q := m.selectedQuestion(); q != nil {
		if j := slices.IndexFunc(m.pending, func(p *pendingAnswer) bool { return p.id == q.ID }); j >= 0 {
			i = j
		}
	}
	p := m.pending[i]
	m.pending = slices.Delete(m.pending, i, i+1)
	m.status = "Took back " + decisionLabel(p.id) + ": nothing was sent"
}

// firePending sends the answers whose countdown has run out, and returns the commands that do.
func (m *paneModel) firePending() tea.Cmd {
	var cmds []tea.Cmd
	now := m.clock()
	m.pending = slices.DeleteFunc(m.pending, func(p *pendingAnswer) bool {
		if now.Before(p.due) {
			return false
		}
		hint := m.agentHint(p.track)
		cmds = append(cmds, func() tea.Msg {
			status, err := answerQuestion(p.id, p.option, p.words, "emil", hint)
			if err != nil {
				return decidedMsg{id: p.id, status: "Not sent: " + decisionLabel(p.id) + ": " + err.Error()}
			}
			return decidedMsg{id: p.id, status: status, sent: true}
		})
		return true
	})
	return tea.Batch(cmds...)
}

// countdown is the pending answer's row text: "Merge now · 4 s ████░  z undo".
func (m *paneModel) countdown(p *pendingAnswer) string {
	left := max(0, p.due.Sub(m.clock()))
	secs := int((left + time.Second - 1) / time.Second)
	full := min(countdownCells, int((left*countdownCells+answerDelay-1)/answerDelay))
	return fmt.Sprintf("%s · %d s %s%s  z undo", p.label, secs, strings.Repeat("█", full), strings.Repeat("░", countdownCells-full))
}

// expansion is the lines space opens under a question: its options with the recommended one
// marked, its evidence and its links.
func expansion(q store.Question) []string {
	var out []string
	for i, o := range q.Options {
		mark := " "
		if i+1 == q.Recommended {
			mark = "★"
		}
		line := fmt.Sprintf("%s %d %s", mark, i+1, o.Label)
		if o.Description != "" {
			line += " – " + o.Description
		}
		out = append(out, line)
	}
	for _, e := range q.Evidence {
		line := "· " + e.Text
		if e.Source != "" {
			line += " (" + e.Source + ")"
		}
		out = append(out, line)
	}
	for _, l := range q.Links {
		out = append(out, "↗ "+l)
	}
	if len(out) > expandMaxLines {
		out = append(out[:expandMaxLines-1], fmt.Sprintf("… %d more", len(out)-expandMaxLines+1))
	}
	for i := range out {
		out[i] = strings.Join(strings.Fields(cleanText(out[i])), " ")
	}
	return out
}

// questionKey handles a key on the selected question while the band has the focus. handled is
// false for keys the pane treats as it always did.
func (m *paneModel) questionKey(k string, q store.Question) (cmd tea.Cmd, handled bool) {
	switch k {
	case "enter":
		m.answerRecommended(q)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.pickOption(q, int(k[0]-'0'))
	case "r":
		m.reply = &replyState{track: q.Track, question: q.ID, ed: lineEditor{limit: maxAnswer}}
	case "z":
		m.undoPending()
	case "space":
		if m.expanded == nil {
			m.expanded = map[int64]bool{}
		}
		m.expanded[q.ID] = !m.expanded[q.ID]
	case "[":
		m.stepQuestion(-1)
	case "]":
		m.stepQuestion(1)
	case "o":
		if len(q.Links) == 0 {
			return nil, false
		}
		return openLinkCmd(link{label: q.Links[0], url: q.Links[0]}), true
	case "g":
		m.hold = q.Track
		return openOrOffer(m.selected(), m.evs), true
	case "esc":
		m.bandFocus = false
	case "a":
		m.bandFocus = false
	default:
		return nil, false
	}
	return nil, true
}
