package tracks

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	startUsage = `usage: tracks start "request" [--dir D] [--model sonnet|opus|haiku] [--name NAME] [--mode focus|background|parked] [--loop [--until HH:MM | --for 8h] [--turns N] [--budget PCT]]`

	// defaultStartMode is the mode of a track nobody chose one for: focus is Emil's, so it is
	// never the default (EMI-423).
	defaultStartMode = "background"

	// startWindow is how far apart a start's log entry and its track's creation may be.
	startWindow = rexNewTimeout + time.Minute
	// maxQuotedAsk is how much of a track's ask the desk's answer quotes back to it.
	maxQuotedAsk = 80
)

// quoteAsk is a track's ask cut to a length the desk's message can quote.
func quoteAsk(s string) string {
	if r := []rune(s); len(r) > maxQuotedAsk {
		s = strings.TrimSpace(string(r[:maxQuotedAsk-1])) + "…"
	}
	return strconv.Quote(s)
}

// deskAnswer is tracks desk answer TRACK N "answer": the desk answers ask N on the track's card
// in the track's session, as [desk] text, and logs the ask and its answer, so u can take it back.
func deskAnswer(args []string, out io.Writer) error {
	if len(args) != 3 || strings.HasPrefix(args[1], "-") {
		return errors.New(deskUsage)
	}
	track, answer := args[0], sanitizeAnswer(args[2])
	n, err := strconv.Atoi(args[1])
	switch {
	case !validName.MatchString(track):
		return fmt.Errorf("not a track name: %q", track)
	case err != nil || n < 1:
		return fmt.Errorf("not an ask number: %q", args[1])
	case answer == "":
		return errors.New("refused: the answer is empty")
	}
	if err := deskAuthorize(); err != nil {
		return err
	}
	deskSess, err := deskSessionNow()
	if err != nil {
		return err
	}
	v, err := getView(track)
	if err != nil {
		return errors.New("refused: " + err.Error())
	}
	i := slices.IndexFunc(v.Asks, func(a store.Ask) bool { return a.N == n })
	if i < 0 {
		return fmt.Errorf("refused: %s has no ask %d", track, n)
	}
	a := v.Asks[i]
	if a.Kind != "agent" {
		// A check ask clears only in the pane or when its check stops holding: a typed answer
		// would be logged as answered while the ask stays open.
		return fmt.Errorf("refused: ask %d on %s is a %s ask, which clears in the pane, not by an answer", n, track, a.Kind)
	}
	if !v.Track.AnsweredAt.IsZero() {
		return fmt.Errorf("refused: Emil is answering %s's asks right now", track)
	}
	sess := v.Track.RexSession
	text := fmt.Sprintf("%sanswer to your ask %d (%s): %s", deskMark, n, quoteAsk(a.Text), answer)
	sent := time.Now()
	held := false
	var failed error
	if bg, ok := bgSession(track); ok && sess == "" {
		// A session the desk started runs as claude --bg with no Rex pane: the answer goes as a note.
		if held, failed, err = noteAnswer(v.Track.Name, bg, "desk", text); err != nil {
			return errors.New("refused: " + err.Error())
		}
	} else {
		if why := deskSendRefusal(track, deskSess); why != "" {
			return errors.New("refused: " + why)
		}
		if _, fail := typeIntoAgent(track, sess, agentBlock(readEvents(), sess), text); fail != nil {
			return errors.New(fail.status)
		}
	}
	err = withStore(func(st *store.Store) error {
		_, err := st.LogDesk(v.Track.Name, "answered", a.Text, answer)
		return err
	}, store.WithClock(func() time.Time { return sent }))
	if err != nil {
		return fmt.Errorf("answered ask %d on %s, but could not log it: %w", n, track, err)
	}
	if failed != nil {
		fmt.Fprintf(out, "answered ask %d on %s: delivery failed (%v), held for the desk's watch to retry\n", n, track, failed)
		return nil
	}
	if held {
		fmt.Fprintf(out, "answered ask %d on %s: held until it stops\n", n, track)
		return nil
	}
	fmt.Fprintf(out, "answered ask %d on %s\n", n, track)
	return nil
}

