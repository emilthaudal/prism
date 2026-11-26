package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	rexNewTimeout   = 15 * time.Second
	maxRequest      = 4000 // a pasted message fits
	maxSlugWords    = 4
	maxSlugLen      = 32
	maxNameTries    = 100
	formMaxWidth    = 110 // the box, border included
	formMargin      = 2   // columns kept clear on each side of the box
	formMinRows     = 3
	formMaxRows     = 8
	defaultGoal     = "One line: what done looks like."
	requestPrompt   = "What do you want?"
	requestHolder   = "investigate our scanner routines, or just ask"
	requestNote     = "An agent starts on it and finds where the work lives."
	requestRequired = "Say what you want first"
)

var requestHints = []hint{{"enter", "start", false}, {"esc", "cancel", false}, {"ctrl+u", "clear", false}}

var errExists = errors.New("a track with this name exists")

// trackSpec is the name and directory a new agent session starts with, and its command when
// it is not the agentArgv a new track starts.
type trackSpec struct {
	name, dir string
	argv      []string
}

// newForm is the one line `n` opens: Emil's request in his own words.
type newForm struct {
	ed      lineEditor
	err     string
	busy    bool
	preview string  // the name and dir the request would get now, on one line
	name    string  // the name part of preview
	dir     string  // the dir part of preview, "" when no default dir is set up
	retired retired // read once when the form opens, not on every key
}

type createdMsg struct {
	name, status string
	cardErr      error  // the card was not written; the form stays open
	formErr      string // nothing was started; the form stays open with this in the footer
	ok           bool
}

func newTrackForm() *newForm {
	return &newForm{ed: lineEditor{limit: maxRequest}, retired: loadRetired()}
}

func userHome() string { h, _ := os.UserHomeDir(); return h }

func expandHome(p string) string {
	if h := userHome(); h != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return h + p[1:]
	}
	return p
}

func tildePath(p string) string {
	if h := userHome(); h != "" && (p == h || strings.HasPrefix(p, h+"/")) {
		return "~" + p[len(h):]
	}
	return p
}

// archivedCard is the file name of NAME's archived card, "" when there is none. The file
// name narrows it to YYYY-MM-DD-NAME.md or YYYY-MM-DD-NAME-N.md; the card's own name:
// decides, because "pan-213" could be track pan's second archive or track pan-213. Case
// does not count: the disk may not tell Pan.md from pan.md.
func archivedCard(name string) string {
	if storeOn() {
		return storeArchived(name)
	}
	re := regexp.MustCompile(`(?i)^[0-9]{4}-[0-9]{2}-[0-9]{2}-` + regexp.QuoteMeta(name) + `(-[0-9]+)?\.md$`)
	files, _ := filepath.Glob(filepath.Join(archiveDir(), "*.md"))
	for _, f := range files {
		b := filepath.Base(f)
		if !re.MatchString(b) {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if n := parseCard(name, string(data)).Meta["name"]; n == "" || strings.EqualFold(n, name) {
			return b
		}
	}
	return ""
}

// cleanRequest is the request as one safe line that cannot start a card section.
func cleanRequest(s string) string {
	s = sanitizeAnswer(s)
	for {
		t := strings.TrimLeftFunc(s, func(r rune) bool { return r == '#' || unicode.IsSpace(r) })
		if r := []rune(t); len(r) > maxRequest {
			t = string(r[:maxRequest])
		}
		if t = strings.TrimSpace(t); t == s {
			return s
		}
		s = t
	}
}

// nameTaken is whether a card or an archived card holds the name, or a track was renamed
// away from it: its agent may still write the old card.
func nameTaken(name string, r retired) bool {
	if storeOn() {
		return storeNameTaken(name)
	}
	if _, err := os.Lstat(cardPath(name)); !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return archivedCard(name) != "" || r.to(name) != ""
}

func numbered(base string, i int) string {
	if i == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, i)
}

// freeName is base, or base-2, base-3… the first one that is not taken.
func freeName(base string, r retired) string {
	for i := 1; i <= maxNameTries; i++ {
		if n := numbered(base, i); !nameTaken(n, r) {
			return n
		}
	}
	return ""
}

// defaultDir is where a new track's agent starts: $TRACKS_DEFAULT_DIR, else ~/Developer.
func defaultDir() (string, error) {
	d, what := os.Getenv("TRACKS_DEFAULT_DIR"), "TRACKS_DEFAULT_DIR"
	if d == "" {
		d, what = filepath.Join(userHome(), "Developer"), "~/Developer"
	}
	d = expandHome(d)
	switch st, err := os.Stat(d); {
	case !filepath.IsAbs(d):
		return "", fmt.Errorf("%s is not a full path: %s", what, d)
	case strings.ContainsFunc(d, unicode.IsControl) || strings.Contains(d, " #"):
		return "", fmt.Errorf("%s cannot go on a card: %q", what, d)
	case err != nil || !st.IsDir():
		return "", fmt.Errorf("%s is not a directory: %s", what, d)
	}
	return filepath.Clean(d), nil
}

