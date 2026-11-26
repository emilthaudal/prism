package tracks

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var cursorSt = lipgloss.NewStyle().Reverse(true)

// lineEditor is a one-line text field with standard line editing.
type lineEditor struct {
	r     []rune
	pos   int
	limit int // 0 is no limit
}

func (e *lineEditor) String() string { return string(e.r) }

func (e *lineEditor) set(s string) {
	e.r, e.pos = nil, 0
	e.insert(s)
}

// insert types s at the cursor as one line: line breaks become spaces and other control
// characters are dropped.
func (e *lineEditor) insert(s string) {
	s = ansi.Strip(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s))
	var in []rune
	for _, r := range s {
		if !unicode.IsControl(r) && r != unicode.ReplacementChar {
			in = append(in, r)
		}
	}
	if e.limit > 0 {
		in = in[:max(0, min(len(in), e.limit-len(e.r)))]
	}
	e.r = append(e.r[:e.pos], append(in, e.r[e.pos:]...)...)
	e.pos += len(in)
}

// key applies an editing key and reports whether it was one.
func (e *lineEditor) key(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "left", "ctrl+b":
		e.pos = max(0, e.pos-1)
	case "right", "ctrl+f":
		e.pos = min(len(e.r), e.pos+1)
	case "home", "ctrl+a":
		e.pos = 0
	case "end", "ctrl+e":
		e.pos = len(e.r)
	case "backspace", "ctrl+h":
		if e.pos > 0 {
			e.r = append(e.r[:e.pos-1], e.r[e.pos:]...)
			e.pos--
		}
	case "delete", "ctrl+d":
		if e.pos < len(e.r) {
			e.r = append(e.r[:e.pos], e.r[e.pos+1:]...)
		}
	case "ctrl+u":
		e.r, e.pos = e.r[e.pos:], 0
	case "ctrl+k":
		e.r = e.r[:e.pos]
	case "ctrl+w", "alt+backspace":
		i := e.pos
		for i > 0 && unicode.IsSpace(e.r[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(e.r[i-1]) {
			i--
		}
		e.r, e.pos = append(e.r[:i], e.r[e.pos:]...), i
	case "alt+left", "alt+b":
		for e.pos > 0 && unicode.IsSpace(e.r[e.pos-1]) {
			e.pos--
		}
		for e.pos > 0 && !unicode.IsSpace(e.r[e.pos-1]) {
			e.pos--
		}
	case "alt+right", "alt+f":
		for e.pos < len(e.r) && unicode.IsSpace(e.r[e.pos]) {
			e.pos++
		}
		for e.pos < len(e.r) && !unicode.IsSpace(e.r[e.pos]) {
			e.pos++
		}
	default:
		if msg.Text == "" || msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper) != 0 {
			return false
		}
		e.insert(msg.Text)
	}
	return true
}

// wrapped renders the text over lines of w cells, at most maxRows of them around the cursor.
func (e *lineEditor) wrapped(w, maxRows int, cursor bool) []string {
	if w <= 0 || maxRows <= 0 {
		return nil
	}
	n := len(e.r)
	if cursor {
		n++
	}
	// Break lines after the last space that fits, else mid-word.
	var lines [][]int // rune indexes per row
	var row []int
	width, lastSpace := 0, -1
	for i := range n {
		c := 1
		if i < len(e.r) {
			c = max(1, ansi.StringWidth(string(e.r[i])))
		}
		if width+c > w && len(row) > 0 {
			cut := len(row)
			if lastSpace >= 0 && lastSpace < len(row)-1 {
				cut = lastSpace + 1
			}
			lines, row = append(lines, row[:cut]), append([]int{}, row[cut:]...)
			width, lastSpace = 0, -1
			for j, k := range row {
				width += max(1, ansi.StringWidth(string(e.r[k])))
				if unicode.IsSpace(e.r[k]) {
					lastSpace = j
				}
			}
		}
		if i < len(e.r) && unicode.IsSpace(e.r[i]) {
			lastSpace = len(row)
		}
		row = append(row, i)
		width += c
	}
	lines = append(lines, row)
	var rows []string
	curRow := 0
	for ri, l := range lines {
		var b strings.Builder
		for _, i := range l {
			ch := " "
			if i < len(e.r) {
				ch = string(e.r[i])
			}
			if cursor && i == e.pos {
				curRow, ch = ri, cursorSt.Render(ch)
			}
			b.WriteString(ch)
		}
		rows = append(rows, b.String())
	}
	if len(rows) > maxRows {
		start := max(0, min(curRow-maxRows+1, len(rows)-maxRows))
		rows = rows[start : start+maxRows]
	}
	return rows
}

// view renders the text in w cells, scrolled so the cursor shows, with the cursor as a
// reversed cell when cursor is set.
func (e *lineEditor) view(w int, cursor bool) string {
	if w <= 0 {
		return ""
	}
	cw := func(r rune) int { return max(1, ansi.StringWidth(string(r))) }
	start, used := e.pos, 1
	for start > 0 && used+cw(e.r[start-1]) <= w {
		start--
		used += cw(e.r[start])
	}
	var b strings.Builder
	width := 0
	for i := start; i <= len(e.r); i++ {
		ch, c := " ", 1
		if i < len(e.r) {
			ch, c = string(e.r[i]), cw(e.r[i])
		} else if !cursor {
			break
		}
		if width+c > w {
			break
		}
		if cursor && i == e.pos {
			ch = cursorSt.Render(ch)
		}
		b.WriteString(ch)
		width += c
	}
	return b.String()
}
