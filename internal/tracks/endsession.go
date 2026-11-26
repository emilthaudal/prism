package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// endSession ends a closed track's Rex session (the one its card names; agents often relabel
// it, so the label does not count) when nothing in it can be lost: it holds one pane besides
// self (the floated pane asking, "" from a shell), and that pane's agent is at rest, or it is
// a bare shell, or its process has exited (see keepReason). Anything else keeps the session
// and marks it done, and why says what kept it.
func endSession(sess, name, self string) (ended bool, why string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*cmdTimeout)
	defer cancel()
	id, _, err := resolveSessionCtx(ctx, sess)
	if errors.Is(err, errSessionNone) {
		return false, "it was already gone", nil
	}
	if err != nil {
		return false, "", err
	}
	if !namesSession(sess, id) {
		return false, "the card names it by too short an id", markSessionDone(id, name)
	}
	if live := liveTrackUsing(id); live != "" {
		return false, "track " + live + " uses it", markSessionDone(id, name)
	}
	// The track's own agent closing it, after Emil said yes: it is mid-turn, so the session
	// ends once that turn is over, under the same rules, from a process that outlives it.
	if self == "" && os.Getenv("REX_SESSION") == id {
		if err := markSessionDone(id, name); err != nil {
			return false, "", err
		}
		if err := startEndWhenIdle(id); err != nil {
			return false, "", fmt.Errorf("could not wait for its agent: %w", err)
		}
		return false, endsWhenIdle, nil
	}
	if why, err = keepReason(ctx, id, self); err != nil || why != "" {
		if err != nil {
			why = "could not read it: " + err.Error()
		}
		return false, why, markSessionDone(id, name)
	}
	if _, err := rexRun(ctx, "session", "kill", id); err != nil {
		return false, "", errors.Join(fmt.Errorf("could not end it: %w", err), markSessionDone(id, name))
	}
	return true, "", nil
}

// keepReason says why a session must stay, or "" when its one pane holds an agent at rest or
// a bare shell.
func keepReason(ctx context.Context, id, self string) (string, error) {
	b, err := rexRun(ctx, "block", "ls", "-s", id, "--json")
	if err != nil {
		return "", err
	}
	var l struct {
		Blocks []struct {
			ID string `json:"block_id"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return "", err
	}
	var blocks []string
	for _, blk := range l.Blocks {
		if blk.ID != self || self == "" {
			blocks = append(blocks, blk.ID)
		}
	}
	if len(blocks) != 1 {
		return fmt.Sprintf("it has %d panes open", len(blocks)), nil
	}
	blk := blocks[0]
	recs, err := agentRecords(ctx, id, blk)
	if err != nil {
		return "", err
	}
	// Any agent record that is not at rest keeps the session, whoever wrote it: a subagent's
	// error must not hide a working root, and a state Rex adds later counts as busy.
	for _, r := range recs {
		if !restStates[r.State] {
			if r.State == "" {
				return "its agent reports no state", nil
			}
			return "its agent is " + r.State, nil
		}
	}
	p, err := blockProcess(ctx, id, blk)
	if err != nil {
		return "", err
	}
	fg := p.Foreground
	switch {
	case fg == nil && p.Child == nil && p.LastExit != nil:
		return "", nil // the terminal's own process has exited; any other shape is unknown, so it keeps
	case fg == nil:
		return "Rex cannot tell what runs in it", nil // e.g. a pipeline whose first stage ended
	}
	name := fg.Name
	if name == "" {
		name = fg.Argv0
	}
	if name == "" {
		return "Rex cannot tell what runs in it", nil
	}
	if !isShell(name) {
		for _, r := range recs {
			if r.Owner.PID != 0 && r.Owner.PID == fg.PID && r.Owner.Name != "" && r.Owner.Name == fg.Name {
				return "", nil // the agent itself, at rest; a pid reused or exec'd into another program differs by name
			}
		}
		return "it runs " + name, nil
	}
	return shellNotBare(ctx, p)
}

// shellNotBare says why a pane whose foreground is a shell is not at its bare prompt, or "".
// A shell is the bare prompt only when it is the pane's outermost shell (a script's bash
// runs under one) and it has no children: no job stopped with ctrl-z or left in the background.
func shellNotBare(ctx context.Context, p blockProc) (string, error) {
	for _, a := range p.Ancestors {
		if isShell(a.Name) && a.Name != "login" { // every pane's shell runs under login
			return "it runs a shell script", nil
		}
	}
	kids, err := hasChildren(ctx, p.Foreground.PID)
	switch {
	case err != nil:
		return "", err
	case kids:
		return "its shell has a job running or stopped", nil
	}
	return "", nil
}

// restStates are the agent states in which nothing is in flight.
var restStates = map[string]bool{"idle": true, "done": true, "error": true}

type procInfo struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Argv0 string `json:"argv0"`
}

type blockProc struct {
	Foreground *procInfo  `json:"foreground"`
	Child      *procInfo  `json:"child"`
	Ancestors  []procInfo `json:"ancestors"`
	// LastExit is set once the terminal's own process has exited; a live one reports null.
	LastExit *json.RawMessage `json:"last_exit"`
}

// agentRecords are the block's Claude Code status records. A failed call or a reply that is
// not a record list is an error, never "no agent".
func agentRecords(ctx context.Context, id, blk string) ([]programRecord, error) {
	b, err := rexRun(ctx, "block", "call", "-s", id, "-b", blk, "program_status")
	if err != nil {
		return nil, err
	}
	var r struct {
		Records *[]programRecord `json:"records"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Records == nil {
		return nil, errors.New("program_status gave no record list")
	}
	var out []programRecord
	for _, rec := range *r.Records {
		if rec.EffectiveApp == claudeApp {
			out = append(out, rec)
		}
	}
	return out, nil
}

// blockProcess is Rex's view of the pane's processes. Any failure to read it is an error.
func blockProcess(ctx context.Context, id, blk string) (blockProc, error) {
	var p blockProc
	b, err := rexRun(ctx, "block", "call", "-s", id, "-b", blk, "process")
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

// hasChildren is whether process pid has child processes, asked of the local process table.
var hasChildren = func(ctx context.Context, pid int) (bool, error) {
	err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid)).Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil // pgrep found none
	}
	return false, fmt.Errorf("could not list the shell's jobs: %w", err)
}

