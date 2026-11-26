package tracks

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	deskModelEnv = "TRACKS_DESK_MODEL"
	// deskTokenEnv carries the secret serve gives each run of the desk's process: a command that
	// presents it to serve is the desk's.
	deskTokenEnv = "TRACKS_DESK_TOKEN"
	// headlessDesk is the desk's session in the desk table while prism server runs it.
	headlessDesk = "tracks-serve"
	// deskFirst is the headless desk's first message: the desk skill, told it runs headless.
	deskFirst = "/desk headless"
	// deskWatch is how often serve does the desk's watch: reports, status changes, the start queue.
	deskWatch = time.Second
	// deskStopWait is how long the desk has to exit after SIGTERM before it is killed.
	deskStopWait = 5 * time.Second
)

// Backoff between restarts of a desk process that died; tests shorten it.
var (
	deskBackoffMin = time.Second
	deskBackoffMax = time.Minute
)

// headlessEnvDrop is what the desk's process must not inherit: Rex's session, and the Claude
// session of whoever started serve.
var headlessEnvDrop = []string{"REX_SESSION", "REX_BLOCK", "REX_SERVER", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", deskTokenEnv}

// deskRunner runs the desk as claude in stream-json mode and decides when it gets a turn: what
// is bound for it queues while it is in a turn and goes in as one message after the turn's result.
type deskRunner struct {
	mu      sync.Mutex
	errw    io.Writer
	stdin   io.WriteCloser
	alive   bool
	busy    bool
	failed  bool
	session string
	emil    []store.Entry
	events  []string
	reply   *store.Entry
	seen    map[string]deskSeen
	first   bool
	version int
	changed func()
	// token is the secret of the desk's running process, "" while none runs.
	token string
	// notice is prism server's own line for the desk's next turn (reaskNotice), "" for none;
	// reasked is whether the turn in progress follows one.
	notice  string
	reasked bool
	// gen counts the desk's processes; a line from an older one is ignored. stop ends the
	// running process, nil while none runs.
	gen  int
	stop context.CancelFunc
	// restarting is a restart under way: the process it ended is not a failure and needs no
	// backoff. restartAfter is the desk's own restart, done once its turn's result comes.
	restarting, restartAfter bool
	// since is when the desk's session started, turns how many turns it had in this process,
	// and nudged whether it was told its context is long.
	since  time.Time
	turns  int
	nudged bool
	// ended are desk sessions a restart ended, for the watch to mark seen.
	ended []string
	// context is the tokens of the desk's last call; compacting is a /compact under way.
	context    int
	compacting bool // interim holds the labels of interim reports the server logged without waking the desk,
	// since interimAt; interimWake after that, one line tells the desk about them.
	interim   []string
	interimAt time.Time
}

// interimWake is how long interim reports gather in the handled log before one line wakes the desk.
var interimWake = 30 * time.Minute

// interimDid is the handled-log line for an interim report: the session's own first line.
func interimDid(label, text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(cleanText(text)), "\n")
	if first = sanitizeAnswer(first); first == "" {
		first = "no message"
	}
	return label + " reported, still working: " + first
}

// The nudge: past either, the desk's next batch tells it its context is long.
var (
	deskLongAge   = 12 * time.Hour
	deskLongTurns = 150
)

// deskLongNotice is prism server's line in the desk's next batch once its session runs long.
const deskLongNotice = "prism server: your context is long (%s): hand off and restart, as the desk skill's \"Keep your own context short\" says."

// errDeskBusy refuses a restart while the desk is in a turn.
var errDeskBusy = errorf(409, "the desk is in a turn: restart it when it is idle, so the turn's work isn't cut off")

// restart ends the desk's session and starts a fresh one; what is queued for the desk waits and
// goes to the new one after its first turn. Emil's restart is refused while the desk is in a
// turn. The desk's own (byDesk) is in a turn by definition, so it runs once that turn's result
// comes. It says what happened, for the caller.
func (r *deskRunner) restart(byDesk bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.restarting || r.restartAfter:
		return "the desk is restarting already", nil
	case r.busy && byDesk:
		r.restartAfter = true
		r.logf("the desk asked to restart; restarting after its turn")
		return "the desk restarts once this turn ends", nil
	case r.busy:
		return "", errDeskBusy
	}
	r.restartNow()
	return "the desk is restarting", nil
}

