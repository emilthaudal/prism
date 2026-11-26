package tracks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	sessFull = "session:0001-aaaaaa-eb6e0e"
	blkA     = "block:aaa"
	blkB     = "block:bbb"
)

const agentJSON = `{"records":[{"state":"%s","kind":"%s","msg":"%s","app":"claude-code","effective_app":"claude-code","updated_at":"2026-11-16T22:38:27+01:00"}]}`

// rexStub replaces the rex stub with one that answers like the real CLI. blkA is a shell,
// blkB runs Claude Code with the given program_status reply.
func rexStub(t *testing.T, bin, status string, renameFails bool) {
	t.Helper()
	rename := `echo "$@" >> ` + bin + `/rex.calls`
	if renameFails {
		rename = `echo boom >&2; exit 1`
	}
	body := `#!/bin/sh
[ "$1" = "--autostart=false" ] || { echo "rex called without --autostart=false: $@" >> ` + bin + `/rex.bad; }
shift
echo "$@" >> ` + bin + `/rex.calls
case "$1 $2" in
"session ls") echo '{"sessions":[{"session_id":"` + sessFull + `","label":"my track"}]}';;
"session rename") ` + rename + `;;
"block ls") echo '{"blocks":[{"block_id":"` + blkA + `"},{"block_id":"` + blkB + `"}]}';;
"block call")
  case "$6" in
  ` + blkB + `) cat <<'EOF'
` + status + `
EOF
;;
  *) echo '{"records":[]}';;
  esac;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func rexCalls(bin string) string {
	b, _ := os.ReadFile(bin + "/rex.calls")
	return string(b)
}

func TestTierParsingAndChip(t *testing.T) {
	for in, want := range map[string]string{"1": "1", "T2": "2", " t3 ": "3", "4": "", "high": "", "": ""} {
		if got := (&Card{Meta: map[string]string{"tier": in}}).tier(); got != want {
			t.Errorf("tier(%q)=%q want %q", in, got, want)
		}
	}
	th, _ := setup(t)
	for _, n := range []string{"low", "mid", "high", "none"} {
		card(t, th, n, "background", "", "w", "")
	}
	for n, tier := range map[string]string{"low": "1", "mid": "T2", "high": "3"} {
		b, _ := os.ReadFile(cardPath(n))
		os.WriteFile(cardPath(n), []byte(strings.Replace(string(b), "dir:", "tier: "+tier+"\ndir:", 1)), 0o644)
	}
	m := newPane()
	out := plain(m, 100, 20)
	has := func(name, chip string) {
		t.Helper()
		for _, row := range strings.Split(out, "\n") {
			l, _, _ := strings.Cut(row, "│")
			if strings.Contains(l, name+" ") {
				l = strings.TrimRight(l, " ")
				if chip != "" && !strings.HasSuffix(l, chip) {
					t.Errorf("%s row lacks %q: %q", name, chip, l)
				}
				if chip == "" && (strings.Contains(l, "T1") || strings.Contains(l, "T2") || strings.Contains(l, "T3")) {
					t.Errorf("%s row has a chip: %q", name, l)
				}
				return
			}
		}
		t.Errorf("no row for %s in\n%s", name, out)
	}
	has("low", "T1")
	has("mid", "T2")
	has("high", "[T3]")
	has("none", "")
	m.cur = "high"
	m.idx = slicesIndex(m, "high")
	if !strings.Contains(plain(m, 100, 20), "[T3] · background") {
		t.Errorf("detail header lacks the T3 chip:\n%s", plain(m, 100, 20))
	}
	m.idx = slicesIndex(m, "mid")
	if !strings.Contains(plain(m, 100, 20), "T2 · background") {
		t.Error("detail header lacks T2")
	}
	m.idx = slicesIndex(m, "none")
	if out := plain(m, 100, 20); !strings.Contains(out, "│ none ") || strings.Contains(out, "] · background") || strings.Contains(out, "T1 · ") || strings.Contains(out, "T2 · ") {
		t.Errorf("missing tier should show no chip in the detail:\n%s", out)
	}
}

func slicesIndex(m *paneModel, name string) int {
	for i, c := range m.cards {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func TestNewCardHasTier(t *testing.T) {
	setup(t)
	if out, code := do("new", "x", "--dir", "/d"); code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(readCard("x"), "\ntier:") {
		t.Errorf("template lacks tier:\n%s", readCard("x"))
	}
}

func TestCloseCardRewrites(t *testing.T) {
	now := time.Date(2026, 11, 16, 22, 0, 0, 0, time.UTC)
	got := closeCard("---\nname: x\nclosed: old\n---\n## Goal\ng\n\n## Where it is\nDone.\n", now)
	if !strings.Contains(got, "closed: 2026-11-16T22:00:00Z\n") || strings.Contains(got, "closed: old") {
		t.Errorf("closed not replaced:\n%s", got)
	}
	if !strings.Contains(got, "## Outcome\nDone.") || strings.Contains(got, "Where it is") {
		t.Errorf("outcome not renamed:\n%s", got)
	}
	got = closeCard("---\r\nname: x\r\n---\r\n## Where it is\r\nDone.\r\n", now)
	if !strings.Contains(got, "closed: 2026-11-16T22:00:00Z\r\n---\r\n## Outcome\r\n") {
		t.Errorf("CRLF broken: %q", got)
	}
	if got := closeCard("## Where it is\nbody\n", now); !strings.Contains(got, "## Outcome") || strings.Contains(got, "closed:") {
		t.Errorf("no frontmatter: %q", got)
	}
}

func TestCloseArchivesAndMarksSession(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), false)
	card(t, th, "la", "focus", "eb6e0e", "Refunds shipped.\nline two", "")
	os.Chmod(cardPath("la"), 0o600)
	now := time.Date(2026, 11, 16, 22, 0, 0, 0, time.Local)
	dest, note, err := closeTrack("la", now, "")
	if err != nil || note != "Closed la; kept its Rex session: it has 2 panes open" {
		t.Fatalf("err=%v note=%q", err, note)
	}
	if want := filepath.Join(th, "cards", "archive", "2026-11-16-la.md"); dest != want {
		t.Errorf("dest %s want %s", dest, want)
	}
	if _, err := os.Stat(cardPath("la")); err == nil {
		t.Error("card still in cards/")
	}
	b, _ := os.ReadFile(dest)
	if !strings.Contains(string(b), "closed: 2026-11-16T22:00:00") || !strings.Contains(string(b), "## Outcome\nRefunds shipped.") {
		t.Errorf("archive:\n%s", b)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if c := rexCalls(bin); !strings.Contains(c, "session rename "+sessFull+" ✓ my track") || strings.Contains(c, "kill") {
		t.Errorf("rex calls:\n%s", c)
	}
	if n := len(loadCards()); n != 0 {
		t.Errorf("loadCards picked up %d archived cards", n)
	}
	if got := closedToday(now); got != 1 {
		t.Errorf("closedToday %d", got)
	}
	if got := closedToday(now.AddDate(0, 0, 1)); got != 0 {
		t.Errorf("closedToday tomorrow %d", got)
	}
}

func TestCloseNeverOverwritesArchive(t *testing.T) {
	th, _ := setup(t)
	now := time.Date(2026, 11, 16, 22, 0, 0, 0, time.Local)
	existing := filepath.Join(th, "cards", "archive", "2026-11-16-la.md")
	if err := writeAtomic(existing, []byte("precious")); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"2026-11-16-la-2.md", "2026-11-16-la-3.md"} {
		card(t, th, "la", "focus", "", "w", "")
		dest, _, err := closeTrack("la", now, "")
		if err != nil || filepath.Base(dest) != want {
			t.Fatalf("round %d: %s %v", i, dest, err)
		}
	}
	if b, _ := os.ReadFile(existing); string(b) != "precious" {
		t.Errorf("existing archive file overwritten: %q", b)
	}
	if entries, _ := filepath.Glob(filepath.Join(th, "cards", "archive", ".tmp-*")); len(entries) != 0 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestCloseRexFailureNeverBlocks(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), true)
	card(t, th, "la", "focus", "eb6e0e", "w", "")
	dest, note, err := closeTrack("la", time.Now(), "")
	if err != nil || !strings.Contains(note, "could not update its Rex session") {
		t.Fatalf("err=%v note=%q", err, note)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Error("archive missing")
	}
	os.WriteFile(bin+"/rex", []byte("#!/bin/sh\nexit 1\n"), 0o755)
	card(t, th, "lb", "focus", "eb6e0e", "w", "")
	if _, note, err := closeTrack("lb", time.Now(), ""); err != nil || note == "" {
		t.Errorf("rex down: err=%v note=%q", err, note)
	}
	if _, _, err := closeTrack("nope", time.Now(), ""); err == nil {
		t.Error("closing a missing card should fail")
	}
	if _, _, err := closeTrack("../x", time.Now(), ""); err == nil {
		t.Error("path traversal accepted")
	}
}

func TestCloseSkipsAlreadyMarkedLabel(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), false)
	os.WriteFile(bin+"/rex", []byte(strings.Replace(mustRead(t, bin+"/rex"), `"label":"my track"`, `"label":"✓ my track"`, 1)), 0o755)
	card(t, th, "la", "focus", "eb6e0e", "w", "")
	closeTrack("la", time.Now(), "")
	if strings.Contains(rexCalls(bin), "rename") {
		t.Errorf("renamed an already marked session:\n%s", rexCalls(bin))
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCloseAndArchiveCommands(t *testing.T) {
	th, _ := setup(t)
	if out, _ := do("archive"); !strings.Contains(out, "no closed tracks yet") {
		t.Errorf("empty archive: %q", out)
	}
	card(t, th, "old", "focus", "", "Old outcome.", "")
	card(t, th, "new", "focus", "", "New outcome.\nsecond", "")
	card(t, th, "bare", "focus", "", "", "")
	for name, at := range map[string]time.Time{
		"old":  time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local),
		"new":  time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local),
		"bare": time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local),
	} {
		if _, _, err := closeTrack(name, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	out, code := do("archive")
	if code != 0 {
		t.Fatal(out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "2026-11-15  new") || !strings.Contains(lines[0], "New outcome.") ||
		!strings.HasPrefix(lines[1], "2026-11-12  bare") || !strings.HasSuffix(lines[1], "-") || !strings.HasPrefix(lines[2], "2026-11-10  old") {
		t.Errorf("archive:\n%s", out)
	}
	card(t, th, "viacli", "focus", "", "w", "")
	if out, code := do("close", "viacli"); code != 0 || !strings.Contains(out, "Closed viacli") {
		t.Errorf("close: %d %q", code, out)
	}
	if _, err := os.Stat(cardPath("viacli")); err == nil {
		t.Error("card not moved")
	}
	if _, code := do("close"); code == 0 {
		t.Error("close without a name should fail")
	}
	if _, code := do("close", "missing"); code == 0 {
		t.Error("close of a missing card should fail")
	}
}

func TestPaneCloseConfirmFlow(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "Done.", "")
	card(t, th, "b", "background", "", "w", "")
	m := newPane()
	key(m, "x")
	if !strings.Contains(plain(m, 100, 12), "Close a? Archives the card.  y close · n keep") {
		t.Fatalf("no confirm:\n%s", plain(m, 100, 12))
	}
	key(m, "n")
	if m.confirm != "" || !strings.Contains(plain(m, 100, 12), "enter open") {
		t.Error("n did not cancel")
	}
	if _, err := os.Stat(cardPath("a")); err != nil {
		t.Fatal("card closed without y")
	}
	key(m, "x")
	key(m, "esc")
	if m.confirm != "" {
		t.Error("esc did not cancel")
	}
	key(m, "x")
	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	if cmd == nil {
		t.Fatal("y did not start the close")
	}
	m.Update(cmd())
	out := plain(m, 100, 12)
	if !strings.Contains(out, "1 closed today") || !strings.Contains(out, "Closed a") || strings.Contains(out, "▌a ") {
		t.Errorf("after close:\n%s", out)
	}
	if m.cur != "b" {
		t.Errorf("cursor %s", m.cur)
	}
}

func TestPaneCloseFailureShowsError(t *testing.T) {
	setup(t)
	m := newPane()
	m.Update(closedMsg{name: "z", err: fmt.Errorf("boom")})
	if out := plain(m, 100, 12); !strings.Contains(out, "could not close z: boom") {
		t.Errorf("no error:\n%s", out)
	}
}

func TestParseProgramStatus(t *testing.T) {
	cases := []struct {
		name, in string
		ok       bool
		want     agentState
	}{
		{"working", fmt.Sprintf(agentJSON, "working", "", "Reading files"), true, agentState{State: "working", Msg: "Reading files"}},
		{"blocked permission", fmt.Sprintf(agentJSON, "blocked", "permission", "Run terraform plan"), true, agentState{State: "blocked", Kind: "permission", Msg: "Run terraform plan"}},
		{"title when no msg", `{"records":[{"id":"x","state":"idle","effective_app":"claude-code","title":"A task"}]}`, true, agentState{State: "idle", Msg: "A task"}},
		{"blocked beats working", `{"records":[{"state":"working","effective_app":"claude-code"},{"id":"s","state":"blocked","kind":"question","msg":"which?","effective_app":"claude-code"}]}`, true, agentState{State: "blocked", Kind: "question", Msg: "which?"}},
		{"msg from the first record that has one", `{"records":[{"state":"working","effective_app":"claude-code"},{"id":"s","state":"idle","msg":"sub msg","title":"sub title","effective_app":"claude-code"}]}`, true, agentState{State: "working", Msg: "sub msg"}},
		{"msg beats an earlier title", `{"records":[{"id":"a","state":"working","title":"T","effective_app":"claude-code"},{"id":"b","state":"working","msg":"M","effective_app":"claude-code"}]}`, true, agentState{State: "working", Msg: "M"}},
		{"other app", `{"records":[{"state":"working","effective_app":"vim"}]}`, false, agentState{}},
		{"empty", `{"records":[]}`, false, agentState{}},
		{"garbage", `nope`, false, agentState{}},
	}
	for _, c := range cases {
		got, ok := parseProgramStatus([]byte(c.in))
		got.At = time.Time{}
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestFetchSessionHintThenScan(t *testing.T) {
	_, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "working", "", "Thinking"), false)
	r := fetchSession(sessFull, blkB, "")
	if !r.found || r.block != blkB || r.state.State != "working" {
		t.Fatalf("hinted: %+v", r)
	}
	if strings.Contains(rexCalls(bin), "block ls") {
		t.Error("scanned although the hint worked")
	}
	os.Remove(bin + "/rex.calls")
	r = fetchSession(sessFull, blkA, "")
	if !r.found || r.block != blkB {
		t.Fatalf("scan after a hint with no agent: %+v", r)
	}
	r = fetchSession(sessFull, "", "")
	if !r.found || r.block != blkB {
		t.Fatalf("scan without a hint: %+v", r)
	}
	rexStub(t, bin, `{"records":[]}`, false)
	if r := fetchSession(sessFull, "", ""); r.found || r.failed {
		t.Errorf("session with no agent: %+v", r)
	}
	os.WriteFile(bin+"/rex", []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if r := fetchSession(sessFull, blkB, ""); r.found || !r.failed {
		t.Errorf("rex failure must be failed, not found or empty: %+v", r)
	}
	if b, err := os.ReadFile(bin + "/rex.bad"); err == nil {
		t.Errorf("rex called without --autostart=false:\n%s", b)
	}
}

// settle runs the pane's rex fetch to completion, as the tick would.
func settle(t *testing.T, m *paneModel) {
	t.Helper()
	m.now = time.Now()
	cmd := m.rexCmd()
	if cmd == nil {
		t.Fatal("no fetch started")
	}
	m.Update(cmd())
}

func TestPaneShowsLiveRexState(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "blocked", "permission", "Wants to run: terraform plan"), false)
	card(t, th, "a", "focus", sessFull, "w", "")
	card(t, th, "b", "background", "", "w", "")
	appendEvent(Event{At: time.Now().UTC(), Event: "prompt", RexSession: sessFull, RexBlock: blkA})
	m := newPane()
	before := plain(m, 100, 14)
	if !strings.Contains(before, "● working") || forYouHeader.MatchString(before) {
		t.Fatalf("hook fallback:\n%s", before)
	}
	settle(t, m)
	out := plain(m, 100, 14)
	for _, s := range []string{"◆ permission", "◆ 1 for you", "◆ Wants to run: terraform plan"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q:\n%s", s, out)
		}
	}
}

func TestStateTable(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", sessFull, "w", "")
	m := newPane()
	cases := []struct{ state, kind, msg, want string }{
		{"working", "", "Editing pane.go", "● working"},
		{"blocked", "permission", "Run it", "◆ permission"},
		{"blocked", "question", "Which one?", "? question"},
		{"done", "", "", "✓ done"},
		{"error", "", "Crashed", "✕ error"},
		{"idle", "", "", "○ idle"},
	}
	for _, c := range cases {
		rexStub(t, bin, fmt.Sprintf(agentJSON, c.state, c.kind, c.msg), false)
		m.rex.lastRun, m.rex.inFlight = time.Time{}, false
		settle(t, m)
		out := plain(m, 100, 14)
		if !strings.Contains(out, c.want) {
			t.Errorf("%s/%s: missing %q:\n%s", c.state, c.kind, c.want, out)
		}
		if c.msg != "" && !strings.Contains(out, c.msg) {
			t.Errorf("%s: detail lacks msg %q", c.state, c.msg)
		}
		needs := strings.Contains(out, "◆ 1 for you")
		if needs != (c.state == "blocked" || c.state == "error") {
			t.Errorf("%s: need-you header = %v", c.state, needs)
		}
	}
	rexStub(t, bin, `{"records":[]}`, false)
	m.rex.lastRun, m.rex.inFlight = time.Time{}, false
	settle(t, m)
	if out := plain(m, 100, 14); strings.Contains(out, "idle") {
		t.Errorf("stale rex state kept after the agent vanished:\n%s", out)
	}
}

func TestRexFetchIsThrottledAndNeverOverlaps(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "working", "", "x"), false)
	card(t, th, "a", "focus", sessFull, "w", "")
	card(t, th, "p", "parked", "other", "w", "")
	m := newPane()
	if got := m.sessions(); len(got) != 1 || got[0] != sessFull {
		t.Fatalf("sessions %v (parked tracks are not polled)", got)
	}
	m.now = time.Now()
	cmd := m.rexCmd()
	if cmd == nil || m.rexCmd() != nil {
		t.Fatal("second fetch started while one is in flight")
	}
	m.Update(cmd())
	if m.rexCmd() != nil {
		t.Error("refetched before the cadence")
	}
	m.now = m.now.Add(rexEvery)
	if m.rexCmd() == nil {
		t.Error("no refetch after the cadence")
	}
}

func TestScrollDetail(t *testing.T) {
	th, _ := setup(t)
	var long []string
	for i := range 30 {
		long = append(long, fmt.Sprintf("line %02d", i))
	}
	card(t, th, "a", "focus", "", strings.Join(long, "\n"), "")
	card(t, th, "b", "background", "", "short", "")
	m := newPane()
	out := plain(m, 100, 14)
	if !strings.Contains(out, "line 00") || strings.Contains(out, "line 29") || !strings.Contains(out, moreHint) {
		t.Fatalf("clipped view:\n%s", out)
	}
	key2 := func(c rune) { m.Update(tea.KeyPressMsg{Code: c}) }
	key2(tea.KeyPgDown)
	out = plain(m, 100, 14)
	if strings.Contains(out, "Where it is") || m.scroll == 0 || !strings.Contains(out, moreHint) {
		t.Errorf("pgdn did not scroll (scroll=%d):\n%s", m.scroll, out)
	}
	for range 10 {
		key2(tea.KeyPgDown)
	}
	out = plain(m, 100, 14)
	if !strings.Contains(out, "line 29") || strings.Contains(out, moreHint) {
		t.Errorf("end of detail:\n%s", out)
	}
	if !strings.Contains(out, "a ") || !strings.Contains(strings.Split(out, "\n")[3], "│ a") {
		t.Errorf("header not pinned:\n%s", out)
	}
	atEnd := m.scroll
	key2(tea.KeyPgDown)
	if m.scroll != atEnd {
		t.Error("scrolled past the end")
	}
	key2(tea.KeyPgUp)
	if m.scroll >= atEnd {
		t.Error("pgup did not scroll back")
	}
	for range 20 {
		m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelUp})
	}
	if m.scroll != 0 {
		t.Errorf("wheel up did not reach the top: %d", m.scroll)
	}
	m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelDown})
	if m.scroll != wheelLines {
		t.Errorf("wheel down scrolled %d", m.scroll)
	}
	m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	if m.scroll != wheelLines {
		t.Error("wheel over the list scrolled the detail")
	}
	key(m, "down")
	if m.scroll != 0 {
		t.Error("scroll kept on another track")
	}
	if out := plain(m, 100, 14); strings.Contains(out, moreHint) {
		t.Errorf("hint on a short detail:\n%s", out)
	}
}

func TestHelpOverlay(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "alpha", "focus", "", "w", "")
	m := newPane()
	key(m, "?")
	out := plain(m, 100, 20)
	for _, s := range []string{"Keys", "close the track", "scroll, or wheel", "set the mode", "quit", "any key closes"} {
		if !strings.Contains(out, s) {
			t.Errorf("overlay lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "GOAL") {
		t.Error("detail shown behind the overlay")
	}
	key(m, "q")
	if m.help {
		t.Error("key did not close the overlay")
	}
	if out := plain(m, 100, 20); !strings.Contains(out, "GOAL") {
		t.Error("detail not back")
	}
	key(m, "?")
	key(m, "?")
	if m.help {
		t.Error("? did not toggle off")
	}
	if len(strings.Split(plain(m, 40, 10), "\n")) != 10 {
		t.Error("overlay breaks height on a small screen")
	}
}

func TestFirstLookAndGitError(t *testing.T) {
	th, bin := setup(t)
	card(t, th, "a", "focus", "s", "w", "")
	m := newPane()
	m.now = time.Now()
	m.Update(m.sinceCmd()())
	out := plain(m, 100, 16)
	if !strings.Contains(out, "First look") || strings.Contains(out, "24h") || !strings.Contains(out, "abc123 a commit") {
		t.Errorf("never opened:\n%s", out)
	}
	appendEvent(Event{At: time.Now().Add(-2 * time.Hour).UTC(), Event: "open", Track: "a"})
	m.reload()
	delete(m.since, "a")
	m.Update(m.sinceCmd()())
	m.now = time.Now().Add(time.Minute)
	if out := plain(m, 100, 16); !strings.Contains(out, "Since you last looked · 2h") {
		t.Errorf("opened before:\n%s", out)
	}
	os.WriteFile(bin+"/git", []byte("#!/bin/sh\necho 'fatal: bad object HEAD' >&2\nexit 128\n"), 0o755)
	delete(m.since, "a")
	m.Update(m.sinceCmd()())
	out = plain(m, 100, 16)
	if strings.Contains(out, "Nothing new") || !strings.Contains(out, "git: exit status 128: fatal: bad object HEAD") {
		t.Errorf("git failure:\n%s", out)
	}
	os.WriteFile(bin+"/git", []byte("#!/bin/sh\necho 'fatal: not a git repository (or any of the parent directories): .git' >&2\nexit 128\n"), 0o755)
	delete(m.since, "a")
	m.Update(m.sinceCmd()())
	out = plain(m, 100, 16)
	if strings.Contains(out, "Nothing new") || strings.Contains(out, "git") || strings.Contains(out, "Since you last looked") || strings.Contains(out, "First look") {
		t.Errorf("a dir outside git shows no since section:\n%s", out)
	}
	card(t, th, "nodir", "focus", "", "w", "")
	os.WriteFile(cardPath("nodir"), []byte("---\nname: nodir\nmode: focus\n---\n"), 0o644)
	m.reload()
	m.cur = "nodir"
	m.idx = slicesIndex(m, "nodir")
	m.Update(m.sinceCmd()())
	if out := plain(m, 100, 16); strings.Contains(out, "Nothing new") || !strings.Contains(out, "no dir on the card") || strings.Contains(out, "git: no dir") {
		t.Errorf("no dir:\n%s", out)
	}
}

func TestWriteAtomicKeepsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "c.md")
	if err := writeAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Errorf("new file mode %v", st.Mode())
	}
	os.Chmod(p, 0o600)
	if err := writeAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if b, _ := os.ReadFile(p); st.Mode().Perm() != 0o600 || string(b) != "two" {
		t.Errorf("mode %v content %q", st.Mode(), b)
	}
	os.Chmod(p, 0o664)
	writeAtomic(p, []byte("three"))
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o664 {
		t.Errorf("mode %v", st.Mode())
	}
}
