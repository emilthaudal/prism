package tracks

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	spawnUsage = `usage: tracks spawn LEAD "task" --dir D [--model sonnet|opus] [--verify CMD]`
	sendUsage  = "usage: tracks send TARGET \"text\" [--replace] [--now]\n       tracks send TRACK \"text\" [--from LEAD]"
	waitUsage  = "usage: tracks wait LEAD [--timeout D]\n       tracks wait --all [--follow] [--timeout D] [--idle D]\n       tracks wait --desk [--follow] [--timeout D]"

	needsStore   = "needs tracks.db: run tracks migrate first"
	maxWaitMsg   = 200
	exitTimedOut = exitCode(2)
)

// A track or thread starts on Opus at medium effort unless the desk or lead passes a model:
// Sonnet is for easy, well-specified work (EMI-449).
const (
	defaultModel  = "opus"
	defaultEffort = "medium"
)

// modelArgs is the claude flags for a model, the default when none is named. Haiku takes no effort.
func modelArgs(model string) []string {
	if model == "" {
		model = defaultModel
	}
	if model == "haiku" {
		return []string{"--model", model}
	}
	return []string{"--model", model, "--effort", defaultEffort}
}

var (
	spawnModels = []string{"sonnet", "opus"}
	startModels = []string{"sonnet", "opus", "haiku"}
	// startModes are the modes a track can start in: waiting is not chosen (EMI-423).
	startModes = []string{"focus", "background", "parked"}
)

// leadMessage is what tracks send types in front of a lead's or the desk's text. The hook
// counts such a prompt as the system's, not Emil's: it never marks asks seen or answered.
var leadMessage = regexp.MustCompile(`^\[(lead [A-Za-z0-9][A-Za-z0-9._-]*|desk)\] `)

// leadFlags splits args into positionals and the value flags in want (--f V or --f=V).
func leadFlags(args []string, want ...string) (pos []string, flags map[string]string, err error) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		if !slices.Contains(want, name) {
			return nil, nil, fmt.Errorf("unknown flag %s", name)
		}
		if _, dup := flags[name]; dup {
			return nil, nil, fmt.Errorf("%s is given twice", name)
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			val = args[i]
		}
		flags[name] = val
	}
	return pos, flags, nil
}

// getView reads the live-or-closed track name points to, with the reason it cannot be used as
// a card when it is missing, closed or renamed away.
func getView(name string) (store.View, error) {
	var v store.View
	err := withStore(func(st *store.Store) (err error) {
		v, err = st.Get(name)
		return err
	})
	switch {
	case errors.Is(err, store.ErrNoTrack):
		return v, fmt.Errorf("no card for %s", name)
	case err != nil:
		return v, err
	case v.Closed:
		return v, fmt.Errorf("%s is closed", name)
	case v.RenamedTo != "":
		return v, fmt.Errorf("%s was renamed to %s", name, v.RenamedTo)
	}
	return v, nil
}

// leadOf is the open lead card name points to: it exists, is open, carries role: lead, and is
// not itself a thread.
func leadOf(name string) (store.View, error) {
	v, err := getView(name)
	switch {
	case err != nil:
		return v, err
	case v.Track.Role() != store.RoleLead:
		return v, fmt.Errorf("%s is not role: lead", name)
	case v.Track.Parent() != "":
		return v, fmt.Errorf("%s is a thread of %s, not a lead", name, v.Track.Parent())
	}
	return v, nil
}

