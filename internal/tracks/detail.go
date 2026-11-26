package tracks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// maxMeasure is the widest the detail body text runs, so long lines stay readable on a wide terminal.
const maxMeasure = 76

const recommendMark = "Recommend:"

var tierLine = regexp.MustCompile(`^Tier [0-9]:`)

// wrapHang wraps text to width columns behind first, with every further line behind rest, so
// wrapped lines hang under the text. Words wider than the room are broken.
func wrapHang(text string, width int, first, rest string) []string {
	room := max(1, width-max(lipgloss.Width(first), lipgloss.Width(rest)))
	text = strings.ReplaceAll(text, "\t", "  ")
	var out []string
	for i, l := range strings.Split(ansi.Wrap(strings.TrimRight(text, " "), room, ""), "\n") {
		prefix := rest
		if i == 0 {
			prefix = first
		}
		out = append(out, prefix+strings.TrimRight(l, " "))
	}
	return out
}

// whereLines renders the Where it is text flush left, without the tier line.
func whereLines(text string, mw int) []string {
	head, rest := wherePartsOf(text, mw)
	return append(head, rest...)
}

// wherePartsOf splits the wrapped Where it is text into its headline, the first line of the
// card's text, and the detail after it.
func wherePartsOf(text string, mw int) (head, rest []string) {
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		l = strings.TrimRight(l, " \t\r")
		if tierLine.MatchString(strings.TrimSpace(l)) || head == nil && l == "" {
			continue
		}
		wrapped := wrapHang(l, mw, "", "")
		if head == nil {
			head = wrapped
			continue
		}
		rest = append(rest, wrapped...)
	}
	return head, rest
}

// notStarted is what Where it is says before the agent has written anything.
const notStarted = "Not started."

// whereBlock is the Where it is section without a heading: the headline bold, the rest dim. A
// card with nothing there, or only the placeholder a new card carries, shows the dim placeholder.
func whereBlock(text string, mw int) []string {
	head, rest := wherePartsOf(text, mw)
	if len(head) == 0 || len(rest) == 0 && strings.TrimSpace(strings.Join(head, " ")) == notStarted {
		return []string{dimSt.Render(notStarted)}
	}
	out := make([]string, 0, len(head)+len(rest))
	for _, l := range head {
		out = append(out, boldSt.Render(l))
	}
	for _, l := range rest {
		out = append(out, dimSt.Render(l))
	}
	return out
}

const (
	todayLabel   = "TODAY       "
	waitingLabel = "WAITING ON  "
	checkLabel   = "CHECK       "

	waitingRowsMax = 2 // a long waiting_on shows two rows, as the goal does
)

// todayRows is what done today means for a focus track, two rows at most, as the goal shows;
// nothing for any other track or when nobody set it.
func todayRows(c *Card, mw int) []string {
	today := strings.Join(strings.Fields(c.Meta["today"]), " ")
	if c.mode() != "focus" || today == "" {
		return nil
	}
	hang := strings.Repeat(" ", len(todayLabel))
	return labelled(capRows(wrapHang(today, mw, todayLabel, hang), waitingRowsMax, mw), len(todayLabel))
}

// waitingRows are the two aligned rows under Where it is on a waiting track: what it waits on,
// and its check with the answer and age tracks check last recorded. Either row is left out when
// its field is empty.
func waitingRows(c *Card, st map[string]checkState, now time.Time, mw int) []string {
	var out []string
	hang := strings.Repeat(" ", len(waitingLabel))
	if on := strings.Join(strings.Fields(c.Meta["waiting_on"]), " "); on != "" {
		rows := capRows(wrapHang(on, mw, waitingLabel, hang), waitingRowsMax, mw)
		out = append(out, labelled(rows, len(waitingLabel))...)
	}
	chk := strings.Join(strings.Fields(c.Meta["check"]), " ")
	if chk == "" {
		return out
	}
	text := chk
	switch s, known := st[c.Name+": "+chk]; {
	case !parsesAsCheck(chk):
		text += " · not a check tracks runs"
	case !known:
		text += " · not run yet"
	default:
		text += " · " + yesNo(s.Answer)
		if !s.RanAt.IsZero() {
			text += " · ran " + shortDur(now.Sub(s.RanAt)) + " ago"
		}
	}
	return append(out, labelled(capRows(wrapHang(text, mw, checkLabel, hang), 1, mw), len(checkLabel))...)
}

// capRows keeps the first n rows, with an ellipsis on the last when any were cut.
func capRows(rows []string, n, mw int) []string {
	if len(rows) <= n {
		return rows
	}
	rows = rows[:n]
	last := strings.TrimRight(rows[n-1], " ")
	rows[n-1] = ansi.Truncate(last+" …", mw, "…")
	return rows
}

