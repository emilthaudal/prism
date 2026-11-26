package tracks

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	needBlocked = iota // a permission prompt or a question waits
	needError          // the agent stopped on an error
	needAsk            // the card has asks
	needDone           // the agent finished a turn
)

const nothingNeedsYou = "Nothing needs you"

// needItem is one track that needs Emil, and why.
type needItem struct {
	card  *Card
	level int
	why   string
	at    time.Time // since when
}

func groupRank(c *Card) int {
	switch groupOf(c) {
	case "focus":
		return 0
	case "background":
		return 1
	}
	return 2
}

func cardUpdated(c *Card) time.Time { return metaTime(c, "updated") }

// cardChanged is the card's updated: stamp when it has one, else its file's mtime.
func cardChanged(c *Card) time.Time {
	if t := cardUpdated(c); !t.IsZero() {
		return t
	}
	return c.Mtime
}

// cardAnswered is when `tracks hook` saw Emil type into the track's session while it had asks.
func cardAnswered(c *Card) time.Time { return metaTime(c, "answered") }

// answeredFor is how long an answered: stamp counts without a Stop to clear it.
const answeredFor = 30 * time.Minute

// answeredPair is how far apart the pane's record of the asks sent and the hook's answered:
// stamp may be and still describe the same answer.
const answeredPair = 2 * time.Minute

// cardAnsweredAt is when the track was answered and whether that still holds: the stamp is
// later than updated: (writing answered: changes the file too, so it is weighed against the
// card's own content stamp) and recent, since Stop does not fire on Esc, API errors or a killed pane.
func cardAnsweredAt(c *Card, now time.Time) (time.Time, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	a := cardAnswered(c)
	return a, a.After(cardUpdated(c)) && now.Sub(a) < answeredFor
}

// askedValue is the answered_asks: value: when the pane sent the reply and which asks it accepted.
// A free reply records no numbers, which also wipes those of an earlier answer.
func askedValue(asks []int, at time.Time) string {
	if len(asks) == 0 {
		return ""
	}
	ns := make([]string, len(asks))
	for i, n := range asks {
		ns[i] = strconv.Itoa(n)
	}
	return at.UTC().Format(time.RFC3339) + " " + strings.Join(ns, ",")
}