// startCmd is tracks start: what the pane's n does, a card for the request and an agent on it
// in a Rex session of its own, which Rex is not told to switch to. Every start is logged as
// the desk's, so u in the handled log can take it back.
func startCmd(args []string, out io.Writer) error {
	loop := slices.Contains(args, "--loop")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--loop" })
	pos, fl, err := leadFlags(args, "--dir", "--model", "--name", "--mode", "--until", "--for", "--turns", "--budget")
	if err != nil || len(pos) != 1 {
		return errors.New(startUsage)
	}
	var loopAt *loopState
	if loop {
		l, err := loopFlags(fl, loopNow())
		if err != nil {
			return fmt.Errorf("%w\n%s", err, startUsage)
		}
		loopAt = &l
		if fl["--model"] == "" {
			fl["--model"] = loopModel
		}
	} else if slices.ContainsFunc([]string{"--until", "--for", "--turns", "--budget"}, func(f string) bool { _, ok := fl[f]; return ok }) {
		return errors.New("--until, --for, --turns and --budget go with --loop\n" + startUsage)
	}
	goal, model := cleanRequest(pos[0]), fl["--model"]
	named, hasName := fl["--name"]
	mode, hasMode := fl["--mode"]
	if !hasMode {
		mode = defaultStartMode
	}
	switch {
	case !slices.Contains(startModes, mode):
		return fmt.Errorf("--mode must be focus, background or parked\n%s", startUsage)
	case goal == "":
		return errors.New("the request is empty\n" + startUsage)
	case model != "" && !slices.Contains(startModels, model):
		return fmt.Errorf("--model must be sonnet, opus or haiku\n%s", startUsage)
	case hasName && !validName.MatchString(named):
		return fmt.Errorf("--name is not a track name: %q", named)
	case !storeOn():
		return errors.New("tracks start " + needsStore)
	}
	if mode == "focus" {
		// A third focus track is refused before anything is made: the desk puts the swap to Emil.
		if others, err := openFocus(named); err != nil {
			return err
		} else if len(others) >= maxFocus {
			return &focusFullError{focus: others}
		}
	}
	desk, err := startCaller()
	if err != nil {
		return err
	}
	dir := fl["--dir"]
	if dir == "" {
		if dir, err = defaultDir(); err != nil {
			return err
		}
	}
	dir = filepath.Clean(expandHome(dir))
	if st, err := os.Stat(dir); !filepath.IsAbs(dir) || !cardValueOK(dir) || err != nil || !st.IsDir() {
		return fmt.Errorf("--dir is not a directory: %s", dir)
	}
	if why := noTrustedFolder(dir); why != "" {
		return errors.New("refused: " + strings.ReplaceAll(why, "thread", "track"))
	}
	if desk {
		return deskStartCmd(goal, dir, model, named, mode, loopAt, out)
	}
	if loop {
		return errors.New("refused: --loop is the desk's: a loop runs unattended in the background")
	}
	name := named
	if hasName {
		err = newNamedTrack(named, goal, dir, mode)
	} else {
		name, err = newRequestTrack(goal, dir, mode)
	}
	if err != nil {
		return err
	}
	argv := append(append([]string{"claude"}, modelArgs(model)...), "--name", name, "/track "+name)
	if argv, err = withOwnTracks(argv...); err != nil {
		return err
	}
	sid, block, err := rexNewSession(trackSpec{name: name, dir: dir, argv: argv})
	if err != nil {
		return fmt.Errorf("made the card %s but could not start a Rex session (card kept): %w", name, err)
	}
	if err := setMeta(name, "rex_session", sid); err != nil {
		return fmt.Errorf("started %s but could not write rex_session on %s's card: %w", sid, name, err)
	}
	did := "started " + name + " in " + tildePath(dir)
	if by := startedBy(); by != "" {
		did += " (" + by + ")"
	}
	var entry store.DeskLogEntry
	logErr := withStore(func(st *store.Store) (err error) {
		entry, err = st.LogDesk(name, "started", goal, did)
		return err
	})
	if logErr != nil {
		logErr = fmt.Errorf("started %s in %s, but could not log the start: %w", name, sid, logErr)
	}
	dialog, started := watchStart(sid, block)
	if dialog != "" {
		why := fmt.Sprintf("refused: Claude stopped the new track in %s at a dialog nobody can answer: %q. Open claude in %s once and answer it, then start again", dir, dialog, dir)
		_, _, cerr := closeTrack(name, time.Now(), "")
		if cerr == nil && logErr == nil {
			cerr = withStore(func(st *store.Store) error { return st.MarkDeskUndone(entry.ID) })
		}
		if cerr != nil {
			return fmt.Errorf("%s. Closing %s failed, so close it by hand: %w", why, name, cerr)
		}
		return errors.New(why + ". " + name + " was closed")
	}
	if logErr != nil {
		return logErr
	}
	if !started {
		fmt.Fprintf(out, "started %s in %s, but its agent did not show it had started within %s: look at that session\n", name, sid, startWait)
		return nil
	}
	fmt.Fprintf(out, "started %s in %s\n", name, sid)
	return nil
}

