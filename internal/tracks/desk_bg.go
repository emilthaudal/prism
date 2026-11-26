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
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	defaultMaxDesk = 4
	maxDeskEnv     = "TRACKS_MAX_DESK"
	// bgTimeout bounds claude --bg, which returns once the background service took the session.
	bgTimeout = time.Minute
	// agentsWait is how long a start looks for its new session in claude agents --json.
	agentsWait = 5 * time.Second
	agentsPoll = 250 * time.Millisecond
	// sessionHookTimeout is the seconds Claude Code gives tracks hook, as in Emil's settings.
	sessionHookTimeout = 2
)

// backgrounded is the line claude --bg prints for the session it started: its short id and name.
var backgrounded = regexp.MustCompile(`(?m)^backgrounded · (\S+) · `)

// sessionHookEvents are the events tracks hook keeps a session's row current from.
var sessionHookEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest", "SessionEnd", "PreToolUse", "SubagentStart", "SubagentStop"}

// deskParentEnv and deskPromptEnv tell the hook of a session the desk started its parent and
// its start prompt.
const (
	deskParentEnv = "TRACKS_DESK_PARENT"
	deskPromptEnv = "TRACKS_DESK_PROMPT"
)

// deskOnlyEnv is the desk's environment that would make claude --bg, or a background service
// it starts, look like the desk's own session.
var deskOnlyEnv = []string{"REX_SESSION", "REX_BLOCK", "REX_SERVER", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "CLAUDECODE", deskTokenEnv}

// maxDesk is how many sessions the desk may have working or blocked at once: TRACKS_MAX_DESK, else 4.
func maxDesk() (int, error) {
	v := os.Getenv(maxDeskEnv)
	if v == "" {
		return defaultMaxDesk, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a whole number above 0: %q", maxDeskEnv, v)
	}
	return n, nil
}

// deskParent is the desk's own Claude session, which Claude Code names to the commands its agent runs.
func deskParent() (string, error) {
	id := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if !claudeSessionID.MatchString(id) && callerSession() == headlessDesk {
		if err := withBareStore(func(st *store.Store) (err error) { id, err = st.DeskClaude(); return err }); err != nil {
			return "", err
		}
	}
	if !claudeSessionID.MatchString(id) {
		return "", errors.New("refused: CLAUDE_CODE_SESSION_ID does not name the desk's Claude session")
	}
	return id, nil
}

// installedTracks is the tracks Emil's global hook runs.
func installedTracks() string { return filepath.Join(userHome(), ".local", "bin", "tracks") }

// writeDeskSettings writes the settings of a desk session on track name. Claude's background
// service starts a session with the environment of whichever claude --bg started the service,
// so the settings give it the environment it needs: this tracks first on PATH, this home, and
// no Rex session, since it has none. tracks verify is its Stop hook, as tracks spawn gives a
// thread, tracks permission its PermissionRequest hook and tracks elicitation its Elicitation
// hook; when this tracks is not the one
// Emil's global hook runs, tracks hook is there too. leanSettings cut its start-up context.
func writeDeskSettings(name, exe, dir, parent, prompt string) (string, error) {
	cmd := func(command string, timeout int) any {
		return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": timeout}}}
	}
	hooks := map[string][]any{
		"Stop":              {cmd(shellJoin([]string{exe, "verify", name}), int((defaultVerifyTimeout + hookSlack).Seconds()))},
		"PermissionRequest": {cmd(shellJoin([]string{exe, "permission"}), permissionHookTimeout)},
		"Elicitation":       {cmd(shellJoin([]string{exe, "elicitation"}), elicitationHookTimeout)},
	}
	if exe != installedTracks() {
		for _, ev := range sessionHookEvents {
			hooks[ev] = append(hooks[ev], cmd(shellJoin([]string{exe, "hook"}), sessionHookTimeout))
		}
	}
	env := map[string]string{
		"PATH":        filepath.Dir(exe) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TRACKS_HOME": os.Getenv("TRACKS_HOME"),
		"REX_SESSION": "",
		"REX_BLOCK":   "",
		deskParentEnv: parent,
		deskPromptEnv: prompt,
	}
	doc := leanSettings(dir)
	doc["env"], doc["hooks"] = env, hooks
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	path := threadSettingsPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, writeAtomic(path, append(b, '\n'))
}

// claudeAgent is one entry of claude agents --json.
type claudeAgent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	// PID is the session's process, 0 once it exited: claude agents keeps listing a stopped
	// background session, with no pid, so that it can be resumed.
	PID int `json:"pid"`
	// Status is the process's own: busy in a turn, idle between turns.
	Status string `json:"status"`
}

func claudeAgents() ([]claudeAgent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentsTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "agents", "--json").Output()
	if err != nil {
		return nil, err
	}
	var list []claudeAgent
	return list, json.Unmarshal(out, &list)
}

