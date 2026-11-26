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

func openEvents(t *testing.T) []Event {
	t.Helper()
	var out []Event
	for _, e := range readEvents() {
		if e.Event == "open" {
			out = append(out, e)
		}
	}
	return out
}

func TestAsksOnWorkingAgentDoNotNeedYou(t *testing.T) {
	cards := []*Card{mk("busy", "focus", iso(time.Hour), "1. q?"), mk("idle", "focus", iso(time.Hour), "1. q?")}
	states := map[string]agentState{"s-busy": st("working", "", time.Minute), "s-idle": st("idle", "", time.Minute)}
	if got := names(needsOrder(cards, states, nil, t0)); got != "idle" {
		t.Errorf("order: %q", got)
	}
	states["s-busy"] = st("blocked", "permission", time.Minute)
	if got := names(needsOrder(cards, states, nil, t0)); got != "busy idle" {
		t.Errorf("blocked still counts: %q", got)
	}
}

func TestPaneHeaderAndTabSkipWorkingAsks(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "working", "", "typing"), false)
	card(t, th, "busy", "focus", sessFull, "w", "1. q?")
	card(t, th, "calm", "background", "", "w", "")
	m := newPane()
	settle(t, m)
	out := plain(m, 100, 14)
	if forYouHeader.MatchString(out) {
		t.Errorf("header counts a working agent's ask:\n%s", out)
	}
	tabKey(m, false)
	if m.cur != "busy" {
		t.Fatalf("cursor %s", m.cur)
	}
	if out := plain(m, 100, 14); !strings.Contains(out, nothingNeedsYou) {
		t.Errorf("tab should say nothing needs you:\n%s", out)
	}
}

func TestExitedAgentWithAsksStillCounts(t *testing.T) {
	c := mk("gone", "focus", iso(time.Hour), "1. q?")
	states := map[string]agentState{"s-gone": {State: "idle", Owner: 5, Exited: true, At: t0}}
	if got := names(needsOrder([]*Card{c}, states, nil, t0)); got != "gone" {
		t.Errorf("%q", got)
	}
}

func TestStaleNotifyIsNotBlocked(t *testing.T) {
	c := mk("h", "focus", "", "")
	old := []Event{{At: t0.Add(-notifyFresh - time.Minute), Event: "notify", RexSession: "s-h"}}
	if items := needsOrder([]*Card{c}, nil, old, t0); len(items) != 0 {
		t.Errorf("a notify from 11 minutes ago is the idle notification: %+v", items)
	}
	fresh := []Event{{At: t0.Add(-notifyFresh + time.Minute), Event: "notify", RexSession: "s-h"}}
	if items := needsOrder([]*Card{c}, nil, fresh, t0); len(items) != 1 || items[0].level != needBlocked {
		t.Errorf("fresh notify: %+v", items)
	}
	if got := liveState(old, "s-h", t0); !strings.HasPrefix(got, "idle") {
		t.Errorf("liveState %q", got)
	}
}

func TestTracksNextSkipsDeadAndAmbiguousSessions(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), false)
	card(t, th, "a-ghost", "focus", "ghost", "w", "1. q?")
	card(t, th, "b-live", "background", sessFull, "w", "1. q?")
	if out, code := do("next"); code != 0 || out != "→ b-live · 1 ask\n" {
		t.Fatalf("dead session: %d %q", code, out)
	}
	if calls := rexCalls(bin); strings.Count(calls, "do session.select") != 1 || !strings.Contains(calls, sessFull) {
		t.Errorf("selected something else:\n%s", calls)
	}
	if ev := openEvents(t); len(ev) != 1 || ev[0].Track != "b-live" {
		t.Errorf("open events %+v", ev)
	}

	foldBack(t)
	os.Remove(logPath())
	sessionsStub(t, bin, `{"sessions":[{"session_id":"session:aaaa-11eb6e0e","label":"first"},{"session_id":"session:bbbb-22eb6e0e","label":"second"}]}`)
	card(t, th, "a-amb", "focus", "eb6e0e", "w", "1. q?")
	card(t, th, "b-live", "background", "22eb6e0e", "w", "1. q?")
	if out, code := do("next"); code != 0 || !strings.HasPrefix(out, "→ b-live · 1 ask\n") {
		t.Fatalf("ambiguous session: %d %q", code, out)
	}
	if ev := openEvents(t); len(ev) != 1 || ev[0].Track != "b-live" {
		t.Errorf("open events %+v", ev)
	}
}

func TestFailedJumpLogsNoOpenEvent(t *testing.T) {
	th, bin := setup(t)
	rexStub(t, bin, fmt.Sprintf(agentJSON, "idle", "", ""), false)
	card(t, th, "ghost", "focus", "ghost", "w", "1. q?")
	if out, code := do("next"); code == 0 || !strings.Contains(out, "→ ghost") {
		t.Errorf("next: %d %q", code, out)
	}
	if ev := openEvents(t); len(ev) != 0 {
		t.Errorf("tracks next logged %+v", ev)
	}
	card(t, th, "nosess", "focus", "", "w", "")
	for _, name := range []string{"ghost", "nosess"} {
		m := newPane()
		for m.cur != name {
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		msg := cmd()
		if o, ok := msg.(offerMsg); !ok || o.name != name {
			t.Errorf("%s: enter offers no new agent: %v", name, msg)
		}
	}
	if ev := openEvents(t); len(ev) != 0 {
		t.Errorf("enter logged %+v", ev)
	}
	card(t, th, "ok", "focus", sessFull, "w", "")
	m := newPane()
	for m.cur != "ok" {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmd()
	if ev := openEvents(t); len(ev) != 1 || ev[0].Track != "ok" {
		t.Errorf("a good jump logs: %+v", ev)
	}
}

func TestTracksNewRefusesArchivedNameExactly(t *testing.T) {
	setup(t)
	noImport(t) // archive files without name: are named by their file name only on files
	dir := archiveDir()
	os.MkdirAll(dir, 0o755)
	write := func(f string) { os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644) }
	write("2026-11-16-ci-alpha.md")
	write("2026-11-16-alpha-extra.md")
	write("2026-11-16-alphabet.md")
	write("notes-alpha.md")
	if out, code := do("new", "alpha", "--dir", "/tmp"); code != 0 {
		t.Fatalf("another track's archive blocked it: %q", out)
	}
	foldBack(t)
	os.Remove(cardPath("alpha"))
	for _, f := range []string{"2026-11-16-alpha.md", "2026-11-10-alpha-2.md"} {
		write(f)
		out, code := do("new", "alpha", "--dir", "/tmp")
		if code == 0 || !strings.Contains(out, f) {
			t.Errorf("%s: %d %q", f, code, out)
		}
		if _, err := os.Stat(cardPath("alpha")); err == nil {
			t.Errorf("%s: card written", f)
		}
		os.Remove(filepath.Join(dir, f))
	}
	if a := archivedCard("a.c"); a != "" {
		t.Errorf("name is not a pattern: %q", a)
	}
}

func TestLinkLabelsDropFormatCharacters(t *testing.T) {
	ls := parseLinks("https://exa‮mple.com/a​b")
	if len(ls) == 1 {
		for _, r := range ls[0].label {
			if r == '‮' || r == '​' {
				t.Errorf("label %q", ls[0].label)
			}
		}
	}
	ls = parseLinks("https://example.com/‮cod.exe")
	if len(ls) != 1 || strings.ContainsRune(ls[0].label, '‮') {
		t.Errorf("%+v", ls)
	}
}
