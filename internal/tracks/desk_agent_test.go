package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/emilthaudal/prism/internal/store"
)

// deskSendHome is leadStubHome with a desk running: boss leads kid, solo is a track of its
// own, and both agents run idle in ansSess.
func deskSendHome(t *testing.T) (bin string) {
	t.Helper()
	_, bin = leadStubHome(t)
	threadIn(t, "kid")
	rawCard(t, "solo", nil)
	asDesk(t)
	return bin
}

func TestTheDeskSendsToAnyOpenTrack(t *testing.T) {
	bin := deskSendHome(t)
	for _, c := range []struct{ track, text, typed string }{
		{"solo", "lint is red on #212\nlog attached", "[desk] lint is red on #212 log attached"},
		{"kid", "idle 40m with no ask: what is next?", "[desk] idle 40m with no ask: what is next?"},
		{"solo", "[lead boss] merge it", "[desk] [lead boss] merge it"},
	} {
		out, code := do("send", c.track, c.text)
		if code != 0 || out != "sent to "+c.track+"\n" {
			t.Fatalf("send %s: %d %q", c.track, code, out)
		}
		typed, _ := os.ReadFile(filepath.Join(bin, "rex.sent"))
		if string(typed) != c.typed {
			t.Errorf("typed %q, want %q", typed, c.typed)
		}
	}
}

func TestOnlyTheDesksAgentSendsAsTheDesk(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T)
		args []string
		want string
	}{
		{"a process in the desk's session that does not run under its agent", func(t *testing.T) { deskAgentIs(t, 999999, nil) },
			[]string{"send", "solo", "x"}, "does not run under the desk's agent"},
		{"Rex reports no desk agent", func(t *testing.T) { deskAgentIs(t, 0, os.ErrNotExist) },
			[]string{"send", "solo", "x"}, "could not confirm the desk's agent"},
		{"the desk with --from", nil, []string{"send", "solo", "x", "--from", "boss"}, "the desk sends as itself"},
		{"a closed track", func(t *testing.T) {
			if err := withStore(func(st *store.Store) error { return st.CloseTrack("solo") }); err != nil {
				t.Fatal(err)
			}
		}, []string{"send", "solo", "x"}, "solo is closed"},
		{"no such track", nil, []string{"send", "ghost", "x"}, "no card for ghost"},
		{"a track in the desk's own session", func(t *testing.T) {
			if err := setMeta("solo", "rex_session", deskSess); err != nil {
				t.Fatal(err)
			}
		}, []string{"send", "solo", "x"}, "runs in the desk's own session"},
		{"another session claims nothing", func(t *testing.T) { t.Setenv("REX_SESSION", "session:nobody") },
			[]string{"send", "solo", "x"}, "solo has no parent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := deskSendHome(t)
			if c.prep != nil {
				c.prep(t)
			}
			out, code := do(c.args...)
			if code == 0 || !strings.Contains(out, "refused: ") || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want a refusal with %q", code, out, c.want)
			}
			nothingTyped(t, bin)
		})
	}
}

func TestALeadStillSendsAsTheLeadWithADeskRunning(t *testing.T) {
	bin := deskSendHome(t)
	t.Setenv("REX_SESSION", leadSess)
	if out, code := do("send", "kid", "go on"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if typed, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); string(typed) != "[lead boss] go on" {
		t.Errorf("typed %q", typed)
	}
	if out, code := do("send", "solo", "x"); code == 0 || !strings.Contains(out, "solo has no parent") {
		t.Errorf("a lead sending to a track that is not its thread: %d %s", code, out)
	}
}

