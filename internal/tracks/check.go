package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	checkTimeout = 15 * time.Second
	yes, no      = "yes", "no"
	pending      = "pending"
	ciQuery      = `query($owner:String!,$repo:String!,$ref:String!){repository(owner:$owner,name:$repo){ref(qualifiedName:$ref){target{... on Commit{statusCheckRollup{contexts(first:100){nodes{... on CheckRun{name status conclusion} ... on StatusContext{context state}}}}}}}}}`
)

// The whole check vocabulary. Card text is agent-written, so nothing outside these
// patterns ever reaches exec, and what does match cannot start with "-".
var (
	// A PR may name its repo as owner/repo#N, which needs no git checkout in the track's dir.
	prCheck     = regexp.MustCompile(`^pr ((?:[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*#)?[0-9]{1,9}) (approved|merged|green)$`)
	ciCheck     = regexp.MustCompile(`^ci ([A-Za-z0-9_][A-Za-z0-9._-]*(?:/[A-Za-z0-9_][A-Za-z0-9._-]*)*) green$`)
	remoteOwner = regexp.MustCompile(`github\.com[:/]([A-Za-z0-9_.-]+)/`)
	passing     = []string{"SUCCESS", "NEUTRAL", "SKIPPED"}
)

type check struct{ kind, arg, want string }

// evaluator answers yes, no, or pending (not yet known).
type evaluator func(dir string, c check) (string, error)

// rollupItem is a CheckRun (name, status, conclusion) or a StatusContext (context, state).
type rollupItem struct{ Name, Status, Conclusion, Context, State string }

type checkState struct {
	Answer bool   `json:"answer"`
	Asked  string `json:"asked,omitempty"`
	// RanAt is when tracks check last got a yes or no for this check. omitzero keeps a file that
	// never had it free of a zero date; the pane reads a missing value as "unknown".
	RanAt time.Time `json:"ran_at,omitempty,omitzero"`
}

func parseCheck(s string) (check, bool) {
	if m := prCheck.FindStringSubmatch(s); m != nil {
		return check{"pr", m[1], m[2]}, true
	}
	m := ciCheck.FindStringSubmatch(s)
	if m == nil || m[1] == "HEAD" || strings.Contains(m[1], "..") || strings.HasSuffix(m[1], ".") {
		return check{}, false
	}
	for _, seg := range strings.Split(m[1], "/") {
		if strings.HasSuffix(seg, ".lock") {
			return check{}, false
		}
	}
	return check{"ci", m[1], "green"}, true
}

func (c check) ghArgs() []string {
	if c.kind == "pr" {
		if repo, n, ok := strings.Cut(c.arg, "#"); ok {
			return []string{"pr", "view", n, "--repo", repo, "--json", "state,reviewDecision,statusCheckRollup"}
		}
		return []string{"pr", "view", c.arg, "--json", "state,reviewDecision,statusCheckRollup"}
	}
	// GraphQL variables carry the branch, so it never lands in a URL or a query string.
	return []string{"api", "graphql", "-F", "owner={owner}", "-F", "repo={repo}", "-f", "ref=refs/heads/" + c.arg, "-f", "query=" + ciQuery}
}

func yesNo(b bool) string {
	if b {
		return yes
	}
	return no
}

// gate matches CircleCI approval jobs (hold, hold-x, hold_x, approve-x), which wait for a human.
var gate = regexp.MustCompile(`^(hold|approve)([-_].*)?$`)

// judgeGreen ignores approval gates. No checks reported yet (just after a push) is pending, not no.
func judgeGreen(items []rollupItem) string {
	passed, waiting := 0, 0
	for _, r := range items {
		name := r.Name + r.Context
		if gate.MatchString(name[strings.LastIndexAny(name, "/ ")+1:]) {
			continue
		}
		switch {
		case r.State == "SUCCESS" || r.State == "" && r.Status == "COMPLETED" && slices.Contains(passing, r.Conclusion):
			passed++
		case r.State == "PENDING" || r.State == "EXPECTED" || r.State == "" && r.Status != "COMPLETED":
			waiting++
		default:
			return no
		}
	}
	if waiting > 0 || passed == 0 {
		return pending
	}
	return yes
}

