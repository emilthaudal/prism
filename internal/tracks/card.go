package tracks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	maxWhereLines = 3 // where it stands, then at most two lines of detail
	cardUsage     = "usage: tracks card NAME < fields (where:, tier:, goal:, dir:, links:, check:, waiting_on:, role:, asks:, later:; Emil's and the desk's: rank:, today:, blocked_by:)"
)

// afterCardRead runs between tracks card reading the card and writing it; tests write there.
var afterCardRead = func() {}

var (
	errNoCard = errors.New("no card")
	// cardFields are the fields tracks card writes; where, asks and later take the lines that
	// follow. portfolioFields among them are Emil's and the desk's, like mode.
	cardFields      = []string{"where", "tier", "goal", "dir", "links", "check", "waiting_on", "role", "asks", "later", "rank", "today", "blocked_by"}
	portfolioFields = []string{"rank", "today", "blocked_by"}
	tierInput       = regexp.MustCompile(`^[Tt]?([123])(?:\s*[:.,;–—-]\s*|\s+|$)(.*)$`)
	askPrefix       = regexp.MustCompile(`^(?:[0-9]+[.)]|[-*])\s+`)
)

// cardUpdate is what an agent sent to tracks card. A nil slice or a missing key is a field
// left out, which stays as it is on the card.
type cardUpdate struct {
	meta    map[string]string // frontmatter: tier, dir, links, check, waiting_on
	tierWhy string
	goal    *string
	where   []string
	asks    []string
	later   []string
}

