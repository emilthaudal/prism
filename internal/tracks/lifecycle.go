package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	doneMark        = "✓ "
	closeDateLayout = "2006-01-02"
	maxArchiveTries = 1000
)

// closeCard returns the card text with closed: set and "## Where it is" renamed "## Outcome".
func closeCard(text string, now time.Time) string {
	lines := strings.Split(text, "\n")
	end := -1
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
	}
	stamp := now.Format(time.RFC3339)
	if end > 0 {
		set := false
		for i := 1; i < end; i++ {
			if k, _, ok := strings.Cut(lines[i], ":"); ok && strings.TrimSpace(k) == "closed" {
				lines[i], set = "closed: "+stamp+trailingCR(lines[i]), true
				break
			}
		}
		if !set {
			lines = slices.Insert(lines, end, "closed: "+stamp+trailingCR(lines[end]))
			end++
		}
	}
	for i := max(end, 0); i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "## Where it is" {
			lines[i] = "## Outcome" + trailingCR(lines[i])
			break
		}
	}
	return strings.Join(lines, "\n")
}

func trailingCR(l string) string {
	if strings.HasSuffix(l, "\r") {
		return "\r"
	}
	return ""
}

// linkArchive writes data to dir/DATE-NAME.md, or DATE-NAME-2.md and up when that exists. It
// hard-links a finished temp file into place, so an existing archive file is never replaced.
func linkArchive(dir, name string, now time.Time, data []byte, mode os.FileMode) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		return "", err
	}
	base := now.Format(closeDateLayout) + "-" + name
	for n := 1; n <= maxArchiveTries; n++ {
		dest := filepath.Join(dir, base+".md")
		if n > 1 {
			dest = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, n))
		}
		switch err := os.Link(tmp.Name(), dest); {
		case err == nil:
			return dest, nil
		case !os.IsExist(err):
			return "", err
		}
	}
	return "", fmt.Errorf("no free archive name for %s", base)
}

// closeTrack archives the card, then ends its Rex session (see endSession; self is the
// caller's own pane). note says what happened to the session; Rex never blocks closing.
func closeTrack(name string, now time.Time, self string) (dest, note string, err error) {
	if !validName.MatchString(name) {
		return "", "", fmt.Errorf("not a track name: %q", name)
	}
	var sess string
	var lostAsks, lostLater []string // EMI-296: carried to inbox.md once the card closes
	if c, cerr := loadCard(name); cerr == nil {
		lostAsks, lostLater = leftovers(c)
	}
	defer func() {
		if err == nil {
			if cerr := carryOver(name, lostAsks, lostLater, now); cerr != nil {
				note += " (could not write inbox.md: " + cerr.Error() + ")"
			}
		}
	}()
	if storeOn() {
		dest, sess, err = closeStore(name, now)
	} else if dest, sess, err = closeFiles(name, now); errors.Is(err, errSwitched) {
		dest, sess, err = closeStore(name, now)
	}
	if err != nil {
		return "", "", err
	}
	dropLoopFiles(name)
	note = "Closed " + name
	if sess != "" {
		ended, why, rerr := endSession(sess, name, self)
		note = closeNote(name, ended, why, rerr)
	}
	return dest, note, nil
}

// closeFiles moves the card to the archive under the cards lock; it returns the archive file
// and the card's Rex session.
func closeFiles(name string, now time.Time) (dest, sess string, err error) {
	src := cardPath(name)
	err = withFileLock(func() error {
		b, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("no card for %q", name)
		}
		mode := os.FileMode(0o644)
		if st, err := os.Stat(src); err == nil {
			mode = st.Mode().Perm()
		}
		sess = parseCard(name, string(b)).Meta["rex_session"]
		if dest, err = linkArchive(archiveDir(), name, now, []byte(closeCard(string(b), now)), mode); err != nil {
			return err
		}
		if err := os.Remove(src); err != nil {
			os.Remove(dest)
			return err
		}
		return nil
	})
	return dest, sess, err
}

// markSessionDone prefixes the session's label with a check mark.
func markSessionDone(sess, name string) error {
	id, label, err := resolveSession(sess)
	if err != nil {
		return err
	}
	if strings.HasPrefix(label, strings.TrimSpace(doneMark)) {
		return nil
	}
	if label == "" {
		label = name
	}
	_, err = run("", "rex", "session", "rename", id, doneMark+label)
	return err
}

var (
	errSessionNone      = errors.New("session not found")
	errSessionAmbiguous = errors.New("session id matches more than one session")
)

// resolveSession finds the full id and label of a card's rex_session: an exact id, or a
// suffix (the short id `rex ls` prints) that exactly one session has.
func resolveSession(sess string) (id, label string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return resolveSessionCtx(ctx, sess)
}