func (c check) answer(b []byte) (string, error) {
	if c.kind == "ci" {
		var resp struct {
			Data struct {
				Repository struct {
					Ref *struct {
						Target struct {
							StatusCheckRollup *struct{ Contexts struct{ Nodes []rollupItem } }
						}
					}
				}
			}
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return "", err
		}
		ref := resp.Data.Repository.Ref
		if ref == nil {
			return "", fmt.Errorf("no branch %q on the remote", c.arg)
		}
		if ref.Target.StatusCheckRollup == nil {
			return pending, nil
		}
		return judgeGreen(ref.Target.StatusCheckRollup.Contexts.Nodes), nil
	}
	var pr struct {
		State, ReviewDecision string
		StatusCheckRollup     []rollupItem
	}
	if err := json.Unmarshal(b, &pr); err != nil {
		return "", err
	}
	switch c.want {
	case "approved":
		return yesNo(pr.ReviewDecision == "APPROVED"), nil
	case "merged":
		return yesNo(pr.State == "MERGED"), nil
	}
	return judgeGreen(pr.StatusCheckRollup), nil
}

// ghRules reads TRACKS_GH_ACCOUNTS, comma-separated KEY=LOGIN pairs for people with more than
// one gh login: KEY is a GitHub owner (acme=me-at-acme) or, when it starts with / or ~/, a
// directory whose tracks use that login (~/Developer/acme=me-at-acme).
func ghRules() (owners map[string]string, dirs [][2]string) {
	owners = map[string]string{}
	for _, f := range envList("TRACKS_GH_ACCOUNTS") {
		k, v, ok := strings.Cut(f, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case !ok || k == "" || v == "":
		case strings.HasPrefix(k, "/"):
			dirs = append(dirs, [2]string{filepath.Clean(k), v})
		case strings.HasPrefix(k, "~/"):
			dirs = append(dirs, [2]string{filepath.Join(userHome(), k[2:]), v})
		default:
			owners[strings.ToLower(k)] = v
		}
	}
	return owners, dirs
}

// ghAccount is the login TRACKS_GH_ACCOUNTS names for the remote's owner, or "" for gh's own.
func ghAccount(remote string) string {
	m := remoteOwner.FindStringSubmatch(remote)
	if m == nil {
		return ""
	}
	owners, _ := ghRules()
	return owners[strings.ToLower(m[1])]
}

// ghAccountFor picks the login for a check: the repo it names, else the origin remote, else
// the directory dir lives in, even when dir itself is not a git repo, else the login
// TRACKS_GH_ACCOUNTS gives * (for when gh's active login is not the one most repos use).
// "" means gh's own login.
func ghAccountFor(dir string, c check, remote string) string {
	owners, dirs := ghRules()
	if repo, _, ok := strings.Cut(c.arg, "#"); ok && c.kind == "pr" {
		if a := ghAccount("github.com/" + repo); a != "" {
			return a
		}
		return owners["*"]
	}
	if a := ghAccount(remote); a != "" {
		return a
	}
	for _, d := range dirs {
		if dir == d[0] || strings.HasPrefix(dir, d[0]+string(filepath.Separator)) {
			return d[1]
		}
	}
	return owners["*"]
}

func withStderr(err error) error {
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

// ghEval picks the gh account per call through GH_TOKEN, since gh auth switch would
// change the account for every other session on the machine.
func ghEval(dir string, c check) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("dir %q is not an absolute path", dir)
	}
	remote, rerr := run(dir, "git", "remote", "get-url", "origin")
	if rerr != nil && !strings.Contains(c.arg, "#") && c.kind == "pr" {
		return "", fmt.Errorf("dir %s is not a git repo with an origin: set dir: to the repo, or write the check as pr owner/repo#N %s", dir, c.want)
	}
	acct, env := ghAccountFor(dir, c, remote), os.Environ()
	if acct != "" {
		token, err := run("", "gh", "auth", "token", "--user", acct)
		if err != nil {
			return "", fmt.Errorf("gh auth token --user %s: %w", acct, withStderr(err))
		}
		env = append(env, "GH_TOKEN="+token)
	} else {
		acct = "the default login"
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", c.ghArgs()...)
	cmd.Dir, cmd.Env = dir, env
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh as %s: %w", acct, withStderr(err))
	}
	return c.answer(b)
}