// spawnCmd is tracks spawn: a thread card under a lead, and a Claude session on it in a new
// Rex session that Rex is not told to switch to.
func spawnCmd(args []string, out io.Writer) error {
	pos, fl, err := leadFlags(args, "--dir", "--model", "--verify")
	if err != nil || len(pos) != 2 {
		return errors.New(spawnUsage)
	}
	lead, task := pos[0], cleanRequest(pos[1])
	model, verify := fl["--model"], strings.TrimSpace(fl["--verify"])
	switch {
	case !validName.MatchString(lead):
		return fmt.Errorf("not a track name: %q", lead)
	case model != "" && !slices.Contains(spawnModels, model):
		return fmt.Errorf("--model must be sonnet or opus\n%s", spawnUsage)
	case task == "":
		return errors.New("the task is empty\n" + spawnUsage)
	case !verifyCmdOK(verify):
		return fmt.Errorf("--verify must be one line with no control characters and no \" # \": %q", verify)
	case !storeOn():
		return errors.New("tracks spawn " + needsStore)
	}
	max, err := maxThreads()
	if err != nil {
		return err
	}
	lv, err := leadOf(lead)
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	if sess := os.Getenv("REX_SESSION"); sess != "" && (lv.Track.RexSession == "" || lv.Track.RexSession != sess) {
		return fmt.Errorf("refused: this runs in Rex session %s, which is not %s's session", sess, lv.Track.Name)
	}
	dir := fl["--dir"]
	if dir == "" {
		dir = lv.Track.Dir
	}
	if dir == "" {
		return errors.New("--dir is required: the lead has no dir to start from\n" + spawnUsage)
	}
	dir = filepath.Clean(expandHome(dir))
	if st, err := os.Stat(dir); !filepath.IsAbs(dir) || !cardValueOK(dir) || err != nil || !st.IsDir() {
		return fmt.Errorf("--dir is not a directory: %s", dir)
	}
	if why := noTrustedFolder(dir); why != "" {
		return errors.New("refused: " + why)
	}
	name, err := spawnThread(lv.Track.Name, task, dir, verify, max)
	if err != nil {
		return err
	}
	settings, err := writeThreadSettings(name)
	if err != nil {
		return fmt.Errorf("made the card %s but could not write its Stop hook settings (card kept, no session started): %w", name, err)
	}
	sid, block, err := rexNewSession(trackSpec{name: name, dir: dir, argv: withTracksHome(append(append([]string{"claude"}, modelArgs(model)...), "--name", name, "--settings", settings, "/track "+name)...)})
	if err != nil {
		return fmt.Errorf("made the card %s but could not start a Rex session (card kept): %w", name, err)
	}
	if err := setMeta(name, "rex_session", sid); err != nil {
		return fmt.Errorf("started %s but could not write rex_session on %s's card: %w", sid, name, err)
	}
	dialog, started := watchStart(sid, block)
	if dialog != "" {
		why := fmt.Sprintf("refused: Claude stopped the new thread in %s at a dialog nobody can answer: %q. Open claude in %s once and answer it (trust the folder, or decide on the MCP server), then spawn again", dir, dialog, dir)
		if err := unspawn(name, sid); err != nil {
			return fmt.Errorf("%s. Taking the thread back failed, so end session %s and close %s by hand: %w", why, sid, name, err)
		}
		return errors.New(why + ". The thread's session was ended and its card removed")
	}
	if !started {
		fmt.Fprintf(out, "spawned %s in %s, but its agent did not show it had started within %s: look at that session\n", name, sid, startWait)
		return nil
	}
	fmt.Fprintf(out, "spawned %s in %s\n", name, sid)
	return nil
}

