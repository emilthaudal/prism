package deskui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	threeCols  = 150 // from here all three columns show
	twoCols    = 100 // from here the conversation keeps a column beside it
	leftW      = 34
	boardW     = 48
	sideW      = 46 // the side column in the two-column layout
	maxCompose = 6  // text rows the composer grows to
	maxQLines  = 2  // a question's text on the board
)

var (
	dimSt   = lipgloss.NewStyle().Faint(true)
	boldSt  = lipgloss.NewStyle().Bold(true)
	amberSt = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	greenSt = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	redSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	blueSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	magSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	cyanSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	lineSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	qChip   = lipgloss.NewStyle().Background(lipgloss.Color("3")).Foreground(lipgloss.Color("0"))
	pChip   = lipgloss.NewStyle().Background(lipgloss.Color("1")).Foreground(lipgloss.Color("15"))
	gChip   = lipgloss.NewStyle().Background(lipgloss.Color("2")).Foreground(lipgloss.Color("0"))
)

// clean makes untrusted text safe to draw: control characters become spaces, and newlines too
// unless keepNL.
func clean(s string, keepNL bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNL:
			return r
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return ' '
		}
		return r
	}, s)
}

func width(s string) int { return lipgloss.Width(s) }

// plain is clean text without Markdown's code backticks, which a terminal can't show as code.
func plain(s string) string { return strings.ReplaceAll(clean(s, false), "`", "") }

// fit cuts s to n cells, or pads it to n.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if width(s) > n {
		s = ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", max(0, n-width(s)))
}

// lr puts left and right at the two ends of n cells; the left side gives way first.
func lr(left, right string, n int) string {
	rw := width(right)
	if rw >= n {
		return fit(right, n)
	}
	return fit(left, n-rw-1) + " " + right
}

