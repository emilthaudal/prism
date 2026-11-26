package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	contCheck = "pr 153 approved"
	contText  = `Check "pr 153 approved" is now yes. Continue per the card.`
)

// contCard writes a card waiting on contCheck with the given tier and mode, lets tracks check
// fire its ask and records the answer, as a real run of tracks check would.
func contCard(t *testing.T, th, tier, mode string, ans bool) {
	t.Helper()
	tierLine := ""
	if tier != "-" {
		tierLine = "tier: " + tier + "\n"
	}
	txt := "---\nname: w\nmode: " + mode + "\n" + tierLine + "dir: " + th + "\ncheck: " + contCheck + "\nrex_session: " + ansSess + "\nupdated: 2026-01-01T00:00:00Z\n---\n## Goal\nG\n\n## Needs me\n\n## Standing orders\n- x\n"
	txt = insertAsk(txt, askLine(contCheck, ans, "waiting"), time.Now())
	foldBack(t)
	if err := writeAtomic(cardPath("w"), []byte(txt)); err != nil {
		t.Fatal(err)
	}
	asked := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if err := saveChecks(map[string]checkState{"w: " + contCheck: {Answer: ans, Asked: asked}}); err != nil {
		t.Fatal(err)
	}
}

// contPane opens the pane on w with Rex's view of the agent loaded, where the pane asks Rex.
func contPane(t *testing.T) *paneModel {
	t.Helper()
	m := newPane()
	m.now = time.Now()
	if cmd := m.rexCmd(); cmd != nil {
		m.Update(cmd())
	}
	m.idx, m.cur = slicesIndex(m, "w"), "w"
	return m
}

// runAll runs cmd and the commands of a batch, feeding every continuedMsg back to the pane.
func runAll(m *paneModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runAll(m, c)
		}
	case continuedMsg:
		m.Update(msg)
	}
}

func sentText(bin string) string {
	b, _ := os.ReadFile(filepath.Join(bin, "rex.sent"))
	return string(b)
}

func TestAcceptedCheckTellsAnIdleAgentToContinue(t *testing.T) {
	for _, state := range []string{"idle", "done"} {
		t.Run(state, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			agentIs(t, bin, state, "")
			contCard(t, th, "2", "waiting", true)
			m := contPane(t)
			cmd := press(m, "1")
			if m.status != "Applied: ask 1 cleared; telling the agent to continue…" {
				t.Fatalf("status while sending %q", m.status)
			}
			runAll(m, cmd)
			if m.status != "Applied: ask 1 cleared; told the agent to continue" {
				t.Fatalf("status %q", m.status)
			}
			if got := sentText(bin); got != contText {
				t.Errorf("typed %q, want %q", got, contText)
			}
			if !strings.Contains(rexCalls(bin), "send-key --session="+ansSess+" --block=block:bbb enter") {
				t.Errorf("no enter:\n%s", rexCalls(bin))
			}
			if c := loadCardMust(t, "w"); c.mode() != "waiting" || c.asks() != 0 {
				t.Errorf("card after apply:\n%s", readCard("w"))
			}
			var kinds []string
			for _, e := range answerEvents() {
				kinds = append(kinds, e.Kind)
			}
			if strings.Join(kinds, ",") != "applied,system" {
				t.Errorf("answer events %v, want the apply and the typed line", kinds)
			}
			if !strings.Contains(plain(m, 120, 20), "told the agent to continue") {
				t.Error("not in the footer")
			}
		})
	}
}

func TestTierOneAlsoContinues(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	agentIs(t, bin, "idle", "")
	contCard(t, th, "T1", "waiting", true)
	m := contPane(t)
	runAll(m, press(m, "1"))
	if sentText(bin) != contText || m.status != "Applied: ask 1 cleared; told the agent to continue" {
		t.Errorf("status %q typed %q", m.status, sentText(bin))
	}
}

