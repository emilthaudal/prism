package tracks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const lsSchema = 1

type lsDoc struct {
	Schema      int       `json:"schema"`
	GeneratedAt string    `json:"generated_at"`
	Tracks      []lsTrack `json:"tracks"`
}

// lsTrack always has every key, so a strict decoder reads a broken card like any other:
// zero values plus error.
type lsTrack struct {
	Name  string  `json:"name"`
	Error *string `json:"error"`
	Mode  string  `json:"mode"`
	// ShownMode is the mode the track is shown in: waiting is computed (EMI-423).
	ShownMode string `json:"shown_mode"`
	// Rank, Today and BlockedBy are Emil's and the desk's portfolio: the order within the mode,
	// what done today means for a focus track, and the tracks this one waits on.
	Rank      *int     `json:"rank"`
	Today     string   `json:"today"`
	BlockedBy []string `json:"blocked_by"`
	Tier      *int     `json:"tier"`
	Role      *string  `json:"role"`
	Parent    *string  `json:"parent"`
	// Verified is a thread's last tracks verify result: "pass|fail RFC3339: what ran or why".
	Verified   *string  `json:"verified"`
	Dir        string   `json:"dir"`
	RexSession string   `json:"rex_session"`
	WaitingOn  string   `json:"waiting_on"`
	Check      string   `json:"check"`
	Links      []lsLink `json:"links"`
	Updated    *string  `json:"updated"`
	// AnsweredAt is when Emil last typed into the track's session, set only while that is
	// later than the card's updated: stamp: the agent is working on his answer and has not
	// updated the card yet. It is not compared with Updated, which is the file's change time.
	AnsweredAt *string `json:"answered_at"`
	Goal       string  `json:"goal"`
	Where      string  `json:"where"`
	Asks       []lsAsk `json:"asks"`
}

type lsLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type lsAsk struct {
	Number  int     `json:"number"`
	Text    string  `json:"text"`
	IsCheck bool    `json:"is_check"`
	Since   *string `json:"since"`
}

// cardError is why the text is not a card: no frontmatter, or frontmatter that never closes.
func cardError(text string) string {
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return "no frontmatter: the card must start with ---"
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			return ""
		}
	}
	return "frontmatter is not closed with ---"
}

func str(s string) *string { return &s }

func timeStr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	return str(t.Format(time.RFC3339))
}

// lsTrackOf builds the entry for a card. updated, and an ask's since (the attention log records
// no asks), is when the card file last changed, so it holds without the agent stamping anything.
func lsTrackOf(c *Card, changed time.Time) lsTrack {
	t := lsTrack{
		Name: c.Name, Mode: lsMode(c), ShownMode: effectiveMode(c, c.Working), Dir: c.Meta["dir"], RexSession: c.Meta["rex_session"],
		WaitingOn: c.Meta["waiting_on"], Check: c.Meta["check"], Links: []lsLink{},
		Updated: timeStr(changed), Goal: c.Sections["Goal"], Where: c.firstLine("Where it is"), Asks: []lsAsk{},
		Today: c.Meta["today"], BlockedBy: c.blockedBy(),
	}
	if r := c.rank(); r > 0 {
		t.Rank = &r
	}
	if a, ok := cardAnsweredAt(c, time.Time{}); ok {
		t.AnsweredAt = timeStr(a)
	}
	if n, err := strconv.Atoi(c.tier()); err == nil {
		t.Tier = &n
	}
	if r := c.Meta["role"]; r != "" {
		t.Role = &r
	}
	if p := c.Meta["parent"]; p != "" {
		t.Parent = &p
	}
	if v := c.Meta[verifiedKey]; v != "" {
		t.Verified = &v
	}
	for _, l := range c.links() {
		t.Links = append(t.Links, lsLink{l.label, l.url})
	}
	for _, a := range c.askList() {
		t.Asks = append(t.Asks, lsAsk{a.n, a.text, isCheckAsk(a.text), timeStr(changed)})
	}
	return t
}

// lsMode is the pane's grouping with parked kept: focus, waiting or parked, else background.
func lsMode(c *Card) string {
	if m := c.mode(); m == "focus" || m == "waiting" || m == "parked" {
		return m
	}
	return "background"
}

func brokenTrack(name, msg string) lsTrack {
	return lsTrack{Name: name, Error: &msg, Links: []lsLink{}, Asks: []lsAsk{}, BlockedBy: []string{}}
}

// loadAllCards reads every card in the cards folder, not archive/; a card that cannot be read
// or parsed comes back as an entry with an error instead of being dropped. A card that
// vanishes between the listing and the read was closed or renamed, and is skipped.
func loadAllCards() []lsTrack {
	if storeOn() {
		return storeLs()
	}
	return loadAllCardsFile()
}

func loadAllCardsFile() []lsTrack {
	files, _ := filepath.Glob(filepath.Join(home(), "cards", "*.md"))
	slices.Sort(files)
	out := []lsTrack{}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") {
			continue
		}
		name := strings.TrimSuffix(base, ".md")
		b, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			out = append(out, brokenTrack(name, err.Error()))
			continue
		}
		if msg := cardError(string(b)); msg != "" {
			out = append(out, brokenTrack(name, msg))
			continue
		}
		c := parseCard(name, string(b))
		changed := cardUpdated(c)
		if st, err := os.Stat(f); err == nil {
			changed = st.ModTime().UTC()
		}
		out = append(out, lsTrackOf(c, changed))
	}
	return out
}

func lsCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("ls", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "print one JSON document")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return errors.New("usage: tracks ls [--json]")
	}
	tracks := loadAllCards()
	if *asJSON {
		b, err := json.MarshalIndent(lsDoc{lsSchema, time.Now().UTC().Format(time.RFC3339), tracks}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODE\tASKS\tUPDATED")
	now := time.Now()
	for _, t := range tracks {
		if t.Error != nil {
			fmt.Fprintf(tw, "%s\t-\t-\terror: %s\n", t.Name, *t.Error)
			continue
		}
		updated := "-"
		if t.Updated != nil {
			if at, err := time.Parse(time.RFC3339, *t.Updated); err == nil {
				updated = shortDur(now.Sub(at)) + " ago"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", t.Name, t.Mode, len(t.Asks), updated)
	}
	return tw.Flush()
}