// splitNames reads a list of track names separated by commas or spaces.
func splitNames(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

func fieldKey(l string) (key, value string, ok bool) {
	k, v, ok := strings.Cut(l, ":")
	if !ok || !slices.Contains(cardFields, k) {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

// parseCardInput reads `key: value` lines. where:, asks: and later: also take every line after
// them up to the next field, so the input is easy to write as a heredoc.
func parseCardInput(s string) (*cardUpdate, error) {
	u := &cardUpdate{meta: map[string]string{}}
	seen := map[string]bool{}
	block := ""
	add := func(v string) {
		switch block {
		case "where":
			u.where = append(u.where, v)
		case "asks":
			u.asks = append(u.asks, askPrefix.ReplaceAllString(v, ""))
		case "later":
			u.later = append(u.later, v)
		}
	}
	for i, raw := range strings.Split(s, "\n") {
		l := strings.TrimRight(raw, " \t\r")
		if k, v, ok := fieldKey(l); ok {
			if seen[k] {
				return nil, fmt.Errorf("line %d: %s: is given twice", i+1, k)
			}
			seen[k], block = true, ""
			switch k {
			case "where", "asks", "later":
				block = k
				switch k {
				case "where":
					u.where = []string{}
				case "asks":
					u.asks = []string{}
				case "later":
					u.later = []string{}
				}
				if v != "" {
					add(v)
				}
			case "tier":
				if v == "" {
					u.meta["tier"] = ""
					break
				}
				m := tierInput.FindStringSubmatch(v)
				if m == nil {
					return nil, fmt.Errorf("line %d: tier must be 1, 2 or 3, optionally followed by why: %q", i+1, v)
				}
				u.meta["tier"], u.tierWhy = m[1], strings.TrimSpace(m[2])
			case "goal":
				u.goal = &v
			case "role":
				if v != "" && v != "lead" {
					return nil, fmt.Errorf("line %d: role must be lead, or empty to clear it: %q", i+1, v)
				}
				u.meta["role"] = v
			case "rank":
				if n, err := strconv.Atoi(v); v != "" && (err != nil || n < 1) {
					return nil, fmt.Errorf("line %d: rank must be a whole number from 1, or empty to clear it: %q", i+1, v)
				}
				u.meta["rank"] = v
			case "blocked_by":
				for _, n := range splitNames(v) {
					if !validName.MatchString(n) {
						return nil, fmt.Errorf("line %d: blocked_by takes track names separated by commas: %q", i+1, n)
					}
				}
				u.meta["blocked_by"] = strings.Join(splitNames(v), ", ")
			default:
				u.meta[k] = v
			}
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if block == "" {
			return nil, fmt.Errorf("line %d: %q is not a field; the fields are %s", i+1, t, strings.Join(cardFields, ", "))
		}
		if block == "later" {
			add(l)
		} else {
			add(t)
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("no fields on stdin; " + cardUsage)
	}
	return u, u.check()
}

func (u *cardUpdate) check() error {
	if u.where != nil && (len(u.where) == 0 || len(u.where) > maxWhereLines) {
		return fmt.Errorf("where: takes one line of where it stands and at most %d lines of detail, got %d lines", maxWhereLines-1, len(u.where))
	}
	body := slices.Concat(u.where, u.asks, u.later, []string{u.tierWhy})
	if u.goal != nil {
		body = append(body, *u.goal)
	}
	for _, l := range body {
		if strings.HasPrefix(strings.TrimSpace(l), "## ") {
			return fmt.Errorf("a line cannot start with ## (it would start a card section): %q", l)
		}
	}
	for k, v := range u.meta {
		if comment.MatchString(v) {
			return fmt.Errorf("%s: %q has \" # \", which the card reads as a comment", k, v)
		}
	}
	if d, ok := u.meta["dir"]; ok && d != "" && !cardValueOK(d) {
		return fmt.Errorf("dir: must be an absolute path: %q", d)
	}
	return nil
}

// applyCardUpdate writes the agent's fields into the card text and stamps updated:. Everything
// else stays: mode, Goal (unless goal: is given), Standing orders, rex_session, claude_*,
// answered, and every check ask under Needs me that answered says Emil has not seen yet.
func applyCardUpdate(name, text string, u *cardUpdate, answered func(string) bool, now time.Time) (string, error) {
	c := parseCard(name, text)
	var err error
	keys := make([]string, 0, len(u.meta))
	for k := range u.meta {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if text, err = upsertMetaText(name, text, k, u.meta[k]); err != nil {
			return "", err
		}
	}
	if u.goal != nil {
		text = setSection(text, "Goal", []string{*u.goal}, "Where it is", "Needs me", "Standing orders", "Later")
	}
	if u.where != nil || u.tierWhy != "" {
		where, tierLn := u.where, ""
		for _, l := range strings.Split(c.Sections["Where it is"], "\n") {
			l = strings.TrimSpace(l)
			switch {
			case tierLine.MatchString(l):
				tierLn = l
			case u.where == nil && l != "":
				where = append(where, l)
			}
		}
		if u.tierWhy != "" {
			tierLn = "Tier " + u.meta["tier"] + ": " + u.tierWhy
		}
		if tierLn != "" {
			where = append(where, tierLn)
		}
		text = setSection(text, "Where it is", where, "Needs me", "Standing orders", "Later")
	}
	if u.asks != nil {
		var lines []string
		have := map[string]bool{}
		push := func(a string) {
			if a != "" && !have[a] {
				have[a] = true
				lines = append(lines, fmt.Sprintf("%d. %s", len(lines)+1, a))
			}
		}
		for _, a := range u.asks {
			push(a)
		}
		for _, a := range c.askList() {
			if isCheckAsk(a.text) && !answered(a.text) {
				push(a.text)
			}
		}
		text = setSection(text, "Needs me", lines, "Standing orders", "Later")
	}
	if u.later != nil {
		text = setSection(text, "Later", u.later)
	}
	return upsertMetaText(name, text, "updated", now.Format(time.RFC3339))
}

// setSection replaces the body of the card's last `## title` section (the one parseCard
// reads). A card without one gets it before the first of the before headings, or at the end.
func setSection(text, title string, body []string, before ...string) string {
	lines := strings.Split(text, "\n")
	bodyStart := 0
	if strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				bodyStart = i + 1
				break
			}
		}
	}
	start := -1
	for i := bodyStart; i < len(lines); i++ {
		if h, ok := heading(lines[i]); ok && h == title {
			start = i
		}
	}
	if start >= 0 {
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if _, ok := heading(lines[i]); ok {
				end = i
				break
			}
		}
		return strings.Join(slices.Replace(lines, start+1, end, append(slices.Clone(body), "")...), "\n")
	}
	sec := append(append([]string{"## " + title}, body...), "")
	for i := bodyStart; i < len(lines); i++ {
		if h, ok := heading(lines[i]); ok && slices.Contains(before, h) {
			return strings.Join(slices.Insert(lines, i, sec...), "\n")
		}
	}
	for len(lines) > bodyStart && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > bodyStart {
		lines = append(lines, "")
	}
	return strings.Join(append(lines, sec...), "\n")
}

// resolveCard is the card a write for name goes to: name's own while it is there, or the name
// it was renamed to. closed is true when that card was archived; errNoCard when there is none.
func resolveCard(name string) (cur string, closed bool, err error) {
	cur = name
	for range 2 { // renamedAway already follows a chain of renames to its end
		if _, err := os.Stat(cardPath(cur)); err == nil {
			return cur, false, nil
		}
		if archivedCard(cur) != "" {
			return cur, true, nil
		}
		to := renamedAway(cur)
		if to == "" || to == cur {
			break
		}
		cur = to
	}
	return cur, false, errNoCard
}

func cardCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(cardUsage)
	}
	name := args[0]
	if !validName.MatchString(name) {
		return fmt.Errorf("not a track name: %q", name)
	}
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	u, err := parseCardInput(string(b))
	if err != nil {
		return err
	}
	if storeOn() {
		return cardStore(name, u, out)
	}
	if _, ok := u.meta["role"]; ok {
		return errors.New("role: needs tracks.db: run tracks migrate first")
	}
	for _, k := range portfolioFields {
		if _, ok := u.meta[k]; ok {
			return fmt.Errorf("%s: needs tracks.db: run tracks migrate first", k)
		}
	}
	var (
		cur    string
		closed bool
		asks   []ask
	)
	// The name is resolved under the cards lock, so a rename in progress is finished and logged
	// first. Only a hand-deleted card can vanish in between: look again once.
	for try := 0; ; try++ {
		err = withFileLock(func() error {
			if cur, closed, err = resolveCard(name); err != nil || closed {
				return err
			}
			answered := checkAskAnswered(cur)
			return updateCard(cur, func(text string) (string, error) {
				afterCardRead()
				t, err := applyCardUpdate(cur, text, u, answered, time.Now())
				asks = parseCard(cur, t).askList()
				return t, err
			})
		})
		if errors.Is(err, fs.ErrNotExist) && try == 0 {
			continue
		}
		if errors.Is(err, errSwitched) {
			return cardStore(name, u, out)
		}
		if errors.Is(err, errNoCard) {
			return fmt.Errorf("no card for %s: run tracks bind %s first", cur, cur)
		}
		if err != nil {
			return err
		}
		break
	}
	if cur != name {
		fmt.Fprintf(out, "renamed to %s\n", cur)
	}
	if closed {
		fmt.Fprintln(out, "closed")
		return nil
	}
	if len(asks) == 0 {
		fmt.Fprintln(out, "no asks")
	}
	for _, a := range asks {
		fmt.Fprintf(out, "%d. %s\n", a.n, a.text)
	}
	return nil
}