// spawnThread writes the thread's card under the first free name for the task.
func spawnThread(lead, task, dir, verify string, max int) (string, error) {
	base, r := slugName(task, time.Now()), loadRetired()
	for i := 1; i <= maxNameTries; i++ {
		name := numbered(base, i)
		if nameTaken(name, r) {
			continue
		}
		err := withStore(func(st *store.Store) error {
			_, err := st.Spawn(lead, store.SpawnSpec{Name: name, Dir: dir, Goal: task, Verify: verify, Max: max})
			return err
		})
		switch {
		case errors.Is(err, store.ErrNameTaken):
			continue
		case errors.Is(err, store.ErrTooManyThreads):
			var capErr *store.ThreadCapError
			open := max
			if errors.As(err, &capErr) {
				open = capErr.Open
			}
			return "", fmt.Errorf("refused: %s already has %d open threads and may have %d (%s changes it): close a finished thread first", lead, open, max, maxThreadsEnv)
		case errors.Is(err, store.ErrNoTrack), errors.Is(err, store.ErrClosed), errors.Is(err, store.ErrNotLead), errors.Is(err, store.ErrIsChild):
			return "", fmt.Errorf("refused: %s: %w", lead, err)
		case err != nil:
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("no free name left for %s", base)
}

// sendRefusal says why the caller may not type into child's agent, or "" when it may. The
// caller is the Rex session in $REX_SESSION; --from names it only outside Rex, where there is
// no session to read. The parent is the one written by tracks spawn, which a thread cannot
// edit, and the caller must be that parent's own session, so a thread naming itself or its
// sibling as the sender is turned away.
func sendRefusal(child, from, callerSession string) (lead string, why string) {
	cv, err := getView(child)
	if err != nil {
		return "", err.Error()
	}
	parent := cv.Track.Parent()
	switch {
	case parent == "":
		return "", child + " has no parent: only a lead's own threads take sent text"
	case strings.EqualFold(parent, cv.Track.Name):
		return "", child + " is its own parent"
	case cv.Track.RexSession == "":
		return "", child + " has no rex_session"
	}
	lv, err := leadOf(parent)
	if err != nil {
		return "", child + "'s parent: " + err.Error()
	}
	if lv.Track.ID == cv.Track.ID {
		return "", child + " is its own parent"
	}
	if callerSession != "" {
		switch {
		case from != "":
			return "", "only " + lv.Track.Name + "'s own session may send to " + child
		case lv.Track.RexSession == "":
			return "", lv.Track.Name + " has no rex_session to match the caller against"
		case callerSession != lv.Track.RexSession:
			return "", "the caller (Rex session " + callerSession + ") is not " + lv.Track.Name + "'s session, and only " + child + "'s parent may send to it"
		case lv.Track.RexSession == cv.Track.RexSession:
			return "", lv.Track.Name + " and " + child + " share a Rex session"
		}
		return lv.Track.Name, ""
	}
	switch {
	case from == "":
		return "", "outside Rex say who sends: --from " + lv.Track.Name
	case !strings.EqualFold(from, lv.Track.Name):
		return "", child + "'s parent is " + lv.Track.Name + ", not " + from
	}
	return lv.Track.Name, ""
}

// sendCmd is tracks send: a note to a session the desk started (deskSendCmd), else [lead LEAD]
// text typed into a thread's agent by its parent, or [desk] text typed into any open track's
// agent by the desk, only while Rex says the agent is idle or done.
func sendCmd(args []string, out io.Writer) error {
	replace, now := slices.Contains(args, "--replace"), slices.Contains(args, "--now")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--replace" || a == "--now" })
	pos, fl, err := leadFlags(args, "--from")
	if err != nil || len(pos) != 2 {
		return errors.New(sendUsage)
	}
	if storeOn() {
		s, ok, err := deskTarget(pos[0])
		if err != nil {
			return err
		}
		if ok {
			text := strings.TrimSpace(pos[1])
			switch {
			case fl["--from"] != "":
				return errors.New("refused: --from is for a lead's thread, not a session the desk started")
			case text == "":
				return errors.New("refused: the text is empty")
			}
			return deskSendCmd(s, text, replace, now, out)
		}
	}
	if replace || now {
		return errors.New("refused: --replace and --now are for a session the desk started")
	}
	child, text := pos[0], sanitizeAnswer(pos[1])
	switch {
	case !validName.MatchString(child):
		return fmt.Errorf("not a track name: %q", child)
	case text == "":
		return errors.New("refused: the text is empty")
	case !storeOn():
		return errors.New("tracks send " + needsStore)
	}
	if f, ok := fl["--from"]; ok && !validName.MatchString(f) {
		return fmt.Errorf("not a track name: %q", f)
	}
	caller := callerSession()
	deskSess, err := deskSessionNow()
	if err != nil {
		return err
	}
	mark := ""
	if caller != "" && caller == deskSess {
		why := deskCallerRefusal(deskSess, caller)
		switch {
		case why != "":
		case fl["--from"] != "":
			why = "the desk sends as itself, without --from"
		default:
			why = deskSendRefusal(child, deskSess)
		}
		if why != "" {
			return errors.New("refused: " + why)
		}
		mark = deskMark
	} else {
		lead, why := sendRefusal(child, fl["--from"], caller)
		if why != "" {
			return errors.New("refused: " + why)
		}
		mark = "[lead " + lead + "] "
	}
	c, err := loadCard(child)
	if err != nil {
		return fmt.Errorf("refused: no card for %s", child)
	}
	hint := ""
	if sess := c.Meta["rex_session"]; sess != "" {
		hint = agentBlock(readEvents(), sess)
	}
	_, fail := typeIntoAgent(child, c.Meta["rex_session"], hint, mark+text)
	if fail != nil {
		return errors.New(fail.status)
	}
	fmt.Fprintf(out, "sent to %s\n", child)
	return nil
}

var (
	waitPoll = rexEvery
	// waitState is where wait reads an agent's state; tests replace it with a fake.
	waitState = func(sess, hint string) rexResult { return fetchSession(sess, hint, "") }
)

// seenState is what wait last saw of a thread.
type seenState struct {
	state string
	// at and owner identify the report behind a done, blocked or error state, so a whole turn
	// that began and ended between two polls still shows as a new report.
	at    time.Time
	owner int
	asks  map[int64]bool
	// kind and msg come with state; answered is whether Rex has answered about this thread, so
	// an empty state means its agent is gone rather than not looked at yet.
	kind, msg string
	answered  bool
}

// oneLine makes agent-written text one short line for the lead to read.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(cleanText(s)), " ")
	if r := []rune(s); len(r) > maxWaitMsg {
		s = string(r[:maxWaitMsg]) + "…"
	}
	return s
}

// waitCmd is tracks wait: it blocks until one of the lead's threads turns done, blocked or
// error, or gains an ask, prints a line per change and exits 0; exit 2 on timeout. A thread
// already done when it starts is not a change: look with tracks ls first.
func waitCmd(args []string, out, errw io.Writer) error {
	if i := slices.Index(args, "--all"); i >= 0 {
		return waitAllCmd(slices.Delete(slices.Clone(args), i, i+1), out, errw)
	}
	if i := slices.Index(args, "--desk"); i >= 0 {
		return waitDeskCmd(slices.Delete(slices.Clone(args), i, i+1), out, errw)
	}
	pos, fl, err := leadFlags(args, "--timeout")
	if err != nil || len(pos) != 1 {
		return errors.New(waitUsage)
	}
	timeout, err := durationFlag(fl, "--timeout")
	if err != nil {
		return err
	}
	switch {
	case !validName.MatchString(pos[0]):
		return fmt.Errorf("not a track name: %q", pos[0])
	case !storeOn():
		return errors.New("tracks wait " + needsStore)
	}
	lv, err := leadOf(pos[0])
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	lead := lv.Track.Name
	start := time.Now()
	seen := map[string]*seenState{}
	blocks := map[string]string{}
	first := true
	for {
		if v, err := getView(lead); err == nil || v.RenamedTo != "" {
			lead = v.Track.Name // a rename while waiting keeps the threads in view
		}
		lines, err := pollThreads(lead, seen, blocks, first)
		if err != nil {
			return err
		}
		first = false
		if len(lines) > 0 {
			for _, l := range lines {
				fmt.Fprintln(out, l)
			}
			return nil
		}
		if timeout > 0 && time.Since(start) >= timeout {
			fmt.Fprintf(errw, "tracks: no change in %s's threads within %s\n", lead, timeout)
			return exitTimedOut
		}
		time.Sleep(waitPoll)
	}
}

// durationFlag is the value of flag as a positive duration, zero when it is not given.
func durationFlag(fl map[string]string, flag string) (time.Duration, error) {
	v, ok := fl[flag]
	if !ok {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s is a duration such as 90s or 30m: %q", flag, v)
	}
	return d, nil
}

// threadsOf are the lead's open threads. An error means the store could not be read, which
// says nothing about the threads.
func threadsOf(lead string) ([]*Card, error) {
	vs, err := liveViews()
	if err != nil {
		return nil, err
	}
	var out []*Card
	for _, v := range vs {
		c := cardOf(v)
		if p := c.Meta["parent"]; p != "" && strings.EqualFold(p, lead) {
			out = append(out, c)
		}
	}
	return out, nil
}

// pollThreads reads every thread once and returns the changes since the last poll. The first
// poll only records what is there; a thread that appears later starts from nothing. A poll
// whose store read fails reports nothing, so an unreadable store never looks like a close.
func pollThreads(lead string, seen map[string]*seenState, blocks map[string]string, first bool) ([]string, error) {
	threads, err := threadsOf(lead)
	if err != nil {
		return nil, nil
	}
	return pollCards(threads, seen, blocks, first)
}

// pollCards is pollThreads over any set of open cards.
func pollCards(threads []*Card, seen map[string]*seenState, blocks map[string]string, first bool) ([]string, error) {
	var lines []string
	var evs []Event
	// A thread that dropped out was closed, or renamed: a renamed one carries on under its new name.
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		if slices.ContainsFunc(threads, func(c *Card) bool { return c.Name == name }) {
			continue
		}
		var v store.View
		err := withStore(func(st *store.Store) (err error) {
			v, err = st.Get(name)
			return err
		})
		switch {
		case err == nil && v.RenamedTo != "":
			seen[v.RenamedTo] = seen[name]
		case errors.Is(err, store.ErrNoTrack), err == nil && v.Closed:
			lines = append(lines, name+" closed")
		default:
			continue // not readable now: look again next poll
		}
		delete(seen, name)
	}
	for _, c := range threads {
		s := seen[c.Name]
		if s == nil {
			s = &seenState{asks: map[int64]bool{}}
			seen[c.Name] = s
		}
		state, msg, at, owner, kind, answered := s.state, s.msg, s.at, s.owner, s.kind, s.answered
		if sess := c.Meta["rex_session"]; sess != "" {
			hint := blocks[sess]
			if hint == "" {
				if evs == nil {
					evs = readEvents()
				}
				hint = agentBlock(evs, sess)
			}
			if r := waitState(sess, hint); !r.failed {
				state, msg, at, owner, kind, answered = "", "", time.Time{}, 0, "", true
				if r.found && !r.state.Exited {
					state, msg, at, owner, kind = r.state.State, r.state.Msg, r.state.At, r.state.Owner, r.state.Kind
					if msg == "" {
						msg = r.state.Kind
					}
					blocks[sess] = r.block
				}
			}
		}
		reportable := state == "done" || state == "blocked" || state == "error"
		switch {
		case state == "" && s.state != "":
			lines = append(lines, c.Name+" exited: its agent is gone")
		case reportable && (state != s.state || at.After(s.at) || owner != s.owner):
			if msg = oneLine(msg); msg == "" {
				msg = "no message"
			}
			lines = append(lines, fmt.Sprintf("%s %s: %s", c.Name, state, msg))
		}
		s.state, s.at, s.owner, s.kind, s.msg, s.answered = state, at, owner, kind, msg, answered
		for i, a := range c.askList() {
			if i >= len(c.askIDs) || s.asks[c.askIDs[i]] {
				continue
			}
			s.asks[c.askIDs[i]] = true
			lines = append(lines, fmt.Sprintf("%s ask: %s", c.Name, oneLine(a.text)))
		}
	}
	if first {
		return nil, nil
	}
	return lines, nil
}
