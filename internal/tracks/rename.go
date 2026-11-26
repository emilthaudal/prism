package tracks

import (
	"bufio"
	"bytes"
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

// renameEvent is logged when a track is renamed: Track is the new name, Kind the old one.
const renameEvent = "rename"

// beforeTake runs between writing the new card and taking the old one; tests write there.
var beforeTake = func() {}

// renameTrack gives a live track a new name. The card moves to NEW.md without replacing
// anything and its name: follows; a rename event in the attention log carries the track's
// history over; the Rex session is relabelled only while its label is still the old name.
// Once the card has moved, a later failure is a note, never an error: the track is renamed.
func renameTrack(old, name string, now time.Time) (notes []string, err error) {
	switch {
	case !validName.MatchString(old):
		return nil, fmt.Errorf("not a track name: %q", old)
	case !validName.MatchString(name):
		return nil, fmt.Errorf("not a valid track name: %q (letters, digits, . _ -, starting with a letter or digit)", name)
	case old == name:
		return nil, fmt.Errorf("%s already has that name", old)
	}
	var meta map[string]string
	var text string
	// Under the cards lock, so a tracks card write waits and then finds the rename event.
	err = errSwitched
	if !storeOn() {
		err = withFileLock(func() error {
			var err error
			text, err = moveCard(old, name, now, &notes)
			return err
		})
	}
	switch {
	case errors.Is(err, errSwitched):
		c, err := renameStore(old, name)
		if err != nil {
			return nil, err
		}
		meta = c.Meta
	case err != nil:
		return nil, err
	default:
		meta = parseCard(name, text).Meta
	}
	if meta["parent"] != "" {
		if err := moveThreadSettings(old, name); err != nil {
			notes = append(notes, "could not move its Stop hook settings: "+err.Error())
		}
	}
	if sess := meta["rex_session"]; sess != "" {
		if note := relabelSession(sess, old, name); note != "" {
			notes = append(notes, note)
		}
	}
	return append(notes, retitleClaude(meta["claude_session"], name)...), nil
}

// moveCard moves old's card to name's and logs the rename; it returns the new card's text.
// A writer that skips the lock (an agent writing by hand) and lands mid-move still reaches
// the new card.
func moveCard(old, name string, now time.Time, notes *[]string) (string, error) {
	src, dst := cardPath(old), cardPath(name)
	b, err := os.ReadFile(src)
	if err != nil {
		if a := archivedCard(old); a != "" {
			return "", fmt.Errorf("track %s is closed (%s): a closed track is not renamed", old, a)
		}
		return "", fmt.Errorf("no card for %q", old)
	}
	// A case-insensitive disk opens OLD.md for old.md; the rename event, the Rex label and
	// the checks are keyed by the exact name, so only that is accepted.
	if n := parseCard(old, string(b)).Meta["name"]; !exactCardFile(old) || n != "" && n != old {
		return "", fmt.Errorf("no card named exactly %q (names are case-sensitive)", old)
	}
	if a := archivedCard(name); a != "" {
		return "", fmt.Errorf("%s is the name of a closed track (%s): pick another", name, a)
	}
	if to := renamedAway(name); to != "" {
		return "", fmt.Errorf("%s was the name of a track renamed to %s: pick another", name, to)
	}
	text, err := renamedText(old, name, string(b))
	if err != nil {
		return "", err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(src); err == nil {
		mode = st.Mode().Perm()
	}
	if err := linkNew(dst, []byte(text), mode); err != nil {
		if errors.Is(err, errExists) {
			return "", fmt.Errorf("a track named %s exists", name)
		}
		return "", err
	}
	beforeTake()
	// Take the old card out of reach in one step. Whatever was written to it since it was
	// read goes to the new card; a writer that comes later finds no card to write.
	taken, err := takeFile(src)
	if err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("%s's card moved meanwhile (closed or renamed?), so it was not renamed: %w", old, err)
	}
	if latest, err := os.ReadFile(taken); err != nil || !bytes.Equal(latest, b) {
		if err == nil {
			text, err = renamedText(old, name, string(latest))
		}
		if err == nil {
			err = writeAtomic(dst, []byte(text))
		}
		if err != nil {
			os.Remove(dst)
			if rerr := os.Rename(taken, src); rerr != nil {
				return "", fmt.Errorf("%s changed during the rename and could not be put back: it is at %s: %w", old, taken, rerr)
			}
			return "", fmt.Errorf("%s changed during the rename, so it was not renamed: %w", old, err)
		}
	}
	os.Remove(taken)
	if err := appendEvent(Event{At: now.UTC(), Event: renameEvent, Track: name, Kind: old}); err != nil {
		*notes = append(*notes, "could not log the rename, so the pane loses its place and history stays under "+old+": "+err.Error())
	}
	return text, nil
}

// renamedText is the card text with name: set to name, added after the opening --- when the
// card has no name: line.
func renamedText(old, name, text string) (string, error) {
	if t, err := setMetaText(old, text, "name", name); err == nil {
		return t, nil
	}
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimSpace(first) != "---" {
		return "", fmt.Errorf("card %s has no frontmatter", old)
	}
	return first + "\nname: " + name + trailingCR(first) + "\n" + rest, nil
}

// takeFile moves path to a hidden temp name next to it and returns that name.
func takeFile(path string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rename-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	if err := os.Rename(path, tmp.Name()); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// relabelSession renames the track's Rex session to name when its label is still old. A
// session labelled anything else is Emil's or another track's, and is left alone.
func relabelSession(sess, old, name string) string {
	id, label, err := resolveSession(sess)
	switch {
	case err != nil:
		return "renamed, but could not find the Rex session " + sess + " to relabel it: " + err.Error()
	case label != old:
		return fmt.Sprintf("the Rex session is labelled %q, not %s, so its label was left as it is", label, old)
	}
	if _, err := run("", "rex", "session", "rename", id, name); err != nil {
		return "renamed, but could not relabel the Rex session " + id + ": " + err.Error()
	}
	return ""
}

// followRenames gives every event the name its track has now: a rename event makes the
// events before it under the old name read as the new one. The log itself is never rewritten.
func followRenames(evs []Event) {
	now := map[string]string{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := &evs[i]
		if n, ok := now[e.Track]; ok {
			e.Track = n
		}
		if e.Event == renameEvent && e.Kind != "" {
			now[e.Kind] = e.Track
		}
	}
}

// exactCardFile is whether cards/ holds a file named exactly name.md.
func exactCardFile(name string) bool {
	entries, err := os.ReadDir(filepath.Dir(cardPath(name)))
	return err == nil && slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == name+".md" })
}