// cardAnsweredAsks is the ask numbers the pane sent for the current answer, nil when it
// recorded none or the record belongs to an older answer.
func cardAnsweredAsks(c *Card) []int {
	stamp, list, ok := strings.Cut(strings.TrimSpace(c.Meta["answered_asks"]), " ")
	sent, err := time.Parse(time.RFC3339, stamp)
	if !ok || err != nil || sent.Sub(cardAnswered(c)).Abs() > answeredPair {
		return nil
	}
	var out []int
	for _, f := range strings.Split(list, ",") {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func metaTime(c *Card, key string) time.Time {
	v := strings.TrimSpace(c.Meta[key])
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// needsOrder is the one ordering of the tracks that need Emil, used by the pane's tab and by
// `tracks next`: blocked first, then error, then asks, then done; within each, focus before background
// before waiting, then the one waiting longest. Parked tracks never need him. A thread needs him only when blocked or in error; its asks are for its lead.
func needsOrder(cards []*Card, states map[string]agentState, evs []Event, now time.Time) []needItem {
	var out []needItem
	threads := threadParents(cards)
	for _, c := range cards {
		if c.mode() == "parked" {
			continue
		}
		lead, thread := threads[c.Name]
		sess := c.Meta["rex_session"]
		s, ok := states[sess]
		v := agentView(s, ok, sess, evs, now)
		it := needItem{card: c, at: cardUpdated(c)}
		asks := c.asks()
		if thread { // its asks are for its lead; only a prompt or an error only Emil can clear counts
			asks = 0
			if v.kind != "blocked" && v.kind != "error" {
				continue
			}
		}
		switch {
		case v.kind == "blocked":
			it.level, it.why = needBlocked, v.label
			if asks > 0 {
				it.why += " · " + plural(asks, "ask")
			}
			if ok && !s.At.IsZero() {
				it.at = s.At
			}
		case v.kind == "error":
			it.level, it.why = needError, v.label
			if asks > 0 {
				it.why += " · " + plural(asks, "ask")
			}
			if ok && !s.At.IsZero() {
				it.at = s.At
			}
		case asks > 0 && v.kind != "working":
			it.level, it.why = needAsk, plural(asks, "ask")
		case v.kind == "done":
			it.level, it.why = needDone, "done"
			if ok && !s.At.IsZero() {
				it.at = s.At
			}
		default:
			continue
		}
		if thread {
			it.why += " · thread of " + lead
		}
		if it.at.IsZero() {
			it.at = now
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b needItem) int {
		if a.level != b.level {
			return a.level - b.level
		}
		if ga, gb := groupRank(a.card), groupRank(b.card); ga != gb {
			return ga - gb
		}
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return strings.Compare(a.card.Name, b.card.Name)
	})
	return out
}

// stepNeed is the track tab (d = 1) or shift+tab (d = -1) goes to from the current one: the
// neighbour in the needs-you order, wrapping. From a track that does not need you, tab goes to
// the first and shift+tab to the last. It returns "" when nothing needs you.
func stepNeed(items []needItem, cur string, d int) string {
	if len(items) == 0 {
		return ""
	}
	i := slices.IndexFunc(items, func(it needItem) bool { return it.card.Name == cur })
	switch {
	case i < 0 && d > 0:
		i = 0
	case i < 0:
		i = len(items) - 1
	default:
		i = ((i+d)%len(items) + len(items)) % len(items)
	}
	return items[i].card.Name
}

// fetchStates asks Rex once for every session on the cards, as the pane does on a tick.
func fetchStates(cards []*Card, evs []Event) (map[string]agentState, map[string]string) {
	t := newRexTracker()
	seen := map[string]bool{}
	var sessions []string
	for _, c := range cards {
		if s := c.Meta["rex_session"]; s != "" && !seen[s] {
			seen[s] = true
			sessions = append(sessions, s)
		}
	}
	if cmd := t.cmd(time.Now(), sessions, evs); cmd != nil {
		if msg, ok := cmd().(rexMsg); ok {
			t.apply(msg)
		}
	}
	return t.states, t.blocks
}

// nextCmd jumps to the first track that needs Emil, as enter does, and prints where it went.
func nextCmd(args []string, out, errw io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: tracks next")
	}
	cards, evs, now := loadCards(), readEvents(), time.Now()
	states, blocks := fetchStates(cards, evs)
	items := needsOrder(cards, states, evs, now)
	if len(items) == 0 {
		fmt.Fprintln(out, nothingNeedsYou)
		return nil
	}
	// The first track Rex can switch to wins: one without a session, with a dead or ambiguous
	// session, or whose jump fails is skipped, so a round never sticks on it.
	var firstFail needItem
	var failNote string
	for _, it := range items {
		c := it.card
		sess := c.Meta["rex_session"]
		if sess == "" {
			continue
		}
		block := blocks[sess]
		if block == "" {
			block = agentBlock(evs, sess)
		}
		note := selectSession(sess, block)
		if !jumped(note) {
			if failNote == "" {
				firstFail, failNote = it, note
			}
			continue
		}
		fmt.Fprintf(out, "→ %s · %s\n", c.Name, it.why)
		if err := appendEvent(Event{At: time.Now().UTC(), Event: "open", RexSession: sess, Track: c.Name, Cwd: c.Meta["dir"]}); err != nil {
			note = "could not log open: " + err.Error()
		}
		if note != "" {
			fmt.Fprintln(errw, "tracks:", note)
		}
		return nil
	}
	if failNote == "" {
		firstFail, failNote = items[0], "no rex_session on the card: not switching"
	}
	fmt.Fprintf(out, "→ %s · %s\n", firstFail.card.Name, firstFail.why)
	return errors.New(failNote)
}

// jumped is whether selectSession's note means Rex switched to the session. Not knowing or
// not focusing the agent's block still counts: the session is selected, so the round stops
// there instead of moving the view a second time.
func jumped(note string) bool {
	return note == "" || strings.HasPrefix(note, "agent's block not known") || strings.HasPrefix(note, "could not focus the agent's block")
}
