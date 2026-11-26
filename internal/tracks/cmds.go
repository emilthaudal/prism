package tracks

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
)

const (
	cmdTimeout      = 5 * time.Second
	waitDelay       = time.Second
	notifyFresh     = 10 * time.Minute
	staleWorking    = 2 * time.Hour
	rexNoStart      = "--autostart=false"
	defaultSince    = 24 * time.Hour
	maxLogLines     = 10
	baselineLine    = "baseline (orch 2-16 Nov): 35% avoidable"
	cardTemplateFmt = `---
name: %s
mode: %s        # focus | background | waiting | parked (Emil sets this)
tier: %-13s# 1 | 2 | 3: the riskiest thing this track touches (you set it)
dir: %s
rex_session:
waiting_on:        # when mode is waiting: who or what, and how to check it
check:             # when waiting: pr N approved | pr N merged | pr N green | ci BRANCH green
links:             # ticket keys and PR URLs, comma-separated: EMI-273, PAN-213, https://github.com/org/repo/pull/7
updated:
---
## Goal
%s

## Where it is
Not started.

## Needs me

## Standing orders

## Later
`
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Run(args []string, in io.Reader, out, errw io.Writer) int {
	p := palette{useColor(out)}
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "", "--float", "--home":
		if isTTY(out) && isTTY(in) {
			if err = runPane(in, out, cmd == "--float", cmd == "--home"); err != nil {
				fmt.Fprintln(errw, "tracks:", err)
				return 1
			}
			return 0
		}
		list(out, p)
	// tui and server are the Prism names (EMI-393); ui and serve stay as the old ones.
	case "tui", "ui":
		return deskui.Main(args, in, out, errw, isTTY(out) && isTTY(in))
	case "server", "serve":
		err = serveCmd(args, out, errw)
	case "hook":
		hookOut = out
		runHook(in)
		return 0
	case "open":
		err = open(args, out, p)
	case "new":
		err = newCard(args, out)
	case "card":
		err = cardCmd(args, in, out)
	case "bind":
		err = bindCmd(args, out)
	case "ls":
		err = lsCmd(args, out)
	case "sessions":
		err = sessionsCmd(args, out)
	case "decision":
		err = decisionCmd(args, out)
	case "decisions":
		err = decisionsCmd(args, out)
	case "decide":
		err = decideCmd(args, out)
	case "orders":
		err = ordersCmd(args, out)
	case "patterns":
		err = patternsCmd(args, out)
	case "ready":
		err = readyCmd(args, out)
	case "stats":
		err = stats(args, out, p)
	case "check":
		err = checkCmd(args, out, errw)
	case "next":
		err = nextCmd(args, out, errw)
	case "home":
		err = homeCmd(args, out)
	case "close":
		err = closeCmd(args, out)
	case "rename":
		err = renameCmd(args, out)
	case "mode":
		err = modeCmd(args, out)
	case "archive":
		err = archiveCmd(args, out, p)
	case "resume":
		err = resumeCmd(args, out)
	case "spawn":
		err = spawnCmd(args, out)
	case "start":
		err = startCmd(args, out)
	case "send":
		err = sendCmd(args, out)
	case "permission":
		permissionCmd(in, out)
		return 0
	case "elicitation":
		elicitationCmd(in, out)
		return 0
	case "permission-answer":
		err = permissionAnswerCmd(args, out)
	case "ask":
		err = askCmd(args, out)
	case "wait":
		err = waitCmd(args, out, errw)
	case "screen":
		err = screenCmd(args, out)
	case "desk":
		err = deskCmd(args, out)
	case "verify":
		err = verifyCmd(args, in, out, errw)
	case "migrate":
		err = migrateCmd(args, out)
	case "export":
		err = exportCmd(args, out)
	case "render":
		err = renderCmd(args, out)
	default:
		err = fmt.Errorf("usage: tracks [open NAME | ls [--json] | sessions [--json] | decision add (TRACK | --desk) --question Q --option 'label|description'... --recommend N [--evidence 'text|source']... [--link URL] [--session ID] [--from desk|agent|check] | decision close ID [--note TEXT] | decisions [--json] | decide ID (N | --text TEXT) [--by emil|desk] | orders [--json] | orders add TEXT | orders drop N | patterns [--json] [--days N] [--min N] | ready [--json] | next | home [--from SESSION] | new NAME --dir D [--mode M] | bind NAME | card NAME < fields | rename NAME NEW | mode NAME focus|background|waiting|parked | close [--yes] NAME | close --sweep | archive [--leftovers] | stats [--days N] | check [--dry-run] | resume NAME|--all [--dry-run|--once-per-server] | spawn LEAD 'task' --dir D [--model sonnet|opus] [--verify CMD] | start 'request' [--dir D] [--model sonnet|opus] [--name NAME] | send TARGET 'text' [--replace] [--now] | send TRACK 'text' [--from LEAD] | permission-answer ID allow|deny|session [--text T] | ask TRACK 'question' [--wait [--timeout D]] | wait LEAD|--all [--timeout D] [--idle D] | screen TRACK | tui [--fixture FILE] | server [--force | --mock FILE | --detach] | desk [--json] [--idle D] | desk start|ask|answer|log|close|pickup … | verify NAME [--timeout D] | migrate [--check] | export | render | hook]\n\nexit codes of open NAME and resume NAME: 0 done, 1 error, 2 the track's Rex session is gone or the card names none (resume: nothing to resume), 3 Rex did not answer (resume: could not ask Rex), 4 resume skipped and the agent is not back. resume --all exits 1 when a track failed or another resume holds resume.lock, else 0")
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if err != nil {
		fmt.Fprintln(errw, "tracks:", err)
		return 1
	}
	return 0
}

// Exit codes of `tracks open NAME` and `tracks resume NAME` when Rex was not switched to the
// track's session or the agent was not brought back. 1 stays the code for every other error.
const (
	exitSessionGone    exitCode = 2
	exitRexUnreachable exitCode = 3
	exitNotResumed     exitCode = 4 // resume only: skipped, and the agent is not back
)

// exitCode is a command's exit status, returned as an error once the command has printed
// everything it has to say.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func liveState(evs []Event, session string, now time.Time) string {
	if session == "" {
		return "-"
	}
	for _, e := range slices.Backward(evs) {
		if e.RexSession != session {
			continue
		}
		switch e.Event {
		case "prompt":
			// No Stop arrives after an interrupt or a crash, so a long-past prompt is not work.
			if now.Sub(e.At) > staleWorking {
				return "idle " + shortDur(now.Sub(e.At))
			}
			return "working"
		case "stop":
			return "idle " + shortDur(now.Sub(e.At))
		case "notify":
			// Claude Code also notifies after 60s idle, so an old notify is not a prompt waiting.
			if now.Sub(e.At) > notifyFresh {
				return "idle " + shortDur(now.Sub(e.At))
			}
			return "needs you"
		}
	}
	return "-"
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func rank(c *Card) int {
	switch {
	case c.mode() == "parked":
		return 5
	case c.mode() == "focus":
		return 0
	case c.asks() > 0:
		return 1
	case c.mode() == "background":
		return 2
	case c.mode() == "waiting":
		return 3
	}
	return 4
}

func list(out io.Writer, p palette) {
	cards := loadCards()
	slices.SortStableFunc(cards, func(a, b *Card) int { return rank(a) - rank(b) })
	evs, now := readEvents(), time.Now()
	var rows []Row
	focus := 0
	for _, c := range cards {
		if c.mode() == "focus" {
			focus++
		}
		rows = append(rows, Row{c.Name, c.mode(), liveState(evs, c.Meta["rex_session"], now), c.firstLine("Where it is"), c.asks()})
	}
	fmt.Fprint(out, renderList(rows, focus, termWidth(), p))
}

func run(dir string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	if name == "rex" {
		args = append([]string{rexNoStart}, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return strings.TrimSpace(string(b)), err
}

func open(args []string, out io.Writer, p palette) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tracks open NAME")
	}
	if !validName.MatchString(args[0]) {
		return fmt.Errorf("not a track name: %q", args[0])
	}
	c, err := loadCard(args[0])
	if err != nil {
		return fmt.Errorf("no card for %q", args[0])
	}
	v := CardView{Name: c.Name, Goal: c.Sections["Goal"], Where: c.Sections["Where it is"], Needs: c.Sections["Needs me"]}
	sess, evs := c.Meta["rex_session"], readEvents()
	note, kind := switchSession(sess, agentBlock(evs, sess))
	if note != "" {
		v.Notes = append(v.Notes, note)
	}
	var serr error
	if v.Since, serr = sinceLog(c, evs); serr != nil && !errors.Is(serr, errNoDir) && !errors.Is(serr, errNotGit) {
		v.Notes = append(v.Notes, "git: "+serr.Error())
	}
	if dir := c.Meta["dir"]; dir != "" {
		if s, err := run(dir, "gh", "pr", "list", "--author", "@me", "--json", "number,title,reviewDecision,statusCheckRollup"); err == nil && s != "" && s != "[]" {
			v.PRs = []string{"PRs: " + s}
		}
	}
	fmt.Fprint(out, renderCard(v, p))
	if err := appendEvent(Event{At: time.Now().UTC(), Event: "open", RexSession: sess, Track: c.Name, Cwd: c.Meta["dir"]}); err != nil {
		return err
	}
	return kind.exit()
}

// switchKind is why Rex was not switched to a session.
type switchKind int

const (
	switched switchKind = iota
	sessionGone
	rexUnreachable
)

func (k switchKind) exit() error {
	switch k {
	case sessionGone:
		return exitSessionGone
	case rexUnreachable:
		return exitRexUnreachable
	}
	return nil
}

// selectSession switches Rex to the session, then focuses the agent's block in it (which
// also switches window), and returns a note when it could not.
func selectSession(sess, block string) string {
	note, _ := switchSession(sess, block)
	return note
}

// switchSession is selectSession with the reason it did not switch. A block that could not
// be focused still counts as switched.
func switchSession(sess, block string) (string, switchKind) {
	if sess == "" {
		return "no rex_session on the card: not switching", sessionGone
	}
	kind := rexUnreachable
	id, _, err := resolveSession(sess)
	if errors.Is(err, errSessionNone) || errors.Is(err, errSessionAmbiguous) {
		kind = sessionGone
	}
	if err != nil && strings.HasPrefix(sess, "session:") {
		id, err = sess, nil // a full id needs no lookup; keep open working when the listing fails
	}
	if err == nil {
		_, err = run("", "rex", "do", "session.select", "session_id="+id)
	}
	if err != nil {
		return "could not switch Rex to the session: " + err.Error(), kind
	}
	if block == "" {
		return "agent's block not known yet: it is logged on the agent's next prompt", switched
	}
	if _, err := run("", "rex", "block", "focus", "-s", id, block); err != nil {
		return "could not focus the agent's block: " + err.Error(), switched
	}
	return "", switched
}

// agentBlock is the Rex block the hook last saw the session's agent in.
func agentBlock(evs []Event, sess string) string {
	for _, e := range slices.Backward(evs) {
		if e.RexSession == sess && e.RexBlock != "" && e.Event != "open" {
			return e.RexBlock
		}
	}
	return ""
}

var (
	errNoDir  = errors.New("no dir on the card")
	errNotGit = errors.New("dir is not a git repository")
)

func sinceLog(c *Card, evs []Event) ([]string, error) {
	at, _ := lastSeen(evs, c)
	return sinceLogFrom(c, at)
}

// sinceLogFrom is the card's repo log since at.
func sinceLogFrom(c *Card, at time.Time) ([]string, error) {
	dir := c.Meta["dir"]
	if dir == "" {
		return nil, errNoDir
	}
	s, err := run(dir, "git", "log", "--branches", "--oneline", "--since="+at.Format(time.RFC3339), "-n", fmt.Sprint(maxLogLines))
	if err != nil {
		// Only "not a git repository (or any of the parent directories / up to mount point)":
		// a broken worktree says "not a git repository: PATH" and is a real error.
		if strings.Contains(err.Error(), "not a git repository (or any") {
			return nil, errNotGit
		}
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

func newCard(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	dir := fs.String("dir", "", "repo directory")
	mode := fs.String("mode", defaultStartMode, "focus | background | waiting | parked")
	if len(args) == 0 {
		return fmt.Errorf("usage: tracks new NAME --dir D [--mode M]")
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid name %q", name)
	}
	if !slices.Contains(cardModes, *mode) {
		return fmt.Errorf("invalid mode %q", *mode)
	}
	err := errSwitched
	if !storeOn() {
		err = withFileLock(func() error {
			path := cardPath(name)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("card %s already exists", path)
			}
			if a := archivedCard(name); a != "" {
				return fmt.Errorf("track %s was closed (%s): it is not recreated", name, a)
			}
			if to := renamedAway(name); to != "" {
				return fmt.Errorf("%s was renamed to %s: use /track %s", name, to, to)
			}
			return writeAtomic(path, []byte(cardText(name, *mode, "", *dir, defaultGoal)))
		})
	}
	if errors.Is(err, errSwitched) {
		err = newCardStore(name, *mode, *dir)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "In the track's session, type: /track %s\n", name)
	return nil
}

// cardModes are the modes a card can be in, as the pane groups them.
var cardModes = []string{"focus", "background", "waiting", "parked"}

// modeCmd is tracks mode NAME MODE: what the pane's f, b, w and p keys do, for the desk and
// scripts.
func modeCmd(args []string, out io.Writer) error {
	usage := "usage: tracks mode NAME " + strings.Join(cardModes, "|") + " [--swap FOCUS_TRACK]"
	swap := ""
	if len(args) == 4 && args[2] == "--swap" {
		swap, args = args[3], args[:2]
	} else if len(args) == 3 && strings.HasPrefix(args[2], "--swap=") {
		swap, args = strings.TrimPrefix(args[2], "--swap="), args[:2]
	}
	switch {
	case len(args) != 2:
		return errors.New(usage)
	case !validName.MatchString(args[0]):
		return fmt.Errorf("not a track name: %q", args[0])
	case !slices.Contains(cardModes, args[1]):
		return fmt.Errorf("invalid mode %q\n%s", args[1], usage)
	}
	if _, err := loadCard(args[0]); err != nil {
		return fmt.Errorf("no card for %s", args[0])
	}
	if err := setModeChecked(args[0], args[1], swap); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is %s\n", args[0], args[1])
	return nil
}

func stats(args []string, out io.Writer, p palette) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	days := fs.Int("days", 7, "days to include")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -*days)
	byDay := map[string]*StatRow{}
	var total StatRow
	for _, e := range readEvents() {
		if e.At.Before(cutoff) || (e.Event != "open" && e.Event != "prompt") {
			continue
		}
		day := e.At.Local().Format("2006-01-02")
		r := byDay[day]
		if r == nil {
			r = &StatRow{Day: day}
			byDay[day] = r
		}
		for _, s := range []*StatRow{r, &total} {
			switch {
			case e.Event == "open":
				s.Opens++
			case e.Kind != "system":
				s.Typed++
				if e.Kind == "go-ahead" || e.Kind == "news" || e.Kind == "status" {
					s.Avoid++
				}
			}
		}
	}
	var rows []StatRow
	for _, r := range byDay {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b StatRow) int { return strings.Compare(a.Day, b.Day) })
	fmt.Fprint(out, renderStats(rows, total, baselineLine, p))
	return nil
}
