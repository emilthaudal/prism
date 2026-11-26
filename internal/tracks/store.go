package tracks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Card struct {
	Name     string
	Meta     map[string]string
	Sections map[string]string
	Mtime    time.Time // the card file's last change; zero for a card parsed from text
	askIDs   []int64   // the asks' ids in tracks.db, in order; nil for a card file
	// Working is whether a session is working on the track now. It is not on the card: the
	// loaders set it, and effectiveMode reads it.
	Working bool
}

func home() string {
	h := os.Getenv("TRACKS_HOME")
	if h == "" {
		u, _ := os.UserHomeDir()
		h = filepath.Join(u, ".local", "state", "tracks")
	}
	for _, r := range forbiddenHomes {
		if c := filepath.Clean(h); c == r || strings.HasPrefix(c, r+string(filepath.Separator)) {
			panic("tracks: a test reached the real state folder " + h)
		}
	}
	return h
}

// forbiddenHomes are the real state folders while tests run, set by TestMain; home() panics on
// them, so no test can read or write the real tracks.db.
var forbiddenHomes []string

func cardPath(name string) string { return filepath.Join(home(), "cards", name+".md") }
func logPath() string             { return filepath.Join(home(), "attention.jsonl") }
func archiveDir() string          { return filepath.Join(home(), "cards", "archive") }

var comment = regexp.MustCompile(`\s+#\s`)

func parseCard(name, text string) *Card {
	c := &Card{Name: name, Meta: map[string]string{}, Sections: map[string]string{}}
	lines := strings.Split(text, "\n")
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i = 1; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
			k, v, ok := strings.Cut(lines[i], ":")
			if !ok {
				continue
			}
			// A comment is whitespace, "#", whitespace; "PR #42" and "C#" stay intact.
			if loc := comment.FindStringIndex(v); loc != nil {
				v = v[:loc[0]]
			}
			c.Meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
		i++
	}
	var sec string
	var body []string
	flush := func() {
		if sec != "" {
			c.Sections[sec] = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	for ; i < len(lines); i++ {
		if h, ok := strings.CutPrefix(lines[i], "## "); ok {
			flush()
			sec, body = strings.TrimSpace(h), nil
		} else {
			body = append(body, lines[i])
		}
	}
	flush()
	return c
}

func loadCard(name string) (*Card, error) {
	if storeOn() {
		return storeCard(name)
	}
	b, err := os.ReadFile(cardPath(name))
	if err != nil {
		return nil, err
	}
	c := parseCard(name, string(b))
	if st, err := os.Stat(cardPath(name)); err == nil {
		c.Mtime = st.ModTime()
	}
	return c, nil
}

func loadCards() []*Card {
	if storeOn() {
		return storeCards()
	}
	files, _ := filepath.Glob(filepath.Join(home(), "cards", "*.md"))
	var cards []*Card
	for _, f := range files {
		if c, err := loadCard(strings.TrimSuffix(filepath.Base(f), ".md")); err == nil {
			cards = append(cards, c)
		}
	}
	return cards
}

func (c *Card) mode() string { return c.Meta["mode"] }

// rank is the card's place within its mode, lower first; 0 is unranked.
func (c *Card) rank() int {
	n, err := strconv.Atoi(c.Meta["rank"])
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// blockedBy is the tracks the card waits on, never nil.
func (c *Card) blockedBy() []string {
	return append([]string{}, splitNames(c.Meta["blocked_by"])...)
}

// tier is "1", "2" or "3"; anything else, or a missing tier, is "".
func (c *Card) tier() string {
	t := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c.Meta["tier"])), "T")
	if t == "1" || t == "2" || t == "3" {
		return t
	}
	return ""
}

func (c *Card) asks() int { return len(c.askList()) }

// ask is one numbered line under Needs me.
type ask struct {
	n    int
	text string // after the number and its . or )
	id   int64  // the ask's row in tracks.db; 0 for a card file
}

func (c *Card) askList() []ask {
	var out []ask
	for _, l := range strings.Split(c.Sections["Needs me"], "\n") {
		if a, ok := parseAsk(l); ok {
			out = append(out, a)
		}
	}
	if len(c.askIDs) == len(out) {
		for i := range out {
			out[i].id = c.askIDs[i]
		}
	}
	return out
}

func parseAsk(l string) (ask, bool) {
	t := strings.TrimLeft(l, " \t")
	d := strings.IndexFunc(t, func(r rune) bool { return r < '0' || r > '9' })
	if d > 0 && d < len(t) && (t[d] == '.' || t[d] == ')') {
		n, err := strconv.Atoi(t[:d])
		return ask{n: n, text: strings.TrimSpace(t[d+1:])}, err == nil
	}
	return ask{}, false
}

