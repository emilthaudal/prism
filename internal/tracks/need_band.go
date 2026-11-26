package tracks

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	bandMaxRows   = 6   // the most rows the band shows, and a third of the body at most
	bandFullBody  = 14  // from here the band also shows the "not yours yet" line
	bandMinBody   = 10  // below this the band would leave the list too little room
	bandMeasure   = 150 // rows stop here, so the age stays near the text on a wide pane
	bandQuestMax  = 96  // the question column is never wider
	bandNameMax   = 22
	bandRecMax    = 32 // the widest the recommendation column gets
	bandRecMin    = 8
	bandAgeW      = 4
	bandGap       = 2
	bandMinQuest  = 12
	bandQuestKeep = 28 // the question gives way to this before the recommendation is cut
)

// bandRow is one open decision for Emil: an ask, or the prompt or error that holds a track.
type bandRow struct {
	card  *Card
	name  string // the track's name, on its first row only
	view  stateView
	ask   bool
	n     int    // the ask's number on its card
	num   string // the question's D id
	q     *store.Question
	pend  *pendingAnswer // the answer waiting out its countdown
	quest string
	rec   string
	age   string
}

// bandRows are the rows of the Needs you band, in needsOrder, from the tracks needsYou counts.
// The header counts the same rows, so the two agree.
func (m *paneModel) bandRows() (rows []bandRow) {
	shown := map[int64]bool{}
	for _, it := range needsOrder(m.all, m.rex.states, m.evs, m.now) {
		c := it.card
		if !m.needsYou(c) {
			continue
		}
		v := m.stateOf(c)
		name := c.Name
		add := func(r bandRow) {
			r.card, r.name = c, name
			name = ""
			rows = append(rows, r)
		}
		if v.kind == "blocked" || v.kind == "error" {
			quest := v.label
			if msg := strings.Join(strings.Fields(v.msg), " "); msg != "" {
				quest += " · " + msg // what the prompt or the error says, not only its kind
			}
			add(bandRow{view: v, quest: quest, age: shortDur(max(0, m.now.Sub(it.at)))})
		}
		// An ask is as old as the card that holds it, also on a blocked track.
		askAge := shortDur(max(0, m.now.Sub(cardChanged(c))))
		if m.isThread(c) {
			continue
		}
		if m.useQuestions {
			for _, q := range m.questionsOf(c.Name) {
				shown[q.ID] = true
				r := m.questionRow(q)
				if q.AskID != 0 {
					r.age = askAge // as old as the card that holds the ask
				}
				add(r)
			}
			continue
		}
		for _, a := range c.askList() {
			q, rec := splitRecommend(strings.Join(strings.Fields(a.text), " "))
			add(bandRow{ask: true, n: a.n, quest: q, rec: shortRec(rec, bandRecMax), age: askAge})
		}
	}
	// A question the desk or a check raised is Emil's whatever its track is doing.
	for _, q := range m.questions {
		if c := m.cardNamed(q.Track); m.useQuestions && !shown[q.ID] && q.From != "agent" && c != nil {
			rows = append(rows, func() bandRow { r := m.questionRow(q); r.card, r.name = c, c.Name; return r }())
		}
	}
	return rows
}

// questionRow is the band row of one open question, or its countdown while its answer waits.
func (m *paneModel) questionRow(q store.Question) bandRow {
	q.Question, q.Recommendation = cleanText(q.Question), cleanText(q.Recommendation)
	r := bandRow{ask: true, num: decisionLabel(q.ID), q: &q, quest: strings.Join(strings.Fields(q.Question), " "),
		age: shortDur(max(0, m.clock().Sub(q.CreatedAt)))}
	r.rec = shortRec(q.Recommendation, bandRecMax)
	if q.Recommended >= 1 && q.Recommended <= len(q.Options) {
		r.rec = cleanText(fmt.Sprintf("%d %s", q.Recommended, q.Options[q.Recommended-1].Label))
	}
	if p := m.pendingFor(q.ID); p != nil {
		r.pend, r.quest, r.rec = p, m.countdown(p), ""
	}
	return r
}

// wordCut fits s into w columns, cutting at a word so a pick never reads as half a word.
func wordCut(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	t := ansi.Truncate(s, max(0, w-1), "")
	if i := strings.LastIndex(t, " "); i > 0 {
		t = strings.TrimRight(t[:i], " ,;")
	}
	return t + "…"
}

// recCuts end a recommendation where its pick ends and its reasons begin.
var recCuts = []string{"; ", ", since", " since ", " because ", ". "}

// shortRec trims a recommendation to the pick: the trailing period goes, and a recommendation
// wider than max is cut at the first place its reasons start, then at max with an ellipsis.
func shortRec(rec string, max int) string {
	rec = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rec), "."))
	if ansi.StringWidth(rec) <= max {
		return rec
	}
	cut := len(rec)
	for _, s := range recCuts {
		if i := strings.Index(rec, s); i > 0 {
			cut = min(cut, i)
		}
	}
	return wordCut(strings.TrimSpace(rec[:cut]), max)
}

// notYetLine is the dim line of asks whose agent still works on them: they are not Emil's yet.
func (m *paneModel) notYetLine(w int) string {
	var parts []string
	for _, c := range m.cards {
		if c.mode() == "parked" || m.isThread(c) || c.asks() == 0 || m.stateOf(c).kind != "working" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s ◆%d", c.Name, c.asks()))
	}
	if len(parts) == 0 {
		return ""
	}
	return fit(dimSt.Render(" not yours yet  "+strings.Join(parts, " · ")), w)
}