func wrap(s string, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(clean(s, true), "\n") {
		out = append(out, strings.Split(ansi.Wrap(para, n, ""), "\n")...)
	}
	return out
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

func (m *Model) ago(t time.Time) string { return age(m.now().Sub(t)) }

func (m *Model) clock(t time.Time) string { return t.In(m.loc).Format("15:04") }

func diffStat(c *Changes) string {
	if c == nil {
		return ""
	}
	return greenSt.Render("+"+strconv.Itoa(c.Added)) + " " + redSt.Render("−"+strconv.Itoa(c.Removed)) +
		dimSt.Render(fmt.Sprintf(" · %d %s", c.Files, plural(c.Files, "file")))
}

func plural(n int, s string) string {
	if n == 1 {
		return s
	}
	return s + "s"
}

// box draws a rounded frame w by h with a title on the top border, a note at its right end,
// and a hint on the bottom border.
func box(title, right, bottom string, inner []string, w, h int, focused bool) []string {
	if w < 4 || h < 2 {
		return nil
	}
	ln := lineSt
	if focused {
		ln = blueSt
	}
	iw := w - 4
	top := ln.Render("╭─") + " " + title + " "
	if right != "" {
		fill := w - width(top) - width(right) - 4
		if fill < 1 {
			right, fill = "", w-width(top)-2
		}
		top += ln.Render(strings.Repeat("─", max(0, fill)))
		if right != "" {
			top += " " + right + " "
		}
	} else {
		top += ln.Render(strings.Repeat("─", max(0, w-width(top)-2)))
	}
	top = fit(top, w-2) + ln.Render("─╮")
	out := []string{top}
	for i := range h - 2 {
		l := ""
		if i < len(inner) {
			l = inner[i]
		}
		out = append(out, ln.Render("│ ")+fit(l, iw)+ln.Render(" │"))
	}
	bot := ln.Render("╰─")
	if bottom != "" {
		bot += " " + bottom + " "
	}
	bot = fit(bot+ln.Render(strings.Repeat("─", max(0, w-width(bot)-2))), w-2) + ln.Render("─╯")
	return append(out, bot)
}

// Render draws the whole window.
func (m *Model) Render() string {
	if m.w < 40 || m.h < 12 {
		return "Prism needs at least 40×12"
	}
	colH := m.h - 1
	var body []string
	if !m.have {
		msg := "Connecting to " + m.b.Label() + "…"
		if m.conn != "" {
			msg = "Waiting for " + m.b.Label() + ": " + m.conn
		}
		body = box(boldSt.Render("Prism"), "", "", []string{"", dimSt.Render(clean(msg, false))}, m.w, colH, true)
	} else {
		cols, widths := m.layout()
		parts := make([][]string, len(cols))
		for i, c := range cols {
			parts[i] = m.column(c, widths[i], colH)
		}
		for r := range colH {
			var b strings.Builder
			for i := range parts {
				if r < len(parts[i]) {
					b.WriteString(parts[i][r])
				}
			}
			body = append(body, b.String())
		}
	}
	if m.help {
		body = m.overlayHelp(body)
	}
	return strings.Join(append(body, m.footer()), "\n")
}

// layout is which columns show and how wide: three from 150 columns, the conversation and
// one side column from 100 (the board, or the sessions while they have focus), else the
// focused column alone.
func (m *Model) layout() ([]column, []int) {
	switch {
	case m.w >= threeCols:
		bw := boardW
		if m.w >= 190 {
			bw = boardW + 4
		}
		return []column{colSessions, colChat, colBoard}, []int{leftW, m.w - leftW - bw, bw}
	case m.w >= twoCols:
		side := colBoard
		if m.focus == colSessions {
			side = colSessions
		}
		if side == colSessions {
			return []column{colSessions, colChat}, []int{sideW, m.w - sideW}
		}
		return []column{colChat, colBoard}, []int{m.w - sideW, sideW}
	}
	return []column{m.focus}, []int{m.w}
}

func (m *Model) column(c column, w, h int) []string {
	switch c {
	case colSessions:
		return m.sessionsCol(w, h)
	case colBoard:
		return m.boardCol(w, h)
	}
	return m.chatCol(w, h)
}

// tabs names the columns in the one-column layout, so tab has somewhere visible to go.
func (m *Model) tabs(c column) string {
	if m.w >= twoCols {
		return ""
	}
	names := []string{"Sessions", "Prism", "Board"}
	var parts []string
	for i, n := range names {
		if column(i) == c {
			continue
		}
		parts = append(parts, n)
	}
	return dimSt.Render("tab " + strings.Join(parts, " · "))
}

// ---- sessions ----

func (m *Model) deskStatus() string {
	switch m.snap.Desk.Status {
	case "working":
		return blueSt.Render("◐ ") + "working"
	case "":
		return dimSt.Render("idle")
	}
	s := dimSt.Render(m.snap.Desk.Status)
	if n := m.snap.Desk.QueuedEvents; n > 0 {
		s = dimSt.Render(fmt.Sprintf("%d queued · ", n)) + s
	}
	return s
}

// chip is a session's state at the right of its row.
func (m *Model) chip(s Session) string {
	if q, ok := m.openQuestionOn(s.ID, ""); ok && s.Status != "blocked" {
		return qChip.Render(" ? Question ") + dimSt.Render(" "+m.ago(q.CreatedAt))
	}
	switch s.Status {
	case "blocked":
		label := " ▲ Blocked "
		if strings.Contains(s.WaitingFor, "permission") {
			label = " ▲ Perm "
		}
		return pChip.Render(label) + dimSt.Render(" "+m.ago(s.UpdatedAt))
	case "error":
		return redSt.Render("✕ Error")
	case "working":
		return blueSt.Render("◐ ") + "now"
	case "done":
		if s.SeenAt == nil {
			return greenSt.Render("● ") + "done"
		}
		return dimSt.Render("● done")
	case "stopped":
		return dimSt.Render("■ stopped")
	}
	if strings.Contains(s.Activity, "queued") {
		return dimSt.Render("○ queued")
	}
	return dimSt.Render("○ idle")
}

func (m *Model) sessionsCol(w, h int) []string {
	iw := w - 4
	focused := m.focus == colSessions
	var lines []string
	for _, r := range m.leftRows() {
		sel := r.key != "" && r.key == m.leftKey
		mark := " "
		if sel {
			mark = blueSt.Render("▌")
		}
		name := func(s string) string {
			if sel {
				return boldSt.Render(s)
			}
			return s
		}
		switch r.kind {
		case rowBlank:
			lines = append(lines, "")
		case rowGroup:
			lines = append(lines, " "+dimSt.Render(r.label))
		case rowParked:
			fold := "▸"
			if m.parkedOpen {
				fold = "▾"
			}
			lines = append(lines, mark+dimSt.Render(fmt.Sprintf("%s %d %s", r.label, r.count, fold)))
		case rowDesk:
			lines = append(lines, lr(mark+magSt.Render("● ")+boldSt.Render("Prism"), m.deskStatus(), iw))
		case rowTrack:
			t := r.track
			var right string
			switch {
			case r.lead > 0:
				right = dimSt.Render(fmt.Sprintf("lead · %d", r.lead))
				if _, ok := m.openQuestionOn("", t.ID); ok {
					right = qChip.Render(" ? ") + " " + right
				}
			case r.sess != nil:
				right = m.chip(*r.sess)
				if r.sess.Owner == "emil" {
					right = cyanSt.Render("emil ") + right
				}
			case t.WaitingOn != "":
				right = dimSt.Render(clean(t.WaitingOn, false) + " · " + m.ago(t.UpdatedAt))
			default:
				right = dimSt.Render("no session")
			}
			lines = append(lines, lr(mark+name(clean(t.Name, false)), right, iw))
		case rowSession:
			s := r.sess
			right := m.chip(*s)
			if s.Owner == "emil" {
				right = cyanSt.Render("emil ") + right
			}
			lines = append(lines, lr(mark+dimSt.Render(" "+r.tree)+name(clean(s.Name, false)), right, iw))
		}
	}
	sel := 0
	for i, r := range m.leftRows() {
		if r.key == m.leftKey {
			sel = i
		}
	}
	lines = windowAround(lines, sel, h-2)
	return box(boldSt.Render("Sessions"), m.tabs(colSessions), "", lines, w, h, focused)
}

// windowAround keeps line sel in a window of n lines.
func windowAround(lines []string, sel, n int) []string {
	if len(lines) <= n || n <= 0 {
		return lines
	}
	off := max(0, min(sel-n/2, len(lines)-n))
	return lines[off : off+n]
}

// ---- conversation ----

func (m *Model) chatCol(w, h int) []string {
	iw := w - 4
	focused := m.focus == colChat
	var title, right string
	var lines []string
	var bottom []string
	newer := 0
	switch {
	case m.view == "":
		title = boldSt.Render("Prism")
		right = m.deskStatus() + dimSt.Render(" · "+m.b.Label())
		var shown []Entry
		shown, newer = m.shownConversation()
		lines = m.conversationLines(shown, iw)
		bottom = m.composer(iw)
	case strings.HasPrefix(m.view, "s:"):
		s, ok := m.snap.session(m.view[2:])
		if !ok {
			title = boldSt.Render("session gone")
			lines = []string{dimSt.Render("This session is no longer listed. esc goes back to Prism.")}
		} else {
			title = boldSt.Render(clean(s.Name, false)) + dimSt.Render(" · transcript")
			right = dimSt.Render(s.Owner+" · "+s.Host+" · ") + m.chip(s)
			lines = m.transcriptLines(s, iw)
		}
		bottom = m.readOnly(iw)
	default:
		t, ok := m.snap.track(m.view[2:])
		if ok {
			title = boldSt.Render(clean(t.Name, false)) + dimSt.Render(" · track")
			right = dimSt.Render(t.Mode + " · tier " + t.Tier)
			lines = m.trackLines(t, iw)
		}
		bottom = m.readOnly(iw)
	}
	avail := h - 2 - len(bottom) - 1
	if avail < 1 {
		bottom, avail = nil, h-2
	}
	maxScroll := max(0, len(lines)-avail)
	m.scroll = min(m.scroll, maxScroll)
	end := len(lines) - m.scroll
	start := max(0, end-avail)
	view := append([]string(nil), lines[start:end]...)
	for len(view) < avail {
		if strings.HasPrefix(m.view, "t:") {
			view = append(view, "")
		} else {
			view = append([]string{""}, view...)
		}
	}
	// The marker takes the blank row above the composer, so nothing above it moves.
	marker := ""
	if newer > 0 {
		marker = lr("", amberSt.Render(fmt.Sprintf("▾ %d new", newer))+dimSt.Render(" · ↓ to read"), iw)
	} else if m.scroll > 0 {
		marker = lr("", dimSt.Render(fmt.Sprintf("▾ %d lines below", m.scroll)), iw)
	}
	inner := append(view, marker)
	inner = append(inner, bottom...)
	return box(title, joinRight(right, m.tabs(colChat)), "", inner, w, h, focused)
}

func joinRight(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	return a + dimSt.Render(" · ") + b
}

func (m *Model) entryHead(label, at string) string {
	return " " + label + dimSt.Render("  "+at)
}

func (m *Model) conversationLines(shown []Entry, iw int) []string {
	notes := m.notes
	var out []string
	add := func(e Entry) {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, m.entryLines(e, iw)...)
	}
	ni := 0
	for _, e := range shown {
		for ni < len(notes) && notes[ni].At.Before(e.At) {
			add(notes[ni])
			ni++
		}
		add(e)
	}
	for ; ni < len(notes); ni++ {
		add(notes[ni])
	}
	if len(out) == 0 {
		out = []string{dimSt.Render(" Nothing said yet. Write to Prism below.")}
	}
	return out
}

