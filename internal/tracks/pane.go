package tracks

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	pollEvery    = 500 * time.Millisecond
	leftWidth    = 40 // the list column at its narrowest; it grows with the longest name, up to half the window
	stateWidth   = 12
	glyphWidth   = 1   // the state column below wideCols: the glyph alone
	wideCols     = 100 // from here the list shows state labels and where-lines
	subIndent    = 3
	listFixed    = 5 + askWidth + chipWidth // a row's columns but name and state: dot, marker, three gaps, asks, tier
	staleAfter   = 48 * time.Hour           // a card unchanged for longer shows its age in warning colour
	chipWidth    = 4
	askWidth     = 3 // "◆12"
	gutterW      = 2 // the bar beside Needs me and its space
	goalRows     = 2
	minSplitCols = 80
	maxFocus     = 2
	wheelLines   = 3
	keysShort    = "↑↓ enter tab x ? q"
	moreHint     = "↓ more"
	replyRows    = 6
	staleChecks  = 30 * time.Minute // the header turns the checks' age yellow past this
	errMark      = "✕ "
	lastErrLabel = "Last error  "
)

// hint is one key and what it does. dot joins it to the hint before it with " · " instead of
// two spaces, for keys that belong together.
type hint struct {
	keys, label string
	dot         bool
}

var (
	answerHints  = []hint{{"1-9", "accept", false}, {"r", "reply", true}}
	editHints    = []hint{{"enter", "send", false}, {"esc", "cancel", false}, {"ctrl+u", "clear", false}}
	confirmHints = []hint{{"y", "close", false}, {"n", "keep", true}}
	offerHints   = []hint{{"y", "start", false}, {"n", "not now", true}}
	answerKeys   = plainHints(answerHints)
	editKeys     = plainHints(editHints)
	// pathRe finds an absolute or ~ path in a message, after a space, "(" or the start.
	pathRe = regexp.MustCompile(`(^|[\s(])((?:~|/)[^\s:()]*/[^\s:()]*)`)
)

func plainHints(hs []hint) string { return renderHints(hs, false) }

func styleHints(hs []hint) string { return renderHints(hs, true) }

func renderHints(hs []hint, styled bool) string {
	var b strings.Builder
	for i, h := range hs {
		if i > 0 {
			sep := "  "
			if h.dot {
				sep = " · "
			}
			if styled {
				sep = dimSt.Render(sep)
			}
			b.WriteString(sep)
		}
		label := ""
		if h.label != "" {
			label = " " + h.label
		}
		switch {
		case !styled:
			b.WriteString(h.keys + label)
		case label == "":
			b.WriteString(boldSt.Render(h.keys))
		default:
			b.WriteString(boldSt.Render(h.keys) + dimSt.Render(label))
		}
	}
	return b.String()
}

// baseNames shortens every file path in a message to its file name, so an error fits the
// footer and still says which file.
func baseNames(s string) string {
	return pathRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := pathRe.FindStringSubmatch(m)
		return sub[1] + filepath.Base(sub[2])
	})
}

// isErrStatus is whether a status message reports a failure.
// A reply that is empty is a slip, not a failure.
func isErrStatus(s string) bool {
	switch {
	case s == "Not sent: the reply is empty", s == "Not saved: the reply is empty":
		return false
	case strings.HasPrefix(s, "could not"), strings.HasPrefix(s, "Not sent"):
		return true
	case strings.HasPrefix(s, "Typed into") && strings.Contains(s, "but not sent"):
		return true
	}
	return strings.Contains(s, ", but could not")
}

// currentErr is the failure the footer is showing: the status, or the open form's error.
func (m *paneModel) currentErr() string {
	switch {
	case m.form != nil && m.form.err != "" && m.form.err != requestRequired:
		return m.form.err
	case isErrStatus(m.status):
		return m.status
	}
	return ""
}

func groupLabel(mode string) string {
	for _, g := range groups {
		if g.mode == mode {
			return g.label
		}
	}
	return strings.ToUpper(mode)
}

var (
	dimSt  = lipgloss.NewStyle().Faint(true)
	boldSt = lipgloss.NewStyle().Bold(true)
	warnSt = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	goodSt = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	badSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	linkSt = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Underline(true)
	selSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // the ▌ marker; ▌ means only the selection
	// selBg tints the selected row: a step off the terminal background, from the 256 greys, so it
	// reads on both. Without colour the marker and bold still identify the row.
	selBg = func(light bool) color.Color {
		return lipgloss.LightDark(!light)(lipgloss.Color("254"), lipgloss.Color("237"))
	}
	// selUnknownBg is the tint before the terminal has said whether it is light or dark: #767676
	// holds 4.5:1 against both black and white text.
	selUnknownBg = lipgloss.Color("243")
	groups       = []struct{ mode, label string }{{"focus", "FOCUS"}, {"background", "BACKGROUND"}, {"waiting", "WAITING"}, {"parked", "PARKED"}}
	keyMode      = map[string]string{"f": "focus", "b": "background", "p": "parked"} // waiting is computed, not set (EMI-423)
	helpKeys     = [][2]string{
		{"↑ ↓  j k", "move"},
		{"tab  ⇧tab", "next, prev needing you"},
		{"enter  o  O", "open in Rex; links"},
		{"1-9  r  a", "accept N, reply, answer"},
		{"f b w p  space", "set the mode; fold threads"},
		{"n  x", "new track, close the track"},
		{"pgup pgdn", "scroll, or wheel; shift-drag selects"},
		{"h  d  ?  esc", "log, desk, help"},
		{"q  ctrl+c", "quit"},
	}
)

type (
	tickMsg  struct{}
	sinceMsg struct {
		name  string
		lines []string
		at    time.Time
		first bool
		err   string
	}
	statusMsg string
	closedMsg struct {
		name, note string
		err        error
	}
	// undoneMsg is the outcome of u on a handled entry.
	undoneMsg string
)

type sinceInfo struct {
	lines []string
	at    time.Time
	first bool
	err   string
}

type paneModel struct {
	cards         []*Card // the rows drawn: tracks that are not threads, and the threads of unfolded leads
	all           []*Card // every live card, threads folded or not
	parents       map[string]string
	unfolded      map[string]bool // leads whose threads are shown
	evs           []Event
	hookFail      hookRecord // hook.err while it is newer than hook.ok
	hookFailing   bool
	checks        map[string]checkState // tracks check's last answers, keyed "NAME: CHECK"
	continuing    map[string]bool       // tracks whose agent is being told to continue
	now           time.Time
	w, h          int
	cur           string
	idx           int
	since         map[string]sinceInfo
	status, stamp string
	hold          string // track opened with enter; its "since" stays as shown
	float         bool
	homed         bool // runs in the tracks session, where cmd+shift+h goes back
	rex           *rexTracker
	closed        int    // tracks closed today
	carried       int    // inbox.md entries from closed tracks, not yet checked off
	confirm       string // track awaiting y/n to close
	confirmQ      string // what closing it does, worked out when x was pressed
	lastErr       string // the newest failure shown in the footer, kept for the help overlay
	lastErrAt     time.Time
	errSeen       string          // the status lastErr was taken from, so one failure is recorded once
	offer         offerMsg        // track without an agent, awaiting y/n to start one
	swap          *focusFullError // a third focus track: awaiting which of the two moves to background
	starting      string          // track whose agent is being started
	help          bool
	scroll        int // detail lines scrolled past
	frame         int
	reply         *replyState
	editLine      int // the reply editor's line in the detail body, -1 when not shown
	form          *newForm
	flash         string // a short footer note that clears itself
	flashUntil    time.Time
	linkTrack     string // the track whose link o opened last
	linkIdx       int
	linkAt        time.Time
	viewNote      string // a view that failed to render, or the newest hand edit kept aside; "" when neither
	light         bool   // the terminal background is light, for the selection tint
	bgKnown       bool   // the terminal answered the background colour query
	desk          deskState
	deskStarting  bool             // d is opening or starting the desk
	logv          *handledLog      // the full handled log, when open
	undoing       bool             // u is taking an entry back
	questions     []store.Question // the open questions, when tracks.db is there
	useQuestions  bool             // the band and its keys read questions, not the cards' asks
	selQ          int64            // the question [ and ] chose on the selected track
	bandFocus     bool             // the band's keys (enter, 1-9, r, z, space) act on the selected question
	expanded      map[int64]bool   // questions whose options and evidence are shown
	pending       []*pendingAnswer // answers waiting out their countdown
	clockFn       func() time.Time // replaces time.Now in tests
}

