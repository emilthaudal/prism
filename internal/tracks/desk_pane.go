package tracks

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	deskNotesRows = 5   // the desk's notes shown on a track
	deskNotesKept = 200 // log entries the pane keeps in memory for those notes
)

// deskState is what the pane reads of the desk: its Rex session, the entries handled today and
// the newest entries of the log, for a track's notes.
type deskState struct {
	session string
	today   []store.DeskLogEntry // oldest first
	notes   []store.DeskLogEntry // newest first
}

// deskLine is text from the desk on one line, without escape sequences.
func deskLine(s string) string { return strings.Join(strings.Fields(cleanText(s)), " ") }

func cleanDeskEntry(e store.DeskLogEntry) store.DeskLogEntry {
	e.Track, e.Asked, e.Did = deskLine(e.Track), deskLine(e.Asked), deskLine(e.Did)
	return e
}

func cleanDeskEntries(es []store.DeskLogEntry) []store.DeskLogEntry {
	for i, e := range es {
		es[i] = cleanDeskEntry(e)
	}
	return es
}

func loadDesk(now time.Time) (deskState, error) {
	var d deskState
	if !storeOn() {
		return d, nil
	}
	err := withStore(func(st *store.Store) error {
		v, err := st.Desk(startOfDay(now))
		if err != nil {
			return err
		}
		d.session, d.today = v.Session, cleanDeskEntries(v.Log)
		notes, err := st.DeskLogRecent(deskNotesKept)
		d.notes = cleanDeskEntries(notes)
		return err
	})
	return d, err
}

// handledLog is the open full handled log: every entry, newest first, and the cursor on one.
type handledLog struct {
	entries []store.DeskLogEntry
	idx     int
}

// landOn selects the track in the list, unfolding its lead when it is a thread, and reports
// whether the list has it.
func (m *paneModel) landOn(name string) (tea.Cmd, bool) {
	if lead, ok := m.parents[name]; ok && !m.unfolded[lead] {
		if m.unfolded == nil {
			m.unfolded = map[string]bool{}
		}
		m.unfolded[lead] = true
		m.cards = m.rows()
	}
	i := slices.IndexFunc(m.cards, func(c *Card) bool { return c.Name == name })
	if i < 0 {
		return nil, false
	}
	return m.moveTo(i), true
}

// openTrack selects the track by the name the desk used, and opens it in Rex as enter does.
func (m *paneModel) openTrack(name string) tea.Cmd {
	name = m.follow(name)
	i := slices.IndexFunc(m.all, func(c *Card) bool { return strings.EqualFold(c.Name, name) })
	if i < 0 {
		m.status = "No track named " + name + " on the desk"
		return nil
	}
	c := m.all[i]
	cmd, _ := m.landOn(c.Name)
	m.hold = c.Name
	return tea.Batch(cmd, openOrOffer(c, m.evs))
}

// deskDoneMsg is the outcome of d. switched means Rex now shows the desk, so a floating pane
// closes as it does after enter.
type deskDoneMsg struct {
	status   string
	switched bool
}

// startOrOpenDesk is d: it switches Rex to the desk's agent when one runs, and otherwise starts
// one, as tracks desk start does, and switches to it. It reads the desk's session from the
// store rather than what the pane last loaded, so a desk started elsewhere meanwhile is opened,
// not started twice; startDesk's lock and its own check cover a start racing this one.
func startOrOpenDesk() tea.Cmd {
	return func() tea.Msg {
		sess, err := deskSessionNow()
		if err != nil {
			return deskDoneMsg{status: "could not read the desk: " + err.Error()}
		}
		if sess != "" {
			r := fetchSession(sess, "", "")
			switch {
			case r.failed:
				return deskDoneMsg{status: "could not ask Rex whether the desk runs, so it was not started"}
			case r.found && !r.state.Exited:
				note := selectSession(r.id, r.block)
				return deskDoneMsg{status: note, switched: note == ""}
			}
		}
		var out strings.Builder
		sid, block, err := startDesk(&out)
		if err != nil {
			return deskDoneMsg{status: "could not start the desk: " + err.Error()}
		}
		status := "Started the desk"
		if strings.Contains(out.String(), "did not show it had started") {
			status += "; its agent did not show it had started, so look at its session"
		}
		if strings.Contains(out.String(), "tracks pane did not open") {
			status += "; the tracks pane did not open beside it"
		}
		if note := selectSession(sid, block); note != "" {
			return deskDoneMsg{status: status + ", but " + note}
		}
		return deskDoneMsg{status: status, switched: true}
	}
}

