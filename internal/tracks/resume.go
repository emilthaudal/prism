package tracks

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	terminalFlavor = "com.superlogical.terminal"
	resumeUsage    = "usage: tracks resume NAME | --all [--dry-run] | --all --once-per-server"
)

var (
	resumeStartWait = 5 * time.Second
	resumeStartPoll = 250 * time.Millisecond
)

// resumeResult is one card's line: what is resumed, recreated, skipped, nothing to resume or
// failed, and why.
type resumeResult struct {
	name, what, detail string
	// session and block are where the agent is back, for the selection after a resume by name.
	session, block string
	// rexErr is why Rex could not be asked, when that is why the resume was skipped.
	rexErr error
}

// exit is the exit status of resuming one track by name: 0 when the agent runs (resumed,
// recreated, already running, or a dry run), 2 when there was nothing to resume or the
// session id is ambiguous, 3 when Rex could not be asked, 4 for any other skip.
func (r resumeResult) exit() error {
	switch {
	case r.what == "nothing to resume":
		return exitSessionGone
	case r.what != "skipped":
		return nil
	case r.rexErr != nil && errors.Is(r.rexErr, errSessionAmbiguous):
		return exitSessionGone
	case r.rexErr != nil:
		return exitRexUnreachable
	case strings.HasPrefix(r.detail, "Claude already runs"):
		return nil
	}
	return exitNotResumed
}

func (r resumeResult) String() string {
	if r.detail == "" {
		return r.name + ": " + r.what
	}
	return r.name + ": " + r.what + " (" + r.detail + ")"
}