func (m *paneModel) flashMsg(s string) {
	m.flash, m.flashUntil = s, time.Now().Add(flashFor)
}

func newPane() *paneModel {
	m := &paneModel{since: map[string]sinceInfo{}, w: 100, h: 30, rex: newRexTracker()}
	m.reload()
	return m
}

// setMode sets the card's mode, adding the mode: line to a card without one, and logs the
// change, so a mode lost later can be traced. The log is only evidence: the mode is set even
// when it cannot be written.
func setMode(name, mode string) error {
	if storeOn() {
		return setModeStore(name, mode)
	}
	err := rewriteCard(name, func(text string) (string, error) {
		return upsertMetaText(name, text, "mode", mode)
	})
	if errors.Is(err, errSwitched) {
		return setModeStore(name, mode)
	}
	if err != nil {
		return err
	}
	_ = appendEvent(Event{At: time.Now().UTC(), Event: "mode", Track: name, Kind: mode})
	return nil
}

// setMeta rewrites only one value of the card's frontmatter, keeping its trailing comment.
func setMeta(name, key, value string) error {
	if storeOn() {
		return setMetasStore(name, [][2]string{{key, value}})
	}
	err := rewriteCard(name, func(text string) (string, error) {
		return setMetaText(name, text, key, value)
	})
	if errors.Is(err, errSwitched) {
		return setMetasStore(name, [][2]string{{key, value}})
	}
	return err
}

// rewriteCard writes the card through fn and keeps the file's mtime: it is what an unstamped
// card's unread dot falls back to, and a value changed here is not the agent writing its card.
func rewriteCard(name string, fn func(text string) (string, error)) error {
	st, statErr := os.Stat(cardPath(name))
	err := withCardLock(name, fn)
	if err == nil && statErr == nil {
		_ = os.Chtimes(cardPath(name), st.ModTime(), st.ModTime())
	}
	return err
}

func setMetaText(name, text, key, value string) (string, error) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", fmt.Errorf("card %s has no frontmatter", name)
	}
	for i := 1; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		rest := ""
		if strings.HasSuffix(v, "\r") {
			v, rest = v[:len(v)-1], "\r"
		}
		if loc := comment.FindStringIndex(v); loc != nil {
			rest, v = v[loc[0]:]+rest, v[:loc[0]]
		}
		pad := v[:len(v)-len(strings.TrimLeft(v, " \t"))]
		if pad == "" {
			pad = " "
		}
		lines[i] = k + ":" + pad + value + rest
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("card %s has no %s line", name, key)
}

// orderedCards groups the cards FOCUS, BACKGROUND, WAITING, PARKED, each by rank with unranked
// cards last, and cards with asks first among the same rank.
func orderedCards(cards []*Card) []*Card {
	var out []*Card
	for _, g := range groups {
		var in []*Card
		for _, c := range cards {
			if groupOf(c) == g.mode {
				in = append(in, c)
			}
		}
		slices.SortStableFunc(in, func(a, b *Card) int {
			if ra, rb := a.rank(), b.rank(); ra != rb {
				switch {
				case ra == 0:
					return 1
				case rb == 0:
					return -1
				}
				return ra - rb
			}
			return b.asks() - a.asks()
		})
		out = append(out, in...)
	}
	return out
}