// restartNow ends the desk's session: forgets it in the store, so neither this run nor the next
// prism server resumes it, and stops its process. Callers hold r.mu.
func (r *deskRunner) restartNow() {
	old := r.session
	if err := withBareStore(func(st *store.Store) error { return st.EndDeskSession(old) }); err != nil {
		r.logf("ending the desk's session: %v", err)
	}
	if old != "" {
		// The old session's row ends now; it is no news for the new desk. The watch goroutine
		// owns r.seen, so it marks it seen at its next pass.
		r.ended = append(r.ended, old)
	}
	r.session, r.restartAfter, r.nudged, r.turns, r.since = "", false, false, 0, time.Time{}
	r.context, r.compacting = 0, false
	r.notice, r.reasked = "", false
	r.restarting = true
	r.gen++
	r.alive, r.busy, r.reply, r.token = false, false, nil, ""
	if r.stop != nil {
		r.stop()
	}
	r.logf("restarting the desk: ended session %s", old)
	r.bump()
}

// longNotice is the nudge for the desk's next batch, "" while its session is short or it was
// nudged already. Callers hold r.mu.
func (r *deskRunner) longNotice(now time.Time) string {
	if r.nudged || r.session == "" {
		return ""
	}
	age := time.Duration(0)
	if !r.since.IsZero() {
		age = now.Sub(r.since)
	}
	if age < deskLongAge && r.turns < deskLongTurns {
		return ""
	}
	r.nudged = true
	return fmt.Sprintf(deskLongNotice, fmt.Sprintf("%dh old, %d turns since prism server started it", int(age.Hours()), r.turns))
}

// isDesk is whether token is the running desk's secret.
func (r *deskRunner) isDesk(token string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(r.token)) == 1
}

func newDeskRunner(errw io.Writer) *deskRunner {
	return &deskRunner{errw: errw, seen: map[string]deskSeen{}, first: true, changed: func() {}}
}

// state is the desk's status for the snapshot and how many messages wait for its next turn.
func (r *deskRunner) state() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := "idle"
	switch {
	case r.failed || !r.alive:
		status = "error"
	case r.busy:
		status = "working"
	}
	return status, len(r.emil) + len(r.events)
}

func (r *deskRunner) stamp() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("desk:%d;", r.version)
}

// bump notes a change of the desk's state; callers hold r.mu.
func (r *deskRunner) bump() {
	r.version++
	go r.changed()
}

func (r *deskRunner) logf(format string, a ...any) {
	fmt.Fprintf(r.errw, "prism server: "+format+"\n", a...)
}

// post is Emil's message to the desk: in the conversation at once, given to the desk at its next turn.
func (r *deskRunner) post(text string) (store.Entry, error) {
	var e store.Entry
	err := withBareStore(func(st *store.Store) (err error) {
		e, err = st.AddEntry(store.Entry{Role: store.RoleEmil, Text: strings.TrimSpace(text)})
		return err
	})
	if err != nil {
		return e, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emil = append(r.emil, e)
	r.bump()
	r.flush()
	return e, nil
}

// event queues a line for the desk's next turn.
func (r *deskRunner) event(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, line)
	r.bump()
	r.flush()
}