// endsWhenIdle is endSession's why when the track's own agent closed it.
const endsWhenIdle = "it ends once nothing in it is running"

// startEndWhenIdle starts `tracks close --end-when-idle ID` in its own process session, so
// it outlives the agent and the pane that ran the close. It is a test seam.
var startEndWhenIdle = func(id string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "close", "--end-when-idle", id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// The waiter polls every endPoll for up to endWait; after that the session stays marked ✓
// for `tracks close --sweep`.
var (
	endPoll = 2 * time.Second
	endWait = 10 * time.Minute
)

// endWhenIdle ends session id once nothing in it can be lost (keepReason, two polls in a row,
// so a gap between two tool calls is not taken for the end of the turn). It acts only while
// an archived card names the session and no live card does, and gives up after endWait.
func endWhenIdle(id string) error {
	if !strings.HasPrefix(id, "session:") {
		return fmt.Errorf("not a session id: %q", id)
	}
	calm := 0
	// Wall-clock time (Round(0) drops the monotonic reading, which stops while the Mac sleeps),
	// so a waiter does not act long after the yes.
	for deadline := time.Now().Round(0).Add(endWait); time.Now().Round(0).Before(deadline); {
		time.Sleep(endPoll)
		if closedTrackOf(id) == "" {
			return nil // a live card names it again, or it was never a closed track's
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*cmdTimeout)
		why, err := keepReason(ctx, id, "")
		if err != nil || why != "" {
			calm = 0
			cancel()
			continue
		}
		if calm++; calm < 2 {
			cancel()
			continue
		}
		_, err = rexRun(ctx, "session", "kill", id)
		cancel()
		return err
	}
	return nil
}

// closeNote is the line that tells Emil what closing did to the session.
func closeNote(name string, ended bool, why string, err error) string {
	switch {
	case err != nil:
		return "Closed " + name + ", but could not update its Rex session: " + err.Error()
	case ended:
		return "Closed " + name + " and ended its Rex session"
	case why == endsWhenIdle:
		return "Closed " + name + "; its Rex session ends once nothing in it is running"
	case why != "":
		return "Closed " + name + "; kept its Rex session: " + why
	}
	return "Closed " + name
}

// minSessSuffix is the shortest rex_session suffix that counts: `rex ls` prints six characters.
const minSessSuffix = 6

// namesSession is whether a card's rex_session is session id: the full id, or a suffix of it
// at least as long as the short id `rex ls` prints.
func namesSession(sess, id string) bool {
	return sess != "" && (sess == id || len(sess) >= minSessSuffix && strings.HasSuffix(id, sess))
}

// sameSession is whether two card rex_session values can name one session: equal, or one the
// suffix of the other, as liveTrackUsing matches them. It only ever makes a close keep a session.
func sameSession(a, b string) bool {
	return a != "" && b != "" && (a == b || strings.HasSuffix(a, b) || strings.HasSuffix(b, a))
}

// liveTrackUsing is the name of a live track whose card names session id, "" when none does.
// Any suffix counts here, as it does for the pane and `tracks next`: this match only keeps.
func liveTrackUsing(id string) string {
	for _, c := range loadCards() {
		if s := c.Meta["rex_session"]; sameSession(s, id) {
			return c.Name
		}
	}
	return ""
}

// closedTrackOf is the name of the closed track whose archived card names session id, "" when
// none does or a live card names it too (the session is live work again).
func closedTrackOf(id string) string {
	if liveTrackUsing(id) != "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(archiveDir(), "*.md"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		c := parseCard("", string(data))
		if name := c.Meta["name"]; validName.MatchString(name) && namesSession(c.Meta["rex_session"], id) {
			return name
		}
	}
	return ""
}

// sweepSessions ends the sessions of tracks closed earlier: ✓ sessions that an archived card
// names and no live card does, under the same rules as closing. The label after ✓ does not
// count, since agents relabel their sessions. The session running the sweep is left alone.
func sweepSessions(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	b, err := rexRun(ctx, "session", "ls", "--json")
	cancel()
	if err != nil {
		return fmt.Errorf("could not list Rex sessions: %w", err)
	}
	var l struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	self := os.Getenv("REX_SESSION")
	n := 0
	for _, s := range l.Sessions {
		if !strings.HasPrefix(s.Label, doneMark) {
			continue
		}
		name := closedTrackOf(s.ID)
		if name == "" {
			continue
		}
		if s.ID == self {
			fmt.Fprintf(out, "kept %s: it runs this command\n", name)
			continue
		}
		ended, why, err := endSession(s.ID, name, "")
		switch {
		case err != nil:
			fmt.Fprintf(out, "kept %s: %v\n", name, err)
		case ended:
			fmt.Fprintf(out, "ended %s\n", name)
			n++
		default:
			fmt.Fprintf(out, "kept %s: %s\n", name, why)
		}
	}
	fmt.Fprintf(out, "ended %d of the closed tracks' sessions\n", n)
	return nil
}