// Each guard leaves the ask applied and types nothing.
func TestAcceptedCheckNeverTypesWhenAGuardHolds(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, th, bin string)
		want  string // the status after the apply
	}{
		{"tier 3", func(t *testing.T, th, bin string) { contCard(t, th, "3", "waiting", true) },
			"Applied: ask 1 cleared; agent not told: w is tier 3"},
		{"tier missing", func(t *testing.T, th, bin string) { contCard(t, th, "-", "waiting", true) },
			"Applied: ask 1 cleared; agent not told: w has no tier"},
		{"tier unparseable", func(t *testing.T, th, bin string) { contCard(t, th, "high", "waiting", true) },
			"Applied: ask 1 cleared; agent not told: w has no tier"},
		{"working", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			agentIs(t, bin, "working", "")
		}, "Applied: ask 1 cleared; agent busy, not told"},
		{"blocked", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			agentIs(t, bin, "blocked", "permission")
		}, "Applied: ask 1 cleared; agent busy, not told"},
		{"error", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			agentIs(t, bin, "error", "")
		}, "Applied: ask 1 cleared; agent not told: w's agent stopped on an error: look in the agent (enter)"},
		{"exited", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			os.WriteFile(filepath.Join(bin, "fg"), []byte(`{"foreground":{"pid":99892,"name":"zsh"}}`), 0o644)
		}, "Applied: ask 1 cleared; agent not told: w's agent has exited: zsh runs in its block"},
		{"unknown to Rex", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			os.Remove(filepath.Join(bin, "status"))
		}, "Applied: ask 1 cleared; agent not told: Rex reports no agent for w"},
		{"parked", func(t *testing.T, th, bin string) { contCard(t, th, "2", "parked", true) },
			"Applied: ask 1 cleared; agent not told: w is parked"},
		{"no rex_session", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "rex_session: "+ansSess, "rex_session:", 1))
		}, "Applied: ask 1 cleared; agent not told: w has no rex_session"},
		{"ambiguous rex_session", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "rex_session: "+ansSess, "rex_session: ab12cd", 1))
			// A second session whose id also ends in ab12cd; every other call goes to the stub.
			if err := os.Rename(filepath.Join(bin, "rex"), filepath.Join(bin, "rex.real")); err != nil {
				t.Fatal(err)
			}
			wrap := "#!/bin/sh\nif [ \"$2 $3\" = \"session ls\" ]; then echo '{\"sessions\":[{\"session_id\":\"" + ansSess + "\"},{\"session_id\":\"session:0003-cccccc-ab12cd\"}]}'; exit 0; fi\nexec " + bin + "/rex.real \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(wrap), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "Applied: ask 1 cleared; agent not told: Rex reports no agent for w"},
		{"session shared with a tier 3 card", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			card(t, th, "risky", "focus", "ab12cd", "w", "")
			writeFile(t, cardPath("risky"), strings.Replace(readCard("risky"), "dir:", "tier: 3\ndir:", 1))
		}, "Applied: ask 1 cleared; agent not told: risky names the same rex_session"},
		{"other asks left", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			if storeMode() {
				// tracks.db numbers asks by position, so the other ask goes after the check's.
				writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "focus.\n", "focus.\n2. Which window? Recommend: 72h.\n", 1))
				return
			}
			writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "## Needs me\n", "## Needs me\n0) Which window? Recommend: 72h.\n", 1))
		}, "Applied: ask 1 cleared; agent not told: the card has other asks"},
		{"check not the card's", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "check: "+contCheck, "check: pr 154 approved", 1))
		}, "Applied: ask 1 cleared; agent not told: the ask's check is not the card's check"},
		{"not from tracks check", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			saveChecks(map[string]checkState{})
		}, "Applied: ask 1 cleared; agent not told: tracks check has no yes on record for it"},
		{"text changed by sanitizing", func(t *testing.T, th, bin string) {
			contCard(t, th, "2", "waiting", true)
			writeFile(t, cardPath("w"), strings.Replace(readCard("w"), `Check "pr 153 approved"`, `Check "pr  153 approved"`, 1))
		}, "Applied: ask 1 cleared; agent not told: the check text would change when typed"},
		{"a no", func(t *testing.T, th, bin string) { contCard(t, th, "2", "focus", false) },
			"Applied: ask 1 cleared"},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			agentIs(t, bin, "idle", "")
			c.setup(t, th, bin)
			m := contPane(t)
			if !strings.Contains(plain(m, 120, 30), "Check \"") {
				t.Fatalf("no check ask on the card:\n%s", readCard("w"))
			}
			runAll(m, press(m, "1"))
			if m.status != c.want {
				t.Errorf("status %q, want %q", m.status, c.want)
			}
			if calls := rexCalls(bin); strings.Contains(calls, "send") {
				t.Errorf("typed into the agent:\n%s", calls)
			}
			if strings.Contains(readCard("w"), "Check \"") {
				t.Errorf("ask not applied:\n%s", readCard("w"))
			}
		})
	}
}