// batch is the next message for the desk: prism server's own notice, Emil's messages, then the
// events, each on one line after "- ", so no event can pass for a line of Emil's or the server's.
func batch(notice string, emil []store.Entry, events []string) string {
	var b strings.Builder
	if notice != "" {
		b.WriteString(notice + "\n")
	}
	for _, e := range emil {
		b.WriteString("Emil: " + e.Text + "\n")
	}
	if len(events) > 0 {
		b.WriteString("Events:\n")
		for _, l := range events {
			b.WriteString("- " + oneLine(l) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func userMessage(text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	return append(b, '\n')
}

// flush starts the desk's next turn with everything queued, unless it is in a turn or not
// running. Callers hold r.mu.
func (r *deskRunner) flush() {
	if !r.alive || r.busy || (len(r.emil) == 0 && len(r.events) == 0 && r.notice == "") {
		return
	}
	notice := r.notice
	if long := r.longNotice(time.Now()); long != "" {
		notice = joinText(notice, long)
	}
	r.send(batch(notice, r.emil, r.events))
	if !r.busy {
		return
	}
	r.notice = ""
	var ids []int64
	for _, e := range r.emil {
		ids = append(ids, e.ID)
	}
	if err := withBareStore(func(st *store.Store) error { return st.DeliverEntries(ids) }); err != nil {
		r.logf("marking Emil's messages delivered: %v", err)
	}
	r.emil, r.events = nil, nil
}

// send writes one turn to the desk's stdin. Callers hold r.mu.
func (r *deskRunner) send(text string) {
	if _, err := r.stdin.Write(userMessage(text)); err != nil {
		r.logf("writing to the desk: %v", err)
		return
	}
	r.busy = true
	r.turns++
	r.bump()
	r.recordSession("working")
}

// recordSession writes the desk's session row with its status. Callers hold r.mu.
func (r *deskRunner) recordSession(status string) {
	if r.session == "" {
		return
	}
	id := r.session
	if err := withBareStore(func(st *store.Store) error { return st.SetDeskSession(id, status) }); err != nil {
		r.logf("recording the desk's session: %v", err)
	}
}

func deskModelName() string {
	if m := os.Getenv(deskModelEnv); m != "" {
		return m
	}
	return deskModel
}

// handOffPrefix starts the entry a desk writes before it restarts (desk skill, "Keep your own
// context short").
const handOffPrefix = "HAND-OFF"

// firstPrompt is a fresh desk's first message: /desk headless, and the newest hand-off in the
// handled log, so a new desk has it even if it skips the skill's read of it.
func firstPrompt(logf func(string, ...any)) string {
	var log []store.DeskLogEntry
	if err := withBareStore(func(st *store.Store) (err error) { log, err = st.DeskLogRecent(0); return err }); err != nil {
		logf("reading the desk's hand-off: %v", err)
		return deskFirst
	}
	for _, e := range log {
		if e.UndoneAt.IsZero() && strings.HasPrefix(strings.TrimSpace(e.Did), handOffPrefix) {
			return fmt.Sprintf("%s\n\nThe newest hand-off in your handled log (entry %d, written %s). Act on it after the usual reads:\n%s",
				deskFirst, e.ID, e.At.Local().Format("2 Jan 15:04"), strings.TrimSpace(e.Did))
		}
	}
	return deskFirst
}

// headlessArgv is the desk's command: Claude Code in stream-json mode, resuming its session when it has one.
func headlessArgv(session string) ([]string, error) {
	settings, err := deskSettings()
	if err != nil {
		return nil, err
	}
	args := []string{"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--model", deskModelName(), "--settings", settings}
	if session != "" {
		args = append(args, "--resume", session)
	}
	return args, nil
}

func headlessEnv(token string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(headlessEnvDrop, k) || k == "PATH" || k == "TRACKS_HOME"
	})
	return append(env, deskTokenEnv+"="+token, "TRACKS_HOME="+home(),
		"PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH")), nil
}

// run keeps the desk running until ctx ends: a process that dies is started again, resuming
// the same session, after a backoff that grows while it keeps dying.
func (r *deskRunner) run(ctx context.Context) {
	var session string
	if err := withBareStore(func(st *store.Store) (err error) { session, err = st.DeskClaude(); return err }); err != nil {
		r.logf("reading the desk's session: %v", err)
	}
	var pending []store.Entry
	if err := withBareStore(func(st *store.Store) (err error) { pending, err = st.UndeliveredEmil(); return err }); err != nil {
		r.logf("reading Emil's undelivered messages: %v", err)
	}
	r.mu.Lock()
	for _, e := range r.emil {
		if !slices.ContainsFunc(pending, func(p store.Entry) bool { return p.ID == e.ID }) {
			pending = append(pending, e)
		}
	}
	var since time.Time
	if err := withBareStore(func(st *store.Store) (err error) { since, err = st.DeskSessionStart(session); return err }); err != nil {
		r.logf("reading when the desk's session started: %v", err)
	}
	r.session, r.emil, r.since = session, pending, since
	r.mu.Unlock()
	go r.watch(ctx)
	wait := deskBackoffMin
	for ctx.Err() == nil {
		started := time.Now()
		err := r.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		if r.restarting {
			// prism desk restart ended it: start the fresh desk at once.
			r.restarting = false
			r.mu.Unlock()
			wait = deskBackoffMin
			continue
		}
		r.failed = true
		r.bump()
		r.mu.Unlock()
		if time.Since(started) > deskBackoffMax {
			wait = deskBackoffMin
		}
		r.logf("the desk stopped (%v); starting it again in %s", err, wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, deskBackoffMax)
	}
}

func (r *deskRunner) runOnce(ctx context.Context) error {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	argv, err := headlessArgv(session)
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	env, err := headlessEnv(token)
	if err != nil {
		return err
	}
	dir, err := deskDir()
	if err != nil {
		return err
	}
	first := deskFirst
	if session == "" {
		first = firstPrompt(r.logf)
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env, cmd.Stderr = dir, env, r.errw
	// The desk's own group, so stopping it also stops what its tools left running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = deskStopWait
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.mu.Lock()
	r.gen++
	gen := r.gen
	r.stop, r.restarting = stop, false
	r.stdin, r.alive, r.busy, r.failed, r.reply, r.token = stdin, true, false, false, nil, token
	if session == "" {
		r.since = time.Now()
		r.send(first)
	} else {
		r.recordSession("idle")
		r.flush()
	}
	r.bump()
	r.mu.Unlock()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		r.handle(gen, sc.Bytes())
	}
	stdin.Close()
	werr := cmd.Wait()
	// A watch the desk's tools left running stops with the desk, also when it exited on its own.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	r.mu.Lock()
	switch {
	case r.gen != gen:
		// A restart already reset the state, and the session is no longer this process's.
	case r.restartAfter:
		// The desk asked to restart, then died before its turn ended: restart it now, rather
		// than resume the session it handed off.
		r.restartNow()
	default:
		r.alive, r.busy, r.reply, r.token, r.stop = false, false, nil, "", nil
		r.recordSession("error")
	}
	r.bump()
	r.mu.Unlock()
	if werr == nil {
		werr = errors.New("it exited")
	}
	return werr
}

// streamLine is the part of a stream-json line the runner reads. A subagent's lines carry the
// Agent call's id in ParentToolUseID.
type streamLine struct {
	Type            string  `json:"type"`
	SessionID       string  `json:"session_id"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *callUsage `json:"usage"`
	} `json:"message"`
}

// handle reads one line the desk printed: its session, its reply growing during the turn, and
// the turn's result, after which what queued meanwhile goes in.
func (r *deskRunner) handle(gen int, line []byte) {
	var l streamLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen {
		// A process prism desk restart ended: its session is no longer the desk's.
		return
	}
	if l.SessionID != "" && l.SessionID != r.session {
		r.session = l.SessionID
		status := "idle"
		if r.busy {
			status = "working"
		}
		r.recordSession(status)
		r.bump()
	}
	switch l.Type {
	case "assistant":
		if l.ParentToolUseID == nil && l.Message.Usage != nil {
			r.context = l.Message.Usage.context()
		}
		text, steps := "", 0
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				text = joinText(text, c.Text)
			case "tool_use":
				steps++
			}
		}
		if text == "" && steps == 0 {
			return
		}
		err := withBareStore(func(st *store.Store) error {
			if r.reply == nil {
				e, err := st.AddEntry(store.Entry{Role: store.RoleDesk, Text: text, Steps: steps})
				r.reply = &e
				return err
			}
			r.reply.Text, r.reply.Steps = joinText(r.reply.Text, text), r.reply.Steps+steps
			return st.UpdateEntry(r.reply.ID, r.reply.Text, r.reply.Steps)
		})
		if err != nil {
			r.logf("recording the desk's reply: %v", err)
		}
	case "result":
		if r.compacting {
			r.compacting, r.busy = false, false
			r.recordSession("idle")
			r.bump()
			r.flush()
			return
		}
		r.guardReply()
		r.busy, r.reply = false, nil
		r.recordSession("idle")
		r.bump()
		if r.restartAfter {
			r.restartNow()
			return
		}
		if r.context >= deskCompactAt {
			r.compact()
			return
		}
		r.flush()
	}
}

// compact runs Claude Code's /compact as the desk's next turn, between two of its own turns;
// what queues meanwhile goes in after it. Callers hold r.mu.
func (r *deskRunner) compact() {
	r.logf("the desk's context is %dk tokens: compacting it", r.context/1000)
	r.context = 0
	r.send(compactPrompt)
	r.compacting = r.busy
}

// guardReply hides a finished reply that numbers questions for Emil in chat and opens the
// desk's next turn with reaskNotice, once: when the reply right after a notice fails too, it
// stays visible and is only logged, so the server and the desk can't loop. The server never
// turns the reply's text into decisions itself. Callers hold r.mu.
func (r *deskRunner) guardReply() {
	reasked := r.reasked
	r.reasked = false
	if r.reply == nil || !numbersQuestions(r.reply.Text) {
		return
	}
	if reasked {
		r.logf("the desk's reply m%d numbers questions in chat again; left visible", r.reply.ID)
		return
	}
	id := r.reply.ID
	if err := withBareStore(func(st *store.Store) error { return st.SetOutcome(id, outcomeReasked) }); err != nil {
		r.logf("hiding the desk's reply m%d: %v", id, err)
		return
	}
	r.logf("hid the desk's reply m%d: it numbers questions in chat", id)
	r.notice, r.reasked = reaskNotice, true
}

// watch does what tracks wait --desk does for the interactive desk: it queues the reports and
// status changes of the desk's sessions and the answers to its questions, and starts queued
// sessions as slots free.
func (r *deskRunner) watch(ctx context.Context) {
	t := time.NewTicker(deskWatch)
	defer t.Stop()
	for {
		lines, reports := r.watchOnce()
		if len(lines) > 0 || len(reports) > 0 {
			if len(reports) > 0 {
				err := withBareStore(func(st *store.Store) error {
					for _, e := range reports {
						if _, err := st.AddEntry(e); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					r.logf("recording reports: %v", err)
				}
			}
			r.mu.Lock()
			r.events = append(r.events, lines...)
			r.bump()
			r.flush()
			r.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// logInterim writes an interim report to the handled log and holds it for the batch line.
func (r *deskRunner) logInterim(e deskEvent) bool {
	label := sessionLabel(e.track, "", e.session)
	err := withBareStore(func(st *store.Store) error {
		_, err := st.LogDesk(e.track, "noted", "", interimDid(label, e.text))
		return err
	})
	if err != nil {
		r.logf("logging an interim report: %v", err)
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.interim) == 0 {
		r.interimAt = time.Now()
	}
	r.interim = append(r.interim, label)
	return true
}

// interimDue is the one line that wakes the desk for the interim reports held longer than interimWake.
func (r *deskRunner) interimDue(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.interim) == 0 || now.Sub(r.interimAt) < interimWake {
		return nil
	}
	line := fmt.Sprintf("%s logged in the handled log without waking you since %s: %s", plural(len(r.interim), "interim report"), r.interimAt.Format("15:04"), strings.Join(r.interim, ", "))
	r.interim = nil
	return []string{line}
}

func (r *deskRunner) watchOnce() ([]string, []store.Entry) {
	r.mu.Lock()
	seen, first, ended := r.seen, r.first, r.ended
	r.ended = nil
	r.mu.Unlock()
	for _, id := range ended {
		seen[id] = deskSeen{"done", "stopped", "desk"}
	}
	evs, err := deskEvents(seen, first)
	if err != nil {
		r.logf("watching the desk's sessions: %v", err)
		return nil, nil
	}
	r.mu.Lock()
	r.first = false
	r.mu.Unlock()
	var lines []string
	var reports []store.Entry
	for _, e := range evs {
		if e.session != "" {
			reports = append(reports, store.Entry{Role: store.RoleReport, Session: e.session, Outcome: e.outcome, Text: e.text})
		}
		if e.interim && e.track != "" && r.logInterim(e) {
			continue
		}
		lines = append(lines, e.line)
	}
	lines = append(lines, r.interimDue(time.Now())...)
	answered, err := answeredLines()
	if err != nil {
		r.logf("reading answers: %v", err)
	}
	return append(append(lines, answered...), drainQueue()...), reports
}