func resolveSessionCtx(ctx context.Context, sess string) (id, label string, err error) {
	b, err := rexRun(ctx, "session", "ls", "--json")
	if err != nil {
		return "", "", err
	}
	out := string(b)
	var l struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		return "", "", err
	}
	found := 0
	for _, s := range l.Sessions {
		if s.ID == sess {
			return s.ID, s.Label, nil
		}
		if sess != "" && strings.HasSuffix(s.ID, sess) {
			id, label = s.ID, s.Label
			found++
		}
	}
	switch found {
	case 0:
		return "", "", fmt.Errorf("%w: %s", errSessionNone, sess)
	case 1:
		return id, label, nil
	}
	return "", "", fmt.Errorf("%w: %s", errSessionAmbiguous, sess)
}

// liveSessionFor finds the session a track's start created when the card never got its id:
// `rex new` can time out after Rex made the session. It returns the id of the one session
// labelled exactly name, "" when there is none, and an error when Rex does not answer or
// several sessions have that label.
func liveSessionFor(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	b, err := rexRun(ctx, "session", "ls", "--json")
	if err != nil {
		return "", err
	}
	var l struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return "", err
	}
	var ids []string
	for _, s := range l.Sessions {
		if s.Label == name {
			ids = append(ids, s.ID)
		}
	}
	switch len(ids) {
	case 0:
		return "", nil
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%w: %d sessions are labelled %s", errSessionAmbiguous, len(ids), name)
}

type closedCard struct {
	Name    string
	At      time.Time
	Outcome string
	File    string
}

func readArchive() []closedCard {
	if storeOn() {
		return storeArchive()
	}
	files, _ := filepath.Glob(filepath.Join(archiveDir(), "*.md"))
	var out []closedCard
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f), ".md")
		c := parseCard(base, string(b))
		at := parseClosed(c.Meta["closed"])
		if at.IsZero() {
			if st, err := os.Stat(f); err == nil {
				at = st.ModTime()
			}
		}
		name := c.Meta["name"]
		if name == "" {
			name = base
		}
		out = append(out, closedCard{name, at, c.firstLine("Outcome"), f})
	}
	slices.SortStableFunc(out, func(a, b closedCard) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return strings.Compare(b.File, a.File)
	})
	return out
}

func parseClosed(s string) time.Time {
	for _, layout := range []string{time.RFC3339, closeDateLayout} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func closedToday(now time.Time) int {
	n := 0
	y, m, d := now.Local().Date()
	for _, c := range readArchive() {
		if cy, cm, cd := c.At.Local().Date(); cy == y && cm == m && cd == d {
			n++
		}
	}
	return n
}

func closeCmd(args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "--sweep" {
		return sweepSessions(out)
	}
	if len(args) == 2 && args[0] == "--end-when-idle" { // started by a track's agent closing it
		return endWhenIdle(args[1])
	}
	yes := false
	if len(args) == 2 && args[0] == "--yes" {
		yes, args = true, args[1:]
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: tracks close [--yes] NAME | tracks close --sweep")
	}
	if c, err := loadCard(args[0]); err == nil && !yes {
		if asks, _ := leftovers(c); len(asks) > 0 {
			if err := confirmLeftovers(args[0], asks, os.Stdin, os.Stderr); err != nil {
				return err
			}
		}
	}
	var threads []string
	if storeOn() {
		cs, _ := threadsOf(args[0])
		for _, c := range cs {
			threads = append(threads, c.Name)
		}
		slices.Sort(threads)
	}
	dest, note, err := closeTrack(args[0], time.Now(), "")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s\narchived to %s\n", note, dest)
	if len(threads) > 0 {
		fmt.Fprintf(out, "left open: %s of %s: %s\n", plural(len(threads), "thread"), args[0], strings.Join(threads, ", "))
	}
	return nil
}

func archiveCmd(args []string, out io.Writer, p palette) error {
	if len(args) == 1 && args[0] == "--leftovers" {
		return archiveLeftovers(out)
	}
	fmt.Fprint(out, renderArchive(readArchive(), termWidth(), p))
	return nil
}

func renderArchive(cards []closedCard, width int, p palette) string {
	if len(cards) == 0 {
		return p.dim("no closed tracks yet") + "\n"
	}
	nw := 4
	for _, c := range cards {
		nw = max(nw, len([]rune(c.Name)))
	}
	var b strings.Builder
	for _, c := range cards {
		outcome := c.Outcome
		if outcome == "" {
			outcome = "-"
		}
		prefix := fmt.Sprintf("%s  %-*s  ", c.At.Local().Format(closeDateLayout), nw, c.Name)
		b.WriteString(p.dim(prefix) + truncate(outcome, width-len([]rune(prefix))) + "\n")
	}
	return b.String()
}