func resumeCmd(args []string, out io.Writer) error {
	var name string
	var all, dry, once bool
	for _, a := range args {
		switch {
		case a == "--all":
			all = true
		case a == "--dry-run":
			dry = true
		case a == "--once-per-server":
			once = true
		case strings.HasPrefix(a, "-") || name != "":
			return errors.New(resumeUsage)
		default:
			name = a
		}
	}
	if all == (name != "") || (once && (!all || dry)) {
		return errors.New(resumeUsage)
	}
	if once {
		return resumeOncePerServer(out)
	}
	var cards []*Card
	if all {
		cards = loadCards()
	} else {
		if !validName.MatchString(name) {
			return fmt.Errorf("not a track name: %q", name)
		}
		c, err := loadCard(name)
		if err != nil {
			return fmt.Errorf("no card for %q", name)
		}
		cards = []*Card{c}
	}
	if !dry {
		unlock, err := tryLockResume()
		if errors.Is(err, errResumeBusy) {
			fmt.Fprintf(out, "%s: skipped (%v)\n", cmp.Or(name, "resume"), err)
			if all {
				return exitCode(1)
			}
			return exitNotResumed
		}
		if err != nil {
			return err
		}
		defer unlock()
	}
	evs := readEvents()
	failed := 0
	var last resumeResult
	for _, c := range cards {
		r := resumeResult{name: c.Name, what: "skipped", detail: "parked: resume it by name"}
		if !all || c.mode() != "parked" {
			r = resumeCard(c, evs, dry)
		}
		if r.what == "failed" {
			failed++
		}
		fmt.Fprintln(out, r)
		last = r
		if !all && !dry && (r.what == "recreated" || r.what == "resumed") {
			if note, _ := switchSession(r.session, r.block); note != "" && !jumped(note) {
				fmt.Fprintln(out, c.Name+": "+note)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d tracks failed to resume", failed, len(cards))
	}
	if !all {
		return last.exit()
	}
	return nil
}

// resumeCard brings back the Claude session of a card whose agent is gone: typed into the
// session's idle shell, in a new pane in place of the pane Claude exited from, or in a new
// Rex session when the card's session is gone.
func resumeCard(c *Card, evs []Event, dry bool) resumeResult {
	res := func(what, detail string) resumeResult { return resumeResult{name: c.Name, what: what, detail: detail} }
	sess := c.Meta["rex_session"]
	live, key := "", sess
	if sess != "" {
		switch id, _, err := resolveSession(sess); {
		case err == nil:
			live, key = id, id
		case !errors.Is(err, errSessionNone):
			r := res("skipped", "could not ask Rex about "+sess+": "+err.Error())
			r.rexErr = err
			return r
		}
	}
	adopt := false
	if live == "" {
		// A session restored or started under the track's name, whose id never reached the card.
		l, err := liveSessionFor(c.Name)
		if err != nil {
			r := res("skipped", "could not ask Rex for a session named "+c.Name+": "+err.Error())
			r.rexErr = err
			return r
		}
		live, adopt = l, l != ""
		if key == "" {
			key = l
		}
	}
	id, cwd, why := resumeTarget(c, key)
	if why != "" {
		return res("nothing to resume", why)
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return res("skipped", "the Claude session's directory is gone: "+cwd)
	}
	argv := withTracksHome(append([]string{"claude", "--resume", id, "--name", c.Name}, threadSettingsArgs(c, dry)...)...)
	line := "cd " + shellQuote(cwd) + " && " + shellJoin(argv)
	if live == "" {
		return recreate(c.Name, id, cwd, argv, dry)
	}
	hints := []string{agentBlock(evs, live), agentBlock(evs, sess)}
	pane, why := pickPane(live, hints...)
	if why != "" {
		return res("skipped", why)
	}
	detail := "in " + pane.block + ": " + line
	if pane.exited {
		detail = "in a new pane in place of exited " + pane.block + ", in " + cwd + ": " + shellJoin(argv)
	}
	if dry {
		if adopt {
			return res("would resume", "adopting "+live+", "+detail)
		}
		return res("would resume", detail)
	}
	if adopt {
		if err := adoptSession(c.Name, sess, live); err != nil {
			return res("failed", "could not record "+live+" on the card: "+err.Error())
		}
	}
	if pane.exited {
		// Every exited --keep-open pane looks alike, so only one the agent's hooks ran in is closed.
		known := pane.block == hints[0] || pane.block == hints[1]
		nb, what, why := replacePane(live, pane.block, cwd, argv, known)
		if what != "" {
			return res(what, why)
		}
		detail = "in " + nb + ", in place of exited " + pane.block + ", in " + cwd + ": " + shellJoin(argv) + why
		pane.block = nb
	} else if what, why := typeResume(live, pane.block, pane.pid, line); what != "" {
		return res(what, why)
	}
	if err := setMetas(c.Name, [][2]string{{"claude_session", id}, {"claude_cwd", cwd}}); err != nil {
		detail += ", but could not record it on the card: " + err.Error()
	}
	return resumeResult{name: c.Name, what: "resumed", detail: detail, session: live, block: pane.block}
}

// resumeTarget is the Claude session to resume and the directory it started in: the card's
// own, else the newest transcript that started in the card's Rex session.
func resumeTarget(c *Card, rexSession string) (id, cwd, why string) {
	id, cwd = c.Meta["claude_session"], c.Meta["claude_cwd"]
	if id == "" {
		var ok bool
		if id, cwd, ok = findTranscript(rexSession); !ok {
			if rexSession == "" {
				return "", "", "no claude_session or rex_session on the card"
			}
			return "", "", "no claude_session on the card and no transcript started in " + rexSession
		}
	}
	if !claudeSessionID.MatchString(id) {
		return "", "", "claude_session is not a session id: " + id
	}
	if cwd == "" {
		cwd = cwdForClaudeSession(id)
	}
	if !cardValueOK(cwd) {
		return "", "", "no usable directory for Claude session " + id
	}
	return id, cwd, ""
}

func recreate(name, id, cwd string, argv []string, dry bool) resumeResult {
	res := func(what, detail string) resumeResult { return resumeResult{name: name, what: what, detail: detail} }
	run := "in " + cwd + ": " + shellJoin(argv)
	if dry {
		return res("would recreate", "new Rex session "+name+" "+run)
	}
	sid, block, err := rexNewSession(trackSpec{name: name, dir: cwd, argv: argv})
	if err != nil {
		return res("failed", "could not start a Rex session: "+err.Error())
	}
	if err := setMetas(name, [][2]string{{"rex_session", sid}, {"claude_session", id}, {"claude_cwd", cwd}}); err != nil {
		return res("failed", "started "+sid+" but could not record it on the card: "+err.Error())
	}
	return resumeResult{name: name, what: "recreated", detail: sid + " " + run, session: sid, block: block}
}

// fgProc is a terminal's foreground process as Rex reports it.
type fgProc struct {
	PID         int    `json:"pid"`
	Name        string `json:"name"`
	Argv0       string `json:"argv0"`
	Path        string `json:"path"`
	InvokedPath string `json:"invoked_path"`
}

func (p *fgProc) label() string {
	if p.Name != "" {
		return p.Name
	}
	return filepath.Base(p.Argv0)
}

func foreground(ctx context.Context, sess, block string) (*fgProc, blockProc, error) {
	var bp blockProc
	b, err := rexRun(ctx, "block", "call", "-s", sess, "-b", block, "process")
	if err != nil {
		return nil, bp, err
	}
	var p struct {
		Foreground *fgProc `json:"foreground"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, bp, err
	}
	if err := json.Unmarshal(b, &bp); err != nil {
		return nil, bp, err
	}
	if p.Foreground == nil || p.Foreground.PID == 0 {
		if bp.Foreground == nil && bp.Child == nil && bp.LastExit != nil {
			return nil, bp, errExited
		}
		return nil, bp, errors.New("Rex does not say what runs in it")
	}
	return p.Foreground, bp, nil
}

// errExited is a --keep-open pane whose program has exited and left nothing running: what a
// track pane shows once Claude quits, since Claude runs there with no shell behind it.
var errExited = errors.New("its program has exited")

// atPrompt says why the pane is not a shell waiting at its bare prompt, or "" when it is.
func atPrompt(ctx context.Context, fg *fgProc, p blockProc) (string, error) {
	if !idleShell(fg) {
		return fg.label() + " runs", nil
	}
	return shellNotBare(ctx, p)
}

// Claude Code's process takes its version as its name, so the paths say more.
var versionName = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)

func isClaudeProc(p *fgProc) bool {
	for _, s := range []string{p.Name, p.Argv0, p.Path, p.InvokedPath} {
		if filepath.Base(s) == "claude" || strings.Contains(s, "/claude/") {
			return true
		}
	}
	return versionName.MatchString(p.Name)
}

// idleShell is whether the shell itself is in front: no job of its own runs, so typed text
// reaches its prompt. login is left out: before the shell starts it reads a password.
func idleShell(p *fgProc) bool {
	n := p.Name
	if n == "" {
		n = p.Argv0
	}
	n = strings.TrimPrefix(filepath.Base(n), "-")
	return n != "login" && isShell(n) && !isClaudeProc(p)
}

// resumePane is where Claude comes back: a shell at its bare prompt (pid is that shell), or an
// exited pane that a new pane replaces.
type resumePane struct {
	block    string
	pid      int
	wasAgent bool
	exited   bool
}

// pickPane finds the one terminal in the session that is an idle shell or has exited. It
// refuses when any terminal runs Claude, when Rex cannot say what runs in a terminal, and
// when several candidates leave it unclear which was the agent's.
func pickPane(sess string, hints ...string) (resumePane, string) {
	var none resumePane
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	b, err := rexRun(ctx, "block", "ls", "-s", sess, "--json")
	if err != nil {
		return none, "could not list the session's panes: " + err.Error()
	}
	var l struct {
		Blocks []struct {
			ID     string `json:"block_id"`
			Flavor string `json:"flavor"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return none, "could not read the session's panes: " + err.Error()
	}
	var cands []resumePane
	var busy []string
	exited := false
	for _, blk := range l.Blocks {
		if !strings.HasPrefix(blk.Flavor, terminalFlavor) {
			continue
		}
		fg, p, err := foreground(ctx, sess, blk.ID)
		if errors.Is(err, errExited) {
			cands, exited = append(cands, resumePane{block: blk.ID, exited: true}), true
			continue
		}
		if err != nil {
			return none, "cannot tell what runs in " + blk.ID + ": " + err.Error()
		}
		if isClaudeProc(fg) {
			return none, "Claude already runs in " + blk.ID
		}
		st, reports, err := programStatus(ctx, sess, blk.ID)
		if err != nil {
			return none, "cannot tell whether Claude runs in " + blk.ID + ": " + err.Error()
		}
		if !idleShell(fg) && reports && st.Owner == fg.PID {
			return none, "Claude already runs in " + blk.ID
		}
		why, err := atPrompt(ctx, fg, p)
		if err != nil {
			return none, "cannot tell what runs in " + blk.ID + ": " + err.Error()
		}
		if why != "" {
			busy = append(busy, why+" in "+blk.ID)
			continue
		}
		cands = append(cands, resumePane{block: blk.ID, pid: fg.PID, wasAgent: reports})
	}
	switch len(cands) {
	case 0:
		if len(busy) == 0 {
			return none, "the session has no terminal pane"
		}
		return none, "no idle shell: " + strings.Join(busy, ", ")
	case 1:
		return cands[0], ""
	}
	for _, h := range hints {
		for _, c := range cands {
			if h != "" && c.block == h {
				return c, ""
			}
		}
	}
	var was []resumePane
	for _, c := range cands {
		if c.wasAgent {
			was = append(was, c)
		}
	}
	if len(was) == 1 {
		return was[0], ""
	}
	what := "idle shells"
	if exited {
		what = "idle shells or exited panes"
	}
	return none, fmt.Sprintf("%d %s and none known as the agent's", len(cands), what)
}

// replacePane opens a pane running argv beside an exited pane, waits for Claude to come to its
// front, then closes the exited pane when closeOld, so the new one takes its place. A pane
// that no longer shows as exited is left alone. why carries a note when the old pane stays.
func replacePane(sess, block, cwd string, argv []string, closeOld bool) (newBlock, what, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if _, _, err := foreground(ctx, sess, block); !errors.Is(err, errExited) {
		return "", "skipped", "the pane " + block + " no longer shows as exited"
	}
	args := []string{"run", "--session=" + sess, "--block=" + block, "--split=right", "--focus=false",
		"--keep-open", "--interactive", "--cwd=" + cwd, "--json", "--"}
	out, err := rexRunIn(ctx, "", append(args, argv...)...)
	if err != nil {
		return "", "failed", "could not open a pane beside " + block + ": " + err.Error()
	}
	var doc struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.BlockIDs) == 0 || doc.BlockIDs[0] == "" {
		return "", "failed", "rex run answered without a block id beside " + block + ": check that session"
	}
	nb := doc.BlockIDs[0]
	if !claudeStarted(sess, nb) {
		return "", "failed", fmt.Sprintf("opened %s beside exited %s but Claude was not in front within %s: check that pane", nb, block, resumeStartWait)
	}
	if !closeOld {
		return nb, "", ", leaving exited " + block + " open: not known as the agent's pane"
	}
	cctx, ccancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer ccancel()
	if _, err := rexRunIn(cctx, "", "block", "close", "--session="+sess, block); err != nil {
		return nb, "", ", but could not close exited " + block + ": " + err.Error()
	}
	return nb, "", ""
}

// typeResume clears the shell's input line, types the command and presses Enter, each only
// while the same shell is still at its bare prompt. It returns "" once Claude is in front.
func typeResume(sess, block string, pid int, line string) (what, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	same := func() bool {
		fg, p, err := foreground(ctx, sess, block)
		if err != nil || fg.PID != pid {
			return false
		}
		why, err := atPrompt(ctx, fg, p)
		return err == nil && why == ""
	}
	if !same() {
		return "skipped", "the shell in " + block + " is no longer idle"
	}
	target := []string{"--session=" + sess, "--block=" + block}
	// Text left at the prompt would run joined to the command.
	if _, err := rexRunIn(ctx, "", append(append([]string{"send-key"}, target...), "ctrl+u")...); err != nil {
		return "failed", "could not clear the input in " + block + ": " + err.Error()
	}
	if _, err := rexRunIn(ctx, line, append([]string{"send"}, target...)...); err != nil {
		return "failed", "could not type into " + block + " (" + err.Error() + "): check its input"
	}
	time.Sleep(answerEnterDelay)
	if !same() {
		return "failed", "typed into " + block + " but not run: the shell is no longer idle. Clear that input"
	}
	if _, err := rexRunIn(ctx, "", append(append([]string{"send-key"}, target...), "enter")...); err != nil {
		return "failed", "typed into " + block + " but enter failed: " + err.Error()
	}
	if !claudeStarted(sess, block) {
		return "failed", fmt.Sprintf("ran the command in %s but Claude was not in front within %s: check that pane", block, resumeStartWait)
	}
	return "", ""
}

// claudeStarted waits a bounded time for Claude to become the pane's foreground process: a
// shell at a continuation prompt, for one, takes the line as more input and runs nothing.
func claudeStarted(sess, block string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), resumeStartWait)
	defer cancel()
	for {
		if fg, _, err := foreground(ctx, sess, block); err == nil && isClaudeProc(fg) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(resumeStartPoll):
		}
	}
}

func shellQuote(s string) string {
	// zsh expands a word that starts with =, so a bare word must not.
	if s != "" && s[0] != '=' && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=+,@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}