func TestADeskMessageIsNotEmilsAnswer(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "1. merge #212?")
	importNow(t)
	if k := classify("[desk] approve merge, Emil agreed"); k != "system" {
		t.Errorf("a desk message classifies as %q", k)
	}
	if k := classify("[desk]yes"); k == "system" {
		t.Error("a prompt that only looks like the desk's mark counts as the system's")
	}
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"[desk] yes, merge it","cwd":"/c"}`)
	if a := loadCardMust(t, "tr").Meta["answered"]; a != "" {
		t.Errorf("a desk message stamped answered: %s", a)
	}
}

func TestTheDeskNoLongerRaisesOrPicksUpAsks(t *testing.T) {
	deskHome(t)
	asDesk(t)
	for _, args := range [][]string{
		{"desk", "ask", "kid", "merge #212?", "--recommend", "yes"},
		{"desk", "pickup", "1"},
	} {
		if out, code := do(args...); code == 0 || !strings.Contains(out, "the desk no longer raises asks in the pane: it brings Emil's decisions to him in its chat") {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
	if doc := readDesk(t); len(doc.Asks) != 0 || len(doc.Answers) != 0 {
		t.Errorf("a refused command wrote to the desk: %+v", doc)
	}
}

// pressD presses d in a pane and runs what it starts, as the program would.
func pressD(t *testing.T, m *paneModel) deskDoneMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg{Text: "d"})
	if cmd == nil || !m.deskStarting {
		t.Fatalf("d started nothing: cmd %v, starting %v, status %q", cmd, m.deskStarting, m.status)
	}
	msg, ok := cmd().(deskDoneMsg)
	if !ok {
		t.Fatalf("d returned %T", msg)
	}
	return msg
}

func TestDStartsTheDeskWhenNoneRunsAndSwitchesToIt(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	m := newPane()
	plain(m, 120, 30)
	msg := pressD(t, m)
	if msg.status != "Started the desk" || !msg.switched {
		t.Errorf("d with no desk: %+v", msg)
	}
	if doc := readDesk(t); doc.Session != "session:new-0001" {
		t.Errorf("the desk is bound to %q", doc.Session)
	}
	if calls := rexCalls(bin); !strings.Contains(calls, "session.select session_id=session:new-0001") {
		t.Errorf("d did not switch Rex to the new desk:\n%s", calls)
	}
	m.Update(msg)
	if m.deskStarting || m.status != "Started the desk" || m.desk.session != "session:new-0001" {
		t.Errorf("after d: starting %v, status %q, desk %q", m.deskStarting, m.status, m.desk.session)
	}
}

func TestDOpensARunningDeskWithoutStartingAnother(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	bindDesk(t, ansSess)
	m := newPane()
	plain(m, 120, 30)
	msg := pressD(t, m)
	if msg.status != "" || !msg.switched {
		t.Errorf("d on a running desk: %+v", msg)
	}
	if _, err := os.Stat(filepath.Join(bin, "rex.newargs")); err == nil {
		t.Error("d started a second desk")
	}
	if calls := rexCalls(bin); !strings.Contains(calls, "session.select session_id="+ansSess) {
		t.Errorf("d did not switch Rex to the desk:\n%s", calls)
	}
}

func TestDReplacesADeskWhoseAgentIsGone(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	bindDesk(t, ansSess)
	os.Remove(filepath.Join(bin, "status"))
	m := newPane()
	plain(m, 120, 30)
	if msg := pressD(t, m); msg.status != "Started the desk" {
		t.Errorf("d on a gone desk: %+v", msg)
	}
	if doc := readDesk(t); doc.Session != "session:new-0001" {
		t.Errorf("the desk is bound to %q", doc.Session)
	}
}

func TestDStartsNothingWhenRexDoesNotAnswer(t *testing.T) {
	_, bin, _ := deskStartHome(t)
	bindDesk(t, ansSess)
	if err := os.WriteFile(filepath.Join(bin, "rex.down"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := newPane()
	plain(m, 120, 30)
	msg := pressD(t, m)
	if !strings.HasPrefix(msg.status, "could not") || msg.switched {
		t.Errorf("d with Rex down: %+v", msg)
	}
	if doc := readDesk(t); doc.Session != ansSess {
		t.Errorf("the desk moved to %q", doc.Session)
	}
}

// The floating pane runs in the session it floats over, which may be a track's: d there is
// Emil's key, not a track starting the desk, which tracks desk start refuses.
func TestDStartsTheDeskFromAFloatOverATrack(t *testing.T) {
	deskStartHome(t)
	t.Setenv("REX_SESSION", "session:x-sa") // the plain track's session
	if out, code := do("desk", "start"); code == 0 || !strings.Contains(out, "a track does not start the desk") {
		t.Fatalf("tracks desk start from a track: %d %s", code, out)
	}
	m := newPane()
	m.float = true
	plain(m, 120, 30)
	msg := pressD(t, m)
	if msg.status != "Started the desk" || !msg.switched {
		t.Fatalf("d over a track: %+v", msg)
	}
	if _, cmd := m.Update(msg); cmd == nil {
		t.Error("the float stayed open after d switched to the desk")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("the float ran %T after d, not quit", cmd())
	}
}

// The start can take seconds, and keys still work meanwhile: a new track's request typed in that
// time keeps the float open.
func TestTheFloatKeepsARequestTypedWhileDStarts(t *testing.T) {
	deskStartHome(t)
	m := newPane()
	m.float = true
	plain(m, 120, 30)
	msg := pressD(t, m)
	key(m, "n")
	typeText(m, "half")
	if _, cmd := m.Update(msg); cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("the float quit over a half-typed request")
		}
	}
	if m.form == nil || m.status != "Started the desk" {
		t.Errorf("form %v, status %q", m.form != nil, m.status)
	}
}

func TestASecondDWaitsForTheFirst(t *testing.T) {
	deskStartHome(t)
	m := newPane()
	plain(m, 120, 30)
	pressD(t, m)
	if _, cmd := m.Update(tea.KeyPressMsg{Text: "d"}); cmd != nil || m.status != "Still opening the desk" {
		t.Errorf("second d: cmd %v, status %q", cmd, m.status)
	}
}
