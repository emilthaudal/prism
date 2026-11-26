package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
	"github.com/emilthaudal/prism/internal/store"
)

// A loop is a desk session that runs unattended, one iteration a turn, until it says it is done,
// runs out of turns or time, or Emil stops it (EMI-466). The watch continues it at each stop
// without waking the desk, so a night of turns costs the desk one report. Its state is a file
// under $TRACKS_HOME/loops, not a row, so loops need no change to tracks.db.

const (
	loopUsage = "usage: tracks loop [stop NAME]"

	loopDefaultFor    = 8 * time.Hour
	loopDefaultTurns  = 20
	loopDefaultBudget = 70
	loopModel         = "sonnet"
	// loopSevenDayCap is the seven-day usage, in percent, at which a loop pauses whatever its budget.
	loopSevenDayCap = 95
	// loopMaxErrors is how many turns in a row may end in an error before the loop ends.
	loopMaxErrors = 3
	// loopUsageStale is how old usage.json may be before a loop treats the usage as unknown.
	loopUsageStale = time.Hour
	// loopUnknownReset is how long a loop pauses for a limit whose reset time is unknown.
	loopUnknownReset = 30 * time.Minute
	// loopLog is the progress log a loop session keeps in its folder.
	loopLog = "loop-log.md"

	loopRunning = "running"
	loopPaused  = "paused"
	loopEnded   = "ended"
)

// loopPermissionWait is how long a loop session's permission prompt waits before it is denied.
var loopPermissionWait = 2 * time.Minute

// loopNow is the loop's clock; tests set it.
var loopNow = time.Now

// loopMarker is the last line a loop turn ends with.
var loopMarker = regexp.MustCompile(`(?m)LOOP:\s*(done|continue)\b[ \t]*[-–—:]?[ \t]*(.*)$`)