// paneStamp changes whenever something the pane shows may have changed: with tracks.db, a
// commit by any other connection (data_version), a hook record or a view note; without it,
// any file.
func paneStamp() string {
	if !storeOn() {
		return fileStamp()
	}
	var b strings.Builder
	b.WriteString(storeStamp())
	for _, p := range append([]string{hookOKPath(), hookErrPath(), renderErrPath()}, editedViews()...) {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", p, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}

func fileStamp() string {
	paths, _ := filepath.Glob(filepath.Join(home(), "cards", "*.md"))
	archived, _ := filepath.Glob(filepath.Join(archiveDir(), "*.md"))
	var b strings.Builder
	for _, p := range append(append(paths, archived...), logPath(), checksPath(), hookOKPath(), hookErrPath()) {
		if st, err := os.Stat(p); err == nil {
			// The inode too: a pane write keeps the mtime (rewriteCard), and a same-size rewrite,
			// such as one rex_session id for another, would otherwise look unchanged. Card writes
			// are atomic renames, so each one gets a new inode.
			var ino uint64
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				ino = uint64(sys.Ino)
			}
			fmt.Fprintf(&b, "%s:%d:%d:%d;", p, st.ModTime().UnixNano(), st.Size(), ino)
		}
	}
	return b.String()
}

// cleanCards strips escape sequences from the text of cards before the pane draws it.
func cleanCards(cards []*Card) []*Card {
	for _, c := range cards {
		for k, v := range c.Meta {
			c.Meta[k] = cleanText(v)
		}
		for k, v := range c.Sections {
			c.Sections[k] = cleanText(v)
		}
	}
	return cards
}

func (m *paneModel) reload() {
	m.stamp = paneStamp()
	cards := cleanCards(loadCards())
	for _, c := range cards {
		if s, ok := m.rex.states[c.Meta["rex_session"]]; ok && s.Owner != 0 && !s.Exited && s.State == "working" {
			c.Working = true
		}
	}
	m.all = orderedCards(cards)
	m.parents = threadParents(m.all)
	m.cards = m.rows()
	m.viewNote = ""
	if ev := editedViews(); len(ev) > 0 {
		m.viewNote = fmt.Sprintf("A card file was edited by hand; the view was rendered again and the edit kept as %s", filepath.Base(ev[len(ev)-1]))
	}
	if r, ok := renderFailure(); ok {
		m.viewNote = "A card view was not rendered, run tracks render: " + r.Error
	}
	m.evs = readEvents()
	m.useQuestions = storeOn()
	m.questions = loadQuestions()
	m.hookFail, m.hookFailing = hookFailure()
	m.checks, _ = loadChecks() // a file that does not parse shows every check as not run yet
	m.closed = closedToday(time.Now())
	m.carried = carriedOver()
	if d, err := loadDesk(m.clock()); err == nil { // an unreadable desk keeps what was shown
		m.desk = d
	}
	m.cur, m.hold = m.follow(m.cur), m.follow(m.hold)
	if lead, ok := m.parents[m.cur]; ok && !m.unfolded[lead] { // its lead folded, or it became a thread
		m.cur = lead
	}
	if m.reply != nil {
		m.reply.track = m.follow(m.reply.track)
	}
	if i := slices.IndexFunc(m.cards, func(c *Card) bool { return c.Name == m.cur }); i >= 0 {
		m.idx = i
	} else {
		m.idx = max(0, min(m.idx, len(m.cards)-1))
		m.scroll = 0
	}
	m.cur = ""
	if len(m.cards) > 0 {
		m.cur = m.cards[m.idx].Name
	}
	if m.reply != nil && m.reply.track != m.cur {
		m.status = "Reply dropped: " + m.reply.track + " is gone from the list"
		m.reply = nil
	}
}

// follow is name, or the name its track was renamed to once no card has name any more.
func (m *paneModel) follow(name string) string {
	if name == "" || slices.ContainsFunc(m.all, func(c *Card) bool { return c.Name == name }) {
		return name
	}
	return renamedTo(m.evs, name)
}

func (m *paneModel) selected() *Card {
	if m.idx < len(m.cards) {
		return m.cards[m.idx]
	}
	return nil
}

func (m *paneModel) sinceCmd() tea.Cmd {
	c := m.selected()
	if c == nil {
		return nil
	}
	// The same time as the list's unread dot, so the two never disagree about what is new.
	at, seen := m.lastSeen(c)
	return func() tea.Msg {
		lines, err := sinceLogFrom(c, at)
		msg := sinceMsg{name: c.Name, lines: lines, at: at, first: !seen}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

func tick() tea.Cmd { return tea.Tick(pollEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *paneModel) sessions() []string {
	var out []string
	for _, c := range m.all {
		if s := c.Meta["rex_session"]; s != "" && c.mode() != "parked" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (m *paneModel) rexCmd() tea.Cmd { return m.rex.cmd(m.now, m.sessions(), m.evs) }

func (m *paneModel) Init() tea.Cmd {
	m.now = time.Now()
	return tea.Batch(tick(), m.sinceCmd(), m.rexCmd(), tea.RequestBackgroundColor)
}

func (m *paneModel) move(d int) tea.Cmd {
	return m.moveTo(m.idx + d)
}

// stepNeeding moves the cursor to the next (d = 1) or previous (d = -1) track that needs you.
func (m *paneModel) stepNeeding(d int) tea.Cmd {
	cmd, ok := m.landOn(stepNeed(needsOrder(m.all, m.rex.states, m.evs, m.now), m.cur, d))
	if !ok {
		m.flashMsg(nothingNeedsYou)
		return nil
	}
	return cmd
}

func (m *paneModel) moveTo(i int) tea.Cmd {
	if len(m.cards) == 0 {
		return nil
	}
	m.idx = max(0, min(i, len(m.cards)-1))
	if m.cards[m.idx].Name != m.cur {
		m.hold = ""
		m.scroll = 0
	}
	m.cur = m.cards[m.idx].Name
	if _, ok := m.since[m.cur]; ok {
		return nil
	}
	return m.sinceCmd()
}

func (m *paneModel) closeCmd(name string) tea.Cmd {
	var threads []string
	for _, t := range m.openThreads(name) {
		threads = append(threads, t.Name)
	}
	self := ""
	if m.float {
		self = os.Getenv("REX_BLOCK") // the float goes with the session; a docked pane is a pane of its own
	}
	return func() tea.Msg {
		note, err := closeLead(name, threads, time.Now(), self)
		return closedMsg{name, note, err}
	}
}

// clock is the model's time, or the real one before the first tick.
func (m *paneModel) clock() time.Time {
	if m.now.IsZero() {
		return time.Now()
	}
	return m.now
}

// noteError keeps a failure shown in the footer as lastErr, once however long it stays up.
func (m *paneModel) noteError() {
	e := m.currentErr()
	if e == "" {
		m.errSeen = ""
		return
	}
	if e != m.errSeen {
		m.errSeen, m.lastErr, m.lastErrAt = e, baseNames(e), time.Now()
	}
}

func (m *paneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	m.noteError()
	return model, cmd
}

func (m *paneModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.light, m.bgKnown = !msg.IsDark(), true
	case tickMsg:
		m.now = m.timeNow()
		m.frame++
		if m.flash != "" && m.now.After(m.flashUntil) {
			m.flash = ""
		}
		send := m.firePending()
		if s := paneStamp(); s != m.stamp {
			m.reload()
			return m, tea.Batch(tick(), m.sinceCmd(), m.rexCmd(), send)
		}
		return m, tea.Batch(tick(), m.rexCmd(), send)
	case decidedMsg:
		m.status = msg.status
		if msg.sent {
			m.rex.lastRun = time.Time{}
		}
		m.reload()
		return m, m.sinceCmd()
	case rexMsg:
		m.rex.apply(msg)
	case sinceMsg:
		// After enter, keep what was shown until the cursor moves; the open event would blank it.
		if _, ok := m.since[msg.name]; !ok || msg.name != m.hold {
			m.since[msg.name] = sinceInfo{msg.lines, msg.at, msg.first, msg.err}
		}
	case noteMsg:
		m.status = string(msg)
	case statusMsg: // the result of enter: empty means the jump worked
		if msg == "" && m.float && m.form == nil && m.reply == nil { // never drop what is being typed
			return m, tea.Quit
		}
		m.status = string(msg)
	case deskDoneMsg:
		m.deskStarting = false
		if msg.switched && m.float && m.form == nil && m.reply == nil { // never drop what is being typed
			return m, tea.Quit
		}
		m.status = msg.status
		m.reload()
	case undoneMsg:
		m.undoing, m.status = false, string(msg)
		m.reload()
		if m.logv != nil {
			l := m.logv
			m.openLog()
			if m.logv != nil && l.idx < len(l.entries) {
				id := l.entries[l.idx].ID
				m.logv.idx = max(0, slices.IndexFunc(m.logv.entries, func(e store.DeskLogEntry) bool { return e.ID == id }))
			}
		}
		return m, m.sinceCmd()
	case closedMsg:
		m.reload()
		switch {
		case msg.err != nil:
			m.status = "could not close " + msg.name + ": " + msg.err.Error()
		default:
			m.status = msg.note
		}
		return m, m.sinceCmd()
	case answerMsg:
		m.status = msg.status
		if m.reply != nil && m.reply.track == msg.name {
			m.reply.sending = false
			if msg.sent || msg.typed {
				m.reply = nil
			}
		}
		if msg.sent {
			m.rex.lastRun = time.Time{}
		}
	case continuedMsg:
		delete(m.continuing, msg.name)
		m.status = msg.status
		if msg.sent {
			m.rex.lastRun = time.Time{}
		}
	case createdMsg:
		return m.created(msg)
	case offerMsg:
		if m.form == nil && m.reply == nil && m.confirm == "" && m.starting == "" {
			m.offer, m.status = msg, ""
		}
	case tea.PasteMsg:
		switch {
		case m.form != nil:
			m.formPaste(msg.Content)
		case m.reply != nil && !m.reply.sending:
			m.reply.ed.insert(msg.Content)
		}
	case tea.MouseWheelMsg:
		if m.w >= minSplitCols && msg.X > m.leftW() && !m.help && m.form == nil {
			switch msg.Button {
			case tea.MouseWheelDown:
				m.scrollBy(wheelLines)
			case tea.MouseWheelUp:
				m.scrollBy(-wheelLines)
			}
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *paneModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.help {
		m.help = false
		return m, nil
	}
	if name := m.confirm; name != "" {
		m.confirm = ""
		if k == "y" {
			return m, m.closeCmd(name)
		}
		return m, nil
	}
	if sw := m.swap; sw != nil {
		m.swap = nil
		if i, err := strconv.Atoi(k); err == nil && i >= 1 && i <= len(sw.focus) {
			if err := setModeChecked(sw.track, "focus", sw.focus[i-1]); err != nil {
				m.status = "could not set mode: " + err.Error()
				return m, nil
			}
			m.reload()
			m.flashMsg("Moved " + sw.focus[i-1] + " to BACKGROUND, " + sw.track + " to FOCUS")
			return m, m.sinceCmd()
		}
		return m, nil
	}
	if o := m.offer; o.name != "" {
		m.offer = offerMsg{}
		if k == "y" {
			m.starting, m.status = o.name, ""
			return m, func() tea.Msg { return startExisting(o.name, o.stale) }
		}
		return m, nil
	}
	if m.starting != "" && k != "q" {
		return m, nil
	}
	if m.form != nil {
		return m.formKey(msg)
	}
	if m.reply != nil {
		return m.replyKey(msg)
	}
	if m.logv != nil {
		cmd := m.logKey(k)
		return m, cmd
	}
	m.status, m.flash = "", ""
	if m.bandFocus {
		if q := m.selectedQuestion(); q != nil {
			if cmd, handled := m.questionKey(k, *q); handled {
				return m, cmd
			}
		} else {
			m.bandFocus = false
		}
	}
	switch k {
	case "a":
		if m.selectedQuestion() == nil {
			m.flashMsg("Nothing to answer on this track")
			break
		}
		m.bandFocus = true
	case "h":
		m.openLog()
	case "d":
		if m.deskStarting {
			m.status = "Still opening the desk"
			break
		}
		m.deskStarting, m.status = true, "Opening the desk…"
		return m, startOrOpenDesk()
	case "tab":
		return m, m.stepNeeding(1)
	case "shift+tab":
		return m, m.stepNeeding(-1)
	case "o", "O":
		if c := m.selected(); c != nil {
			return m, m.openLink(c, k == "O")
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m, m.accept(int(k[0] - '0'))
	case "r":
		m.openReply()
	case "space":
		m.toggleFold()
	case "n":
		m.form = newTrackForm()
	case "q":
		return m, tea.Quit
	case "esc":
		if m.float {
			return m, tea.Quit
		}
	case "?":
		m.help = true
	case "x":
		if c := m.selected(); c != nil {
			m.confirm, m.confirmQ = c.Name, m.closeQuestion(c)
		}
	case "pgdown":
		m.scrollBy(m.page())
	case "pgup":
		m.scrollBy(-m.page())
	case "up", "k":
		m.bandFocus = false
		return m, m.move(-1)
	case "down", "j":
		m.bandFocus = false
		return m, m.move(1)
	case "enter":
		if c := m.selected(); c != nil {
			m.hold = c.Name
			return m, openOrOffer(c, m.evs)
		}
	default:
		if c, mode := m.selected(), keyMode[k]; c != nil && mode != "" {
			name, label := c.Name, groupLabel(mode)
			if c.mode() == mode {
				m.flashMsg(name + " is already in " + label)
				break
			}
			if err := setModeChecked(name, mode, ""); err != nil {
				var full *focusFullError
				if errors.As(err, &full) {
					m.swap = full // asks which focus track moves to background
					break
				}
				m.status = "could not set mode: " + err.Error()
				break
			}
			m.reload() // follows m.cur by name, so the cursor goes where the track went
			m.flashMsg("Moved " + name + " to " + label)
			return m, m.sinceCmd()
		}
	}
	return m, nil
}

func openCmd(c *Card, evs []Event) tea.Cmd {
	return func() tea.Msg {
		sess := c.Meta["rex_session"]
		note := selectSession(sess, agentBlock(evs, sess))
		if jumped(note) {
			if err := appendEvent(Event{At: time.Now().UTC(), Event: "open", RexSession: sess, Track: c.Name, Cwd: c.Meta["dir"]}); err != nil && note == "" {
				note = "could not log open: " + err.Error()
			}
		}
		return statusMsg(note)
	}
}

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) > n {
		s = ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", max(0, n-lipgloss.Width(s)))
}

func center(s string, w int) string {
	return strings.Repeat(" ", max(0, (w-lipgloss.Width(s))/2)) + s
}

// stateView is what a track's row and detail show for its agent.
type stateView struct {
	kind  string // working, blocked, error, done, idle, none
	glyph string
	label string
	msg   string
}

func (m *paneModel) stateOf(c *Card) stateView {
	s, ok := m.rex.state(c.Meta["rex_session"])
	return agentView(s, ok, c.Meta["rex_session"], m.evs, m.now)
}

// agentView is what a track shows for its agent: Rex's state when it has one, else the hooks'.
func agentView(s agentState, ok bool, sess string, evs []Event, now time.Time) stateView {
	if sess != "" && ok {
		if s.Exited {
			msg := "agent exited"
			if s.Foreground != "" {
				msg += " · " + s.Foreground + " runs in its block"
			}
			return stateView{"exited", "○", "exited", msg}
		}
		switch s.State {
		case "working":
			return stateView{"working", "●", "working", s.Msg}
		case "blocked":
			if s.Kind == "question" {
				return stateView{"blocked", "?", "question", s.Msg}
			}
			label := "blocked"
			if s.Kind == "permission" {
				label = "permission"
			}
			return stateView{"blocked", "◆", label, s.Msg}
		case "done":
			return stateView{"done", "✓", "done", s.Msg}
		case "error":
			return stateView{"error", "✕", "error", s.Msg}
		}
		label := "idle"
		if !s.At.IsZero() {
			label += " " + shortDur(now.Sub(s.At))
		}
		return stateView{"idle", "○", label, s.Msg}
	}
	switch live := liveState(evs, sess, now); {
	case live == "working":
		return stateView{"working", "●", "working", ""}
	case live == "needs you":
		return stateView{"blocked", "◆", "needs you", ""}
	case strings.HasPrefix(live, "idle"):
		return stateView{"idle", "○", live, ""}
	}
	return stateView{kind: "none"}
}

func (m *paneModel) styleState(v stateView, s string) string {
	switch v.kind {
	case "working": // calm: a working agent needs nothing, so it should not outshine one that does
		return goodSt.Faint(true).Render(s)
	case "blocked":
		return warnSt.Render(s)
	case "error":
		return badSt.Render(s)
	case "done":
		return s
	}
	return dimSt.Render(s)
}

// chip is the tier marker, chipWidth wide. Only T3 is outlined: it changes what the agent may do alone.
func chip(c *Card) string {
	switch t := c.tier(); t {
	case "":
		return strings.Repeat(" ", chipWidth)
	case "3":
		return boldSt.Render("[T3]")
	default:
		return dimSt.Render(" T" + t + " ")
	}
}

func (m *paneModel) needsYou(c *Card) bool {
	if c.mode() == "parked" { // parked tracks never need him, as in needsOrder
		return false
	}
	k := m.stateOf(c).kind
	if m.isThread(c) { // its asks are for its lead; a prompt or error only Emil can clear still counts
		return k == "blocked" || k == "error"
	}
	return (c.asks() > 0 && k != "working") || k == "blocked" || k == "error"
}

func (m *paneModel) lastSeen(c *Card) (at time.Time, ok bool) { return lastSeen(m.evs, c) }

// lastSeen is when Emil last opened the track or typed a prompt into its session. Tracks
// reached straight from Rex never log an open, so a prompt counts as seeing them. ok is false
// for a track never seen, where the time falls back to the last day, as "Since you last looked" does.
func lastSeen(evs []Event, c *Card) (at time.Time, ok bool) {
	at = time.Now().Add(-defaultSince)
	sess := c.Meta["rex_session"]
	for _, e := range evs {
		mine := e.Track == c.Name || sess != "" && e.RexSession == sess
		if mine && (e.Event == "open" || e.Event == "prompt" && e.Kind != "system") && (!ok || e.At.After(at)) {
			at, ok = e.At, true
		}
	}
	return at, ok
}

// unread is whether the card changed since the track was last seen. Only the agent's own card
// writes stamp updated:, so the pane's mode keys and the hook's bookkeeping never mark a track
// unread; a card never stamped falls back to its mtime, and only until the track is seen once.
// Parked tracks show no dot.
func (m *paneModel) unread(c *Card) bool {
	if c.mode() == "parked" {
		return false
	}
	at, seen := m.lastSeen(c)
	if u := cardUpdated(c); !u.IsZero() {
		return u.After(at)
	}
	return !seen && c.Mtime.After(at)
}

// subLine is the dim second line under a track: what its card says is going on, and how long
// ago the card last changed. A waiting track says what it waits for first.
func (m *paneModel) subLine(c *Card, w int) string {
	text := whereFirst(c.Sections["Where it is"])
	if waits(c, c.Working) {
		on, chk := c.Meta["waiting_on"], c.Meta["check"]
		switch {
		case chk != "" && on != "":
			text = "check: " + chk + " · " + on
		case chk != "":
			text = "check: " + chk
		case on != "":
			text = on
		}
	}
	if text = strings.Join(strings.Fields(text), " "); text == "" {
		return ""
	}
	age, ageSt := "", dimSt
	if at := cardChanged(c); !at.IsZero() {
		d := m.now.Sub(at)
		age = shortDur(d)
		if d > staleAfter {
			ageSt = warnSt
		}
	}
	tail := ""
	if age != "" {
		tail = " · " + age
	}
	// One column short of w, where a row's last visible cell (the tier chip) ends.
	text = ansi.Truncate(text, max(1, w-1-subIndent-lipgloss.Width(tail)), "…")
	line := strings.Repeat(" ", subIndent) + dimSt.Render(text)
	if age != "" {
		line += dimSt.Render(" · ") + ageSt.Render(age)
	}
	return line
}

// tint paints the selection background across w columns, through the resets inside s.
func (m *paneModel) tint(s string, w int) string {
	var tintColor color.Color = selUnknownBg
	if m.bgKnown {
		tintColor = selBg(m.light)
	}
	bg := ansi.NewStyle().BackgroundColor(tintColor).String()
	return bg + strings.ReplaceAll(fit(s, w), ansi.ResetStyle, ansi.ResetStyle+bg) + ansi.ResetStyle
}

func (m *paneModel) stateW() int {
	if m.w >= wideCols {
		return stateWidth
	}
	return glyphWidth
}

func (m *paneModel) counts() (focus, needs int) {
	for _, c := range m.all {
		if c.mode() == "focus" && !m.isThread(c) {
			focus++
		}
		if m.needsYou(c) {
			needs++
		}
	}
	return
}

// checksAge is the header's note on when tracks check last ran. Yellow when it is stale, and
// "" when no open card has a check, since the age would say nothing then.
func (m *paneModel) checksAge() string {
	if !slices.ContainsFunc(m.all, func(c *Card) bool {
		return c.mode() != "parked" && strings.TrimSpace(c.Meta["check"]) != ""
	}) {
		return ""
	}
	var newest time.Time
	for _, s := range m.checks {
		if s.RanAt.After(newest) {
			newest = s.RanAt
		}
	}
	if newest.IsZero() {
		return dimSt.Render("checks not run yet") // a new check is unrun for up to 10 minutes
	}
	age := m.clock().Sub(newest)
	st := dimSt
	if age > staleChecks {
		st = warnSt
	}
	return st.Render("checks " + shortDur(age) + " ago")
}

// headerLabel is the header's title, after its one space.
const headerLabel = "Desk"

func (m *paneModel) header() string {
	rows := m.bandRows() // the same decisions the band lists, one per row
	needs := len(rows)
	forYou := dimSt.Render("0 for you")
	if needs > 0 {
		forYou = warnSt.Bold(true).Render(fmt.Sprintf("◆ %d for you", needs))
	}
	// keep orders what the header gives up first when it does not fit: low goes before high.
	type part struct {
		text string
		keep int
	}
	parts := []part{
		{dimSt.Render(plural(len(m.all), "track")), 3},
		{forYou, 6},
		{dimSt.Render(fmt.Sprintf("handled %d today", len(m.desk.today))), 4},
	}
	hook := hookWarning(m.all, m.rex.states, m.evs, m.clock(), m.hookFail, m.hookFailing)
	if hook != "" {
		parts = append(parts, part{warnSt.Render(hook), 5})
	}
	if m.closed > 0 {
		parts = append(parts, part{dimSt.Render(fmt.Sprintf("%d closed today", m.closed)), 2})
	}
	if m.carried > 0 {
		parts = append(parts, part{warnSt.Render(fmt.Sprintf("%d carried over", m.carried)), 5})
	}
	if s := m.checksAge(); s != "" {
		parts = append(parts, part{s, 1})
	}
	join := func(parts []part) string {
		texts := make([]string, len(parts))
		for i, p := range parts {
			texts[i] = p.text
		}
		return strings.Join(texts, dimSt.Render(" · "))
	}
	lead := 1 + len(headerLabel)
	for len(parts) > 1 && lead+lipgloss.Width(join(parts))+2 > m.w {
		low := 0
		for i, p := range parts {
			if p.keep < parts[low].keep {
				low = i
			}
		}
		parts = slices.Delete(parts, low, low+1)
	}
	// The hook warning is cut to the room left rather than dropped: its reason can be long.
	if hook != "" && len(parts) == 1 {
		room := m.w - lead - 2 - lipgloss.Width(join(parts)) - lipgloss.Width(" · ")
		// Only a failure reason is cut; a silence warning shows whole or not at all, so its hours
		// are never cut mid-number.
		if room >= len(hook) || (strings.HasPrefix(hook, "hook failing: ") && room >= len("hook failing: …")+8) {
			parts = append(parts, part{warnSt.Render(ansi.Truncate(hook, room, "…")), 5})
		}
	}
	right := join(parts)
	return " " + boldSt.Render(headerLabel) + strings.Repeat(" ", max(1, m.w-lead-lipgloss.Width(right)-1)) + right
}

func (m *paneModel) left(w, bodyH int) []string {
	var lines []string
	var kinds []byte
	selLine := 0
	nameW := max(4, w-listFixed-m.stateW())
	subs := m.w >= wideCols
	i := 0
	for _, g := range groups {
		first := true
		for ; i < len(m.cards) && m.rowGroup(m.cards[i]) == g.mode; i++ {
			c := m.cards[i]
			if first {
				label := dimSt.Render(g.label)
				if n := m.focusCount(); g.mode == "focus" && n > maxFocus {
					label = warnSt.Render(fmt.Sprintf("%s  %d, plan says %d", g.label, n, maxFocus))
				}
				if len(lines) > 0 {
					lines, kinds = append(lines, ""), append(kinds, lineGap)
				}
				lines, kinds = append(lines, " "+label), append(kinds, lineGap)
				first = false
			}
			v := m.stateOf(c)
			state := dimSt.Render(fit("-", m.stateW()))
			if v.kind != "none" {
				text := v.glyph
				if m.stateW() == stateWidth {
					text += " " + v.label
				}
				state = m.styleState(v, fit(text, m.stateW()))
			}
			asks := strings.Repeat(" ", askWidth)
			switch n := c.asks(); {
			case n > 0:
				asks = fmt.Sprintf("%*s", askWidth, fmt.Sprintf("◆%d", n))
				asks = dimSt.Render(asks) // the Needs you band carries the colour
			case v.kind == "error":
				asks = badSt.Render(fmt.Sprintf("%*s", askWidth, "!"))
			}
			lead, marker := " ", " "
			name, strip := fit(c.Name, nameW), ""
			switch glyphs, gw := m.glyphStrip(c); {
			case m.isThread(c):
				name = dimSt.Render(" └ ") + fit(c.Name, nameW-threadIndent)
			case gw > 0:
				name, strip = fit(c.Name, max(1, nameW-gw-1)), " "+glyphs
			}
			if m.unread(c) {
				lead = warnSt.Render("•")
			}
			sub := ""
			if subs && !m.isThread(c) {
				sub = m.subLine(c, w)
			}
			tail := strip + " " + state + " " + asks + " " + chip(c)
			row := lead + marker + name + tail
			if i == m.idx {
				row = m.tint(lead+selSt.Render("▌")+boldSt.Render(name)+tail, w)
				if sub != "" {
					sub = m.tint(sub, w)
				}
				selLine = len(lines)
			}
			lines, kinds = append(lines, row), append(kinds, lineTrack)
			if sub != "" {
				lines, kinds = append(lines, sub), append(kinds, lineSub)
			}
		}
	}
	return windowList(lines, kinds, selLine, bodyH)
}

// The kinds of list line, for windowList.
const (
	lineGap   = 'g' // a blank line or a group heading
	lineTrack = 't'
	lineSub   = 's' // a track's second line
)

// windowList fits the list into bodyH lines around the selection. When tracks are cut off
// below, the last line says how many, and no group heading is left without a track under it.
func windowList(lines []string, kinds []byte, selLine, bodyH int) []string {
	if len(lines) <= bodyH {
		return lines
	}
	off, end := listWindow(kinds, selLine, max(1, bodyH-1)) // one line for the "more" note
	hidden := 0
	for _, k := range kinds[end:] {
		if k == lineTrack {
			hidden++
		}
	}
	if hidden == 0 { // no note needed: use its line, ending on the last track
		off = max(0, len(lines)-bodyH)
		if off < selLine && kinds[off] == lineSub {
			off++
		}
		return slices.Clip(lines[off:])
	}
	out := slices.Clip(lines[off:end]) // the note must not overwrite the caller's lines
	return append(out, dimSt.Render(fmt.Sprintf("  ↓ %d more", hidden)))
}

// listWindow picks the lines [off, end) around the selection. A track and its sub-line are in
// the window together or not at all.
func listWindow(kinds []byte, selLine, avail int) (off, end int) {
	n := len(kinds)
	off = max(0, min(selLine-avail/2, n-avail))
	for {
		if off < selLine && kinds[off] == lineSub { // its track is cut off above
			off++
		}
		end = min(n, off+avail)
		if end < n && kinds[end] == lineSub { // the cut falls between a track and its sub-line
			if end-1 == selLine {
				off++ // the selection keeps its sub-line
				continue
			}
			end--
		}
		break
	}
	for end > off+1 && end-1 != selLine && kinds[end-1] == lineGap { // a heading or gap has nothing under it
		end--
	}
	return off, end
}

func groupOf(c *Card) string { return effectiveMode(c, c.Working) }

func countMode(cards []*Card, mode string) int {
	n := 0
	for _, c := range cards {
		if groupOf(c) == mode {
			n++
		}
	}
	return n
}

func detailChip(c *Card) string {
	if t := c.tier(); t != "" {
		if t == "3" {
			return boldSt.Render("[T3]")
		}
		return dimSt.Render("T" + t)
	}
	return ""
}

// right returns the detail pane: head stays pinned, body scrolls.
func (m *paneModel) right(w int) (head, body []string) {
	c := m.selected()
	if c == nil {
		return nil, nil
	}
	mw := min(w, maxMeasure)
	tag := dimSt.Render(c.mode())
	if ch := detailChip(c); ch != "" {
		tag = ch + dimSt.Render(" · "+c.mode())
	}
	name := c.Name
	if lipgloss.Width(name) > w-lipgloss.Width(tag)-2 {
		name = ansi.Truncate(name, max(1, w-lipgloss.Width(tag)-2), "…")
	}
	head = []string{boldSt.Render(name) + "  " + tag} // the tag reads with the name, not at the far edge
	if v := m.stateOf(c); v.kind == "none" {
		// A card with a session but no state yet (Rex down, not polled) shows no agent line.
		if c.Meta["rex_session"] == "" {
			head = append(head, dimSt.Render(fit("no agent · enter starts one", mw)))
		}
	} else {
		text := v.msg
		if text == "" {
			text = v.label
		}
		if v.kind == "done" && v.msg != "" {
			text = v.label + " · " + v.msg
		}
		if st, ok := m.rex.state(c.Meta["rex_session"]); ok && st.State == "blocked" && !st.Exited {
			if v.msg == "" {
				text = "blocked"
			}
			if st.Kind != "" {
				text += " · " + st.Kind
			}
		}
		head = append(head, m.styleState(v, fit(v.glyph+" "+text, mw)))
	}
	m.editLine = -1
	// Sections run Where it is, Needs me, Goal with its links, Since; each starts with one blank line.
	body = append(body, "")
	body = append(body, whereBlock(c.Sections["Where it is"], mw)...)
	body = append(body, todayRows(c, mw)...)
	if c.mode() == "waiting" {
		body = append(body, waitingRows(c, m.checks, m.now, mw)...)
	}
	editing := m.reply != nil && m.reply.track == c.Name
	needs := strings.TrimSpace(c.Sections["Needs me"])
	answered := needs != "" && m.answeredNow(c)
	if needs != "" || editing {
		title, hint := "Needs me", ""
		if needs == "" {
			title = "Reply"
		}
		switch {
		case editing && m.reply.sending:
			hint = "sending…"
		case editing:
			hint = "enter send · esc cancel"
		case answered:
		case m.canAnswer(c) && c.asks() > 0:
			hint = askKeysHint(c) + " · r reply"
		}
		// A track that needs you gets a bar down the left of its asks: the one part of the
		// detail to act on. Without one the block sits flush with the other sections.
		bar, gw := "", 0
		titleSt, numSt := boldSt, boldSt
		if m.needsYou(c) && !answered {
			bar, gw = warnSt.Render("┃")+" ", gutterW
			titleSt, numSt = warnSt.Bold(true), warnSt.Bold(true)
		}
		if lipgloss.Width(title)+2+lipgloss.Width(hint) > mw-gw {
			hint = ""
		}
		var sec []string
		top := titleSt.Render(title)
		if hint != "" {
			top += "  " + dimSt.Render(hint)
		}
		sec = append(sec, top)
		if answered {
			sec = append(sec, dimSt.Render(fit(answeredLine(cardAnsweredAsks(c), m.stateOf(c).kind == "blocked"), mw-gw)))
			var checks []string
			for _, a := range c.askList() {
				if isCheckAsk(a.text) {
					checks = append(checks, fmt.Sprintf("%d. %s", a.n, a.text))
				}
			}
			if len(checks) > 0 {
				sec = append(sec, "")
				sec = append(sec, needsLines(strings.Join(checks, "\n"), mw-gw, numSt)...)
			}
			if editing {
				sec = append(sec, "")
			}
		} else if needs != "" {
			sec = append(sec, "")
			sec = append(sec, needsLines(needs, mw-gw, numSt)...)
			if editing {
				sec = append(sec, "")
			}
		}
		if editing {
			for i, l := range m.reply.ed.wrapped(mw-gw-2, replyRows, !m.reply.sending) {
				prefix := "  "
				if i == 0 {
					prefix = boldSt.Render("› ")
				}
				sec = append(sec, prefix+l)
			}
		}
		body = append(body, "")
		for _, l := range sec {
			body = append(body, bar+l)
		}
		if editing {
			m.editLine = len(body) - 1
		}
	}
	body = append(body, "", dimSt.Render("GOAL"))
	if goal := strings.Join(strings.Fields(c.Sections["Goal"]), " "); goal != "" {
		for _, l := range goalLines(goal, mw) {
			body = append(body, dimSt.Render(l))
		}
	} else {
		body = append(body, dimSt.Render("No goal yet."))
	}
	if ls := c.links(); len(ls) > 0 {
		hint := dimSt.Render("  o open")
		if len(ls) > 1 {
			hint = dimSt.Render("  o open · O next")
		}
		labels := linkLabels(ls)
		for i, l := range labels {
			labels[i] = linkSt.Render(l)
		}
		line := dimSt.Render("Links") + "  " + strings.Join(labels, dimSt.Render(" · "))
		if lipgloss.Width(line)+lipgloss.Width(hint) <= mw {
			line += hint
		}
		body = append(body, ansi.Truncate(line, mw, "…"))
	}
	body = append(body, m.deskNotesLines(c, mw)...)
	si, ok := m.since[c.Name]
	if ok && si.err == errNotGit.Error() {
		return head, body
	}
	title := "Since you last looked"
	switch {
	case ok && si.first:
		title = "First look"
	case ok:
		title += " · " + shortDur(m.now.Sub(si.at))
	}
	// Nothing to show folds into the title line, faint.
	switch {
	case !ok:
		body = append(body, "", dimSt.Render(title+" · …"))
	case si.err != "":
		text := "git: " + si.err
		if si.err == errNoDir.Error() {
			text = si.err
		}
		body = append(body, "", dimSt.Render(title), dimSt.Render(fit(text, mw)))
	case len(si.lines) == 0:
		body = append(body, "", dimSt.Render(title+" · Nothing new"))
	default:
		body = append(body, "", boldSt.Render(title))
		for _, l := range si.lines {
			hash, msg, _ := strings.Cut(l, " ")
			body = append(body, fit(dimSt.Render(hash)+" "+msg, mw))
		}
	}
	return head, body
}

func (m *paneModel) detailGeom() (w, bodyH int) {
	return m.w - m.leftW() - 4, m.h - 3 - len(m.needBand(m.w, m.h-3))
}

// leftW is the list column's width: as wide as the longest track name needs, between leftWidth
// and half the window.
func (m *paneModel) leftW() int {
	longest := 0
	for _, c := range m.cards {
		w := lipgloss.Width(c.Name)
		if m.isThread(c) {
			w += threadIndent
		}
		longest = max(longest, w)
	}
	return max(leftWidth, min(m.w/2, longest+listFixed+m.stateW()))
}

// window clips body to avail lines starting at scroll, trading the last line for a hint when
// more lies below. It returns the lines and the scroll clamped to what exists.
func window(body []string, scroll, avail int) ([]string, int) {
	if avail <= 0 {
		return nil, 0
	}
	if len(body) <= avail {
		return body, 0
	}
	scroll = max(0, min(scroll, len(body)-avail))
	if scroll+avail >= len(body) {
		return body[scroll:], scroll
	}
	out := append([]string{}, body[scroll:scroll+avail-1]...)
	return append(out, dimSt.Render(moreHint)), scroll
}

func (m *paneModel) page() int {
	_, bodyH := m.detailGeom()
	return max(1, bodyH/2)
}

func (m *paneModel) scrollBy(d int) {
	w, bodyH := m.detailGeom()
	head, body := m.right(w)
	_, m.scroll = window(body, m.scroll+d, bodyH-len(head))
}

func (m *paneModel) footer() string {
	switch {
	case m.help:
		return " " + styleHints([]hint{{"any key", "closes", false}})
	case m.confirm != "":
		return " " + m.prompt(m.confirmQ, confirmHints)
	case m.swap != nil:
		q := "Focus is full. Move which to background for " + m.swap.track + "?"
		hs := []hint{}
		for i, n := range m.swap.focus {
			label := n
			if i == 0 {
				label += " (older)"
			}
			hs = append(hs, hint{strconv.Itoa(i + 1), label, false})
		}
		return " " + m.prompt(q, append(hs, hint{"esc", "cancel", true}))
	case m.offer.name != "":
		return " " + m.prompt(offerQuestion(m.offer.name), offerHints)
	case m.starting != "":
		return " " + dimSt.Render(fit("Starting an agent for "+m.starting+"…", m.w-2))
	case m.form != nil:
		return " " + m.formFooter()
	case m.logv != nil && m.status == "":
		return " " + fit(styleHints([]hint{{"↑↓", "scroll", false}, {"enter", "open the track", false}, {"u", "take back", false}, {"esc", "back", false}}), m.w-2)
	case m.flash != "":
		return " " + dimSt.Render(fit(m.flash, m.w-2))
	case isErrStatus(m.status):
		return " " + badSt.Render(fit(errMark+baseNames(m.status), m.w-2))
	case m.status == answeredNote:
		return " " + fit(dimSt.Render(m.status)+"  "+styleHints([]hint{{"r", "reply", false}}), m.w-2)
	case m.status != "":
		return " " + dimSt.Render(fit(m.status, m.w-2))
	case m.reply != nil && m.w < minSplitCols:
		return " " + boldSt.Render("› ") + m.reply.ed.view(m.w-4, !m.reply.sending)
	case m.reply != nil:
		return " " + fit(styleHints(editHints), m.w-2)
	case m.viewNote != "":
		return " " + warnSt.Render(fit(m.viewNote, m.w-2))
	}
	return " " + fit(m.keysLine(), m.w-2)
}

// prompt is a question in warning colour with its keys after it. The question gives way to
// the keys when the window is narrow, never the other way round.
func (m *paneModel) prompt(question string, keys []hint) string {
	k := styleHints(keys)
	room := m.w - 2 - 2 - lipgloss.Width(k)
	return warnSt.Render(ansi.Truncate(question, max(1, room), "…")) + "  " + k
}

// closeQuestion says what closing the track does. closeTrack always archives the card and
// ends the session only when nothing in it can be lost, which the pane cannot know in full
// (pane count, foreground process). It names the two reasons it does know: another card
// uses the session, or the agent is in a state outside restStates.
//
// A lead's open threads close with it, each under the same rules, so the question counts them
// and how many of their sessions stay.
func (m *paneModel) closeQuestion(c *Card) string {
	closing := map[string]bool{c.Name: true}
	threads := m.openThreads(c.Name)
	for _, t := range threads {
		closing[t.Name] = true
	}
	q := "Close " + c.Name + "? "
	if len(threads) > 0 {
		q = fmt.Sprintf("Close %s and %s? ", c.Name, plural(len(threads), "thread"))
		kept := 0
		for _, t := range threads {
			if k, _ := m.keepsSession(t, closing); k {
				kept++
			}
		}
		switch {
		case kept == 1:
			q += "1 session stays. "
		case kept > 1:
			q += fmt.Sprintf("%d sessions stay. ", kept)
		}
	}
	if asks, later := leftovers(c); len(asks)+len(later) > 0 {
		q += fmt.Sprintf("%d left over go to the inbox. ", len(asks)+len(later))
	}
	_, detail := m.keepsSession(c, closing)
	return q + detail
}

// keepsSession is whether closing c keeps its session for a reason the pane knows, and what
// closing says about it. closing holds the tracks that close together: one of them using the
// session does not keep it.
func (m *paneModel) keepsSession(c *Card, closing map[string]bool) (kept bool, detail string) {
	sess := c.Meta["rex_session"]
	if sess == "" {
		return false, "Archives the card."
	}
	for _, o := range m.all {
		if !closing[o.Name] && sameSession(o.Meta["rex_session"], sess) {
			return true, "Archives the card, keeps the session: track " + o.Name + " uses it."
		}
	}
	if st, ok := m.rex.state(sess); ok && !st.Exited && st.State != "" && !restStates[st.State] {
		// "while it is busy": the agent may come to rest before y, and then closeTrack ends the session.
		return true, "Its agent is " + st.State + "; archives the card, keeps the session while it is busy."
	}
	return false, "Archives the card; ends its session if nothing in it can be lost."
}

// keysLine is the longest key summary that fits: the full list, then a terse one, then initials.
// It lists only what works for the selected track now.
func (m *paneModel) keysLine() string {
	c := m.selected()
	if q := m.selectedQuestion(); q != nil && m.bandFocus {
		hs := []hint{{"enter", "recommended", false}, {"1-9", "pick", false}, {"r", "words", false}, {"z", "undo", false}, {"space", "evidence", false}, {"o", "link", false}, {"g", "open", false}, {"esc", "back", false}}
		for len(hs) > 2 && lipgloss.Width(plainHints(hs)) > m.w-2 {
			hs = hs[:len(hs)-1]
		}
		return styleHints(hs)
	}
	var answer, link, move, open, next, mode, closeKey, fold []hint
	if c != nil && len(m.openThreads(c.Name)) > 0 {
		fold = []hint{{"space", "threads", false}}
	}
	if m.canAnswer(c) {
		answer = answerHints
	}
	if c != nil && len(c.links()) > 0 {
		link = []hint{{"o", "link", false}}
	}
	if c != nil {
		move, open, next = []hint{{"↑↓", "move", false}}, []hint{{"enter", "open", false}}, []hint{{"tab", "next", false}}
		mode, closeKey = []hint{{"f b w p", "mode", false}}, []hint{{"x", "close", false}}
	}
	quit := hint{"q", "quit", false}
	switch {
	case m.float:
		quit = hint{"esc", "close", false}
	case m.homed:
		quit = hint{"⌘⇧H", "back", false}
	}
	newKey, helpKey := []hint{{"n", "new", false}}, []hint{{"?", "keys", false}}
	terse := []hint{{"↑↓ enter", "", false}}
	initials := "n ? q"
	if c == nil {
		terse = nil
	} else {
		initials = keysShort
	}
	var handledKey []hint
	if len(m.desk.today) > 0 {
		handledKey = []hint{{"h", "handled", false}}
	}
	sets := [][]hint{
		slices.Concat(move, open, next, answer, link, fold, newKey, mode, closeKey, handledKey, helpKey, []hint{quit}),
		slices.Concat(terse, next, answer, link, fold, newKey, closeKey, helpKey, []hint{quit}),
	}
	if fold != nil { // a lead keeps its fold key before "new" at 80 columns
		sets = append(sets, slices.Concat(terse, next, answer, []hint{{"space", "fold", false}}, closeKey, helpKey, []hint{quit}))
	}
	sets = append(sets,
		slices.Concat(terse, next, answer, newKey, closeKey, helpKey, []hint{quit}),
		slices.Concat(terse, next, answer, closeKey, helpKey, []hint{quit}),
	)
	for _, hs := range sets {
		if lipgloss.Width(plainHints(hs)) <= m.w-2 {
			return styleHints(hs)
		}
	}
	return styleHints([]hint{{initials, "", false}})
}

func (m *paneModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *paneModel) helpBody(bodyH int) []string {
	const chrome = 2 + 4 // border and padding, both sides
	kw, natural := 0, 0
	for _, r := range helpKeys {
		kw = max(kw, lipgloss.Width(r[0]))
	}
	for _, r := range helpKeys {
		natural = max(natural, kw+2+lipgloss.Width(r[1]))
	}
	// The last error shows only where the box has the rows: a short window keeps the keys whole.
	var errRow string
	if m.lastErr != "" && len(helpKeys)+2+2+2 <= bodyH { // title and blank, blank and error, border
		age := lastErrLabel + shortDur(m.clock().Sub(m.lastErrAt)) + " ago: "
		errRow = dimSt.Render(age) + badSt.Render(m.lastErr)
		natural = max(natural, lipgloss.Width(age)+lipgloss.Width(m.lastErr))
	}
	inner := max(8, min(natural, m.w-chrome-2))
	kw = min(kw, inner/2)
	var rows []string
	rows = append(rows, boldSt.Render("Keys"), "")
	for _, r := range helpKeys {
		desc := strings.Split(lipgloss.NewStyle().Width(max(1, inner-kw-2)).Render(r[1]), "\n")
		for i, d := range desc {
			k := strings.Repeat(" ", kw)
			if i == 0 {
				k = boldSt.Render(fit(r[0], kw))
			}
			rows = append(rows, k+"  "+dimSt.Render(strings.TrimRight(d, " ")))
		}
	}
	if errRow != "" {
		rows = append(rows, "", ansi.Truncate(errRow, inner, "…"))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 2).
		Render(strings.Join(rows, "\n"))
	lines := strings.Split(box, "\n")
	var body []string
	for range max(0, (bodyH-len(lines))/2) {
		body = append(body, "")
	}
	for _, l := range lines {
		body = append(body, fit(center(l, m.w), m.w))
	}
	return body
}

// splitRows sets the list column l beside the detail column r, bodyH rows tall.
func splitRows(l, r []string, lw, bodyH int) []string {
	sep := dimSt.Render("│")
	var body []string
	for i := range bodyH {
		ll, rl := "", ""
		if i < len(l) {
			ll = l[i]
		}
		if i < len(r) {
			rl = r[i]
		}
		if i >= max(len(l), len(r)) { // the rule stops where both columns end
			body = append(body, "")
			continue
		}
		body = append(body, fit(ll, lw)+" "+sep+" "+rl)
	}
	return body
}

func (m *paneModel) render() string {
	if m.w < 24 || m.h < 6 {
		return "Too small"
	}
	bodyH := m.h - 3
	var body []string
	switch {
	case m.help:
		body = m.helpBody(bodyH)
	case m.logv != nil:
		body = m.logBody(bodyH)
	case m.form != nil && m.w >= minSplitCols && len(m.cards) > 0:
		body = m.formBeside(bodyH)
	case m.form != nil:
		body = m.formBody(bodyH)
	case len(m.cards) == 0:
		mid := bodyH / 2
		title, hint := "No tracks yet", "n starts one, or type /track NAME in a session"
		for i := range bodyH {
			switch i {
			case mid - 1:
				body = append(body, center(title, m.w))
			case mid:
				body = append(body, center(dimSt.Render(hint), m.w))
			default:
				body = append(body, "")
			}
		}
	case m.w < minSplitCols:
		body = m.needBand(m.w, bodyH)
		for _, l := range m.left(m.w, bodyH-len(body)) {
			body = append(body, fit(l, m.w))
		}
	default:
		band := m.needBand(m.w, bodyH)
		listH := bodyH - len(band)
		lw := m.leftW()
		l := m.left(lw, listH)
		w, _ := m.detailGeom()
		head, rest := m.right(w)
		if avail := listH - len(head); m.editLine >= 0 && avail > 1 {
			// Keep the reply editor in view, clear of the "more" line.
			m.scroll = max(min(m.scroll, m.editLine), m.editLine-avail+2)
		}
		clipped, scroll := window(rest, m.scroll, listH-len(head))
		m.scroll = scroll
		body = append(band, splitRows(l, append(head, clipped...), lw, listH)...)
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	return m.header() + "\n" + strings.Join(body[:bodyH], "\n") + "\n\n" + m.footer()
}

// runPane runs the pane. Floating (over a window, from the Rex hotkey), it closes after a
// successful enter and on esc, so the hotkey is the way home.
// In the tracks session (home), it stays open: the hotkey goes back instead.
func runPane(in io.Reader, out io.Writer, float, homed bool) error {
	m := newPane()
	m.float, m.homed = float, homed
	_, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}