// fullSessionID is the session id claude agents --json lists for the short id claude --bg printed.
func fullSessionID(short string) (string, error) {
	deadline := time.Now().Add(agentsWait)
	for {
		list, err := claudeAgents()
		if err == nil {
			if i := slices.IndexFunc(list, func(a claudeAgent) bool { return a.ID == short }); i >= 0 && claudeSessionID.MatchString(list[i].SessionID) {
				return list[i].SessionID, nil
			}
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("it is not listed")
			}
			return "", fmt.Errorf("claude agents --json: %w", err)
		}
		time.Sleep(agentsPoll)
	}
}

// bgStart is a desk session to start: the track, its folder, the model and the parent session.
type bgStart struct {
	track, dir, model, parent, prompt string
}

// startBackground starts a Claude session in the background on a track whose card exists, and
// records it as the desk's. err from claude itself is its own words, for the refusal.
func startBackground(s bgStart) (id string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	settings, err := writeDeskSettings(s.track, exe, s.dir, s.parent, s.prompt)
	if err != nil {
		return "", err
	}
	args := []string{"--bg", "-n", s.track, "--settings", settings}
	args = append(args, modelArgs(s.model)...)
	ctx, cancel := context.WithTimeout(context.Background(), bgTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", append(args, s.prompt)...)
	cmd.Dir = s.dir
	cmd.Env = withoutDeskEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(settings)
		if msg := oneLine(string(out)); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	m := backgrounded.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("claude --bg did not say which session it started: %s", oneLine(string(out)))
	}
	short := string(m[1])
	if id, err = fullSessionID(short); err != nil {
		return "", fmt.Errorf("started background session %s, but could not read its full id (%w): stop it with claude stop %s", short, err, short)
	}
	err = withBareStore(func(st *store.Store) error {
		return st.StartDeskSession(store.DeskStart{ID: id, Parent: s.parent, Track: s.track, Name: s.track, Cwd: s.dir, Model: s.model, Prompt: s.prompt})
	})
	if err != nil {
		return id, fmt.Errorf("started background session %s, but could not record it: %w", short, err)
	}
	return id, nil
}

// trackPrompt is what a track's agent starts with.
func trackPrompt(name string) string { return "/track " + name }

// deskStartCmd is tracks start run by the desk's agent: the card is made, then its session starts
// in the background, owned by the desk, or waits in the queue while the desk runs as many
// sessions as it may or other starts wait.
func deskStartCmd(goal, dir, model, named, mode string, loop *loopState, out io.Writer) error {
	max, err := maxDesk()
	if err != nil {
		return err
	}
	parent, err := deskParent()
	if err != nil {
		return err
	}
	var active int
	var queued []store.QueuedStart
	err = withBareStore(func(st *store.Store) (err error) {
		if active, err = st.DeskActive(); err == nil {
			queued, err = st.QueuedDeskStarts()
		}
		return err
	})
	if err != nil {
		return err
	}
	name := named
	if named != "" {
		err = newNamedTrack(named, goal, dir, mode)
	} else {
		name, err = newRequestTrack(goal, dir, mode)
	}
	if err != nil {
		return err
	}
	prompt := trackPrompt(name)
	if loop == nil {
		dropLoopFiles(name)
	} else {
		os.Remove(loopStopPath(name))
		loop.Track = name
		if err := writeLoop(*loop); err != nil {
			return takeBackStart(name, fmt.Errorf("could not write the loop's state: %w", err))
		}
		prompt = loopPrompt(name, *loop)
	}
	if active >= max || len(queued) > 0 {
		var ahead int
		err := withBareStore(func(st *store.Store) (err error) {
			ahead, err = st.QueueDeskStart(name, parent, model, prompt)
			return err
		})
		if err != nil {
			return fmt.Errorf("made the card %s but could not queue its start (card kept): %w", name, err)
		}
		fmt.Fprintf(out, "queued %s (%d ahead)\n", name, ahead)
		return logLoopStart(loop)
	}
	id, err := startBackground(bgStart{track: name, dir: dir, model: model, parent: parent, prompt: prompt})
	if err != nil && id == "" {
		return takeBackStart(name, err)
	}
	if loop != nil {
		setLoopSession(name, id)
	}
	logErr := logDeskStart(name, goal, dir, id)
	if logErr == nil {
		logErr = logLoopStart(loop)
	}
	if err != nil {
		return err
	}
	if logErr != nil {
		return logErr
	}
	fmt.Fprintf(out, "started %s in the background as %s\n", name, shortID(id))
	return nil
}