func (m *paneModel) openLog() {
	var entries []store.DeskLogEntry
	err := withStore(func(st *store.Store) (err error) {
		entries, err = st.DeskLogRecent(0)
		return err
	})
	if err != nil {
		m.status = "could not read the handled log: " + err.Error()
		return
	}
	m.logv = &handledLog{entries: cleanDeskEntries(entries)}
}

func (m *paneModel) logKey(k string) tea.Cmd {
	l := m.logv
	if k != "u" {
		m.status = ""
	}
	switch k {
	case "esc", "h", "q":
		m.logv = nil
	case "up", "k":
		l.idx = max(0, l.idx-1)
	case "down", "j":
		l.idx = min(len(l.entries)-1, l.idx+1)
	case "pgup":
		l.idx = max(0, l.idx-m.logPage())
	case "pgdown":
		l.idx = max(0, min(len(l.entries)-1, l.idx+m.logPage()))
	case "enter":
		if l.idx < len(l.entries) {
			return m.openTrack(l.entries[l.idx].Track)
		}
	case "u":
		if l.idx < len(l.entries) {
			return m.undoCmd(l.entries[l.idx])
		}
	case "?":
		m.help = true
	}
	return nil
}

// undoCmd takes back the handled entry e on Emil's u: the desk's answer, a track the desk
// started, or one it closed. Only this keypress reaches undoEntry, so nothing but Emil closes
// or reopens a track from here.
func (m *paneModel) undoCmd(e store.DeskLogEntry) tea.Cmd {
	switch {
	case m.undoing:
		m.status = "Still taking back the last one"
		return nil
	case e.Kind != "answered" && e.Kind != "started" && e.Kind != "closed":
		m.status = "Only the desk's answers, starts and closes can be taken back"
		return nil
	case !e.UndoneAt.IsZero():
		m.status = "That was taken back already"
		return nil
	}
	self := ""
	if m.float {
		self = os.Getenv("REX_BLOCK")
	}
	m.undoing, m.status = true, "Taking back the desk's "+map[string]string{"answered": "answer to ", "started": "start of ", "closed": "close of "}[e.Kind]+e.Track+"…"
	return func() tea.Msg { return undoneMsg(undoEntry(e, self)) }
}

func (m *paneModel) logPage() int { return max(1, (m.h-4)/2) }

// whenAt is a time of the desk's log: the clock for today, the date too before that.
func whenAt(t, now time.Time) string {
	t = t.Local()
	if startOfDay(t).Equal(startOfDay(now)) {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func (m *paneModel) logBody(bodyH int) []string {
	l := m.logv
	if len(l.entries) == 0 {
		mid := bodyH / 2
		var body []string
		for i := range bodyH {
			switch i {
			case mid:
				body = append(body, center(dimSt.Render("Nothing handled yet"), m.w))
			default:
				body = append(body, "")
			}
		}
		return body
	}
	now := m.clock()
	lines, kinds := []string{" " + dimSt.Render("HANDLED")}, []byte{lineGap}
	selLine := 0
	for i, e := range l.entries {
		when := whenAt(e.At, now)
		text := e.Kind + " " + e.Track + ": " + e.Did
		if e.Asked != "" {
			text += " (" + e.Asked + ")"
		}
		if !e.UndoneAt.IsZero() {
			text += " · taken back"
		}
		row := " " + dimSt.Render(fmt.Sprintf("%-12s", when)) + " " + text
		if i == l.idx {
			row = m.tint(selSt.Render("▌")+boldSt.Render(fmt.Sprintf("%-12s", when))+" "+boldSt.Render(text), m.w)
			selLine = len(lines)
		}
		lines, kinds = append(lines, fit(row, m.w)), append(kinds, lineTrack)
	}
	return windowList(lines, kinds, selLine, bodyH)
}

// deskNotesLines is the Desk notes section of a track's detail: the desk's newest log entries
// for it, oldest of those first.
func (m *paneModel) deskNotesLines(c *Card, mw int) []string {
	var mine []store.DeskLogEntry
	for _, e := range m.desk.notes {
		if strings.EqualFold(e.Track, c.Name) {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	earlier := max(0, len(mine)-deskNotesRows)
	mine = mine[:len(mine)-earlier]
	slices.Reverse(mine)
	now := m.clock()
	out := []string{"", boldSt.Render("Desk notes")}
	for _, e := range mine {
		text := e.Kind + ": " + e.Did
		if e.Asked != "" {
			text += " (" + e.Asked + ")"
		}
		out = append(out, ansi.Truncate(dimSt.Render(whenAt(e.At, now))+" "+text, mw, "…"))
	}
	if earlier > 0 {
		out = append(out, dimSt.Render(fmt.Sprintf("%d earlier · h opens the log", earlier)))
	}
	return out
}
