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

func sessionsStub(t *testing.T, bin, sessions string) {
	t.Helper()
	body := "#!/bin/sh\nshift\necho \"$@\" >> " + bin + "/rex.calls\n" +
		"if [ \"$1 $2\" = \"session ls\" ]; then cat <<'EOS'\n" + sessions + "\nEOS\nfi\n"
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

const twoSessions = `{"sessions":[{"session_id":"session:aaaa-11eb6e0e","label":"first"},{"session_id":"session:bbbb-22eb6e0e","label":"second"},{"session_id":"session:cccc-33ffffff","label":""}]}`

func TestSessionMatchingIsExactOrUniqueSuffix(t *testing.T) {
	th, bin := setup(t)
	sessionsStub(t, bin, twoSessions)
	card(t, th, "amb", "focus", "eb6e0e", "w", "")
	_, note, err := closeTrack("amb", time.Now(), "")
	if err != nil || !strings.Contains(note, "could not update its Rex session") || !strings.Contains(note, "more than one") {
		t.Fatalf("ambiguous suffix: err=%v note=%q", err, note)
	}
	if strings.Contains(rexCalls(bin), "rename") {
		t.Errorf("renamed a session on an ambiguous id:\n%s", rexCalls(bin))
	}
	card(t, th, "exact", "focus", "session:bbbb-22eb6e0e", "w", "")
	if _, note, _ := closeTrack("exact", time.Now(), ""); !strings.Contains(note, "kept its Rex session") || !strings.Contains(rexCalls(bin), "session rename session:bbbb-22eb6e0e ✓ second") {
		t.Errorf("exact id: note=%q calls:\n%s", note, rexCalls(bin))
	}
	card(t, th, "uniq", "focus", "33ffffff", "w", "")
	closeTrack("uniq", time.Now(), "")
	if !strings.Contains(rexCalls(bin), "session rename session:cccc-33ffffff ✓ uniq") {
		t.Errorf("empty label should become the track name:\n%s", rexCalls(bin))
	}
	card(t, th, "gone", "focus", "0000", "w", "")
	if _, note, _ := closeTrack("gone", time.Now(), ""); !strings.Contains(note, "already gone") || strings.Contains(rexCalls(bin), "kill") {
		t.Errorf("unknown session: %q", note)
	}
	if msg := selectSession("eb6e0e", "blk"); !strings.Contains(msg, "could not switch") || strings.Contains(rexCalls(bin), "session.select") {
		t.Errorf("select on an ambiguous id: %q", msg)
	}
	if r := fetchSession("eb6e0e", "", ""); r.found || r.failed {
		t.Errorf("ambiguous id in the state fetch must find nothing, got %+v", r)
	}
}

func TestNeverRecreatesClosedCard(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "waiting", "", "w", "")
	os.Remove(cardPath("a"))
	if err := setMode("a", "focus"); err == nil {
		t.Error("setMode on a closed card succeeded")
	}
	if err := writeExisting(cardPath("a"), []byte("x")); err == nil {
		t.Error("writeExisting created a file")
	}
	if err := fire("a", "pr 1 green", true); err == nil {
		t.Error("fire on a closed card succeeded")
	}
	if _, err := os.Stat(cardPath("a")); err == nil {
		t.Error("a closed card came back")
	}
}

func TestCloseConfirmWarnsWhenWorking(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "working", "", "Busy"), false)
	card(t, th, "a", "focus", sessFull, "w", "")
	m := newPane()
	key(m, "x")
	if out := plain(m, 120, 12); strings.Contains(out, "is working") || !strings.Contains(out, "Close a? Archives the card; ends its session if nothing in it can be lost.  y close · n keep") {
		t.Errorf("before Rex answered:\n%s", out)
	}
	key(m, "n")
	settle(t, m)
	key(m, "x")
	if out := plain(m, 120, 12); !strings.Contains(out, "Close a? Its agent is working; archives the card, keeps the session while it is busy.  y close · n keep") {
		t.Errorf("working confirm:\n%s", out)
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Text: "y"}); cmd == nil {
		t.Error("y did not close")
	}
}

