package tracks

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// All terminal output lives here. Functions take plain structs and return strings.

type Row struct {
	Name, Mode, Live, Where string
	Asks                    int
}

type CardView struct {
	Name, Goal, Where, Needs string
	Since                    []string
	PRs                      []string
	Notes                    []string
}

type StatRow struct {
	Day                 string
	Opens, Typed, Avoid int
}

func isTTY(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func useColor(w io.Writer) bool { return os.Getenv("NO_COLOR") == "" && isTTY(w) }

type palette struct{ on bool }

func (p palette) paint(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (p palette) bold(s string) string { return p.paint("1", s) }
func (p palette) dim(s string) string  { return p.paint("2", s) }
func (p palette) warn(s string) string { return p.paint("33", s) }

func termWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 100
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n < 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func renderList(rows []Row, focusCount, width int, p palette) string {
	if len(rows) == 0 {
		return p.dim("no tracks yet: tracks new NAME --dir D") + "\n"
	}
	nw := 4
	for _, r := range rows {
		nw = max(nw, len(r.Name))
	}
	var b strings.Builder
	for _, r := range rows {
		live := r.Live
		prefix := fmt.Sprintf("%-*s  %-10s  %2d  %-9s  ", nw, r.Name, r.Mode, r.Asks, live)
		line := prefix + truncate(r.Where, width-len([]rune(prefix)))
		if r.Mode == "focus" {
			line = p.bold(line)
		} else if r.Asks == 0 {
			line = p.dim(line)
		}
		b.WriteString(line + "\n")
	}
	if focusCount > 2 {
		b.WriteString(p.warn(fmt.Sprintf("warning: %d tracks in focus; the plan says at most 2", focusCount)) + "\n")
	}
	return b.String()
}

func renderCard(v CardView, p palette) string {
	var b strings.Builder
	sec := func(title, body string) {
		if strings.TrimSpace(body) != "" {
			b.WriteString("\n" + p.bold(title) + "\n" + body + "\n")
		}
	}
	b.WriteString(p.bold(v.Name) + "\n")
	for _, n := range v.Notes {
		b.WriteString(p.warn(n) + "\n")
	}
	sec("Goal", v.Goal)
	sec("Where it is", v.Where)
	sec("Needs me", v.Needs)
	sec("Since you last looked", strings.Join(append(v.Since, v.PRs...), "\n"))
	return b.String()
}

func renderStats(rows []StatRow, total StatRow, baseline string, p palette) string {
	var b strings.Builder
	line := func(r StatRow) string {
		per := "-"
		if r.Opens > 0 {
			per = fmt.Sprintf("%.1f", float64(r.Typed)/float64(r.Opens))
		}
		av := "-"
		if r.Typed > 0 {
			av = fmt.Sprintf("%d%%", r.Avoid*100/r.Typed)
		}
		return fmt.Sprintf("%-10s  %5d  %7d  %9s  %11s\n", r.Day, r.Opens, r.Typed, av, per)
	}
	b.WriteString(p.bold(fmt.Sprintf("%-10s  %5s  %7s  %9s  %11s", "day", "opens", "prompts", "avoidable", "per open")) + "\n")
	for _, r := range rows {
		b.WriteString(line(r))
	}
	total.Day = "total"
	b.WriteString(p.bold(line(total)))
	b.WriteString(p.dim(baseline) + "\n")
	return b.String()
}