// bindCmd ties the calling session to the track: it makes the card when there is none, records
// $REX_SESSION on it and prints it. A closed or renamed track is reported and left alone. On
// tracks.db it refuses a track another live session holds, unless --force (EMI-427).
func bindCmd(args []string, out io.Writer) error {
	force := false
	if len(args) == 2 && args[0] == "--force" {
		force, args = true, args[1:]
	}
	if len(args) != 1 {
		return errors.New("usage: tracks bind [--force] NAME")
	}
	name := args[0]
	if !validName.MatchString(name) {
		return fmt.Errorf("not a track name: %q", name)
	}
	if storeOn() {
		return bindStore(name, force, out)
	}
	// Resolving, creating and recording the session all happen under the cards lock, so a rename
	// in progress is finished first and its old name is never made into a second card.
	var res bytes.Buffer
	err := withFileLock(func() error {
		cur, closed, err := resolveCard(name)
		switch {
		case closed || cur != name:
			if cur != name {
				fmt.Fprintf(&res, "renamed to %s\n", cur)
			}
			if closed {
				fmt.Fprintln(&res, "closed")
			}
			return nil
		case errors.Is(err, errNoCard):
			dir, _ := os.Getwd()
			if !cardValueOK(dir) {
				dir = ""
			}
			if err := createCard(name, []byte(cardText(name, defaultStartMode, "", dir, defaultGoal))); err != nil && !errors.Is(err, errExists) {
				return err
			}
		case err != nil:
			return err
		}
		if sess := os.Getenv("REX_SESSION"); sess != "" {
			err := updateCard(name, func(text string) (string, error) {
				return upsertMetaText(name, text, "rex_session", sess)
			})
			if err != nil {
				return err
			}
		}
		b, err := os.ReadFile(cardPath(name))
		res.Write(b)
		return err
	})
	if errors.Is(err, errSwitched) {
		return bindStore(name, force, out)
	}
	if err != nil {
		return err
	}
	_, err = out.Write(res.Bytes())
	return err
}