// startCaller says whether the desk's agent runs this start. It turns away a track's agent,
// which never starts tracks, and a process that claims the desk's session without running
// under the desk's agent.
func startCaller() (desk bool, err error) {
	caller := callerSession()
	if caller == "" {
		return false, nil
	}
	deskSess, err := deskSessionNow()
	if err != nil {
		return false, err
	}
	if caller == deskSess {
		if why := deskCallerRefusal(deskSess, caller); why != "" {
			return false, errors.New("refused: " + why)
		}
		return true, nil
	}
	cards, err := openCards()
	if err != nil {
		return false, err
	}
	if i := slices.IndexFunc(cards, func(c *Card) bool { return c.Meta["rex_session"] == caller }); i >= 0 {
		return false, fmt.Errorf("refused: Rex session %s runs the agent of %s, and a track does not start tracks", caller, cards[i].Name)
	}
	return false, nil
}

// startedBy says who made a start when it was not the desk's agent, "" when it was: every start
// is logged, so u can take it back, but only the desk's own read as the desk's.
func startedBy() string {
	deskSess, err := deskSessionNow()
	switch {
	case err != nil:
		return "caller unknown: " + err.Error()
	case deskSess == "":
		return "not the desk: no desk is bound"
	}
	pid, err := deskAgentPID(deskSess)
	switch {
	case errors.Is(err, errNoDeskAgent):
		return "not the desk: no desk agent runs"
	case err != nil:
		return "caller unknown: " + err.Error()
	}
	under, err := runsUnder(pid)
	switch {
	case err != nil:
		return "caller unknown: " + err.Error()
	case !under:
		return "not the desk"
	}
	return ""
}

// withOwnTracks is argv run with this binary's folder first on the PATH the session's login shell
// set up, and TRACKS_HOME passed on, as the desk runs: the new agent writes its card with the
// tracks that started it, to the same store.
func withOwnTracks(argv ...string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return append([]string{"sh", "-c", `export PATH="$0:$PATH"; exec "$@"`, filepath.Dir(exe)}, withTracksHome(argv...)...), nil
}

// newRequestTrack writes the card for a request under the first free name, as the pane's n does.
func newRequestTrack(goal, dir, mode string) (string, error) {
	base, r := slugName(goal, time.Now()), loadRetired()
	for i := 1; i <= maxNameTries; i++ {
		name := numbered(base, i)
		if nameTaken(name, r) {
			continue
		}
		err := newTrack(name, mode, "", dir, goal)
		if errors.Is(err, errExists) {
			continue
		}
		return name, err
	}
	return "", fmt.Errorf("no free name left for %s", base)
}

// newNamedTrack writes the card for a request under the name the desk chose, refusing a name an
// open or closed track holds or a track was renamed away from. named_by tells the track's agent
// to keep the name.
func newNamedTrack(name, goal, dir, mode string) error {
	err := withStore(func(st *store.Store) error {
		_, err := st.NewTrack(store.NewTrackSpec{Name: name, Mode: mode, Dir: dir, Goal: goal, Meta: map[string]string{"named_by": "desk"}})
		return err
	})
	if errors.Is(err, store.ErrNameTaken) {
		return fmt.Errorf("refused: the name %s is taken by another track, open or closed: pick another", name)
	}
	return err
}

