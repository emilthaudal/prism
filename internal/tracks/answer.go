package tracks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const answeredNote = "Already answered; waiting for the agent"

const (
	maxAnswer   = 2000
	sendTimeout = 10 * time.Second
)

// answerEnterDelay lets the agent take the typed text before Enter arrives, so Enter is not
// read as part of the input burst.
var answerEnterDelay = 300 * time.Millisecond

// cleanText strips escape sequences and control characters from s and keeps its line breaks, so
// text from a card cannot move the cursor or break the colours around it.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.NewReplacer("\r\n", "\n", "\r", " ", "\t", " ").Replace(s)
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		// ZWJ and ZWNJ join emoji and Persian or Indic letters; they move nothing.
		if r != '\n' && r != '‌' && r != '‍' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, s)
}

// sanitizeAnswer makes text safe to type into an agent: one line, no escape sequences or
// control characters, white space squeezed, at most maxAnswer characters.
func sanitizeAnswer(s string) string {
	s = cleanText(strings.NewReplacer("\r\n", " ", "\n", " ").Replace(s))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxAnswer {
		s = strings.TrimSpace(string(r[:maxAnswer]))
	}
	// A trailing backslash makes Claude Code's Enter insert a newline instead of submitting.
	for strings.HasSuffix(s, `\`) {
		s = strings.TrimSpace(strings.TrimSuffix(s, `\`))
	}
	return s
}

// modeRefusal is why text cannot be typed into Claude Code's prompt as a reply: a leading
// ! or # switches the prompt to bash or memory mode. A leading / is a slash command.
func modeRefusal(text string) string {
	if strings.HasPrefix(text, "!") || strings.HasPrefix(text, "#") {
		return "starts with ! or #, which Claude Code treats as bash or memory mode: type it in the agent"
	}
	return ""
}

// acceptText is the reply that accepts the recommendations of the numbered asks: the batch
// heading, then one line per ask that quotes it, so an agent that rewrote its asks between the
// render and the keypress can't take a bare number for another ask.
func acceptText(asks []int, text map[int]string) string {
	lines := make([]string, len(asks))
	for i, n := range asks {
		q, _ := splitRecommend(text[n])
		lines[i] = fmt.Sprintf("Emil's answer to your ask %d, %s: go with your recommendation.", n, quoteAsk(q))
	}
	return batchText(lines, " ")
}

// replyState is the open reply editor for one track.
type replyState struct {
	track    string
	question int64 // the question the reply answers; 0 for a free reply to the track
	ed       lineEditor
	asks     []int // the asks the prefill accepts; nil for a free reply
	ids      map[int]int64
	text     map[int]string // each accepted ask's text, to quote it
	sending  bool
}

// askIDs are the ids, with tracks.db, of the asks the prefill accepts, as the pane showed them
// when each digit was pressed.
func (r *replyState) askIDs() []int64 {
	var out []int64
	for _, n := range r.asks {
		if id := r.ids[n]; id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// prefilled is whether the text is still exactly the generated accept line, so more digits
// add asks instead of typing digits.
func (r *replyState) prefilled() bool {
	return len(r.asks) > 0 && r.ed.String() == acceptText(r.asks, r.text)
}

func (r *replyState) addAsk(a ask) {
	n := a.n
	if !slices.Contains(r.asks, n) {
		r.asks = append(r.asks, n)
		slices.Sort(r.asks)
		if r.ids == nil {
			r.ids, r.text = map[int]int64{}, map[int]string{}
		}
		r.ids[n], r.text[n] = a.id, a.text
	}
	r.ed.set(acceptText(r.asks, r.text))
}

// answerMsg is the outcome of a send. typed means the text reached the agent's input but
// Enter did not follow, so the editor must not offer it again. busy means the agent was working
// or waiting on a prompt: before the text nothing was typed; after it, the text sits in the
// input of the block target names. working is busy because the agent runs a turn, not because it
// waits on a permission or question prompt.
type answerMsg struct {
	name, status               string
	sent, typed, busy, working bool
	target                     []string
}

// rexRunIn is rexRun with input on stdin and rex's complaint in the error.
func rexRunIn(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "rex", append([]string{rexNoStart}, args...)...)
	cmd.WaitDelay = waitDelay
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// refusal says why an agent in state s must not be typed into, or "" when it may.
func refusal(name string, s agentState) string {
	switch {
	case s.Owner == 0:
		return "Rex does not say which process runs " + name + "'s agent"
	case s.Exited && s.Foreground != "":
		return name + "'s agent has exited: " + s.Foreground + " runs in its block"
	case s.Exited:
		return name + "'s agent has exited"
	}
	switch s.State {
	case "idle", "done":
		return ""
	case "working":
		return name + " is working: answer when it stops"
	case "blocked":
		what := "a prompt"
		if s.Kind != "" {
			what = "a " + s.Kind + " prompt"
		}
		return name + " is waiting on " + what + ": answer it in the agent (enter)"
	case "error":
		return name + "'s agent stopped on an error: look in the agent (enter)"
	}
	return fmt.Sprintf("%s's agent state is %q", name, s.State)
}

// busy is whether refusal turns the agent away because it is working or waiting on a prompt.
func busy(s agentState) bool {
	return s.Owner != 0 && !s.Exited && (s.State == "working" || s.State == "blocked")
}

// stateWord is how the typed-not-sent footer names what the agent is doing.
func stateWord(s agentState, r rexResult) string {
	switch {
	case r.failed:
		return "not answering Rex"
	case !r.found:
		return "gone"
	case s.Exited:
		return "exited"
	case s.State == "blocked" && s.Kind != "":
		return "blocked on a " + s.Kind + " prompt"
	}
	return s.State
}

// typeIntoAgent types text (already sanitized) into the agent of the Rex session sess and
// presses Enter, only when Rex says right now, before the text and again before Enter, that
// the same live agent is idle or done. fail is nil once Enter was sent; r is the agent Rex found.
func typeIntoAgent(name, sess, hint, text string) (r rexResult, fail *answerMsg) {
	return typeInto(name, sess, hint, text, refusal)
}

// typeInto is typeIntoAgent with refuse in place of refusal: the states it turns the agent away in.
func typeInto(name, sess, hint, text string, refuse func(string, agentState) string) (r rexResult, fail *answerMsg) {
	no := func(why string) (rexResult, *answerMsg) {
		return r, &answerMsg{name: name, status: "Not sent: " + why}
	}
	typed := func(msg answerMsg) (rexResult, *answerMsg) { return r, &msg }
	typedOnly := func(why string) answerMsg {
		return answerMsg{name: name, typed: true, status: "Typed into " + name + " but not sent: " + why + ". Press enter in the agent."}
	}
	if sess == "" {
		return no(name + " has no rex_session")
	}
	r = fetchSession(sess, hint, "")
	switch {
	case r.failed:
		return no("could not ask Rex what " + name + "'s agent is doing")
	case !r.found:
		return no("Rex reports no agent in " + name + "'s session")
	}
	if why := refuse(name, r.state); why != "" {
		_, msg := no(why)
		msg.busy = busy(r.state)
		return r, msg
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	target := []string{"--session=" + r.id, "--block=" + r.block}
	if _, err := rexRunIn(ctx, text, append([]string{"send"}, target...)...); err != nil {
		// Some of the text may have arrived, so it is not offered for sending again.
		return typed(answerMsg{name: name, typed: true, status: "could not type into " + name + "'s agent (" + err.Error() + "): check its input before you answer again."})
	}
	time.Sleep(answerEnterDelay)
	again := fetchSession(sess, r.block, r.id)
	if again.failed {
		return typed(answerMsg{name: name, typed: true, status: "Typed into " + name + " but not sent: Rex did not answer, so its agent could not be confirmed. Check that input before you press enter."})
	}
	if !again.found || again.block != r.block || again.state.Owner != r.state.Owner || again.state.Exited {
		// The agent is gone, so the text may sit at a shell prompt: never advise pressing enter.
		return typed(answerMsg{name: name, typed: true, status: "Typed into " + name + " but not sent: its agent exited. Clear that input before you run anything there."})
	}
	if refuse(name, again.state) != "" {
		msg := typedOnly("it is " + stateWord(again.state, again))
		msg.busy, msg.target = busy(again.state), target
		msg.working = again.state.State == "working"
		return typed(msg)
	}
	if _, err := rexRunIn(ctx, "", append(append([]string{"send-key"}, target...), "enter")...); err != nil {
		return typed(typedOnly("enter failed (" + err.Error() + ")"))
	}
	return r, nil
}

// sendAnswer types text into the track's agent and presses Enter, only when Rex says right
// now, before the text and again before Enter, that the same live agent is idle or done.
// The text goes on stdin, so it can never become a flag. asks are the numbers the answer
// accepts and, with tracks.db, ids the same asks as the pane showed them.
func sendAnswer(name, text, hint string, asks []int, ids []int64) answerMsg {
	no := func(why string) answerMsg { return answerMsg{name: name, status: "Not sent: " + why} }
	text = sanitizeAnswer(text)
	if text == "" {
		return no("the reply is empty")
	}
	if why := modeRefusal(text); why != "" {
		return no(why)
	}
	c, err := loadCard(name)
	if err != nil {
		return no("no card for " + name)
	}
	sess := c.Meta["rex_session"]
	if sess == "" {
		if bg, ok := bgSession(name); ok {
			return sendBgAnswer(name, bg, text, c.Meta["dir"], ids)
		}
	}
	r, fail := typeIntoAgent(name, sess, hint, text)
	if fail != nil {
		return *fail
	}
	status := "Sent to " + name
	ev := Event{At: time.Now().UTC(), Event: "answer", Track: name, RexSession: r.id, RexBlock: r.block, Cwd: c.Meta["dir"], Len: len(text), Kind: classify(text)}
	if storeOn() {
		if err := answerSentStore(name, ids, ev); err != nil {
			status += ", but could not record it: " + err.Error()
		}
		return answerMsg{name: name, status: status, sent: true}
	}
	// The hook stamps answered: when the agent takes the prompt; the numbers only the pane knows.
	if len(asks) > 0 || c.Meta["answered_asks"] != "" {
		if err := setMetas(name, [][2]string{{"answered_asks", askedValue(asks, time.Now())}}); err != nil {
			status += ", but could not record the asks: " + err.Error()
		}
	}
	if err := appendEvent(ev); err != nil {
		status += ", but could not log it: " + err.Error()
	}
	return answerMsg{name: name, status: status, sent: true}
}

// sendBgAnswer is sendAnswer for a track whose agent the desk started as a background session:
// the answer goes as a note, now or when the session stops, and the asks count as answered.
func sendBgAnswer(name string, s store.Session, text, dir string, ids []int64) answerMsg {
	from := "emil"
	if p := os.Getenv("CLAUDE_CODE_SESSION_ID"); p != "" && p == s.ParentID {
		from = "desk"
	}
	held, failed, err := noteAnswer(name, s, from, text)
	if err != nil {
		return answerMsg{name: name, status: "Not sent: " + err.Error()}
	}
	status := "Sent to " + name
	switch {
	case failed != nil:
		status = "Could not deliver to " + name + " (" + failed.Error() + "): held, the desk's watch will retry"
	case held:
		status = "Held for " + name + ": delivered when it stops"
	}
	ev := Event{At: time.Now().UTC(), Event: "answer", Track: name, Cwd: dir, Len: len(text), Kind: classify(text)}
	if err := answerSentStore(name, ids, ev); err != nil {
		status += ", but could not record it: " + err.Error()
	}
	return answerMsg{name: name, status: status, sent: true}
}

func (m *paneModel) sendCmd(name, text string, asks []int, ids []int64) tea.Cmd {
	hint := m.agentHint(name)
	return func() tea.Msg { return sendAnswer(name, text, hint, asks, ids) }
}

// agentHint is the block the track's agent was last seen in, for sendAnswer to look at first.
func (m *paneModel) agentHint(name string) string {
	hint := agentBlock(m.evs, m.cardSession(name))
	if b := m.rex.blocks[m.cardSession(name)]; b != "" {
		hint = b
	}
	return hint
}

func (m *paneModel) cardNamed(name string) *Card {
	for _, c := range m.all {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (m *paneModel) cardSession(name string) string {
	if c := m.cardNamed(name); c != nil {
		return c.Meta["rex_session"]
	}
	return ""
}

// canAnswer is whether the pane offers to answer the track: it has asks, or its agent is at rest.
func (m *paneModel) canAnswer(c *Card) bool {
	if c == nil {
		return false
	}
	if c.asks() > 0 {
		return true
	}
	k := m.stateOf(c).kind
	return k == "idle" || k == "done"
}

// openReply opens the reply editor on the selected track, empty for r.
func (m *paneModel) openReply() {
	c := m.selected()
	if c == nil {
		return
	}
	if !m.canAnswer(c) {
		m.status = "Nothing to answer: " + c.Name + " has no asks and its agent is not at rest"
		return
	}
	if m.continuing[c.Name] {
		m.status = continuingNote(c.Name)
		return
	}
	m.reply = &replyState{track: c.Name, ed: lineEditor{limit: maxAnswer}}
}

// answeredGrace covers the moment between Enter and Rex reporting the agent as working. A send
// starts a poll on the next tick (pollEvery); if that poll still reads idle, the next one comes
// rexEvery later and can take rexTimeout, about 5.5s in all. The hook stamps whole seconds, so
// 6s is the least that covers it. Raise it if either Rex timing grows.
const answeredGrace = 6 * time.Second

// answeredNow is whether the card's asks are being handled: the answered stamp holds, and the
// agent is working or blocked, or the stamp is too fresh for Rex to have caught up. An idle,
// errored, exited or missing agent never took the answer (no Stop fires on Esc or a killed pane).
func (m *paneModel) answeredNow(c *Card) bool {
	now := m.now
	if now.IsZero() {
		now = time.Now()
	}
	at, ok := cardAnsweredAt(c, now)
	if !ok {
		return false
	}
	switch m.stateOf(c).kind {
	case "working", "blocked":
		return true
	}
	return now.Sub(at) < answeredGrace
}

// accept handles digit n with the editor closed: a check ask is applied on the card and may tell
// the agent to continue, an agent's ask opens the reply prefilled to accept it. Without asks
// digits do nothing.
func (m *paneModel) accept(n int) tea.Cmd {
	c := m.selected()
	if c == nil || c.asks() == 0 {
		return nil
	}
	a, ok := c.askN(n)
	if !ok {
		m.status = fmt.Sprintf("%s has no ask %d", c.Name, n)
		return nil
	}
	if m.continuing[c.Name] {
		m.status = continuingNote(c.Name)
		return nil
	}
	if !isCheckAsk(a.text) && m.answeredNow(c) {
		m.status = answeredNote
		return nil
	}
	if isCheckAsk(a.text) {
		name, wasParked := c.Name, c.mode() == "parked"
		status, applied := applyCheckAsk(name, a)
		m.status = status
		m.reload()
		if !applied {
			return m.sinceCmd()
		}
		return tea.Batch(m.sinceCmd(), m.continueCmd(name, a, wasParked, status))
	}
	m.reply = &replyState{track: c.Name, ed: lineEditor{limit: maxAnswer}}
	m.reply.addAsk(a)
	return nil
}

// askKeysHint names the digits that accept the card's asks: "1 accept", "1-3 accept", "2, 5 accept".
func askKeysHint(c *Card) string {
	var ns []string
	contiguous := true
	for i, a := range c.askList() {
		if a.n > 9 {
			continue
		}
		ns = append(ns, strconv.Itoa(a.n))
		contiguous = contiguous && a.n == i+1
	}
	switch {
	case len(ns) == 0:
		return "no 1-9 asks"
	case len(ns) == 1:
		return ns[0] + " accept"
	case contiguous:
		return "1-" + ns[len(ns)-1] + " accept"
	}
	return strings.Join(ns, ", ") + " accept"
}

// isCheckAsk is whether the ask was written by tracks check: Emil's own, answered on the card.
// Only the exact shapes askLine writes count; an agent's own "Check …" question does not.
func isCheckAsk(text string) bool {
	return checkAskShape.MatchString(text)
}

var (
	checkAskShape = regexp.MustCompile(`^Check ".*" is now (yes|no)\. Recommend: (set mode to focus|open the track|open the track and see why)\.$`)
	recommendMode = regexp.MustCompile(`Recommend: set mode to (focus)\.$`) // anchored: the quoted check text cannot trigger it
)

// applyCheckAsk takes the ask's recommendation on the card and removes the ask, in one write
// to a card that still holds it, and says what it did. ok is true only for the call that
// removed the ask. It never sends anything to the agent. The answer event keeps tracks check
// from putting the ask back.
func applyCheckAsk(name string, a ask) (status string, ok bool) {
	if storeOn() {
		return applyCheckAskStore(name, a)
	}
	applied := fmt.Sprintf("Applied: ask %d cleared", a.n)
	errChanged := fmt.Errorf("could not apply ask %d: it changed on the card", a.n)
	err := withCardLock(name, func(card string) (string, error) {
		text, removed := removeAsk(card, a)
		if !removed {
			return "", errChanged
		}
		if mm := recommendMode.FindStringSubmatch(a.text); mm != nil {
			var err error
			if text, err = setMetaText(name, text, "mode", mm[1]); err != nil {
				return "", err
			}
			applied = "Applied: mode " + mm[1]
		}
		return text, nil
	})
	switch {
	case errors.Is(err, errSwitched):
		return applyCheckAskStore(name, a)
	case errors.Is(err, errChanged):
		return err.Error(), false
	case err != nil:
		return "could not apply ask: " + err.Error(), false
	}
	if err := appendEvent(Event{At: time.Now().UTC(), Event: "answer", Track: name, Kind: "applied"}); err != nil {
		applied += ", but could not log it: " + err.Error()
	}
	return applied, true
}

const continueTail = " Continue per the card."

// continueShape is the line continueText types; the hook reads it as not typed by Emil.
var continueShape = regexp.MustCompile(`^Check "(?:` + strings.Trim(prCheck.String(), "^$") + `|` + strings.Trim(ciCheck.String(), "^$") + `)" is now yes\.` + regexp.QuoteMeta(continueTail) + `$`)

// continuedMsg is the outcome of telling an agent to continue after a check ask was applied.
type continuedMsg struct {
	name, status string
	sent         bool
}

func continuingNote(name string) string {
	return "Still telling " + name + "'s agent to continue: wait for that to finish"
}

// continueText is the line that tells the agent on card c to go on after Emil applied check ask a,
// and why it must not be typed when it must not. Both are empty for a no: a check that turned
// no needs Emil to look, not the agent to carry on. The ask text is the agent's to write, so the
// check must be the card's own, in the check vocabulary, with a yes tracks check recorded for it.
// cards are all the live cards: one that shares c's session could be a tier 3 track's agent.
func continueText(c *Card, a ask, recs map[string]checkState, cards []*Card) (text, why string) {
	m := checkAskQuote.FindStringSubmatch(a.text)
	if m == nil || m[2] != yes {
		return "", ""
	}
	switch t := c.tier(); {
	case t == "":
		return "", c.Name + " has no tier"
	case t == "3":
		return "", c.Name + " is tier 3"
	case c.mode() == "parked":
		return "", c.Name + " is parked"
	case c.Meta["closed"] != "":
		return "", c.Name + " is closed"
	case c.Meta["rex_session"] == "":
		return "", c.Name + " has no rex_session"
	case c.asks() > 0:
		return "", "the card has other asks"
	}
	for _, o := range cards {
		if o.Name != c.Name && sameSession(o.Meta["rex_session"], c.Meta["rex_session"]) {
			return "", o.Name + " names the same rex_session"
		}
	}
	chk, err := strconv.Unquote(m[1])
	if err != nil {
		return "", "the ask's check does not parse"
	}
	text = askKey(chk, true) + continueTail
	if sanitizeAnswer(text) != text {
		return "", "the check text would change when typed"
	}
	if _, ok := parseCheck(chk); !ok || chk != c.Meta["check"] {
		return "", "the ask's check is not the card's check"
	}
	if st, ok := recs[c.Name+": "+chk]; !ok || !st.Answer {
		return "", "tracks check has no yes on record for it"
	}
	return text, ""
}

// continueCmd tells the track's agent to continue after check ask a was applied, only when every
// guard holds: continueText's, the pane's last word from Rex that the agent is idle or done, and
// sendAnswer's own fresh checks. Otherwise the status line says why the agent was not told.
func (m *paneModel) continueCmd(name string, a ask, wasParked bool, applied string) tea.Cmd {
	notTold := func(why string) tea.Cmd { m.status = applied + "; agent not told: " + why; return nil }
	c := m.cardNamed(name)
	if c == nil {
		return notTold("no card for " + name)
	}
	text, why := continueText(c, a, m.checks, m.all)
	switch {
	case why != "":
		return notTold(why)
	case text == "":
		return nil
	case wasParked:
		return notTold(name + " was parked")
	}
	s, ok := m.rex.state(c.Meta["rex_session"])
	if !ok {
		return notTold("Rex reports no agent for " + name)
	}
	if why := refusal(name, s); why != "" {
		if busy(s) {
			m.status = applied + "; agent busy, not told"
			return nil
		}
		return notTold(why)
	}
	m.status = applied + "; telling the agent to continue…"
	if m.continuing == nil {
		m.continuing = map[string]bool{}
	}
	m.continuing[name] = true
	hint := m.agentHint(name)
	return func() tea.Msg { return tellContinue(name, a, hint, applied) }
}

// tellContinue reads the card again and types the continue line through sendAnswer. A line
// typed but not sent is tracks', not Emil's: it is cleared when the agent turned busy, and the
// footer says to delete it otherwise, so his next typing is not joined to it.
func tellContinue(name string, a ask, hint, applied string) continuedMsg {
	done := func(status string) continuedMsg { return continuedMsg{name: name, status: applied + "; " + status} }
	c, err := loadCard(name)
	if err != nil {
		return done("agent not told: no card for " + name)
	}
	recs, _ := loadChecks()
	text, why := continueText(c, a, recs, loadCards())
	if text == "" {
		return done("agent not told: " + why)
	}
	r := sendAnswer(name, text, hint, nil, nil)
	switch {
	case r.sent:
		msg := done("told the agent to continue")
		msg.sent = true
		return msg
	case r.typed && r.busy && !r.working:
		// A permission or question prompt has the keyboard: a key sent now would go to it.
		return done("agent turned blocked before enter: the continue line sits in " + name + "'s input; delete it, it is not yours")
	case r.typed && r.busy:
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if _, err := rexRunIn(ctx, "", append(append([]string{"send-key"}, r.target...), "ctrl+u")...); err != nil {
			return done("agent turned busy and the continue line could not be withdrawn (" + err.Error() + "): delete it from " + name + "'s input, it is not yours")
		}
		return done("agent turned busy; cleared its input line, with the continue line")
	case r.typed:
		return done("the continue line was typed but not sent: " + r.status + " The line is tracks', not yours: delete it before you type there")
	case r.busy:
		return done("agent busy, not told")
	}
	return done("agent not told: " + strings.TrimPrefix(r.status, "Not sent: "))
}

// removeAsk drops the ask's line from the last Needs me section, if it is still there as is.
func removeAsk(text string, a ask) (string, bool) {
	lines := strings.SplitAfter(text, "\n")
	start, end := -1, len(lines)
	for i, l := range lines {
		if h, ok := heading(l); ok {
			if h == "Needs me" {
				start, end = i, len(lines)
			} else if start >= 0 && end == len(lines) {
				end = i
			}
		}
	}
	for i := start + 1; start >= 0 && i < end; i++ {
		if got, ok := parseAsk(strings.TrimRight(lines[i], "\r\n")); ok && got == a {
			return strings.Join(slices.Delete(lines, i, i+1), ""), true
		}
	}
	return text, false
}

func (m *paneModel) replyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.reply
	if r.sending {
		return m, nil
	}
	k := msg.String()
	switch {
	case k == "esc":
		m.reply, m.status = nil, ""
		return m, nil
	case k == "enter":
		text := sanitizeAnswer(r.ed.String())
		if text == "" {
			m.status = "Not sent: the reply is empty"
			return m, nil
		}
		if why := modeRefusal(text); why != "" {
			m.status = "Not sent: " + why
			return m, nil
		}
		if r.question != 0 {
			m.answerInWords(r.question, text)
			m.reply = nil
			return m, nil
		}
		r.sending, m.status = true, "Sending to "+r.track+"…"
		var (
			sent []int
			ids  []int64
		)
		if r.prefilled() { // edited text no longer answers exactly those asks
			sent, ids = r.asks, r.askIDs()
		}
		return m, m.sendCmd(r.track, text, sent, ids)
	case len(k) == 1 && k >= "1" && k <= "9" && r.prefilled():
		n := int(k[0] - '0')
		c := m.selected()
		a, ok := ask{}, false
		if c != nil {
			a, ok = c.askN(n)
		}
		switch {
		case !ok:
			m.status = fmt.Sprintf("%s has no ask %d", r.track, n)
		case isCheckAsk(a.text):
			m.status = fmt.Sprintf("Ask %d is a check: press %d with the reply closed", n, n)
		default:
			r.addAsk(a)
			m.status = ""
		}
		return m, nil
	default:
		r.ed.key(msg)
	}
	m.status = ""
	return m, nil
}