func TestRexHangDoesNotHold(t *testing.T) {
	_, bin := setup(t)
	// The background sleep keeps rex's stdout open after rex itself is killed.
	os.WriteFile(filepath.Join(bin, "rex"), []byte("#!/bin/sh\nsleep 5 &\nsleep 5\n"), 0o755)
	old := rexTimeout
	rexTimeout = 200 * time.Millisecond
	defer func() { rexTimeout = old }()
	start := time.Now()
	r := fetchSession(sessFull, blkB, "")
	if d := time.Since(start); d > 3*time.Second || !r.failed {
		t.Errorf("hung rex held the fetch: %v %+v", d, r)
	}
}

func TestRexFailureKeepsLastState(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "blocked", "permission", "Run it"), false)
	card(t, th, "a", "focus", sessFull, "w", "")
	m := newPane()
	settle(t, m)
	os.WriteFile(filepath.Join(bin, "rex"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	m.rex.lastRun = time.Time{}
	settle(t, m)
	if out := plain(m, 100, 14); !strings.Contains(out, "◆ permission") {
		t.Errorf("state dropped on a failed call:\n%s", out)
	}
	rexStub(t, bin, `{"records":[]}`, false)
	m.rex.lastRun = time.Time{}
	settle(t, m)
	if out := plain(m, 100, 14); strings.Contains(out, "permission") {
		t.Errorf("state kept after Rex said there is no agent:\n%s", out)
	}
}

func TestDetailShowsMsgFromSubagentAndKind(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"},{"id":"s","state":"idle","title":"Refactor pane","app":"claude-code","effective_app":"claude-code"}]}`, false)
	card(t, th, "a", "focus", sessFull, "w", "")
	m := newPane()
	settle(t, m)
	if out := plain(m, 100, 14); !strings.Contains(out, "● Refactor pane") {
		t.Errorf("msg:\n%s", out)
	}
	rexStub(t, bin, `{"records":[{"state":"blocked","kind":"permission","effective_app":"claude-code"}]}`, false)
	m.rex.lastRun = time.Time{}
	settle(t, m)
	if out := plain(m, 100, 14); !strings.Contains(out, "◆ blocked · permission") {
		t.Errorf("blocked kind:\n%s", out)
	}
}

func TestHeaderDropsClosedThenNeed(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "1. q")
	card(t, th, "b", "focus", "", "w", "")
	card(t, th, "x", "focus", "", "w", "")
	closeTrack("x", time.Now(), "")
	m := newPane()
	if out := plain(m, 100, 12); !strings.Contains(out, "◆ 1 for you · handled 0 today · 1 closed today") {
		t.Errorf("wide:\n%s", out)
	}
	line := func(w int) string { return strings.Split(plain(m, w, 12), "\n")[0] }
	if l := line(34); strings.Contains(l, "closed") || !strings.Contains(l, "◆ 1 for you") || len([]rune(l)) > 34 {
		t.Errorf("drop closed first: %q", l)
	}
	if l := line(24); !strings.Contains(l, "◆ 1 for you") || len([]rune(l)) > 24 {
		t.Errorf("need you stays at the smallest width: %q", l)
	}
}

func TestHelpFitsNarrowAndListsEverything(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	m := newPane()
	key(m, "?")
	wide := plain(m, 100, 24)
	for _, s := range []string{"shift-drag", "esc", "ctrl+c"} {
		if !strings.Contains(wide, s) {
			t.Errorf("overlay lacks %q", s)
		}
	}
	for _, w := range []int{24, 30, 40, 60} {
		out := plain(m, w, 42) // at 24 columns every description wraps a word a line, and the box is 39 rows tall
		for _, l := range strings.Split(out, "\n") {
			if len([]rune(l)) > w {
				t.Errorf("width %d: line over width: %q", w, l)
			}
		}
		if !strings.Contains(out, "Keys") || !strings.Contains(out, "╰") {
			t.Errorf("width %d: box cut:\n%s", w, out)
		}
	}
}