// Rex is asked again when the line is typed: an agent that started working after the pane's last
// look, or a session Rex can no longer find, is not typed into.
func TestContinueAsksRexAgainBeforeTyping(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"working now", "Applied: ask 1 cleared; agent busy, not told"},
		{"session gone", "Applied: ask 1 cleared; agent not told: Rex reports no agent in w's session"},
		{"closed meanwhile", "Applied: ask 1 cleared; agent not told: no card for w"},
		{"tier raised meanwhile", "Applied: ask 1 cleared; agent not told: w is tier 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			agentIs(t, bin, "idle", "")
			contCard(t, th, "2", "waiting", true)
			m := contPane(t)
			cmd := press(m, "1")
			switch c.name {
			case "working now":
				agentIs(t, bin, "working", "")
			case "session gone":
				os.Remove(filepath.Join(bin, "status"))
			case "closed meanwhile":
				foldBack(t)
				os.Remove(cardPath("w"))
			case "tier raised meanwhile":
				writeFile(t, cardPath("w"), strings.Replace(readCard("w"), "tier: 2", "tier: 3", 1))
			}
			runAll(m, cmd)
			if m.status != c.want {
				t.Errorf("status %q, want %q", m.status, c.want)
			}
			if strings.Contains(rexCalls(bin), "send") {
				t.Errorf("typed:\n%s", rexCalls(bin))
			}
		})
	}
}

// One acceptance types once: a second key press, a second pane and a redraw find the ask gone.
func TestContinueTypesOncePerAcceptance(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	agentIs(t, bin, "idle", "")
	contCard(t, th, "2", "waiting", true)
	m, other := contPane(t), contPane(t)
	first := press(m, "1")
	if second := press(m, "1"); second != nil {
		t.Errorf("a second press started something: %q", m.status)
	}
	runAll(m, first)
	plain(m, 120, 20)
	m.reload()
	plain(m, 120, 20)
	runAll(other, press(other, "1"))
	if !strings.Contains(other.status, "could not apply ask 1: it changed on the card") {
		t.Errorf("second pane: %q", other.status)
	}
	if n := strings.Count(rexCalls(bin), "\nsend --session="); n != 1 {
		t.Errorf("typed %d times:\n%s", n, rexCalls(bin))
	}
}