func checksPath() string { return filepath.Join(home(), "checks.json") }

func loadChecks() (map[string]checkState, error) {
	if storeOn() {
		return storeChecks()
	}
	return loadChecksFile()
}

func loadChecksFile() (map[string]checkState, error) {
	m := map[string]checkState{}
	b, err := os.ReadFile(checksPath())
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func saveChecks(m map[string]checkState) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(checksPath(), append(b, '\n'))
}

func lockChecks() (func(), error) {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(home(), "checks.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another tracks check is running")
	}
	return func() { f.Close() }, nil
}

// moveRenamedChecks files a renamed track's stored answers under its new name, so asks Emil
// has answered do not come back. Stored answers are keyed "NAME: CHECK".
func moveRenamedChecks(prev map[string]checkState, cards []*Card, evs []Event) {
	live := map[string]bool{}
	for _, c := range cards {
		live[c.Name] = true
	}
	for k, st := range prev {
		name, text, ok := strings.Cut(k, ": ")
		if !ok || live[name] {
			continue
		}
		if to := renamedTo(evs, name); to != name {
			if nk := to + ": " + text; !has(prev, nk) {
				prev[nk] = st
				delete(prev, k)
			}
		}
	}
}

func has(m map[string]checkState, k string) bool { _, ok := m[k]; return ok }

// keepStaleAsks keeps the record of an earlier check of c while its ask is still under Needs me,
// so a write to asks: can tell whether Emil has seen it and drops it once he has.
func keepStaleAsks(prev, next map[string]checkState, c *Card, current string) {
	for k, st := range prev {
		name, text, ok := strings.Cut(k, ": ")
		if ok && name == c.Name && text != current && hasCheckAsk(c, askKey(text, st.Answer)) {
			next[k] = st
		}
	}
}

func checkCmd(args []string, out, errw io.Writer) error {
	fl := flag.NewFlagSet("check", flag.ContinueOnError)
	dry := fl.Bool("dry-run", false, "print what would change and write nothing")
	if err := fl.Parse(args); err != nil {
		return err
	}
	return runChecks(*dry, ghEval, out, errw)
}

func askKey(text string, ans bool) string {
	return fmt.Sprintf("Check %q is now %s.", text, yesNo(ans))
}

func askLine(text string, ans bool, mode string) string {
	if ans {
		return askKey(text, ans) + " Recommend: open the track."
	}
	return askKey(text, ans) + " Recommend: open the track and see why."
}

// askLost is true when the ask for the stored answer is gone from the card although
// Emil has not opened the track or typed into it since it was added: an agent's
// rewrite from an older copy dropped it.
func askLost(c *Card, text string, st checkState, evs []Event) bool {
	asked, err := time.Parse(time.RFC3339Nano, st.Asked)
	if err != nil || hasCheckAsk(c, askKey(text, st.Answer)) {
		return false
	}
	return !seenSince(evs, c.Name, asked)
}

// seenSince is whether Emil opened the track, typed into it or applied one of its asks after at.
func seenSince(evs []Event, name string, at time.Time) bool {
	return slices.ContainsFunc(evs, func(e Event) bool {
		return e.Track == name && e.At.After(at) && (e.Event == "open" || e.Event == "answer" || e.Event == "prompt" && e.Kind != "system")
	})
}

var checkAskQuote = regexp.MustCompile(`^Check ("(?:[^"\\]|\\.)*") is now (yes|no)\.`)