// loopState is one loop: its limits, how far it got and whether it still runs.
type loopState struct {
	Track       string     `json:"track"`
	StartedAt   time.Time  `json:"started_at"`
	Until       time.Time  `json:"until"`
	Turns       int        `json:"turns"`
	Budget      int        `json:"budget"`
	Turn        int        `json:"turn"`
	State       string     `json:"state"`
	PausedUntil *time.Time `json:"paused_until,omitempty"`
	// Errors counts the turns in a row that ended in an error.
	Errors  int        `json:"errors,omitempty"`
	Ended   string     `json:"ended,omitempty"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
}

func loopsDir() string                 { return filepath.Join(home(), "loops") }
func loopPath(track string) string     { return filepath.Join(loopsDir(), track+".json") }
func loopStopPath(track string) string { return filepath.Join(loopsDir(), track+".stop") }

func readLoop(track string) (loopState, bool) {
	var l loopState
	b, err := os.ReadFile(loopPath(track))
	if err != nil || json.Unmarshal(b, &l) != nil || l.Track != track {
		return loopState{}, false
	}
	return l, true
}

func writeLoop(l loopState) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(loopsDir(), 0o755); err != nil {
		return err
	}
	return writeAtomic(loopPath(l.Track), append(b, '\n'))
}

// readLoops is every loop, oldest first.
func readLoops() []loopState {
	ents, _ := os.ReadDir(loopsDir())
	var out []loopState
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if l, ok := readLoop(name); ok {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b loopState) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

// stopRequested is whether tracks loop stop asked the loop to end after its current turn. It is
// a file of its own, so it never races the watch's writes of the loop's state.
func stopRequested(track string) bool {
	_, err := os.Stat(loopStopPath(track))
	return err == nil
}

// loopFlags reads tracks start's loop limits: --until HH:MM or --for D, --turns N, --budget PCT.
func loopFlags(fl map[string]string, now time.Time) (loopState, error) {
	l := loopState{StartedAt: now, Until: now.Add(loopDefaultFor), Turns: loopDefaultTurns, Budget: loopDefaultBudget, Turn: 1, State: loopRunning}
	until, hasUntil := fl["--until"]
	dur, hasFor := fl["--for"]
	switch {
	case hasUntil && hasFor:
		return l, errors.New("give --until or --for, not both")
	case hasUntil:
		t, err := time.ParseInLocation("15:04", until, now.Location())
		if err != nil {
			return l, fmt.Errorf("--until must be HH:MM: %q", until)
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		l.Until = at
	case hasFor:
		d, err := time.ParseDuration(dur)
		if err != nil || d <= 0 || d > 24*time.Hour {
			return l, fmt.Errorf("--for must be a duration up to 24h, such as 8h: %q", dur)
		}
		l.Until = now.Add(d)
	}
	if v, ok := fl["--turns"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return l, fmt.Errorf("--turns must be a whole number from 1 to 200: %q", v)
		}
		l.Turns = n
	}
	if v, ok := fl["--budget"]; ok {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
		if err != nil || n < 1 || n > 100 {
			return l, fmt.Errorf("--budget must be a percent from 1 to 100: %q", v)
		}
		l.Budget = n
	}
	return l, nil
}

// loopRules is the loop's rules in one line, as each continue note restates them.
func loopRules() string {
	return "One iteration: pick the next improvement toward the goal, make it, measure it, commit it on your branch and add a line to " + loopLog +
		"; never ask or wait; never merge, push, deploy or install; end with `LOOP: continue - <next step>` or `LOOP: done - <why>`."
}

// loopPrompt is the first prompt of a loop session: the track, then the loop's rules.
func loopPrompt(track string, l loopState) string {
	return trackPrompt(track) + "\n\n" + fmt.Sprintf(`This track runs as an unattended loop while Emil is away: until %s, at most %d turns. Each turn is ONE iteration toward the goal, and the next turn starts on its own.
- Each turn: pick the next improvement toward the goal, make it, measure it, commit it on your branch, and append one line to %s in your folder with what changed and the measurement.
- Never ask Emil anything and never wait for an answer: decide, and write the decision under "Decisions for Emil" in %s. Put no asks on the card.
- Never merge, push, deploy or install.
- A permission prompt is denied after two minutes: find a way inside your folder that needs no new permission, or skip the step and note it in the log.
- End every turn with a last line `+"`LOOP: continue - <next step>`"+` or `+"`LOOP: done - <why nothing worth doing is left>`"+`.`,
		l.Until.Local().Format("15:04"), l.Turns, loopLog, loopLog)
}

// loopNote is the note that starts the loop's next turn.
func loopNote(l loopState, errored bool) string {
	s := fmt.Sprintf("Continue the loop: iteration %d of %d, until %s. %s", l.Turn, l.Turns, l.Until.Local().Format("15:04"), loopRules())
	if errored {
		s = "Your last turn was cut off. " + s
	}
	return s
}

// loopInput is what the next step of a loop is decided from.
type loopInput struct {
	last    string
	errored bool
	stop    bool
	now     time.Time
	usage   *deskui.Usage
}

// loopStep is what the watch does at a loop's stop: continue, pause until, or end, and why.
type loopStep struct {
	kind  string
	why   string
	until time.Time
}

// loopNext decides a loop's next step at the end of a turn, or when a pause ran out. A turn that
// errored is tried again without counting as an iteration, up to loopMaxErrors in a row.
func loopNext(l loopState, in loopInput) loopStep {
	end := func(why string) loopStep { return loopStep{kind: loopEnded, why: why} }
	if in.stop {
		return end("stopped with tracks loop stop")
	}
	if verdict, why := loopVerdict(in.last); verdict == "done" {
		if why == "" {
			why = "nothing worth doing is left"
		}
		return end("LOOP: done - " + why)
	}
	if in.errored && l.Errors >= loopMaxErrors {
		return end(fmt.Sprintf("%d turns in a row ended in an error", loopMaxErrors))
	}
	if !in.errored && l.Turn >= l.Turns {
		return end(fmt.Sprintf("all %d turns run", l.Turns))
	}
	if !in.now.Before(l.Until) {
		return end("the deadline " + l.Until.Local().Format("15:04") + " passed")
	}
	if why, reset, over := loopOverBudget(l, in.now, in.usage); over {
		if !reset.Before(l.Until) {
			return end(why + ", and it resets after the deadline")
		}
		return loopStep{kind: loopPaused, why: why, until: reset}
	}
	return loopStep{kind: loopRunning}
}

// loopVerdict is the last LOOP: line of an answer: done or continue, and the words after it.
func loopVerdict(text string) (verdict, why string) {
	m := loopMarker.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return "", ""
	}
	last := m[len(m)-1]
	return last[1], strings.TrimSpace(strings.Trim(strings.TrimSpace(last[2]), "`"))
}