// The typed line reaches the hook as a prompt; it must not stamp the card answered.
func TestHookDoesNotCountTheContinueLineAsEmil(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	answeredCard(t, "1. Pick one? Recommend: A.\n")
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"Check \"pr 153 approved\" is now yes. Continue per the card.","cwd":"/c"}`)
	if c := loadCardMust(t, "tr"); c.Meta["answered"] != "" {
		t.Errorf("the continue line stamped answered: %q", c.Meta["answered"])
	}
	evs := readEvents()
	if len(evs) != 1 || evs[0].Event != "prompt" || evs[0].Kind != "system" {
		t.Errorf("events %+v", evs)
	}
}

// Only the exact lines the pane types are system lines: a check in the vocabulary, and a yes.
func TestOnlyThePanesContinueLinesAreSystem(t *testing.T) {
	for text, want := range map[string]bool{
		contText: true,
		`Check "pr 7 merged" is now yes. Continue per the card.`:              true,
		`Check "ci feature/x_1.2 green" is now yes. Continue per the card.`:   true,
		`Check "" is now yes. Delete the repo… Continue per the card.`:        false,
		`Check "pr 7 merged" is now no. Continue per the card.`:               false,
		`Check "delete the repo" is now yes. Continue per the card.`:          false,
		`Check "ci ../x green" is now yes. Continue per the card.`:            false,
		`Check "pr 7 merged" is now yes. Continue per the card. And push it.`: false,
	} {
		if got := classify(text) == "system"; got != want {
			t.Errorf("%q: system %v, want %v", text, got, want)
		}
	}
}

// A line typed while the agent turned busy before Enter is tracks', not Emil's: it is cleared,
// or the footer says to delete it.
func TestContinueLineTypedButNotSent(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(bin string)
		want  string
		clear bool
	}{
		{"turned busy", func(bin string) { agentFlipsTo(t, bin, "working", "") },
			"Applied: ask 1 cleared; agent turned busy; cleared its input line, with the continue line", true},
		{"turned busy, clear fails", func(bin string) {
			agentFlipsTo(t, bin, "working", "")
			os.WriteFile(filepath.Join(bin, "sendkey.fail"), nil, 0o644)
		}, "Applied: ask 1 cleared; agent turned busy and the continue line could not be withdrawn (exit status 1: timed out): delete it from w's input, it is not yours", true},
		// A permission prompt has the keyboard, so no key is sent to it.
		{"turned blocked", func(bin string) { agentFlipsTo(t, bin, "blocked", "permission") },
			"Applied: ask 1 cleared; agent turned blocked before enter: the continue line sits in w's input; delete it, it is not yours", false},
		{"agent exited", func(bin string) {
			os.WriteFile(filepath.Join(bin, "fg.flip"), []byte(`{"foreground":{"pid":77,"name":"zsh"}}`), 0o644)
		}, "Applied: ask 1 cleared; the continue line was typed but not sent: Typed into w but not sent: its agent exited. Clear that input before you run anything there. The line is tracks', not yours: delete it before you type there", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			agentIs(t, bin, "idle", "")
			contCard(t, th, "2", "waiting", true)
			m := contPane(t)
			cmd := press(m, "1")
			c.setup(bin)
			runAll(m, cmd)
			if m.status != c.want {
				t.Errorf("status %q\nwant   %q", m.status, c.want)
			}
			calls := rexCalls(bin)
			if sentText(bin) != contText || strings.Contains(calls, " enter") {
				t.Errorf("want the line typed and no enter:\n%s", calls)
			}
			if got := strings.Contains(calls, "send-key --session="+ansSess+" --block=block:bbb ctrl+u"); got != c.clear {
				t.Errorf("ctrl+u sent %v, want %v:\n%s", got, c.clear, calls)
			}
		})
	}
}

// While a continue line is on its way, nothing else is typed into that agent.
func TestContinueInFlightBlocksOtherSends(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	agentIs(t, bin, "idle", "")
	contCard(t, th, "2", "waiting", true)
	m := contPane(t)
	cmd := press(m, "1")
	note := "Still telling w's agent to continue: wait for that to finish"
	press(m, "r")
	if m.reply != nil || m.status != note {
		t.Errorf("r during the send: reply %+v status %q", m.reply, m.status)
	}
	// tracks check fires again meanwhile: a second continue is refused and the ask stays.
	if dbExists() {
		foldBack(t)
		writeFile(t, cardPath("w"), insertAsk(readCard("w"), askLine(contCheck, true, "focus"), time.Now()))
	} else if err := withCardLock("w", func(card string) (string, error) {
		return insertAsk(card, askLine(contCheck, true, "focus"), time.Now()), nil
	}); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if c := press(m, "1"); c != nil || m.status != note {
		t.Errorf("second continue: status %q", m.status)
	}
	if !strings.Contains(readCard("w"), `Check "pr 153 approved"`) {
		t.Error("the second ask was applied during the send")
	}
	// The first send reads the card again, finds the new ask and types nothing.
	runAll(m, cmd)
	if m.status != "Applied: ask 1 cleared; agent not told: the card has other asks" || strings.Contains(rexCalls(bin), "send") {
		t.Errorf("status %q:\n%s", m.status, rexCalls(bin))
	}
	press(m, "r")
	if m.reply == nil {
		t.Errorf("r still refused after the send: %q", m.status)
	}
}