// checkAskAnswered tells, for a check ask on the track's card, whether Emil has seen it since
// tracks check added it: the same test askLost uses, so an ask dropped this way is not put back.
// An ask tracks check has no record of counts as unanswered.
func checkAskAnswered(name string) func(text string) bool {
	prev, _ := loadChecksFile()
	evs := readEventsFile()
	return func(text string) bool {
		m := checkAskQuote.FindStringSubmatch(text)
		if m == nil {
			return false
		}
		chk, err := strconv.Unquote(m[1])
		if err != nil {
			return false
		}
		st, ok := prev[name+": "+chk]
		if !ok || st.Answer != (m[2] == "yes") {
			return false
		}
		asked, err := time.Parse(time.RFC3339Nano, st.Asked)
		return err == nil && seenSince(evs, name, asked)
	}
}

// runChecks treats a missing stored answer as "no", so a check that is already
// true fires once. Errors and pending answers never fire and keep the stored answer.
func runChecks(dry bool, eval evaluator, out, errw io.Writer) error {
	if storeOn() {
		return runChecksStore(dry, eval, out, errw)
	}
	if !dry {
		unlock, err := lockChecks()
		if err != nil {
			return err
		}
		// tracks migrate holds checks.lock through the switch, so this is the files' last word.
		if storeOn() {
			unlock()
			return runChecksStore(dry, eval, out, errw)
		}
		defer unlock()
	}
	prev, err := loadChecks()
	if err != nil {
		return fmt.Errorf("%s: %w", checksPath(), err)
	}
	evs, cards := readEvents(), loadCards()
	moveRenamedChecks(prev, cards, evs)
	next := map[string]checkState{}
	for _, c := range cards {
		text := c.Meta["check"]
		keepStaleAsks(prev, next, c, text)
		if text == "" {
			continue
		}
		key := c.Name + ": " + text
		old, seen := prev[key]
		if seen {
			next[key] = old
		}
		if c.mode() == "parked" {
			continue
		}
		chk, ok := parseCheck(text)
		if !ok {
			fmt.Fprintf(errw, "%s: unknown check %q, not run\n", c.Name, text)
			continue
		}
		got, err := eval(c.Meta["dir"], chk)
		if err != nil {
			fmt.Fprintf(errw, "%s: check %q: %v\n", c.Name, text, err)
			continue
		}
		if got == pending {
			fmt.Fprintf(out, "%s: %q is pending, not recorded\n", c.Name, text)
			continue
		}
		ans := got == yes
		ran := time.Now().UTC()
		verb := "add"
		if ans == old.Answer {
			if !dry {
				old.RanAt = ran
				next[key] = old
			}
			if !askLost(c, text, old, evs) {
				fmt.Fprintf(out, "%s: %q is %s, unchanged\n", c.Name, text, got)
				continue
			}
			verb = "re-add"
		}
		if dry {
			fmt.Fprintf(out, "%s: %q is %s, would %s an ask\n", c.Name, text, got, verb)
			continue
		}
		if err := fire(c.Name, text, ans); err != nil {
			fmt.Fprintf(errw, "%s: %v\n", c.Name, err)
			continue
		}
		fmt.Fprintf(out, "%s: %q is %s, %sed an ask\n", c.Name, text, got, verb)
		if verb == "re-add" {
			continue
		}
		st := checkState{Answer: ans, Asked: ran.Format(time.RFC3339Nano), RanAt: ran}
		prev[key], next[key] = st, st
		if err := saveChecks(prev); err != nil {
			return err
		}
		if err := appendEvent(Event{At: time.Now().UTC(), Event: "check", Track: c.Name, Cwd: c.Meta["dir"], Kind: got}); err != nil {
			fmt.Fprintf(errw, "%s: attention log: %v\n", c.Name, err)
		}
	}
	if dry {
		return nil
	}
	return saveChecks(next)
}