func cardText(name, mode, tier, dir, goal string) string {
	return fmt.Sprintf(cardTemplateFmt, name, mode, tier, dir, goal)
}

// createCard writes a new card in one step and never replaces one: a finished temp file is
// hard-linked into place.
func createCard(name string, data []byte) error { return linkNew(cardPath(name), data, 0o644) }

// linkNew writes path in one step with the given mode, and fails with errExists rather than
// replace a file that is there.
func linkNew(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if os.IsExist(err) {
			return errExists
		}
		return err
	}
	return nil
}

// agentArgv is the command the new session runs. The session gets Rex's environment, not
// the pane's, so a TRACKS_HOME the pane runs with goes along. --name titles Claude's window
// with the track's name rather than one Claude makes up from "/track NAME".
func agentArgv(name string) []string {
	return withTracksHome("claude", "--name", name, "/track "+name)
}

func withTracksHome(argv ...string) []string {
	if h := os.Getenv("TRACKS_HOME"); h != "" {
		argv = append([]string{"env", "TRACKS_HOME=" + h}, argv...)
	}
	return argv
}

// rexNewArgs is the argv for rex new. The name is a validName and the dir an absolute path,
// so neither can read as a flag; the command follows --. --interactive reads ~/.zshrc, where
// PATH gets ~/.local/bin: the floated pane runs with the Rex server's bare PATH, and without
// it the new session cannot find claude.
func rexNewArgs(s trackSpec) []string {
	argv := s.argv
	if argv == nil {
		argv = agentArgv(s.name)
	}
	return append([]string{"new", s.name, "--cwd=" + s.dir, "--keep-open", "--interactive", "--json", "--"}, argv...)
}