// labelled dims the first n bytes of each line, the ASCII label column.
func labelled(lines []string, n int) []string {
	for i, l := range lines {
		lines[i] = dimSt.Render(l[:n]) + l[n:]
	}
	return lines
}

func parsesAsCheck(s string) bool {
	_, ok := parseCheck(s)
	return ok
}

// whereFirst is the first line of the Where it is text that is not the tier line, on one line.
func whereFirst(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !tierLine.MatchString(l) {
			return strings.Join(strings.Fields(l), " ")
		}
	}
	return ""
}

// goalLines is the Goal under the track's name: what done looks like, at most goalRows lines.
func goalLines(goal string, mw int) []string {
	lines := strings.Split(ansi.Wrap(goal, mw, ""), "\n")
	if len(lines) > goalRows {
		lines = lines[:goalRows]
		last := strings.TrimRight(lines[goalRows-1], " ")
		lines[goalRows-1] = ansi.Truncate(last+" …", mw, "…")
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// answeredLine is what Needs me says while the agent works on the answer just sent: the asks
// accepted, when the pane recorded them.
func answeredLine(asks []int, blocked bool) string {
	text := "Answered"
	if len(asks) > 0 {
		ns := make([]string, len(asks))
		for i, n := range asks {
			ns[i] = strconv.Itoa(n)
		}
		text += " " + strings.Join(ns, ", ")
	}
	if blocked {
		return text + " · agent waiting"
	}
	return text + " · agent working"
}

// splitRecommend splits an ask at its last Recommend: into the question and the pick.
func splitRecommend(text string) (question, rec string) {
	i := strings.LastIndex(text, recommendMark)
	if i < 0 {
		return text, ""
	}
	return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+len(recommendMark):])
}

// needsLines renders the Needs me section: a number column, the question wrapped, the
// recommendation on its own line behind an arrow, and a blank line between asks.
func needsLines(text string, mw int, numSt lipgloss.Style) []string {
	type item struct {
		n     int
		text  string
		notes []string // continuation lines after the Recommend: line, shown under the arrow
	}
	var items []item
	var pre, post []string // text before the first numbered ask, and after a blank line ends the asks
	open := false          // the last ask still takes continuation lines
	for _, l := range strings.Split(text, "\n") {
		if a, ok := parseAsk(l); ok {
			items = append(items, item{n: a.n, text: a.text})
			open = true
			continue
		}
		l = strings.Join(strings.Fields(l), " ")
		switch {
		case l == "":
			open = false
		case open && !strings.Contains(l, recommendMark) && strings.Contains(items[len(items)-1].text, recommendMark):
			items[len(items)-1].notes = append(items[len(items)-1].notes, l)
		case open:
			items[len(items)-1].text += " " + l
		case len(items) > 0:
			post = append(post, l)
		default:
			pre = append(pre, l)
		}
	}
	var out []string
	for _, l := range pre {
		out = append(out, wrapHang(l, mw, "  ", "  ")...)
	}
	numW := 1
	for _, it := range items {
		numW = max(numW, len(strconv.Itoa(it.n)))
	}
	indent := strings.Repeat(" ", numW+2)
	for i, it := range items {
		if i > 0 || len(pre) > 0 {
			out = append(out, "")
		}
		q, rec := splitRecommend(strings.Join(strings.Fields(it.text), " "))
		num := numSt.Render(fmt.Sprintf("%*d", numW, it.n)) + "  "
		if q == "" {
			out = append(out, strings.TrimRight(num, " "))
		} else {
			out = append(out, wrapHang(q, mw, num, indent)...)
		}
		if rec != "" {
			out = append(out, wrapHang(rec, mw, indent+warnSt.Render("→")+" ", indent+"  ")...)
		}
		for _, n := range it.notes {
			for _, l := range wrapHang(n, mw, indent, indent) {
				out = append(out, dimSt.Render(l))
			}
		}
	}
	for i, l := range post {
		if i == 0 {
			out = append(out, "")
		}
		out = append(out, wrapHang(l, mw, "  ", "  ")...)
	}
	return out
}

// linkLabels is what the Links line shows: Confluence pages collapse into one entry at the end.
func linkLabels(ls []link) []string {
	var out []string
	pages := 0
	for _, l := range ls {
		if l.label == confluenceLabel {
			pages++
			continue
		}
		out = append(out, l.label)
	}
	switch pages {
	case 0:
	case 1:
		out = append(out, confluenceLabel)
	default:
		out = append(out, fmt.Sprintf("%d Confluence pages", pages))
	}
	return out
}