// loopOverBudget is whether usage is at or over the loop's budget of the five-hour window, or the
// seven-day cap, and when that window resets. Usage older than loopUsageStale, or a window whose
// reset has passed, is unknown and never pauses the loop.
func loopOverBudget(l loopState, now time.Time, u *deskui.Usage) (why string, reset time.Time, over bool) {
	if u == nil || now.Sub(u.UpdatedAt) > loopUsageStale {
		return "", time.Time{}, false
	}
	check := func(lim *deskui.UsageLimit, name string, cap int) bool {
		if lim == nil || lim.UsedPercentage < float64(cap) || lim.ResetsAt != nil && !lim.ResetsAt.After(now) {
			return false
		}
		why = fmt.Sprintf("the %s usage is at %.0f%%, at or over %d%%", name, lim.UsedPercentage, cap)
		reset = now.Add(loopUnknownReset)
		if lim.ResetsAt != nil {
			reset = *lim.ResetsAt
		}
		return true
	}
	if check(u.FiveHour, "five-hour", l.Budget) || check(u.SevenDay, "seven-day", loopSevenDayCap) {
		return why, reset, true
	}
	return "", time.Time{}, false
}

// activeLoops are the loops that have not ended, by track.
func activeLoops() map[string]loopState {
	out := map[string]loopState{}
	for _, l := range readLoops() {
		if l.State != loopEnded {
			out[l.Track] = l
		}
	}
	return out
}

// loopSessionOf is the newest session the desk started on track, a running one before a stopped one.
func loopSessionOf(list []store.Session, track string) (store.Session, bool) {
	var found store.Session
	ok := false
	for _, s := range list {
		if s.Track != track {
			continue
		}
		if !ok || (found.Host == "stopped") && s.Host != "stopped" || (found.Host == "stopped") == (s.Host == "stopped") && s.CreatedAt.After(found.CreatedAt) {
			found, ok = s, true
		}
	}
	return found, ok
}

// loopEvents runs the loops at their sessions' stops: a done or error event of a running loop is
// withheld while the watch continues the loop itself, and passed on with a one-line summary once
// the loop ends. A usage-limit block of a loop session is withheld too: the reaper turns it into
// an error once the limit resets, and the loop goes on from there. tick then resumes paused loops
// whose pause ran out, and ends the loops whose session Emil took over.
func loopEvents(evs []deskEvent, list []store.Session) []deskEvent {
	loops := activeLoops()
	if len(loops) == 0 {
		return evs
	}
	now := loopNow()
	var usage *deskui.Usage
	usageRead := false
	getUsage := func() *deskui.Usage {
		if !usageRead {
			usage, usageRead = readUsage(), true
		}
		return usage
	}
	out := make([]deskEvent, 0, len(evs))
	for _, e := range evs {
		i := slices.IndexFunc(list, func(s store.Session) bool { return s.ID == e.session })
		if e.session == "" || e.interim || i < 0 {
			out = append(out, e)
			continue
		}
		s := list[i]
		l, ok := loops[s.Track]
		if !ok || s.Owner != "desk" {
			out = append(out, e)
			continue
		}
		switch {
		case e.outcome == "blocked" && store.UsageLimited(s.WaitingFor):
			continue
		case e.outcome != "done" && e.outcome != "error":
			out = append(out, e)
			continue
		case l.State == loopPaused, compactMarked(s.ID):
			// A paused loop goes on from tick; a compaction turn's stop leaves the loop's note waiting.
			continue
		}
		last := s.LastAnswer
		if last == "" {
			last = e.text
		}
		errored := e.outcome == "error"
		step := loopNext(l, loopInput{last: last, errored: errored, stop: stopRequested(l.Track), now: now, usage: getUsage()})
		if errored && step.kind != loopEnded {
			l.Errors++
		} else if !errored {
			l.Errors = 0
		}
		if ended, ok := applyLoopStep(&l, step, s, !errored, now); ended {
			out = append(out, loopEndEvent(e, l))
		} else if !ok {
			out = append(out, e)
		}
		loops[l.Track] = l
	}
	return append(out, loopTick(loops, list, now, getUsage)...)
}

