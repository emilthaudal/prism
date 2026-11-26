package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestPaneAudit renders every pane state to $TRACKS_AUDIT/<state>.ans with colour, for the
// design audit and before/after captures. It is skipped unless TRACKS_AUDIT is set.
func TestPaneAudit(t *testing.T) {
	out := os.Getenv("TRACKS_AUDIT")
	if out == "" {
		t.Skip("set TRACKS_AUDIT=dir to write captures")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	type scene struct {
		name   string
		w, h   int
		cards  map[string]string
		states map[string]agentState
		sel    int
		prep   func(m *paneModel)
	}
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	many := map[string]string{
		"pan-213-refund-retries": auditCard("pan-213-refund-retries", "focus", "2", "s-a", "PAN-213, https://github.com/acme/shop-api/pull/42", "",
			"Refunds retry on carrier failure for up to 72h, shipped behind a flag in shop-api.",
			"PR 42 is open and waits on review; next I fix its comments.\nCI is green on the head commit; staging e2e passed.",
			"1. Ship refunds behind the flag first? Recommend: yes.\n2. Retry window 24h or 72h? Recommend: 72h, since the carrier retries for two days.\n"),
		"gatekeeper-guided-reviews": auditCard("gatekeeper-guided-reviews", "focus", "3", "s-b", "INC-412", "",
			"Gatekeeper walks a reviewer through a deploy's risky diffs before the approve button unlocks.",
			"Implementer is on step 2 of 4: the diff ranking.", ""),
		"metrics-meeting-prep": auditCard("metrics-meeting-prep", "focus", "1", "s-c", "OPS-1380", "",
			"A one-page brief for Thursday's Metrics trial review.",
			"Brief drafted; waiting on your read before I send it.", "1. Send the brief to the channel now, or after Thursday's standup? Recommend: now.\n"),
		"hammi-streak-reset": auditCard("hammi-streak-reset", "background", "2", "s-d", "", "",
			"Streaks reset at local midnight, not UTC.",
			"Fix merged and deployed; watching for a day of streak events.", ""),
		"drivetrain-chain-wear": auditCard("drivetrain-chain-wear", "background", "", "s-e", "", "",
			"Chain wear reminders use ride distance, not days.",
			"Agent hit an error running the migration locally.", ""),
		"meridian-calendar-sync": auditCard("meridian-calendar-sync", "background", "2", "", "", "",
			"Meridian shows today's calendar beside the queue.", "Not started.", ""),
		"pan-88-pan-89": auditCard("pan-88-pan-89", "waiting", "1", "s-f", "PAN-88, PAN-89", "Robin to review D3 in the PAN-92 canvas",
			"Both Pantry decision logs finalised.", "D3 waits on Robin's canvas review.", ""),
		"shop-api-pr-51": auditCard("shop-api-pr-51", "waiting", "2", "s-g", "https://github.com/acme/shop-api/pull/51", "PR 51 review from the checkout team",
			"PR 51 merged.", "Waiting for approval.", "1. Check \"pr 51 approved\" is now yes. Recommend: merge it.\n"),
		"tars-paused": auditCard("tars-paused", "parked", "", "", "", "",
			"TARS stays paused until tracks settles.", "Parked.", ""),
	}
	manyStates := map[string]agentState{
		"s-a": {State: "idle", At: ago(25 * time.Minute)},
		"s-b": {State: "working", Msg: "Running go test ./..."},
		"s-c": {State: "blocked", Kind: "question", Msg: "Which channel should the brief go to?"},
		"s-d": {State: "done", Msg: "Watching"},
		"s-e": {State: "error", Msg: "migration failed: relation \"rides\" does not exist"},
		"s-f": {State: "idle", At: ago(3 * time.Hour)},
		"s-g": {State: "idle", At: ago(2 * 24 * time.Hour)},
	}
	worst := worstCards()
	longName := "a-very-long-track-name-that-goes-on-and-on-and-on-and-on-and-on-and-on-and-on-and-on"

	many["shop-api-pr-51"] = strings.Replace(many["shop-api-pr-51"], "\nlinks:", "\ncheck: pr 51 approved\nlinks:", 1)

	scenes := []scene{
		{name: "01-empty", w: 140, h: 40},
		{name: "02-empty-80x24", w: 80, h: 24},
		{name: "03-one-not-started", w: 140, h: 40, cards: map[string]string{
			"meridian-calendar-sync": many["meridian-calendar-sync"]}},
		{name: "04-many", w: 160, h: 45, cards: many, states: manyStates},
		{name: "05-many-80x24", w: 80, h: 24, cards: many, states: manyStates},
		{name: "06-many-100x30", w: 100, h: 30, cards: many, states: manyStates},
		{name: "07-blocked-question", w: 160, h: 45, cards: many, states: manyStates, sel: 1},
		{name: "08-working", w: 160, h: 45, cards: many, states: manyStates, sel: 2},
		{name: "09-error", w: 160, h: 45, cards: many, states: manyStates, sel: 3},
		{name: "10-waiting-check", w: 160, h: 45, cards: many, states: manyStates, sel: 6},
		{name: "11-parked", w: 160, h: 45, cards: many, states: manyStates, sel: 8},
		{name: "12-long-name", w: 140, h: 40, cards: map[string]string{longName: worst[longName], "pan-213-refund-retries": many["pan-213-refund-retries"]}, states: manyStates, sel: 1},
		{name: "13-nine-asks", w: 140, h: 40, cards: map[string]string{"nine-asks": worst["nine-asks"]}},
		{name: "14-nine-asks-80x24", w: 80, h: 24, cards: map[string]string{"nine-asks": worst["nine-asks"]}},
		{name: "15-long-url-ask", w: 140, h: 40, cards: map[string]string{"long-url": worst["long-url"]}},
		{name: "16-unicode", w: 140, h: 40, cards: map[string]string{"unicode": worst["unicode"]}},
		{name: "17-no-recommend", w: 140, h: 40, cards: map[string]string{"no-recommend": worst["no-recommend"]}},
		{name: "18-empty-card", w: 140, h: 40, cards: map[string]string{"empty": worst["empty"]}},
		{name: "19-footer-error", w: 160, h: 45, cards: many, states: manyStates, prep: func(m *paneModel) {
			m.status = "could not set mode: open /Users/me/.local/state/tracks/cards/pan-213-refund-retries.md: permission denied"
		}},
		{name: "20-help", w: 160, h: 45, cards: many, states: manyStates, prep: func(m *paneModel) {
			m.Update(noteMsg("could not set mode: open /Users/me/.local/state/tracks/cards/pan-213-refund-retries.md: permission denied"))
			m.lastErrAt = time.Now().Add(-4*time.Minute - 5*time.Second)
			m.status = ""
			key(m, "?")
		}},
		{name: "21-help-80x24", w: 80, h: 24, cards: many, states: manyStates, prep: func(m *paneModel) { key(m, "?") }},
		{name: "22-new-track", w: 160, h: 45, cards: many, states: manyStates, prep: func(m *paneModel) {
			os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Developer"), 0o755) // the default dir the form previews
			key(m, "n")
			for _, r := range "Fix the refund retry window in shop-api, PAN-213" {
				m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
			}
		}},
		{name: "23-reply", w: 160, h: 45, cards: many, states: manyStates, prep: func(m *paneModel) { key(m, "1") }},
		{name: "24-reply-80x24", w: 80, h: 24, cards: many, states: manyStates, prep: func(m *paneModel) { key(m, "r") }},
		{name: "25-close-confirm", w: 160, h: 45, cards: many, states: manyStates, sel: 1, prep: func(m *paneModel) { key(m, "x") }},
		{name: "26-over-focus", w: 160, h: 45, cards: withExtraFocus(many), states: manyStates},
		{name: "27-narrow-60x20", w: 60, h: 20, cards: many, states: manyStates},
		{name: "30-many-120x30", w: 120, h: 30, cards: many, states: manyStates},
		{name: "31-question-120x30", w: 120, h: 30, cards: many, states: manyStates, sel: 1},
		{name: "32-waiting-120x30", w: 120, h: 30, cards: many, states: manyStates, sel: 6, prep: func(m *paneModel) {
			saveChecks(map[string]checkState{"shop-api-pr-51: pr 51 approved": {Answer: true, RanAt: time.Now().Add(-4*time.Minute - 5*time.Second)}})
			m.reload()
		}},
	}
	leads := leadCards(many)
	leadStates := map[string]agentState{
		"s-l":  {State: "idle", At: ago(6 * time.Minute)},
		"s-ka": {State: "working", Msg: "Running go test ./..."},
		"s-kb": {State: "done", Msg: "PR 17 is open"},
		"s-kc": {State: "blocked", Kind: "permission", Msg: "Allow docker compose up?"},
		"s-c":  manyStates["s-c"],
		"s-d":  manyStates["s-d"],
		"s-a":  manyStates["s-a"],
	}
	unfold := func(m *paneModel) { m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) }
	scenes = append(scenes,
		scene{name: "33-lead-folded", w: 140, h: 40, cards: leads, states: leadStates, sel: 2},
		scene{name: "34-lead-unfolded", w: 140, h: 40, cards: leads, states: leadStates, sel: 2, prep: unfold},
		scene{name: "35-lead-thread-selected", w: 140, h: 40, cards: leads, states: leadStates, sel: 2, prep: func(m *paneModel) { unfold(m); key(m, "down") }},
		scene{name: "36-lead-close-confirm", w: 140, h: 40, cards: leads, states: leadStates, sel: 2, prep: func(m *paneModel) { key(m, "x") }},
		scene{name: "37-lead-unfolded-80x24", w: 80, h: 24, cards: leads, states: leadStates, sel: 2, prep: unfold},
		scene{name: "38-lead-folded-80x24", w: 80, h: 24, cards: leads, states: leadStates, sel: 2},
		scene{name: "39-lead-close-confirm-80x24", w: 80, h: 24, cards: leads, states: leadStates, sel: 2, prep: func(m *paneModel) { key(m, "x") }},
	)
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			th, _ := setup(t)
			for n, body := range s.cards {
				putCard(t, th, n, body)
			}
			m := newPane()
			m.now = time.Now()
			for k, v := range s.states {
				m.rex.states[k] = v
			}
			m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
			for range s.sel {
				key(m, "down")
			}
			if c := m.selected(); c != nil {
				m.Update(sinceMsg{name: c.Name, err: errNotGit.Error(), at: m.now})
			}
			if s.prep != nil {
				s.prep(m)
			}
			m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
			if err := os.WriteFile(filepath.Join(out, s.name+".ans"), []byte(m.render()), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func auditCard(name, mode, tier, sess, links, waiting, goal, where, needs string) string {
	return "---\nname: " + name + "\nmode: " + mode + "\ntier: " + tier + "\ndir: %DIR%\nrex_session: " + sess +
		"\nwaiting_on: " + waiting + "\nlinks: " + links + "\n---\n## Goal\n" + goal + "\n\n## Where it is\n" + where +
		"\n\n## Needs me\n" + needs + "\n## Standing orders\n- Commit, push branches and open PRs without asking.\n"
}

// leadCards is a lead with three threads among a few ordinary tracks: one thread working, one
// done and one blocked on a permission that has asks of its own.
func leadCards(many map[string]string) map[string]string {
	asRole := func(card, line string) string { return strings.Replace(card, "\nmode:", "\n"+line+"\nmode:", 1) }
	out := map[string]string{
		"pan-213-refund-retries": many["pan-213-refund-retries"],
		"metrics-meeting-prep":   many["metrics-meeting-prep"],
		"hammi-streak-reset":     many["hammi-streak-reset"],
		"tars-paused":            many["tars-paused"],
		"refund-lead": asRole(auditCard("refund-lead", "focus", "3", "s-l", "PAN-300", "",
			"Refunds retry on carrier failure and the customer is told, across shop-api and the notifier.",
			"Two of three parts are running; the retry part is checked and merged.",
			"1. Merge the notifier PR to staging now? Recommend: yes, e2e passed.\n"), "role: lead"),
		"refund-retry-window": asRole(auditCard("refund-retry-window", "focus", "3", "s-ka", "", "",
			"Retry window is 72h in shop-api.", "Writing the retry window and its tests.", ""), "parent: refund-lead"),
		"refund-notify-customer": asRole(auditCard("refund-notify-customer", "focus", "3", "s-kb", "", "",
			"The notifier sends a mail when a refund is retried.", "Done and pushed; PR 17 is open.", ""), "parent: refund-lead"),
		"refund-carrier-fixture": asRole(auditCard("refund-carrier-fixture", "focus", "3", "s-kc", "", "",
			"A carrier fixture for the e2e run.", "Blocked on a permission.",
			"1. Allow the thread to run docker compose?\n2. Which carrier sandbox account?\n"), "parent: refund-lead"),
	}
	return out
}

func withExtraFocus(cards map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cards {
		out[k] = v
	}
	out["hammi-streak-reset"] = strings.Replace(cards["hammi-streak-reset"], "mode: background", "mode: focus", 1)
	return out
}