// takeBackStart removes the card of a start whose session never started, so nothing is left.
func takeBackStart(name string, why error) error {
	derr := withStore(func(st *store.Store) error { return st.DeleteUnstarted(name) })
	if derr == nil {
		if err := os.Remove(cardPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			derr = err
		}
	}
	os.Remove(threadSettingsPath(name))
	os.Remove(loopPath(name))
	if derr != nil {
		return fmt.Errorf("refused: %w. Removing %s's card failed, so close it by hand: %v", why, name, derr)
	}
	return fmt.Errorf("refused: %w", why)
}

func logDeskStart(name, goal, dir, id string) error {
	return withStore(func(st *store.Store) error {
		_, err := st.LogDesk(name, "started", goal, "started "+name+" in "+tildePath(dir)+" in the background ("+shortID(id)+")")
		return err
	})
}

// drainQueue starts queued desk starts, oldest first, while the desk has free slots.
func drainQueue() []string {
	max, err := maxDesk()
	if err != nil {
		return []string{"tracks wait: " + err.Error()}
	}
	var active int
	var queued []store.QueuedStart
	err = withBareStore(func(st *store.Store) (err error) {
		if active, err = st.DeskActive(); err == nil {
			queued, err = st.QueuedDeskStarts()
		}
		return err
	})
	if err != nil {
		return []string{"tracks wait: could not read the start queue: " + oneLine(err.Error())}
	}
	var lines []string
	for _, q := range queued {
		if q.Held {
			if err := withBareStore(func(st *store.Store) error { return st.DropDeskStart(q.ID) }); err == nil {
				lines = append(lines, q.Track+" not started from the queue: Emil has it")
			}
			continue
		}
		if active >= max {
			break
		}
		var took bool
		if err := withBareStore(func(st *store.Store) (err error) { took, err = st.TakeDeskStart(q.ID); return err }); err != nil || !took {
			continue
		}
		id, err := startBackground(bgStart{track: q.Track, dir: q.Dir, model: q.Model, parent: q.Parent, prompt: q.Prompt})
		if err != nil && id == "" {
			lines = append(lines, q.Track+" not started from the queue: "+oneLine(takeBackStart(q.Track, err).Error()))
			continue
		}
		active++
		setLoopSession(q.Track, id)
		if lerr := logDeskStart(q.Track, q.Goal, q.Dir, id); err == nil {
			err = lerr
		}
		if err != nil {
			lines = append(lines, q.Track+" started from the queue, but: "+oneLine(err.Error()))
			continue
		}
		lines = append(lines, q.Track+" started from the queue as "+shortID(id))
	}
	return lines
}

// deskSeen is what wait --desk last saw of a session.
type deskSeen struct{ status, host, owner string }

// sessionLabel is how a wait line names a session: its track, else its name, else its id.
func sessionLabel(track, name, id string) string {
	switch {
	case track != "":
		return track
	case name != "":
		return name
	}
	return shortID(id)
}

// deskEvent is one thing the desk hears about its sessions: the line it reads, and for a report
// or a status change the session, its outcome and its text. An interim report is one from a
// session that is still working or waiting on its own subagents; track is its track, "" for none.
type deskEvent struct {
	line, session, outcome, text, track string
	interim                             bool
}

// pollDesk is the desk's watch: the events of its sessions (see deskEvents), then the answers to
// the questions it raised, then the starts it made from the queue.
func pollDesk(seen map[string]deskSeen, first bool) ([]string, error) {
	evs, err := deskEvents(seen, first)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, e := range evs {
		lines = append(lines, e.line)
	}
	answered, err := answeredLines()
	if err != nil {
		return nil, err
	}
	return append(append(lines, answered...), drainQueue()...), nil
}

