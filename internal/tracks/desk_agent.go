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
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	deskLabel = "desk"
	deskModel = "opus"
	// deskPaneRatio is the share of the desk's window, in percent, the tracks pane takes on the right.
	deskPaneRatio = 40
	// deskMark is what tracks send types in front of the desk's text. Like a lead's, the hook
	// counts such a prompt as the system's, not Emil's.
	deskMark = "[desk] "
)

// deskAllowed are the commands the desk's agent runs without a permission prompt: reading the
// desk and the tracks, waiting on them, writing its answers and log, sending to and asking
// tracks, starting tracks, reading their screens, moving them between modes, writing their cards, and closing and
// renaming them on Emil's word. Everything else goes through
// auto mode, as in any of Emil's sessions.
var deskAllowed = []string{
	"Bash(tracks desk:*)",
	"Bash(tracks wait:*)",
	"Bash(tracks ls:*)",
	"Bash(tracks ready:*)",
	"Bash(tracks send:*)",
	"Bash(tracks ask:*)",
	"Bash(tracks start:*)",
	"Bash(tracks screen:*)",
	"Bash(tracks mode:*)",
	"Bash(tracks card:*)",
	"Bash(tracks close:*)",
	"Bash(tracks rename:*)",
}

// deskDenied are the two tracks commands that would make the desk something it is not: tracks
// bind would turn it into a track, and tracks hook would record events no session sent.
// Everything else follows the same rules as any of Emil's sessions.
var deskDenied = []string{
	"Bash(tracks bind:*)",
	"Bash(tracks hook:*)",
}

// deskSettings are the Claude Code settings of the desk's agent, passed as JSON on its command
// line rather than as a file, so nothing in the writable state folder can change them while it
// runs. The desk runs in auto mode, like Emil's other sessions, and bypass mode stays disabled.
func deskSettings() (string, error) {
	b, err := json.Marshal(map[string]any{"permissions": map[string]any{
		"defaultMode":                  "auto",
		"disableBypassPermissionsMode": "disable",
		"allow":                        deskAllowed,
		"deny":                         deskDenied,
	}})
	return string(b), err
}

var errNoDeskAgent = errors.New("Rex reports no agent in the desk's session")

// deskAgentPID is the process of the agent Rex reports in the desk's session, errNoDeskAgent
// when Rex reports none; tests replace it.
var deskAgentPID = func(sess string) (int, error) {
	r := fetchSession(sess, agentBlock(readEvents(), sess), "")
	switch {
	case r.failed:
		return 0, errors.New("Rex did not answer")
	case !r.found || r.state.Exited:
		return 0, errNoDeskAgent
	case r.state.Owner <= 1:
		return 0, errors.New("Rex does not name the process of the desk's agent")
	}
	return r.state.Owner, nil
}

// runsUnder is whether pid is a parent, at any depth, of this process. pid 1 never counts.
var runsUnder = func(pid int) (bool, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return false, err
	}
	parent := map[int]int{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		p, err1 := strconv.Atoi(f[0])
		pp, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			parent[p] = pp
		}
	}
	for p, hops := os.Getppid(), 0; p > 1 && hops < len(parent)+1; p, hops = parent[p], hops+1 {
		if p == pid {
			return true, nil
		}
	}
	return false, nil
}

// callerSession is the desk session this process claims: its Rex session, or, for a process
// that carries a desk token, the session prism server binds the headless desk to.
func callerSession() string {
	if s := os.Getenv("REX_SESSION"); s != "" {
		return s
	}
	if os.Getenv(deskTokenEnv) != "" {
		return headlessDesk
	}
	return ""
}

// deskCallerRefusal says why this process may not act as the desk, or "" when it may. The desk
// is the agent tracks desk start opened: the caller must be in the desk's Rex session and be a
// child of the agent Rex reports there. $REX_SESSION alone is not enough, since any process can
// set it; the process tree is what another session's agent cannot fake. For the headless desk,
// the caller must present the secret serve gave the desk's running process.
func deskCallerRefusal(deskSess, caller string) string {
	switch {
	case deskSess == "":
		return "no desk is running: tracks desk start opens one"
	case caller == "":
		return "only the desk's agent acts as the desk, and this runs outside Rex"
	case caller != deskSess:
		return "the caller (Rex session " + caller + ") is not the desk's session"
	}
	if deskSess == headlessDesk {
		if err := proveDesk(); err != nil {
			return "could not confirm the headless desk: " + err.Error()
		}
		return ""
	}
	pid, err := deskAgentPID(deskSess)
	if err != nil {
		return "could not confirm the desk's agent: " + err.Error()
	}
	under, err := runsUnder(pid)
	switch {
	case err != nil:
		return "could not read this process's parents: " + err.Error()
	case !under:
		return fmt.Sprintf("this does not run under the desk's agent (pid %d), so it cannot act as the desk", pid)
	}
	return ""
}

// deskDir is where the desk's agent starts: always ~/Developer, never a folder the caller names,
// so no track can start the desk in its own worktree under that folder's CLAUDE.md and settings.
func deskDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Developer"), nil
}

// deskArgv is the desk's command. The desk runs the tracks that started it: the shell puts this
// binary's folder first on the PATH the session's login shell set up, as a thread's Stop hook
// names the tracks that spawned it. TRACKS_HOME and TRACKS_DESK_IDLE go along, so the desk
// reads the same store and judges stalls as the pane that started it.
func deskArgv(settings string) ([]string, error) {
	return asTheDesk("claude", "--model", deskModel, "--name", deskLabel, "--settings", settings, "/desk")
}

