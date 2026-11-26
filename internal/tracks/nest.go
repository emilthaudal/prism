package tracks

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	maxGlyphs    = 6 // thread glyphs on a lead's row; more show as a "+"
	threadIndent = 3 // " └ " before a thread's name
)

// threadParents maps the name of each thread to the name of its lead. A card is a thread here
// only when its parent is another card in the list that carries role: lead and is no thread
// itself; a thread whose lead is closed or gone stays an ordinary track, so it is never hidden.
func threadParents(cards []*Card) map[string]string {
	leads := map[string]string{}
	for _, c := range cards {
		if c.Meta["role"] == "lead" && c.Meta["parent"] == "" {
			leads[strings.ToLower(c.Name)] = c.Name
		}
	}
	out := map[string]string{}
	for _, c := range cards {
		p := c.Meta["parent"]
		if lead, ok := leads[strings.ToLower(p)]; ok && lead != c.Name {
			out[c.Name] = lead
		}
	}
	return out
}

// threadsOfLead are the lead's threads among cards, by name.
func threadsOfLead(cards []*Card, parents map[string]string, lead string) []*Card {
	var out []*Card
	for _, c := range cards {
		if parents[c.Name] == lead {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b *Card) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// rows is the list as drawn: every track that is not a thread, with the threads of an unfolded
// lead right after it.
func (m *paneModel) rows() []*Card {
	var out []*Card
	for _, c := range m.all {
		if _, thread := m.parents[c.Name]; thread {
			continue
		}
		out = append(out, c)
		if m.unfolded[c.Name] {
			out = append(out, threadsOfLead(m.all, m.parents, c.Name)...)
		}
	}
	return out
}

// focusCount is the tracks in FOCUS: threads sit under their lead and do not count.
func (m *paneModel) focusCount() int {
	n := 0
	for _, c := range m.all {
		if groupOf(c) == "focus" && !m.isThread(c) {
			n++
		}
	}
	return n
}

func (m *paneModel) isThread(c *Card) bool {
	_, ok := m.parents[c.Name]
	return ok
}

// rowGroup is the group a row sits in: a thread sits in its lead's.
func (m *paneModel) rowGroup(c *Card) string {
	if lead, ok := m.parents[c.Name]; ok {
		if l := m.cardNamed(lead); l != nil {
			return groupOf(l)
		}
	}
	return groupOf(c)
}

// glyphStrip is one state glyph per thread of the lead, and how wide it is. It is empty for a
// track with no threads.
func (m *paneModel) glyphStrip(c *Card) (string, int) {
	threads := threadsOfLead(m.all, m.parents, c.Name)
	var b strings.Builder
	w := 0
	for i, t := range threads {
		if i == maxGlyphs {
			b.WriteString(dimSt.Render("+"))
			w++
			break
		}
		v := m.stateOf(t)
		g := "·"
		if v.kind != "none" {
			g = v.glyph
		}
		b.WriteString(m.styleState(v, g))
		w += lipgloss.Width(g)
	}
	return b.String(), w
}

// toggleFold unfolds or folds the selected lead's threads. On a thread it folds its lead and
// puts the cursor on the lead, since the thread is about to leave the list.
func (m *paneModel) toggleFold() {
	c := m.selected()
	if c == nil {
		return
	}
	lead := c.Name
	if l, ok := m.parents[c.Name]; ok {
		lead = l
	}
	n := len(threadsOfLead(m.all, m.parents, lead))
	if n == 0 {
		m.flashMsg(c.Name + " has no threads")
		return
	}
	if m.unfolded == nil {
		m.unfolded = map[string]bool{}
	}
	m.cur = lead
	m.unfolded[lead] = !m.unfolded[lead]
	m.cards = m.rows()
	if i := slices.IndexFunc(m.cards, func(c *Card) bool { return c.Name == lead }); i >= 0 {
		m.idx = i
	}
	if m.unfolded[lead] {
		m.flashMsg(fmt.Sprintf("%s: %s shown", lead, plural(n, "thread")))
	}
}

// closeLead closes a track and, first, each of its threads, every one through closeTrack, so a
// thread's session ends only when `tracks close THREAD` would end it. A thread that cannot be
// closed keeps the lead open: the lead's card is what names the rest.
func closeLead(name string, threads []string, now time.Time, self string) (string, error) {
	if len(threads) == 0 {
		_, note, err := closeTrack(name, now, self)
		return note, err
	}
	var odd []string // what closing did that is not the plain end of a session
	var failed []string
	note := func(who, n string) {
		if n != "Closed "+who && !strings.HasSuffix(n, " and ended its Rex session") {
			odd = append(odd, strings.TrimPrefix(n, "Closed "))
		}
	}
	for _, t := range threads {
		who, n, err := closeThread(t, now, self)
		if err != nil {
			failed = append(failed, t+": "+err.Error())
			continue
		}
		if who != "" {
			note(who, n)
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("%s; %s stays open", strings.Join(failed, "; "), name)
	}
	_, n, err := closeTrack(name, now, self)
	if err != nil {
		return "", fmt.Errorf("%w (its %s are closed)", err, plural(len(threads), "thread"))
	}
	note(name, n)
	out := fmt.Sprintf("Closed %s and its %s", name, plural(len(threads), "thread"))
	if len(odd) > 0 {
		out += " · " + strings.Join(odd, " · ")
	}
	return out, nil
}

// closeThread closes one thread of a lead. A thread that was closed since the pane listed it
// counts as closed (who is ""), and one that was renamed is closed under its new name.
func closeThread(name string, now time.Time, self string) (who, note string, err error) {
	_, note, err = closeTrack(name, now, self)
	if err == nil {
		return name, note, nil
	}
	var v store.View
	gerr := withStore(func(st *store.Store) (err error) {
		v, err = st.Get(name)
		return err
	})
	switch {
	case gerr == nil && v.RenamedTo != "":
		_, note, err = closeTrack(v.RenamedTo, now, self)
		return v.RenamedTo, note, err
	case gerr == nil && v.Closed:
		return "", "", nil
	}
	return "", "", err
}

// openThreads are the lead's threads that close with it: every card that names it as parent.
func (m *paneModel) openThreads(lead string) []*Card {
	return threadsOfLead(m.all, m.parents, lead)
}