func (c *Card) askN(n int) (ask, bool) {
	for _, a := range c.askList() {
		if a.n == n {
			return a, true
		}
	}
	return ask{}, false
}

func (c *Card) firstLine(sec string) string {
	for _, l := range strings.Split(c.Sections[sec], "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

const (
	cardLockPoll = 10 * time.Millisecond
	// Claude Code kills a hook after about 2 seconds; the hook writes the card up to twice, so
	// each write gives up on the lock after half a second.
	hookLockWait = 500 * time.Millisecond
	// The hook's event waits longer: only tracks migrate holds the events lock for more than an
	// append, and it lets go of the cards lock at the same moment, so the card writes then go fast.
	hookEventWait = 1000 * time.Millisecond
)

// cardLockWait is how long a writer waits for the cards lock before it fails.
var cardLockWait = 5 * time.Second

// withLockWait sets how long writers wait for the cards lock and returns the undo.
func withLockWait(d time.Duration) func() {
	prev := cardLockWait
	cardLockWait = d
	return func() { cardLockWait = prev }
}

func cardsLockPath() string  { return filepath.Join(home(), "cards.lock") }
func eventsLockPath() string { return filepath.Join(home(), "events.lock") }

// waitingForLock runs when a writer first finds a lock held; tests let the holder go on there.
var waitingForLock = func(path string) {}

// withCardsLock runs fn while holding the lock every card writer takes, so one writer's
// read-modify-write never drops another's change. It is not reentrant: fn must not take it again.
func withCardsLock(fn func() error) error { return withFlock(cardsLockPath(), "the cards", fn) }

// withEventsLock runs fn while holding the lock every append to attention.jsonl takes.
func withEventsLock(fn func() error) error {
	return withFlock(eventsLockPath(), "the attention log", fn)
}

func withFlock(path, what string, fn func() error) error {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(cardLockWait)
	for waited := false; ; waited = true {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if !waited {
			waitingForLock(path)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s stayed locked for %v (%s); try again", what, cardLockWait, path)
		}
		time.Sleep(cardLockPoll)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// errSwitched is a file writer that got its lock after tracks migrate made tracks.db the
// source of truth: nothing was written, and the caller does its write on the database instead.
var errSwitched = errors.New("tracks.db holds the tracks: card files are views, written only from the database")

// withFileLock is withCardsLock for a writer of the files. tracks migrate holds the lock from
// its import through the switch, so the files are the source for as long as fn runs only if
// tracks.db is still missing once the lock is held.
func withFileLock(fn func() error) error {
	return withCardsLock(func() error {
		if storeOn() {
			return errSwitched
		}
		return fn()
	})
}

// withCardLock re-reads the card under the cards lock, hands its text to fn and writes back
// what fn returns, atomically. A card that is gone stays gone; unchanged text is not written.
func withCardLock(name string, fn func(text string) (string, error)) error {
	return withFileLock(func() error { return updateCard(name, fn) })
}

// updateCard is withCardLock for a caller that already holds the cards lock.
func updateCard(name string, fn func(text string) (string, error)) error {
	b, err := os.ReadFile(cardPath(name))
	if err != nil {
		return err
	}
	text, err := fn(string(b))
	if err != nil || text == string(b) {
		return err
	}
	return writeExisting(cardPath(name), []byte(text))
}

// writeExisting replaces a file that is still there; a card closed in the meantime stays closed.
func writeExisting(path string, data []byte) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

type Event struct {
	At         time.Time `json:"at"`
	Event      string    `json:"event"`
	RexSession string    `json:"rex_session"`
	RexBlock   string    `json:"rex_block,omitempty"`
	Track      string    `json:"track"`
	Cwd        string    `json:"cwd"`
	Len        int       `json:"len"`
	Kind       string    `json:"kind"`
}

func appendEvent(e Event) error {
	if storeOn() {
		return storeEvent(e)
	}
	if err := appendEventFile(e); !errors.Is(err, errSwitched) {
		return err
	}
	return storeEvent(e)
}

// appendEventFile appends e to attention.jsonl under the events lock, which tracks migrate
// holds while it imports the log and renames it away. It returns errSwitched, having written
// nothing, once tracks.db holds the events.
func appendEventFile(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return withEventsLock(func() error {
		if storeOn() {
			return errSwitched
		}
		f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(append(b, '\n'))
		return err
	})
}

func readEvents() []Event {
	if storeOn() {
		return storeEvents()
	}
	return readEventsFile()
}

// readEventsFile is the attention log with each event under its track's current name.
func readEventsFile() []Event {
	f, err := os.Open(logPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var evs []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			evs = append(evs, e)
		}
	}
	followRenames(evs)
	return evs
}