// deskPaneArgv is the tracks pane beside the desk, run with the same tracks and store as the desk.
func deskPaneArgv() ([]string, error) { return asTheDesk("tracks", "--home") }

// asTheDesk is argv run with this binary's folder first on the PATH and the desk's environment.
func asTheDesk(argv ...string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	out := []string{"sh", "-c", `export PATH="$0:$PATH"; exec "$@"`, filepath.Dir(exe), "env"}
	for _, k := range []string{"TRACKS_HOME", deskIdleEnv} {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return append(out, argv...), nil
}

// splitDeskPane opens the tracks pane on the right of the desk's agent, without taking focus
// from the agent.
func splitDeskPane(sid, block string) error {
	if block == "" {
		return errors.New("Rex did not name the desk's block")
	}
	argv, err := deskPaneArgv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
	defer cancel()
	args := append([]string{"split", "-s", sid, "-b", block, "--split=right", "--ratio=" + strconv.Itoa(deskPaneRatio),
		"--focus=false", "--interactive", "--label=tracks", "--json", "--"}, argv...)
	_, err = rexRun(ctx, args...)
	return err
}

// lockDesk holds desk.lock in the tracks state directory until unlock, so two desk starts
// cannot both find no desk and open one each.
func lockDesk() (unlock func(), err error) {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(home(), "desk.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("refused: another tracks desk start is running")
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// deskStart is tracks desk start: the desk's agent in a Rex session of its own, bound as the
// desk. A desk whose agent still runs is left alone; one whose agent is gone is replaced.
func deskStart(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New(deskUsage)
	}
	if sess := os.Getenv("REX_SESSION"); sess != "" {
		cards, err := openCards()
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(cards, func(c *Card) bool { return c.Meta["rex_session"] == sess }); i >= 0 {
			return fmt.Errorf("refused: Rex session %s runs the agent of %s, and a track does not start the desk", sess, cards[i].Name)
		}
	}
	_, _, err := startDesk(out)
	return err
}

// startDesk starts the desk's agent and returns its Rex session and block. It is deskStart
// without the caller check, for the pane's d: the floating pane runs in whatever session Emil
// floats it over, a track's included, and a key Emil presses is not a track starting the desk.
func startDesk(out io.Writer) (sid, block string, err error) {
	dir, err := deskDir()
	if err != nil {
		return "", "", err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("the desk starts in %s, which is not a directory", dir)
	}
	if why := noTrustedFolder(dir); why != "" {
		return "", "", errors.New("refused: " + why)
	}
	unlock, err := lockDesk()
	if err != nil {
		return "", "", err
	}
	defer unlock()
	cur, err := deskSessionNow()
	if err != nil {
		return "", "", err
	}
	if cur != "" {
		r := fetchSession(cur, "", "")
		switch {
		case r.failed:
			return "", "", fmt.Errorf("refused: could not ask Rex whether the desk in %s still runs, so no second one was started", cur)
		case r.found && !r.state.Exited:
			return "", "", fmt.Errorf("refused: the desk already runs in Rex session %s: d in the pane opens it", cur)
		}
	}
	settings, err := deskSettings()
	if err != nil {
		return "", "", err
	}
	argv, err := deskArgv(settings)
	if err != nil {
		return "", "", err
	}
	sid, block, err = rexNewSession(trackSpec{name: deskLabel, dir: dir, argv: argv})
	if err != nil {
		return "", "", fmt.Errorf("could not start the desk's Rex session: %w", err)
	}
	// Bound before the agent's first turn, which already writes as the desk.
	if err := withStore(func(st *store.Store) error { return st.BindDesk(sid, true) }); err != nil {
		return "", "", fmt.Errorf("started %s but could not bind it as the desk, so end that session: %w", sid, err)
	}
	func() {
		ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
		defer cancel()
		moveToTop(ctx, sid) // the desk is always first in the sidebar
	}()
	paneErr := splitDeskPane(sid, block)
	dialog, started := watchStart(sid, block)
	if dialog != "" {
		ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
		defer cancel()
		_, kerr := rexRun(ctx, "session", "kill", sid)
		why := fmt.Sprintf("refused: Claude stopped the desk in %s at a dialog nobody can answer: %q. Open claude in %s once and answer it, then start the desk again", dir, dialog, dir)
		if kerr != nil {
			return "", "", fmt.Errorf("%s. Ending the desk's session %s failed, so end it by hand: %w", why, sid, kerr)
		}
		return "", "", errors.New(why)
	}
	if !started {
		fmt.Fprintf(out, "the desk runs in Rex session %s, but its agent did not show it had started within %s: look at that session\n", sid, startWait)
	} else {
		fmt.Fprintf(out, "the desk runs in Rex session %s\n", sid)
	}
	if paneErr != nil {
		fmt.Fprintf(out, "the tracks pane did not open beside it: %v\n", paneErr)
	}
	return sid, block, nil
}

// deskSendRefusal says why the desk may not type into track, or "" when it may: the track is
// open, its agent has a session, and that session is not the desk's own.
func deskSendRefusal(track, deskSess string) string {
	v, err := getView(track)
	switch {
	case err != nil:
		return err.Error()
	case v.Track.RexSession == "":
		return track + " has no rex_session"
	case v.Track.RexSession == deskSess:
		return track + " runs in the desk's own session"
	}
	return ""
}