// retired maps each name a track was renamed away from, in lowercase, to its current name.
// Case does not count: the disk may not tell OLD.md from old.md.
type retired map[string]string

// loadRetired reads only the log's rename events.
func loadRetired() retired {
	if storeOn() {
		return storeRetired()
	}
	r := retired{}
	f, err := os.Open(logPath())
	if err != nil {
		return r
	}
	defer f.Close()
	var evs []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if bytes.Contains(sc.Bytes(), []byte(`"event":"`+renameEvent+`"`)) && json.Unmarshal(sc.Bytes(), &e) == nil {
			evs = append(evs, e)
		}
	}
	followRenames(evs)
	for _, e := range evs {
		if e.Kind != "" && !strings.EqualFold(e.Kind, e.Track) {
			r[strings.ToLower(e.Kind)] = e.Track
		}
	}
	return r
}

// to is the name a track called name was renamed to, or "" when none was.
func (r retired) to(name string) string { return r[strings.ToLower(name)] }

// renamedAway is the name a track called name was renamed to, or "" when none was.
func renamedAway(name string) string { return loadRetired().to(name) }

// renamedTo is the name the track called name was last renamed to, or name.
func renamedTo(evs []Event, name string) string {
	for _, e := range slices.Backward(evs) {
		if e.Event == renameEvent && strings.EqualFold(e.Kind, name) {
			return e.Track
		}
	}
	return name
}

func renameCmd(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: tracks rename NAME NEW")
	}
	notes, err := renameTrack(args[0], args[1], time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Renamed %s to %s: %s\n", args[0], args[1], cardPath(args[1]))
	for _, n := range notes {
		fmt.Fprintln(out, n)
	}
	return nil
}