// applyLoopStep records step on l and acts on it: delivers the next turn's note, records the
// pause, or ends the loop. advance counts the turn that just ended as an iteration. ok is false
// when the step could not be recorded, so the event goes to the desk as it is.
func applyLoopStep(l *loopState, step loopStep, s store.Session, advance bool, now time.Time) (ended, ok bool) {
	switch step.kind {
	case loopPaused:
		l.State, l.PausedUntil = loopPaused, &step.until
		return false, writeLoop(*l) == nil
	case loopEnded:
		endLoop(l, step.why, now)
		return true, true
	}
	if advance {
		l.Turn++
	}
	l.State, l.PausedUntil = loopRunning, nil
	if err := writeLoop(*l); err != nil {
		return false, false
	}
	if err := continueLoop(s, loopNote(*l, l.Errors > 0)); errors.Is(err, store.ErrEmils) {
		endLoop(l, "Emil took the session over", now)
		return true, true
	}
	return false, true
}

// continueLoop gives the loop session its next turn's note: at once when it is stopped, held for
// the watch when it still works or waits on a compaction. The note replaces one already held, so
// a loop never stacks two turns.
func continueLoop(s store.Session, text string) error {
	var n store.NoteSend
	if err := withBareStore(func(st *store.Store) (err error) { n, err = st.SendNote(s.ID, "desk", text, true, false); return err }); err != nil {
		return err
	}
	if !n.Deliver {
		return nil
	}
	if holdForCompaction(s) || deliverNote(s, n.Text) != nil {
		return withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) })
	}
	return nil
}

// endLoop marks l ended and logs it in the desk's handled log.
func endLoop(l *loopState, why string, now time.Time) {
	l.State, l.Ended, l.EndedAt, l.PausedUntil = loopEnded, why, &now, nil
	_ = writeLoop(*l)
	os.Remove(loopStopPath(l.Track))
	_ = withStore(func(st *store.Store) error {
		_, err := st.LogDesk(l.Track, "noted", "loop", loopSummary(*l))
		return err
	})
}

// logLoopStart logs a loop's start and its limits in the desk's handled log; nil logs nothing.
func logLoopStart(l *loopState) error {
	if l == nil {
		return nil
	}
	did := fmt.Sprintf("loop started: %d turns until %s, budget %d%% of the five-hour usage", l.Turns, l.Until.Local().Format("15:04"), l.Budget)
	return withStore(func(st *store.Store) error {
		_, err := st.LogDesk(l.Track, "noted", "loop", did)
		return err
	})
}

// loopSummary is the one line a loop's end report starts with.
func loopSummary(l loopState) string {
	return fmt.Sprintf("loop ended after %d of %d turns: %s", l.Turn, l.Turns, l.Ended)
}

// loopEndEvent is e, the stop that ended the loop, with the loop's summary in front.
func loopEndEvent(e deskEvent, l loopState) deskEvent {
	label, _, _ := strings.Cut(e.line, " "+e.outcome+": ")
	sum := "[" + loopSummary(l) + "]"
	e.line = fmt.Sprintf("%s %s: %s %s", label, e.outcome, sum, oneLine(e.text))
	e.text = sum + "\n\n" + e.text
	return e
}

// loopTick resumes the paused loops whose pause ran out, ends the paused loops a stop was asked
// for, and ends the loops whose session Emil took over.
func loopTick(loops map[string]loopState, list []store.Session, now time.Time, usage func() *deskui.Usage) []deskEvent {
	var out []deskEvent
	for _, track := range slices.Sorted(maps.Keys(loops)) {
		l := loops[track]
		if l.State == loopEnded {
			continue
		}
		s, ok := loopSessionOf(list, track)
		if !ok {
			continue
		}
		if s.Owner != "desk" {
			endLoop(&l, "Emil took the session over", now)
			continue
		}
		if l.State != loopPaused {
			continue
		}
		stop := stopRequested(track)
		if !stop && l.PausedUntil != nil && now.Before(*l.PausedUntil) {
			continue
		}
		step := loopNext(l, loopInput{stop: stop, errored: l.Errors > 0, now: now, usage: usage()})
		if ended, _ := applyLoopStep(&l, step, s, l.Errors == 0, now); ended {
			label := sessionLabel(s.Track, s.Name, s.ID)
			e := deskEvent{line: label + " done: ", session: s.ID, outcome: "done", text: s.LastAnswer, track: s.Track}
			out = append(out, loopEndEvent(e, l))
		}
	}
	return out
}

// isLoopSession is whether the session runs a loop that has not ended.
func isLoopSession(id string) bool {
	var list []store.Session
	if withBareStore(func(st *store.Store) (err error) { list, err = st.DeskSessions(); return err }) != nil {
		return false
	}
	i := slices.IndexFunc(list, func(s store.Session) bool { return s.ID == id })
	if i < 0 || list[i].Track == "" || list[i].Owner != "desk" {
		return false
	}
	l, ok := readLoop(list[i].Track)
	return ok && l.State != loopEnded
}