func rexNewSession(s trackSpec) (sid, block string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), rexNewTimeout)
	defer cancel()
	out, err := rexRunIn(ctx, "", rexNewArgs(s)...)
	if err != nil {
		return "", "", err
	}
	var doc struct {
		SessionID      string `json:"session_id"`
		InitialWindows []struct {
			BlockIDs []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || doc.SessionID == "" {
		return "", "", fmt.Errorf("rex new answered without a session id")
	}
	if len(doc.InitialWindows) > 0 && len(doc.InitialWindows[0].BlockIDs) > 0 {
		block = doc.InitialWindows[0].BlockIDs[0]
	}
	return doc.SessionID, block, nil
}

// startRequest names a track after the request, writes its card with the request as the
// goal, and starts the agent. The request reaches the agent only through the card.
func startRequest(goal, dir string, now time.Time) createdMsg {
	base, r := slugName(goal, now), loadRetired()
	for i := 1; i <= maxNameTries; i++ {
		name := numbered(base, i)
		if nameTaken(name, r) {
			continue
		}
		err := newTrack(name, defaultStartMode, "", dir, goal)
		if errors.Is(err, errExists) {
			continue
		}
		if err != nil {
			return createdMsg{name: name, cardErr: err}
		}
		return launch(trackSpec{name: name, dir: dir})
	}
	return createdMsg{formErr: "no free name left for " + base}
}

// offerMsg asks whether to start an agent for a card whose rex_session is empty or gone.
type offerMsg struct{ name, stale string }

// offerQuestion is the footer's question for an offer.
func offerQuestion(name string) string { return "No agent for " + name + ". Start one?" }

// openOrOffer jumps to the track's agent, or offers to start one when Rex has no session
// for it. A listing that fails is not proof the session is gone, so it jumps as before.
func openOrOffer(c *Card, evs []Event) tea.Cmd {
	sess := c.Meta["rex_session"]
	return func() tea.Msg {
		if sess != "" {
			if _, _, err := resolveSession(sess); !errors.Is(err, errSessionNone) {
				return openCmd(c, evs)()
			}
		}
		// No session on the card, or it is gone. A start that timed out may still have made
		// one: adopt it rather than offer a second agent for the same card.
		// Answers are statusMsg, like any enter: they never touch a form opened meanwhile.
		live, err := liveSessionFor(c.Name)
		switch {
		case err != nil:
			return statusMsg("could not check Rex for " + c.Name + "'s agent, so none was started: " + err.Error())
		case live != "":
			if err := adoptSession(c.Name, sess, live); err != nil {
				return statusMsg("could not adopt " + c.Name + "'s agent: " + err.Error())
			}
			nc := *c
			nc.Meta = maps.Clone(c.Meta)
			nc.Meta["rex_session"] = live
			return openCmd(&nc, evs)()
		}
		return offerMsg{name: c.Name, stale: sess}
	}
}

// adoptSession records a live session labelled with the track's name on its card, if the
// card still has the empty or dead id it had.
func adoptSession(name, stale, live string) error {
	c, err := loadCard(name)
	switch {
	case err != nil:
		return errors.New("the card is gone")
	case c.Meta["rex_session"] != stale && c.Meta["rex_session"] != live:
		return errors.New("the card has a session now: " + c.Meta["rex_session"])
	}
	return setMeta(name, "rex_session", live)
}

// startExisting starts an agent for a card whose rex_session is still the empty or dead
// one the offer was made for, in the card's own dir.
func startExisting(name, stale string) createdMsg {
	fail := func(why string) createdMsg { return createdMsg{name: name, formErr: why} }
	if !validName.MatchString(name) {
		return fail("not a valid track name: " + name)
	}
	c, err := loadCard(name)
	switch {
	case err != nil:
		return fail("the card is gone")
	case c.Meta["rex_session"] != stale:
		return fail("the card has a session now: " + c.Meta["rex_session"])
	}
	// Check again right before starting: the earlier start's session may have appeared.
	switch live, err := liveSessionFor(name); {
	case err != nil:
		return fail("could not check Rex for " + name + "'s session: " + err.Error())
	case live != "":
		if err := adoptSession(name, stale, live); err != nil {
			return fail(err.Error())
		}
		status := "Found " + name + "'s agent from an earlier start"
		if note := selectSession(live, ""); note != "" {
			status += ", but " + note
		}
		return createdMsg{name: name, status: status, ok: true}
	}
	dir := filepath.Clean(expandHome(c.Meta["dir"]))
	if st, err := os.Stat(dir); c.Meta["dir"] == "" || !filepath.IsAbs(dir) || err != nil || !st.IsDir() {
		return fail("the card's dir is not a directory: " + c.Meta["dir"])
	}
	return launch(trackSpec{name: name, dir: dir})
}

// launch starts the agent's Rex session for a written card and records it on the card.
func launch(s trackSpec) createdMsg {
	sid, block, err := rexNewSession(s)
	if err != nil {
		return createdMsg{name: s.name, status: "could not start a Rex session for " + s.name + " (card kept) · enter to retry · " + err.Error()}
	}
	if err := setMeta(s.name, "rex_session", sid); err != nil {
		return createdMsg{name: s.name, status: "could not write rex_session " + sid + " on " + s.name + "'s card: " + err.Error()}
	}
	if note := selectSession(sid, block); note != "" {
		return createdMsg{name: s.name, status: "Started " + s.name + ", but " + note, ok: true}
	}
	return createdMsg{name: s.name, status: "Started " + s.name, ok: true}
}

// edited refreshes what the form shows after its text changed.
func (f *newForm) edited() {
	f.err, f.preview, f.name, f.dir = "", "", "", ""
	goal := cleanRequest(f.ed.String())
	if goal == "" {
		return
	}
	f.name = freeName(slugName(goal, time.Now()), f.retired)
	f.preview = "→ " + f.name
	if dir, err := defaultDir(); err == nil {
		f.dir = tildePath(dir)
		f.preview += " · " + f.dir
	}
}

func (m *paneModel) submitForm() tea.Cmd {
	f := m.form
	goal := cleanRequest(f.ed.String())
	if goal == "" {
		f.err = requestRequired
		return nil
	}
	dir, err := defaultDir()
	if err != nil {
		f.err = err.Error()
		return nil
	}
	f.busy, f.err = true, ""
	return func() tea.Msg { return startRequest(goal, dir, time.Now()) }
}

func (m *paneModel) created(msg createdMsg) (tea.Model, tea.Cmd) {
	m.starting = ""
	if msg.cardErr != nil || msg.formErr != "" {
		if m.form == nil {
			m.status = "could not start an agent for " + msg.name + ": " + msg.formErr
		} else {
			m.form.busy = false
			m.form.err = msg.formErr
			if msg.cardErr != nil {
				m.form.err = "could not write the card: " + msg.cardErr.Error()
			}
		}
		return m, nil
	}
	m.form = nil
	m.status = msg.status
	m.cur, m.scroll, m.hold = msg.name, 0, ""
	m.reload()
	if msg.ok && m.float && msg.status == "Started "+msg.name {
		return m, tea.Quit
	}
	return m, tea.Batch(m.sinceCmd(), m.rexCmd())
}

func (m *paneModel) formKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	if f.busy {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.form = nil
	case "enter":
		return m, m.submitForm()
	default:
		before := f.ed.String()
		if f.ed.key(msg) && f.ed.String() != before {
			f.edited()
		}
	}
	return m, nil
}

func (m *paneModel) formPaste(text string) {
	if f := m.form; !f.busy {
		f.ed.insert(text)
		f.edited()
	}
}

