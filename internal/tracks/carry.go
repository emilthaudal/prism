package tracks

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/emilthaudal/prism/internal/store"
)

// EMI-296: closing a track must not lose the asks nobody answered or the Later lines. close
// asks first while asks remain; whatever is left is appended to inbox.md, and the pane header
// counts the entries still open there as "carried over".

func inboxPath() string { return filepath.Join(home(), "inbox.md") }

const inboxOpen = "- [ ] "

// isCloseAsk is the ask a track raises about closing itself: closing answers it.
func isCloseAsk(text string) bool { return strings.HasPrefix(text, "Close this track?") }

func laterLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "-")); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// leftovers are a card's asks that closing would leave unanswered, bar the close ask, and its
// Later lines.
func leftovers(c *Card) (asks, later []string) {
	for _, a := range c.askList() {
		if !isCloseAsk(a.text) {
			asks = append(asks, a.text)
		}
	}
	return asks, laterLines(c.Sections["Later"])
}

// carryOver appends the leftovers of track name to inbox.md, one open item each.
func carryOver(name string, asks, later []string, now time.Time) error {
	if len(asks)+len(later) == 0 {
		return nil
	}
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(inboxPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	day := now.Format(closeDateLayout)
	var b strings.Builder
	for _, a := range asks {
		fmt.Fprintf(&b, "%s%s %s: unanswered ask: %s\n", inboxOpen, day, name, flat(a))
	}
	for _, l := range later {
		fmt.Fprintf(&b, "%s%s %s: later: %s\n", inboxOpen, day, name, flat(l))
	}
	_, err = io.WriteString(f, b.String())
	return err
}

func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// carriedOver counts the inbox entries nobody has checked off.
func carriedOver() int {
	b, err := os.ReadFile(inboxPath())
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, inboxOpen) {
			n++
		}
	}
	return n
}

// confirmLeftovers is the y/N tracks close asks while numbered asks remain. Without a
// terminal it refuses and names --yes.
func confirmLeftovers(name string, asks []string, in io.Reader, out io.Writer) error {
	var list strings.Builder
	fmt.Fprintf(&list, "%s has %s open:\n", name, plural(len(asks), "ask"))
	for i, a := range asks {
		fmt.Fprintf(&list, "  %d. %s\n", i+1, a)
	}
	if f, ok := in.(*os.File); !ok || !term.IsTerminal(f.Fd()) { // /dev/null is a char device, so isTTY is not enough
		return fmt.Errorf("not closing\n%sanswer the asks or move them to Later, or run tracks close --yes %s to carry them to %s", list.String(), name, inboxPath())
	}
	fmt.Fprint(out, list.String())
	fmt.Fprint(out, "Close anyway and carry them to the inbox? [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
		return fmt.Errorf("not closing %s", name)
	}
	return nil
}

// archiveLeftovers lists what closed tracks left behind: asks closed unanswered and Later
// lines, from the store.
func archiveLeftovers(out io.Writer) error {
	if !storeOn() {
		fmt.Fprintln(out, "leftovers need tracks.db")
		return nil
	}
	n := 0
	err := withStore(func(st *store.Store) error {
		ts, err := st.ClosedTracks()
		if err != nil {
			return err
		}
		for _, t := range ts {
			asks, err := st.AsksClosedWith(t.Name)
			if err != nil {
				return err
			}
			day := t.ClosedAt.Local().Format(closeDateLayout)
			for _, a := range asks {
				if !isCloseAsk(a.Text) {
					fmt.Fprintf(out, "%s  %s  ask: %s\n", day, t.Name, flat(a.Text))
					n++
				}
			}
			for _, l := range laterLines(t.Later) {
				fmt.Fprintf(out, "%s  %s  later: %s\n", day, t.Name, flat(l))
				n++
			}
		}
		return nil
	})
	if err == nil {
		fmt.Fprintf(out, "%s left behind\n", plural(n, "item"))
	}
	return err
}