func indent(lines []string, n int) []string {
	pad := strings.Repeat(" ", n)
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

func (m *Model) entryLines(e Entry, iw int) []string {
	at := m.clock(e.At)
	var out []string
	body := e.Text
	switch e.Role {
	case "emil":
		out = append(out, m.entryHead(cyanSt.Bold(true).Render("You"), at))
	case "desk":
		if e.Outcome == "reasked" {
			// prism server hid a reply that numbered questions in chat; they come back as decisions.
			return []string{lr(dimSt.Render(" · Prism re-asked these as decisions"), dimSt.Render(at), iw)}
		}
		out = append(out, m.entryHead(magSt.Bold(true).Render("Prism"), at))
	case "note":
		return []string{lr(dimSt.Render(" · "+clean(e.Text, false)), dimSt.Render(at), iw)}
	case "report":
		name := "a session"
		s, ok := m.snap.session(e.Session)
		if ok {
			name = s.Name
		}
		out = append(out, m.entryHead(dimSt.Render("From ")+boldSt.Render(clean(name, false)), at))
		body = strings.ReplaceAll(body, "`", "")
		switch e.Outcome {
		case "done":
			body = "finished. " + body
		case "blocked":
			waits := "a prompt"
			if ok && s.WaitingFor != "" {
				waits = "a " + s.WaitingFor
			}
			body = "is waiting on " + waits + ":\n" + body
		case "error":
			body = "failed. " + body
		}
		out = append(out, indent(wrap(body, iw-4), 3)...)
		if ok && s.Changes != nil && e.Outcome == "done" {
			chip := dimSt.Render("landed")
			if !s.Changes.Merged {
				chip = gChip.Render(" ready to land ")
			}
			stat := "   " + diffStat(s.Changes)
			if s.Changes.Branch != "" {
				stat += dimSt.Render(" · " + clean(s.Changes.Branch, false))
			}
			out = append(out, lr(stat, chip, iw))
		}
		return out
	default:
		out = append(out, m.entryHead(boldSt.Render(clean(e.Role, false)), at))
	}
	out = append(out, indent(wrap(body, iw-4), 3)...)
	if e.Steps > 0 {
		out = append(out, dimSt.Render(fmt.Sprintf("   ▸ %d %s", e.Steps, plural(e.Steps, "step"))))
	}
	return out
}

func (m *Model) transcriptLines(s Session, iw int) []string {
	entries, ok := m.transcripts[s.ID]
	var out []string
	if s.WaitingFor != "" || s.Activity != "" {
		what := s.Activity
		if s.WaitingFor != "" {
			what = "waiting on " + s.WaitingFor + ": " + s.Activity
		}
		out = append(out, dimSt.Render(" Now: ")+clean(what, false), "")
	}
	if !ok {
		return append(out, dimSt.Render(" Reading the transcript…"))
	}
	if len(entries) == 0 {
		return append(out, dimSt.Render(" No transcript yet."))
	}
	for i, e := range entries {
		if i > 0 {
			out = append(out, "")
		}
		var label string
		switch e.Role {
		case "user":
			label = magSt.Bold(true).Render("Prism")
			if s.Owner == "emil" {
				label = cyanSt.Bold(true).Render("You")
			}
		case "assistant":
			label = boldSt.Render(clean(s.Name, false))
		default:
			label = dimSt.Render(clean(e.Role, false))
		}
		out = append(out, m.entryHead(label, m.clock(e.At)))
		out = append(out, indent(wrap(e.Text, iw-4), 3)...)
		if e.Steps > 0 {
			out = append(out, dimSt.Render(fmt.Sprintf("   ▸ %d %s", e.Steps, plural(e.Steps, "step"))))
		}
	}
	return out
}

func (m *Model) trackLines(t Track, iw int) []string {
	field := func(k, v string) []string {
		if v == "" {
			return nil
		}
		const kw = 13
		ls := wrap(v, iw-kw)
		for i := range ls {
			if i == 0 {
				ls[i] = dimSt.Render(fit(" "+k, kw)) + ls[i]
			} else {
				ls[i] = strings.Repeat(" ", kw) + ls[i]
			}
		}
		return ls
	}
	var out []string
	out = append(out, field("Goal", t.Goal)...)
	out = append(out, field("Where", t.Where)...)
	out = append(out, field("Today", t.Today)...)
	if t.WaitingOn != "" {
		out = append(out, field("Waiting on", t.WaitingOn+" · "+m.ago(t.UpdatedAt))...)
	}
	out = append(out, field("Links", strings.Join(t.Links, "  "))...)
	ss := m.liveSessions(t.ID)
	if len(ss) > 0 {
		out = append(out, "", " "+dimSt.Render("SESSIONS"))
		for _, s := range ss {
			out = append(out, lr("  "+clean(s.Name, false), m.chip(s), iw))
		}
	}
	return out
}

func (m *Model) readOnly(iw int) []string {
	msg := "Read-only. t takes it over in Rex, g gives it back, esc returns to Prism."
	inner := wrap(msg, iw-4)
	if len(m.text) > 0 {
		inner = append(inner, amberSt.Render("Your draft to Prism is kept."))
	}
	return box(dimSt.Render("read-only"), "", "", inner, iw, len(inner)+2, false)
}

// composer is the box under the conversation: a message to the desk, or, with a question
// selected, its options and an answer in words.
func (m *Model) composer(iw int) []string {
	focused := m.focus == colChat
	wordsHere := m.words && m.selQ != ""
	var panel []string
	if m.selQ != "" {
		panel = m.questionComposer(iw, focused && wordsHere)
	}
	text := m.textLines(iw-4, focused && !wordsHere, "Write to Prism…")
	if wordsHere {
		text = []string{dimSt.Render("Write to Prism…")}
	}
	if len(text) > maxCompose {
		text = text[len(text)-maxCompose:]
	}
	hint := dimSt.Render("enter send · alt+enter new line")
	if n := len(m.openQuestions()); n > 0 {
		hint = dimSt.Render(fmt.Sprintf("%d %s on the board · enter send", n, plural(n, "question")))
	}
	return append(panel, box(boldSt.Render("Message Prism"), "", hint, text, iw, len(text)+2, focused && !wordsHere)...)
}

// textLines draws the composer's text, wrapped, with the cursor when focused.
func (m *Model) textLines(n int, focused bool, placeholder string) []string {
	if len(m.text) == 0 {
		cur := ""
		if focused {
			cur = "▌"
		}
		return []string{cur + dimSt.Render(placeholder)}
	}
	s := string(m.text[:m.cur])
	if focused {
		s += "▌"
	}
	s += string(m.text[m.cur:])
	return wrap(s, n)
}

func (m *Model) questionComposer(iw int, focused bool) []string {
	q, open := m.question()
	p := m.pendingFor(q.ID)
	n := iw - 4
	title := boldSt.Render(q.ID) + " " + clean(m.trackName(q.Track), false) + dimSt.Render(" · ") + plain(q.Question)
	var inner []string
	for i, o := range q.Options {
		key := dimSt.Render("^" + strconv.Itoa(i+1) + " ")
		label := clean(o.Label, false)
		if i == q.Recommended {
			label = greenSt.Bold(true).Render(label)
		}
		right := ""
		if i == q.Recommended {
			right = greenSt.Render("recommended")
		}
		if p != nil && p.option == i {
			right = amberSt.Render("sending")
		}
		inner = append(inner, lr(key+label, right, n))
		if o.Description != "" {
			inner = append(inner, "   "+dimSt.Render(fit(clean(o.Description, false), n-3)))
		}
		if a := o.Attachment; a != nil {
			inner = append(inner, "   "+dimSt.Render(fit(a.glyph()+" "+clean(a.Label, false)+" · o opens", n-3)))
		}
	}
	for _, a := range q.Attachments {
		inner = append(inner, dimSt.Render(fit(a.glyph()+" "+clean(a.Label, false)+" · o opens", n)))
	}
	inner = append(inner, "")
	if m.words {
		inner = append(inner, m.textLines(n, focused, "Or answer in your own words…")...)
	} else {
		inner = append(inner, dimSt.Render("Or answer in your own words…")+dimSt.Render(" · r on the board"))
	}
	var hint string
	switch {
	case p != nil:
		secs := int((p.deadline.Sub(m.now()) + time.Second - 1) / time.Second)
		title = amberSt.Render(clean(p.label(), false)+" · sending in "+strconv.Itoa(max(0, secs))+" s") + dimSt.Render(" · z undo")
		hint = dimSt.Render(q.ID + " · z takes it back")
	case !open:
		hint = amberSt.Render(q.ID+" was answered elsewhere") + dimSt.Render(" · esc Prism")
	default:
		qs := m.openQuestions()
		idx := 1
		for i, x := range qs {
			if x.ID == q.ID {
				idx = i + 1
			}
		}
		hint = dimSt.Render(fmt.Sprintf("%d of %d · enter %s · esc Prism", idx, len(qs), "recommended"))
	}
	return box(title, "", hint, inner, iw, len(inner)+2, focused)
}

func (m *Model) trackName(id string) string {
	if t, ok := m.snap.track(id); ok {
		return t.Name
	}
	return id
}

// ---- board ----

func (m *Model) boardCol(w, h int) []string {
	iw := w - 4
	focused := m.focus == colBoard
	rows := m.boardRows()
	qs := m.openQuestions()
	running := 0
	for _, r := range rows {
		if r.kind == brFlight && r.s.Status == "working" {
			running++
		}
	}
	right := amberSt.Render(fmt.Sprintf("%d need you", len(qs))) + dimSt.Render(" · ") + blueSt.Render(fmt.Sprintf("%d running", running))
	if m.w < threeCols {
		right = joinRight(right, m.tabs(colBoard))
	}
	var lines []string
	selLine := 0
	mark := func(r boardRow) string {
		if focused && r.key == m.boardKey {
			selLine = len(lines)
			return blueSt.Render("▌")
		}
		if r.key == m.boardKey {
			selLine = len(lines)
		}
		return " "
	}
	day := m.now().In(m.loc).Format("Monday 2 Jan")
	lines = append(lines, lr(boldSt.Render(day), dimSt.Render(fmt.Sprintf("%d tracks", len(m.snap.Tracks)-len(m.tracksIn("parked")))), iw))
	nl, nr, nf := len(m.landedSessions()), 0, 0
	for _, r := range rows {
		switch r.kind {
		case brReady:
			nr++
		case brFlight:
			nf++
		}
	}
	lines = append(lines, dimSt.Render(fmt.Sprintf("%d landed · %d ready · %d in flight", nl, nr, nf)))
	if tot := nl + nr + nf; tot > 0 {
		done := iw * nl / tot
		lines = append(lines, greenSt.Render(strings.Repeat("━", done))+lineSt.Render(strings.Repeat("━", iw-done)))
	}
	section := func(label string, n int, st lipgloss.Style) {
		lines = append(lines, "", lr(st.Bold(true).Render(label), dimSt.Render(strconv.Itoa(n)), iw))
	}
	if len(qs) > 0 {
		section("NEEDS YOU", len(qs), amberSt)
	} else {
		lines = append(lines, "", dimSt.Render("Nothing needs you"))
	}
	kindSeen := map[boardKind]bool{}
	for _, r := range rows {
		switch r.kind {
		case brQuestion:
			lines = append(lines, m.questionRows(r, mark(r), iw)...)
		case brQueued:
			if !kindSeen[r.kind] {
				kindSeen[r.kind] = true
				section("ANSWERED, WAITING TO SEND", len(m.queuedQuestions()), dimSt)
			}
			q := r.qd
			lines = append(lines, lr(mark(r)+dimSt.Render("◇ ")+boldSt.Render(q.ID+" "+clean(m.trackName(q.Track), false)), dimSt.Render(m.ago(q.CreatedAt)), iw))
			lines = append(lines, "   "+dimSt.Render(fit("→ "+clean(q.AnswerLabel(), false)+" · sent when its agent stops", iw-3)))
		case brFlight, brReady:
			if !kindSeen[r.kind] {
				kindSeen[r.kind] = true
				if r.kind == brFlight {
					section("IN FLIGHT", nf, blueSt)
				} else {
					section("READY TO LAND", nr, greenSt)
				}
			}
			lines = append(lines, m.sessionRows(r, mark(r), iw)...)
		case brWaiting:
			if !kindSeen[r.kind] {
				kindSeen[r.kind] = true
				n := 0
				for _, x := range rows {
					if x.kind == brWaiting {
						n++
					}
				}
				section("WAITING ON PEOPLE", n, dimSt)
			}
			lines = append(lines, lr(mark(r)+clean(r.t.Name, false), dimSt.Render(clean(r.t.WaitingOn, false)+" · "+m.ago(r.t.UpdatedAt)), iw))
		case brLanded:
			fold := "▸"
			if m.landedOpen {
				fold = "▾"
			}
			lines = append(lines, "")
			lines = append(lines, lr(mark(r)+dimSt.Bold(true).Render("LANDED"), dimSt.Render(fmt.Sprintf("%d %s", nl, fold)), iw))
		case brLandedItem:
			lines = append(lines, lr(mark(r)+greenSt.Render("✓ ")+clean(r.s.Name, false), diffStat(r.s.Changes), iw))
		}
	}
	lines = windowAround(lines, selLine, h-2)
	return box(boldSt.Render("Board"), right, "", lines, w, h, focused)
}

func shortLabel(s string) string {
	if i := strings.IndexAny(s, ",("); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func (m *Model) questionRows(r boardRow, mark string, iw int) []string {
	q := r.q
	glyph := amberSt.Render("◆ ")
	if s, ok := m.snap.session(q.Session); ok && s.Status == "blocked" {
		glyph = redSt.Render("▲ ")
	}
	head := lr(mark+glyph+boldSt.Render(q.ID+" "+clean(m.trackName(q.Track), false)), dimSt.Render(q.From+" · "+m.ago(q.CreatedAt)), iw)
	out := []string{head}
	ql := wrap(plain(q.Question), iw-3)
	if len(ql) > maxQLines {
		ql = ql[:maxQLines]
		ql[maxQLines-1] = fit(ql[maxQLines-1]+" …", iw-3)
	}
	out = append(out, indent(ql, 3)...)
	if p := m.pendingFor(q.ID); p != nil {
		secs := int((p.deadline.Sub(m.now()) + time.Second - 1) / time.Second)
		out = append(out, "   "+amberSt.Render(fit("→ "+clean(p.label(), false), iw-24))+amberSt.Render(fmt.Sprintf(" sending in %d s", max(0, secs)))+dimSt.Render(" · z"))
		return out
	}
	line := ""
	for i, o := range q.Options {
		s := strconv.Itoa(i+1) + " " + shortLabel(clean(o.Label, false))
		if i == q.Recommended {
			s = greenSt.Bold(true).Render("★" + s)
		}
		if line != "" && width(line)+2+width(s) > iw-3 {
			out = append(out, "   "+line)
			line = ""
		}
		if line != "" {
			line += "  "
		}
		line += s
	}
	out = append(out, "   "+line)
	if r.key == m.boardKey {
		for _, e := range q.Evidence {
			out = append(out, lr("   "+greenSt.Render("✓ ")+clean(e.Text, false), dimSt.Render(clean(e.Source, false)), iw))
		}
		for _, l := range q.Links {
			out = append(out, "   "+dimSt.Render("o ")+blueSt.Render(clean(l, false)))
		}
		for _, l := range q.attachmentLinks() {
			out = append(out, "   "+dimSt.Render("o ")+blueSt.Render(fit(clean(l, false), iw-5)))
		}
	} else if len(q.Evidence) > 0 {
		var ev []string
		for _, e := range q.Evidence {
			ev = append(ev, clean(e.Text, false))
		}
		out = append(out, "   "+dimSt.Render(fit("✓ "+strings.Join(ev, " · "), iw-3)))
	}
	return out
}

func (m *Model) sessionRows(r boardRow, mark string, iw int) []string {
	s := r.s
	var glyph, sub string
	track := m.trackName(s.Track)
	switch {
	case r.kind == brReady:
		glyph = greenSt.Render("● ")
		sub = "not merged"
		if s.Changes.Branch != "" {
			sub = s.Changes.Branch + " · not merged"
		}
	case s.Status == "working":
		glyph = blueSt.Render("◐ ")
		sub = s.Activity
	case s.Status == "blocked":
		glyph = redSt.Render("▲ ")
		sub = "waiting on " + s.WaitingFor
	case s.Status == "error":
		glyph = redSt.Render("✕ ")
		sub = s.LastAnswer
	default:
		glyph = dimSt.Render("○ ")
		sub = s.Activity
		if sub == "" {
			sub = "idle"
		}
	}
	right := diffStat(s.Changes)
	if right == "" && strings.Contains(s.Activity, "queued") {
		right = dimSt.Render("queued")
	}
	name := clean(s.Name, false)
	if s.Owner == "emil" {
		name += cyanSt.Render(" emil")
	}
	out := []string{lr(mark+glyph+name, right, iw)}
	if track != "" && track != s.Name {
		sub = track + " · " + sub
	}
	return append(out, "   "+dimSt.Render(fit(clean(sub, false), iw-3)))
}

// ---- footer and help ----

func (m *Model) footer() string {
	conn := greenSt.Render("● ") + dimSt.Render(m.b.Label())
	if !m.live {
		conn = amberSt.Render("○ reconnecting")
	}
	if m.status != "" {
		st := clean(m.status, false)
		if m.statusErr {
			st = redSt.Render(st)
		}
		return lr(" "+st, conn, m.w)
	}
	var keys string
	switch {
	case m.focus == colBoard:
		keys = "enter recommended  1-9 pick  r in words  z undo  t take over  g give back  o link  tab column  ? keys"
	case m.focus == colSessions:
		keys = "↑↓ move  enter open  space fold  t take over  g give back  o link  tab column  ? keys  q quit"
	case m.view != "":
		keys = "↑↓ scroll  t take over  g give back  o link  esc Prism  tab column  ? keys"
	case m.selQ != "":
		keys = "enter recommended (empty box) or send to Prism  alt+1-9 pick  z undo (empty box)  esc Prism  tab column"
	default:
		keys = "enter send  alt+enter new line  ↑↓ scroll  ctrl+u clear  tab column"
	}
	return lr(" "+dimSt.Render(keys), conn, m.w)
}

var helpRows = [][2]string{
	{"tab  shift+tab", "next, previous column (also ctrl+→ ctrl+←)"},
	{"↑ ↓  j k", "move; in the conversation, scroll"},
	{"enter", "board: take the recommendation · sessions: open"},
	{"1-9", "board: pick option N"},
	{"alt+1-9", "composer: pick option N of its question"},
	{"r", "board: answer in your own words"},
	{"z  ctrl+z", "take back an answer within 5 s"},
	{"t  g", "take a session over in Rex; give it back"},
	{"o", "open the link"},
	{"space", "fold parked tracks, landed sessions"},
	{"esc", "back to Prism's chat"},
	{"alt+enter", "new line in the composer"},
	{"q  ctrl+c", "quit (q outside the composer)"},
}

func (m *Model) overlayHelp(body []string) []string {
	kw := 0
	for _, r := range helpRows {
		kw = max(kw, width(r[0]))
	}
	inner := []string{boldSt.Render("Keys"), ""}
	for _, r := range helpRows {
		inner = append(inner, boldSt.Render(fit(r[0], kw))+"  "+dimSt.Render(r[1]))
	}
	inner = append(inner, "", dimSt.Render("any key closes"))
	bw := 0
	for _, l := range inner {
		bw = max(bw, width(l))
	}
	bw = min(bw+4, m.w-2)
	b := box(boldSt.Render("Help"), "", "", inner, bw, len(inner)+2, true)
	top := max(0, (len(body)-len(b))/2)
	left := max(0, (m.w-bw)/2)
	out := append([]string(nil), body...)
	for i, l := range b {
		if top+i >= len(out) {
			break
		}
		row := out[top+i]
		out[top+i] = ansi.Truncate(row, left, "") + strings.Repeat(" ", max(0, left-width(ansi.Truncate(row, left, "")))) + l + ansi.TruncateLeft(row, left+bw, "")
	}
	return out
}