// loopDenyNote closes a loop session's prompt that nobody answered in loopPermissionWait.
const loopDenyNote = "denied: Emil is away and the session runs a loop"

// loopDenyText is what a loop session is told when its prompt is denied.
const loopDenyText = "Denied: Emil is away and this session runs as an unattended loop, so it gets no new permission. Find a way inside your folder that needs no new permission, or skip this step and note it under \"Decisions for Emil\" in " + loopLog + "."

// loopDenied is whether question id closed as a loop's unanswered prompt.
func loopDenied(id int64) bool {
	var q store.Question
	if withBareStore(func(st *store.Store) (err error) { q, err = st.GetQuestion(id); return err }) != nil {
		return false
	}
	return !q.Open() && q.AnsweredAt.IsZero() && q.AnswerText == loopDenyNote
}

// loopCmd is tracks loop: the loops, or tracks loop stop NAME, which ends one after its current turn.
func loopCmd(args []string, out io.Writer) error {
	switch {
	case len(args) == 0:
		return listLoops(out, loopNow())
	case len(args) == 2 && args[0] == "stop":
		return stopLoop(args[1], out)
	}
	return errors.New(loopUsage)
}

func listLoops(out io.Writer, now time.Time) error {
	loops := readLoops()
	if len(loops) == 0 {
		fmt.Fprintln(out, "no loops")
		return nil
	}
	width := 0
	for _, l := range loops {
		width = max(width, len(l.Track))
	}
	for _, l := range loops {
		fmt.Fprintf(out, "%-*s  turn %d/%d  until %s  %s\n", width, l.Track, l.Turn, l.Turns, loopClock(l.Until, now), loopStateText(l, now))
	}
	return nil
}

// loopClock is HH:MM, with the date when t is not today.
func loopClock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.YearDay() != now.YearDay() || t.Year() != now.Year() {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("15:04")
}

func loopStateText(l loopState, now time.Time) string {
	switch {
	case l.State == loopEnded:
		return "ended: " + l.Ended
	case l.State == loopPaused && l.PausedUntil != nil:
		s := "paused until " + loopClock(*l.PausedUntil, now)
		if stopRequested(l.Track) {
			s += ", stopping"
		}
		return s
	case stopRequested(l.Track):
		return "running, stops after this turn"
	}
	return l.State
}

func stopLoop(track string, out io.Writer) error {
	l, ok := readLoop(track)
	switch {
	case !ok:
		return fmt.Errorf("no loop on %s", track)
	case l.State == loopEnded:
		return fmt.Errorf("%s's loop has ended: %s", track, l.Ended)
	}
	if err := os.WriteFile(loopStopPath(track), nil, 0o644); err != nil {
		return err
	}
	if l.State == loopPaused {
		fmt.Fprintf(out, "%s's loop ends at the watch's next look; its session is not stopped\n", track)
		return nil
	}
	fmt.Fprintf(out, "%s's loop ends after its current turn; its session is not stopped\n", track)
	return nil
}

// loopsStamp changes when a loop's state does, so serve sends the sessions again.
func loopsStamp() string {
	ents, _ := os.ReadDir(loopsDir())
	var b strings.Builder
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			fmt.Fprintf(&b, "|%s:%d", e.Name(), fi.ModTime().UnixNano())
		}
	}
	return "loops" + b.String()
}

// apiLoop is a session's loop in the snapshot.
type apiLoop struct {
	Turn        int        `json:"turn"`
	Turns       int        `json:"turns"`
	Until       time.Time  `json:"until"`
	State       string     `json:"state"`
	PausedUntil *time.Time `json:"paused_until"`
	Ended       string     `json:"ended,omitempty"`
}

// snapshotLoops are the loops by track, for the snapshot's sessions.
func snapshotLoops() map[string]*apiLoop {
	out := map[string]*apiLoop{}
	for _, l := range readLoops() {
		a := &apiLoop{Turn: l.Turn, Turns: l.Turns, Until: apiTime(l.Until), State: l.State, Ended: l.Ended}
		if l.PausedUntil != nil {
			t := apiTime(*l.PausedUntil)
			a.PausedUntil = &t
		}
		out[l.Track] = a
	}
	return out
}