// needBand is the full-width Needs you band above the list: a heading with its count, one row
// per open decision, what is not his yet, and a rule. With nothing it is the line
// "Nothing needs you" and the rule, so the list below does not jump. It is nil where there is no room.
// The count is of rows, as the header's is: one per decision, not per track.
func (m *paneModel) needBand(w, bodyH int) []string {
	if bodyH < bandMinBody {
		return nil
	}
	rows := m.bandRows()
	notYet := ""
	if bodyH >= bandFullBody { // a short pane keeps its lines for the list
		notYet = m.notYetLine(w)
	}
	if len(rows) == 0 {
		out := []string{fit(dimSt.Render(" "+nothingNeedsYou), w)}
		if notYet != "" {
			out = append(out, notYet)
		}
		return append(out, dimSt.Render(strings.Repeat("─", w)))
	}
	out := []string{fit(" "+warnSt.Bold(true).Render("NEEDS YOU")+" "+dimSt.Render(strconv.Itoa(len(rows))), w)}
	shown, hidden := rows, 0
	// The whole band, heading, rule and "not yours yet" included, keeps to a third of the body.
	chrome := 2
	if notYet != "" {
		chrome++
	}
	if limit := max(2, min(bandMaxRows, bodyH/3-chrome)); len(rows) > limit {
		start := 0
		if i := slices.IndexFunc(rows, func(r bandRow) bool {
			return r.q != nil && m.selectedQuestion() != nil && r.q.ID == m.selectedQuestion().ID
		}); i >= limit-1 {
			start = i - (limit - 2)
		}
		shown, hidden = rows[start:start+limit-1], len(rows)-(limit-1)
	}
	out = append(out, m.bandLines(shown, w)...)
	if hidden > 0 {
		out = append(out, fit(dimSt.Render(fmt.Sprintf(" +%d more · tab walks them", hidden)), w))
	}
	if notYet != "" {
		out = append(out, notYet)
	}
	return append(out, dimSt.Render(strings.Repeat("─", w)))
}

// bandLines lays the rows out in columns: marker, glyph, track, ask number, question,
// recommendation, age, the age column ending where the widest row does. When the width falls short the age goes first, then the
// recommendation shrinks, then the question is cut.
func (m *paneModel) bandLines(rows []bandRow, w int) []string {
	cw := min(w, bandMeasure)
	nameW, numW, qNat, recNat := 4, 1, 0, 0
	for _, r := range rows {
		nameW = max(nameW, min(bandNameMax, ansi.StringWidth(r.name)))
		if r.ask && r.num == "" {
			numW = max(numW, len(strconv.Itoa(r.n)))
		}
		numW = max(numW, len(r.num))
		qNat = max(qNat, ansi.StringWidth(r.quest))
		if r.rec != "" {
			recNat = max(recNat, ansi.StringWidth(r.rec)+2)
		}
	}
	nameW = min(nameW, max(4, cw/4))
	qNat = min(qNat, bandQuestMax)
	avail := cw - (nameW + numW + 5)
	ageW, recW, recGap := bandAgeW+1, recNat, 0
	if recNat > 0 {
		recGap = bandGap
	}
	if qNat+recGap+recW+ageW > avail {
		ageW = 0
	}
	// Then the question gives way, down to bandQuestKeep, and only then the recommendation.
	qW := min(qNat, avail-ageW-recGap-recW)
	if qW < min(qNat, bandQuestKeep) && recNat > 0 {
		qW = min(qNat, bandQuestKeep)
		recW = max(min(recNat, bandRecMin), avail-ageW-recGap-qW)
	}
	qW = max(1, min(qW, avail-ageW-recGap-recW))
	ageEnd := min(cw, nameW+numW+5+qW+recGap+recW+ageW)
	sel, selQ := "", int64(0)
	if m.idx >= 0 && m.idx < len(m.cards) {
		sel = m.cards[m.idx].Name
	}
	if q := m.selectedQuestion(); q != nil {
		selQ = q.ID
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		marker := " "
		if r.card.Name == sel && (r.q == nil || selQ == 0 || r.q.ID == selQ) {
			marker = selSt.Render("▌")
		}
		glyph, quest := warnSt.Render("◆"), r.quest
		num := strings.Repeat(" ", numW)
		if r.pend != nil {
			glyph, quest = goodSt.Render("✓"), goodSt.Render(r.quest)
		}
		if r.ask {
			label := r.num
			if label == "" {
				label = strconv.Itoa(r.n)
			}
			num = boldSt.Render(fmt.Sprintf("%*s", numW, label))
		} else {
			g := r.view.glyph
			if g == "◆" { // ◆ is an ask in the band; a prompt that holds the agent gets its own mark
				g = "▲"
			}
			glyph, quest = m.styleState(r.view, g), m.styleState(r.view, r.quest)
		}
		line := marker + glyph + " " + dimSt.Render(fit(r.name, nameW)) + " " + num + " " + fit(quest, qW)
		if recGap > 0 {
			rec := ""
			if r.rec != "" {
				rec = goodSt.Render("→ " + wordCut(r.rec, max(1, recW-2)))
			}
			line += strings.Repeat(" ", recGap) + fit(rec, recW)
		}
		if ageW > 0 {
			line = fit(line, ageEnd-bandAgeW) + dimSt.Render(fmt.Sprintf("%*s", bandAgeW, r.age))
		}
		out = append(out, fit(line, w))
		if r.q != nil && m.expanded[r.q.ID] {
			for _, l := range expansion(*r.q) {
				out = append(out, fit(strings.Repeat(" ", nameW+numW+4)+dimSt.Render(l), w))
			}
		}
	}
	return out
}