// fire adds the ask under the cards lock. An agent that writes the card by hand instead of
// with tracks card can still drop it; askLost repairs that on a later run.
func fire(name, text string, ans bool) error {
	if storeOn() {
		return withStore(func(st *store.Store) error {
			_, err := st.RecordCheck(name, text, ans)
			return err
		})
	}
	return withCardLock(name, func(card string) (string, error) {
		c := parseCard(name, card)
		if c.mode() == "parked" || c.Meta["check"] != text {
			return "", errors.New("card changed during the check, next run retries")
		}
		// Drop the opposite ask first, in the same write, so the number does not climb past 9
		// and an opposite ask an agent's write put back still goes.
		card = dropAsk(card, name, askKey(text, !ans))
		if hasCheckAsk(parseCard(name, card), askKey(text, ans)) {
			return card, nil
		}
		return insertAsk(card, askLine(text, ans, c.mode()), time.Now()), nil
	})
}

// dropAsk removes the ask lines that begin with key from the Needs me section, leaving every
// other line as it is. A flipped check's new ask supersedes the old one, which no answer would
// ever clear: its record says the opposite.
func dropAsk(text, name, key string) string {
	body := strings.Split(parseCard(name, text).Sections["Needs me"], "\n")
	kept := slices.DeleteFunc(slices.Clone(body), func(l string) bool {
		a, ok := parseAsk(l)
		return ok && checkAskFor(a, key)
	})
	if len(kept) == len(body) {
		return text
	}
	return setSection(text, "Needs me", kept, "Standing orders", "Later")
}

// checkAskFor is true when a is the check ask that key begins. An agent's own question that
// merely contains the check sentence is not one.
func checkAskFor(a ask, key string) bool {
	return isCheckAsk(a.text) && strings.HasPrefix(a.text, key)
}

func hasCheckAsk(c *Card, key string) bool {
	return slices.ContainsFunc(c.askList(), func(a ask) bool { return checkAskFor(a, key) })
}

func heading(l string) (string, bool) {
	h, ok := strings.CutPrefix(l, "## ")
	return strings.TrimSpace(h), ok
}

// insertAsk only inserts lines and rewrites the updated: line; every other byte stays.
// Like parseCard, it takes the last "## Needs me" heading.
func insertAsk(text, ask string, now time.Time) string {
	lines := strings.SplitAfter(text, "\n")
	fmEnd := -1
	if strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				fmEnd = i
				break
			}
		}
	}
	needs, standing := -1, -1
	for i := fmEnd + 1; i < len(lines); i++ {
		if h, ok := heading(lines[i]); ok && h == "Needs me" {
			needs = i
		} else if ok && h == "Standing orders" && standing < 0 {
			standing = i
		}
	}
	switch {
	case needs >= 0:
		end, pos, n := len(lines), needs+1, 0
		for i := needs + 1; i < len(lines); i++ {
			if _, ok := heading(lines[i]); ok {
				end = i
				break
			}
		}
		for i := needs + 1; i < end; i++ {
			t := strings.TrimLeft(lines[i], " \t")
			if strings.TrimSpace(t) != "" {
				pos = i + 1
			}
			d := strings.IndexFunc(t, func(r rune) bool { return r < '0' || r > '9' })
			if v, err := strconv.Atoi(t[:max(d, 0)]); err == nil && (t[d] == '.' || t[d] == ')') {
				n = max(n, v)
			}
		}
		item := fmt.Sprintf("%d. %s\n", n+1, ask)
		if !strings.HasSuffix(lines[pos-1], "\n") {
			item = "\n" + item
		}
		lines = slices.Insert(lines, pos, item)
	case standing >= 0:
		lines = slices.Insert(lines, standing, "## Needs me\n1. "+ask+"\n\n")
	default:
		sec := "\n## Needs me\n1. " + ask + "\n"
		if !strings.HasSuffix(text, "\n") {
			sec = "\n" + sec
		}
		lines = append(lines, sec)
	}
	if fmEnd > 0 {
		stamp := "updated: " + now.Format(time.RFC3339) + "\n"
		i := slices.IndexFunc(lines[1:fmEnd], func(l string) bool { k, _, _ := strings.Cut(l, ":"); return strings.TrimSpace(k) == "updated" })
		if i >= 0 {
			lines[i+1] = stamp
		} else {
			lines = slices.Insert(lines, fmEnd, stamp)
		}
	}
	return strings.Join(lines, "")
}