// formFooter is the footer while the form is open: progress, an error, or the keys.
func (m *paneModel) formFooter() string {
	f := m.form
	switch {
	case f.busy:
		return dimSt.Render(fit("Starting…", m.w-2))
	case f.err == requestRequired: // a slip, not a failure
		return dimSt.Render(fit(f.err, m.w-2))
	case f.err != "":
		return badSt.Render(fit(errMark+baseNames(f.err), m.w-2))
	}
	return fit(styleHints(requestHints), m.w-2)
}

// formBeside is the form in the detail column, with the list still on the left, dimmed: the
// request is written with the tracks it will join in view.
func (m *paneModel) formBeside(bodyH int) []string {
	lw := m.leftW()
	w, _ := m.detailGeom()
	l := m.left(lw, bodyH)
	for i, line := range l {
		l[i] = dimSt.Render(ansi.Strip(line)) // the selection tint and colours would pull the eye
	}
	return splitRows(l, m.formDetail(w, bodyH), lw, bodyH)
}

// formDetail is the form's lines for a column w wide: the title, the editor, then the name
// and dir the request would get and a note on what happens next.
func (m *paneModel) formDetail(w, bodyH int) []string {
	f := m.form
	note := strings.Split(lipgloss.NewStyle().Width(w).Render(requestNote), "\n")
	const fixedRows = 6 // title, blank, blank, name, dir, blank
	maxRows := max(1, min(formMaxRows, bodyH-fixedRows-len(note)))
	title := boldSt.Render("New track")
	if n := len([]rune(f.ed.String())); n > 0 {
		count := fmt.Sprintf("%d/%d", n, maxRequest)
		title += strings.Repeat(" ", max(1, w-lipgloss.Width("New track")-len(count))) + dimSt.Render(count)
	}
	rows := []string{title, ""}
	prompt := boldSt.Render("› ")
	var input []string
	if f.ed.String() == "" && !f.busy {
		input = append(input, prompt+cursorSt.Render(" ")+dimSt.Render(fit(requestHolder, max(0, w-3))))
	} else {
		for i, l := range f.ed.wrapped(max(1, w-2), maxRows, !f.busy) {
			lead := "  "
			if i == 0 {
				lead = prompt
			}
			input = append(input, lead+fit(l, max(0, w-2)))
		}
	}
	for len(input) < min(formMinRows, maxRows) {
		input = append(input, "")
	}
	rows = append(rows, input...)
	rows = append(rows, "")
	for _, kv := range [][2]string{{"name", f.name}, {"dir ", f.dir}} {
		if kv[1] == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, dimSt.Render(ansi.Truncate(kv[0]+"  "+kv[1], w, "…")))
	}
	rows = append(rows, "")
	for _, l := range note {
		rows = append(rows, dimSt.Render(strings.TrimRight(l, " ")))
	}
	return rows
}

// formBody is the form's box: as wide as the pane allows up to formMaxWidth, with an input
// that grows from formMinRows to formMaxRows as the text wraps and scrolls past that.
func (m *paneModel) formBody(bodyH int) []string {
	f := m.form
	const chrome = 2 + 4    // border and padding, both sides
	const fixedRows = 2 + 4 // border, then the prompt, two blank lines and the note
	inner := max(16, min(formMaxWidth, m.w-2*formMargin)-chrome)
	maxRows := max(1, min(formMaxRows, bodyH-fixedRows))
	prompt := boldSt.Render("› ")
	rows := []string{boldSt.Render(requestPrompt), ""}
	var input []string
	if f.ed.String() == "" && !f.busy {
		input = append(input, prompt+cursorSt.Render(" ")+dimSt.Render(fit(requestHolder, inner-3)))
	} else {
		for i, l := range f.ed.wrapped(inner-2, maxRows, !f.busy) {
			lead := "  "
			if i == 0 {
				lead = prompt
			}
			input = append(input, lead+fit(l, inner-2))
		}
	}
	for len(input) < min(formMinRows, maxRows) {
		input = append(input, "")
	}
	rows = append(rows, input...)
	note := requestNote
	if f.preview != "" {
		note = f.preview
	}
	count := ""
	if n := len([]rune(f.ed.String())); n > 0 {
		count = fmt.Sprintf("%d/%d", n, maxRequest)
	}
	rows = append(rows, "", dimSt.Render(fit(note, inner-len(count)-1)+" "+count))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 2).
		Render(strings.Join(rows, "\n"))
	lines := strings.Split(box, "\n")
	var body []string
	// The top stays put as the input grows: it is placed for the tallest box.
	for range max(0, (bodyH-fixedRows-maxRows)/2) {
		body = append(body, "")
	}
	for _, l := range lines {
		body = append(body, fit(center(l, m.w), m.w))
	}
	return body
}