// disregardText is what u types into a track whose answer from the desk Emil takes back.
func disregardText(e store.DeskLogEntry) string {
	return sanitizeAnswer(fmt.Sprintf("%sdisregard my answer to your ask %s (I said: %s). Emil answers it himself: keep the ask on your card and undo anything you did on my answer.", deskMark, quoteAsk(e.Asked), e.Did))
}

// whileWorking is refusal, except that an agent at work takes the text too: Claude Code
// queues it and reads it before the agent's next step.
func whileWorking(name string, s agentState) string {
	if s.Owner != 0 && !s.Exited && s.State == "working" {
		return ""
	}
	return refusal(name, s)
}

// undoEntry takes back the desk's handled entry e, for u in the handled log: an answer the
// track has not acted on, a start whose track never started work, or a close. It returns the
// status.
func undoEntry(e store.DeskLogEntry, self string) string {
	switch e.Kind {
	case "answered":
		return undoAnswer(e.ID)
	case "started":
		return undoStart(e.ID, self)
	case "closed":
		return undoClose(e.ID)
	}
	return "Only the desk's answers, starts and closes can be taken back"
}

// undoClose opens again the track the desk closed in entry id, with its card as it was when it
// closed. Its session stays ended: enter opens the track again.
func undoClose(id int64) string {
	var t store.Track
	err := withStore(func(st *store.Store) (err error) {
		t, err = st.ReopenClosed(id)
		return err
	})
	switch {
	case errors.Is(err, store.ErrNotClosed):
		return "That track is open already"
	case err != nil:
		return undoMiss(err)
	}
	return "Reopened " + t.Name + ": its card is back, enter opens it"
}

func undoMiss(err error) string {
	switch {
	case errors.Is(err, store.ErrUndone):
		return "That was taken back already"
	case errors.Is(err, store.ErrNotUndo):
		return "Only the desk's answers, starts and closes can be taken back"
	}
	return "could not take it back: " + err.Error()
}

// undoAnswer tells the track to disregard the desk's answer and leaves its ask on its card for
// Emil, only while the track has not acted on the answer (see store.AnswerActedOn).
func undoAnswer(id int64) string {
	var (
		e     store.DeskLogEntry
		acted string
	)
	err := withStore(func(st *store.Store) (err error) {
		e, acted, err = st.AnswerActedOn(id)
		return err
	})
	if err != nil {
		return undoMiss(err)
	}
	if acted != "" {
		return fmt.Sprintf("Not taken back: %s %s, after the desk's answer. Tell it yourself (enter opens it)", e.Track, acted)
	}
	v, err := getView(e.Track)
	if err != nil {
		return "Not taken back: " + err.Error()
	}
	sess := v.Track.RexSession
	if _, fail := typeInto(e.Track, sess, agentBlock(readEvents(), sess), disregardText(e), whileWorking); fail != nil {
		return fail.status
	}
	if err := withStore(func(st *store.Store) error { return st.UndoDeskAnswer(id) }); err != nil {
		return "Told " + e.Track + " to disregard the desk's answer, but could not mark it taken back: " + err.Error()
	}
	return "Took back the desk's answer to " + e.Track + ": its ask waits for you on its card"
}

// undoStart closes the track the desk started in entry id, only when it never started work
// (see store.CloseUnstarted), and ends its session.
func undoStart(id int64, self string) string {
	var t store.Track
	err := withStore(func(st *store.Store) (err error) {
		t, err = st.CloseUnstarted(id, startWindow)
		return err
	})
	var sw *store.StartedWork
	switch {
	case errors.As(err, &sw):
		return "Not closed: " + sw.Why + ". x closes it once you have looked"
	case errors.Is(err, store.ErrClosed):
		return "That track is closed already"
	case err != nil:
		return undoMiss(err)
	}
	if t.RexSession == "" {
		return "Closed " + t.Name + ", which had not started work"
	}
	ended, why, rerr := endSession(t.RexSession, t.Name, self)
	return closeNote(t.Name, ended, why, rerr)
}