// deskEvents delivers the held notes of desk sessions that stopped working, reads the reports
// not delivered yet from the desk's sessions and marks them delivered, then a line per desk
// session that turned done, blocked or error, or stopped, and once for one Emil took over, and
// last each question a desk session asks. A session that asks a question reports nothing else
// meanwhile, and one a note was just delivered to does not report the stop that delivery
// made. The first poll only records the sessions' state. Sessions Emil owns are never
// reported, and neither is the headless desk's own session.
func deskEvents(seen map[string]deskSeen, first bool) ([]deskEvent, error) {
	var (
		reports   []store.Report
		list      []store.Session
		agents    []store.Subagent
		questions []store.Question
		noted     map[string]bool
		self      string
	)
	// A session whose process is gone, or whose turn a usage limit cut off, must not stay
	// working: its held notes would never go out (EMI-426).
	reapDue()
	now := time.Now()
	err := withBareStore(func(st *store.Store) (err error) {
		if reports, err = st.DeskReports(); err != nil {
			return err
		}
		if list, err = st.DeskSessions(); err != nil {
			return err
		}
		if agents, err = st.Subagents(); err != nil {
			return err
		}
		if questions, err = st.DeskQuestions(now.Add(-closedQuiet)); err != nil {
			return err
		}
		if noted, err = st.NotedSessions(now.Add(-notedQuiet)); err != nil {
			return err
		}
		self, err = st.DeskClaude()
		return err
	})
	if err != nil {
		return nil, err
	}
	asking := map[string]bool{}
	for _, q := range questions {
		if q.Open() {
			asking[q.SessionID] = true
		}
	}
	held, err := deliverHeld(seen)
	if err != nil {
		return nil, err
	}
	var lines []deskEvent
	for _, l := range held {
		lines = append(lines, deskEvent{line: l})
	}
	reported := map[string]bool{}
	var ids []int64
	for _, r := range reports {
		state := "reported"
		if i := slices.IndexFunc(list, func(s store.Session) bool { return s.ID == r.From }); i >= 0 && (list[i].Status == "done" || list[i].Status == "blocked") {
			state = list[i].Status
		}
		text := oneLine(r.Text)
		if text == "" {
			text = "no message"
		}
		reported[r.From] = true
		ids = append(ids, r.ID)
		if !asking[r.From] {
			interim := state != "blocked" && slices.ContainsFunc(list, func(s store.Session) bool {
				return s.ID == r.From && (s.Status == "working" || state == "done" && slices.ContainsFunc(agents, func(a store.Subagent) bool { return a.Session == s.ID && a.Status == "working" }))
			})
			lines = append(lines, deskEvent{fmt.Sprintf("%s %s: %s", sessionLabel(r.Track, r.Name, r.From), state, text), r.From, state, r.Text, r.Track, interim})
		}
	}
	for _, s := range list {
		if s.ID == self {
			continue
		}
		old, known := seen[s.ID]
		seen[s.ID] = deskSeen{s.Status, s.Host, s.Owner}
		label := sessionLabel(s.Track, s.Name, s.ID)
		switch {
		case s.Owner != "desk":
			if known && old.owner == "desk" {
				lines = append(lines, deskEvent{line: label + " is Emil's now: he typed into it, so leave it to him"})
			}
		case first || reported[s.ID]:
		case s.Host == "stopped" && (noted[s.ID] || compactMarked(s.ID)):
		case s.Host == "stopped" && old.host != "stopped":
			lines = append(lines, deskEvent{line: label + " stopped", session: s.ID, outcome: "stopped"})
		case s.Status == "blocked" && asking[s.ID]:
		case s.Status != old.status && (s.Status == "done" || s.Status == "blocked" || s.Status == "error"):
			note := s.WaitingFor
			if note == "" {
				note = s.LastAnswer
			}
			if note = oneLine(note); note == "" {
				note = "no message"
			}
			lines = append(lines, deskEvent{line: fmt.Sprintf("%s %s: %s", label, s.Status, note), session: s.ID, outcome: s.Status, text: note})
		}
	}
	if err := withBareStore(func(st *store.Store) error { return st.DeliverReports(ids) }); err != nil {
		return nil, err
	}
	lines = loopEvents(lines, list, first, noted)
	for _, l := range questionLines(questions, seen) {
		lines = append(lines, deskEvent{line: l})
	}
	return lines, nil
}

// waitDeskCmd is tracks wait --desk: the desk's watch over the sessions it owns. Without
// --follow it returns after the first poll that prints; with it, it keeps watching, and a
// failed poll is a line rather than the end.
func waitDeskCmd(args []string, out, errw io.Writer) error {
	follow := slices.Contains(args, "--follow")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--follow" })
	pos, fl, err := leadFlags(args, "--timeout")
	if err != nil || len(pos) != 0 {
		return errors.New(waitUsage)
	}
	timeout, err := durationFlag(fl, "--timeout")
	if err != nil {
		return err
	}
	if !storeOn() {
		return errors.New("tracks wait " + needsStore)
	}
	start := time.Now()
	seen := map[string]deskSeen{}
	first := true
	lastErr := ""
	for {
		lines, err := pollDesk(seen, first)
		switch {
		case err != nil && !follow:
			return err
		case err != nil:
			if msg := "tracks wait: " + oneLine(err.Error()) + ", still watching"; msg != lastErr {
				fmt.Fprintln(out, msg)
				lastErr = msg
			}
		default:
			first, lastErr = false, ""
			for _, l := range lines {
				fmt.Fprintln(out, l)
			}
			if len(lines) > 0 && !follow {
				return nil
			}
		}
		if timeout > 0 && time.Since(start) >= timeout {
			if follow {
				fmt.Fprintf(errw, "tracks: stopped watching after %s\n", timeout)
			} else {
				fmt.Fprintf(errw, "tracks: no change in the desk's sessions within %s\n", timeout)
			}
			return exitTimedOut
		}
		time.Sleep(waitPoll)
	}
}
