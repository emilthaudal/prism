package tracks

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var at1407 = time.Date(2026, 11, 17, 14, 7, 0, 0, time.Local)

func TestSlugName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"i think we should investigate our scanner routines", "investigate-scanner-routines"},
		{"Can you please fix the flaky CI on shop-api?", "fix-flaky-ci-shop"},
		{"Look into PAN-213 payments refunds and retries", "pan-213-payments-refunds-retries"},
		{"Vi skal kigge på døgnrytme for lageret", "doegnrytme-lageret"},
		{"Emil's ÆØÅ blæksprutte", "emils-aeoeaa-blaeksprutte"},
		{"🚀 ship the 🔥 release notes 🎉", "ship-release-notes"},
		{"研究 scanner 日本", "scanner"},
		{"what does naïve café mean", "naive-cafe-mean"},
		// Links count only for their last meaningful path segment: no scheme, host, query or fragment.
		{"https://claude.dev/blog/how-the-claude-code-team-thinks-about-verification/#not-all-tests-are-created-equal-3-tests-worth-investing-in and see if there are any learnings we can make", "claude-code-verification"},
		{"https://example.com/docs/getting-started.html?utm_source=x#install", "getting-started"},
		{"https://github.com/acme/shop-api/pull/623", "shop-api-pr-623"},
		{"review https://github.com/acme/shop-api/pull/623 please", "review-shop-api-pr-623"},
		{"https://github.com/emilthaudal/prism/issues/12 is still open", "prism-issue-12-open"},
		{"https://example.atlassian.net/browse/PAN-213", "pan-213"},
		{"https://linear.app/example/issue/EMI-273/tracks-baseline-numbers", "emi-273-tracks-baseline-numbers"},
		{"https://news.ycombinator.com/item?id=4242", "news"},
		{"https://example.com/", "example"},
		{"https://github.com/acme/shop-api", "shop-api"},
		{"https://github.com/acme/shop-api/tree/main", "shop-api"},
		{"https://github.com/acme/shop-api/actions/runs/1234567890 is red", "shop-api-red"},
		{"https://github.com/acme/shop-api/commit/3f9a2c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39", "shop-api"},
		{"https://github.com/acme/shop-api/pulls", "shop-api"},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"https://x.com/someone/status/1858123456789012345", "x"},
		{"https://example.atlassian.net/jira/software/projects/PAN/boards/12", "example"},
		{"https://app.datadoghq.eu/logs?query=service%3Ashop-api", "datadoghq"},
		{"https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdEFG/edit", "docs"},
		{"https://docs.google.com/document/d/1Ri4HsxGm-Ckn7WZ0Lb-6tKc3pQ_rS8vYwXzAbCdEfGh/edit", "docs"},
		{"https://docs.google.com/spreadsheets/d/1kE-aB_9xYzQ-wErTy_UiOpAsD-fGhJkLzXcVb-NmQwEr/edit#gid=0", "docs"},
		{"https://drive.google.com/file/d/1aBcD-eFgH_iJkL-mNoP_qRsT-uVwX_yZ01-23456/view", "drive"},
		{"https://docs.google.com/presentation/d/1oKqzVwLzPjYbTyGkEeRtAaQqHhJjNnMmLlKkOoPpIi_/edit", "docs"},
		{"summarise https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdEFG/edit for the payments squad", "summarise-payments-squad"},
		{"http://", "track-1407"},
		{"http:// fix the refund retries", "fix-refund-retries"},
		{"https://example.com/2026/add-feed-2", "add-feed-2"},
		{"https://example.com/decade-2025", "decade-2025"},
		{"https://gitlab.example.com/acme/shop-api", "shop-api"},
		{"https://example.com/docs/3f9a2c1d/release-notes-1ab2", "release-notes-1ab2"},
		{"my own scanner setup", "scanner-setup"},
		{"https://example.atlassian.net/wiki/spaces/ENG/pages/1001/PAN-213+Refund+Design", "pan-213-refund-design"},
		{"https://example.atlassian.net/wiki/spaces/ENG/pages/1001/Refund+Design+2025", "refund-design-2025"},
		{"https://www.notion.so/acme/Refund-flow-redesign-1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d", "refund-flow-redesign"},
		{"https://example.com/Checkout-Redesign-2025", "checkout-redesign-2025"},
		{"https://example.com/docs/Kubernetes-1.31-Upgrade", "kubernetes-1-31-upgrade"},
		{"https://example.com/blog/postgres16replication", "postgres16replication"},
		{"https://app.incident.io/acme/incidents/3f9a2c1d-8e7b-4a5f-9e3d-2c1b0a9f8e7d", "incidents"},
		{"https://github.com/acme/shop-api/wiki/PAN-213-Refund-Runbook", "shop-api-pan-213-refund-runbook"},
		{"https://github.com/acme/shop-api/blob/main/src/handlers/refund.ts", "shop-api-refund"},
		{"https://github.com/acme/shop-api/commits/main", "shop-api"},
		{"read https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"PAN-213 can you check why the refund consumer is stuck", "pan-213-refund-consumer-stuck"},
		{"pan-213 og PAN-214: hvordan virker refusion", "pan-213-pan-214-virker-refusion"},
		{"kan du kigge på hvorfor ordreflowet fejler i test", "ordreflowet-fejler-test"},
		{"see if there are any learnings", "see-learnings"},
		// Pasted temp and screenshot paths, typed ticket keys, and filler from real requests.
		{`/var/folders/sf/y1gz68z166v1slczv95nb72w0000gn/T/TemporaryItems/NSIRD_screencaptureui_8HAVxp/Screenshot\ 2026-11-20\ at\ 12.49.25.png i see these quite often in meridian even though i am connected to the office net where gatekeeper and jira is available (jira is available everywhere)`, "meridian-connected-office-net"},
		{`/var/folders/sf/y1gz68z166v1slczv95nb72w0000gn/T/TemporaryItems/NSIRD_screencaptureui_g3bJVm/Screenshot\ 2026-11-20\ at\ 23.02.23.png i get this when trying to go to a track`, "get-trying-go-track"},
		{"https://app.datadoghq.com/monitors/1001?from_ts=1795210306000&to_ts=1795211506000&link_source=monitor_notif", "monitors-1001"},
		{"linear EMI-316", "emi-316"},
		{"Lets do linear task EMI-306", "emi-306"},
		{"is the linear EMI-306 version of track installed and the one im in here?", "emi-306-version-installed"},
		{`i read something about people having a "local proof" feature for their CI basically meaning if i just ran the CI steps locally`, "local-proof-feature-ci"},
		{"I have some usage left on this week. Lets do a harness and workflow review of my local claude setup", "harness-workflow-review-local"},
		{"we have a lot of tracks improvements in the linear backlog, lets work through them", "tracks-improvements-backlog"},
		{"what is easel mcp? i saw it in the list", "easel-mcp"},
		{"look at ~/Developer/tracks/docs/naming-rules.md", "naming-rules"},
		{"Screenshot at /Users/me/Desktop/Screenshot\\ 2026-11-20.png fix the banner", "fix-banner"},
		// Intensifiers and judgements say how Emil feels, not what the work is.
		{"the naming of tracks is still not very good to be honest, can we improve that", "naming-tracks"},
		{"improve the checkout", "improve-checkout"},
		{"improve the checkout retry flow", "checkout-retry-flow"},
		{"make it better", "make-better"},
		{"read this article www.example.org/2026/11/17/rust-async-pitfalls/, then summarise", "rust-async-pitfalls-summarise"},
		{"supercalifragilisticexpialidocious-and-then-some words", "supercalifragilisticexpialidocio"},
		{"refactor authentication middleware consolidation everywhere now", "refactor-authentication"},
		{"i think we should", "track-1407"},
		{"🚀🔥🎉", "track-1407"},
		{"日本語だけ", "track-1407"},
		{"", "track-1407"},
	} {
		got := slugName(c.in, at1407)
		if got != c.want {
			t.Errorf("slugName(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(got) > maxSlugLen || !validName.MatchString(got) {
			t.Errorf("slugName(%q) = %q breaks the name rules", c.in, got)
		}
	}
	long := strings.Repeat("investigate everything carefully ", 100)
	if got := slugName(long, at1407); got != "investigate-everything-carefully" {
		t.Errorf("long input: %q", got)
	}
}

func TestFreeNameSkipsCardsAndArchives(t *testing.T) {
	th, _ := setup(t)
	if got := freeName("scanner", nil); got != "scanner" {
		t.Errorf("free: %q", got)
	}
	card(t, th, "scanner", "focus", "", "w", "")
	card(t, th, "scanner-2", "focus", "", "w", "")
	if _, _, err := closeTrack("scanner-2", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if got := freeName("scanner", nil); got != "scanner-3" {
		t.Errorf("a card and an archived card hold -1 and -2: %q", got)
	}
	card(t, th, "old", "focus", "", "w", "")
	if _, _, err := closeTrack("old", time.Now().AddDate(0, -2, 0), ""); err != nil {
		t.Fatal(err)
	}
	if got := freeName("old", nil); got != "old-2" {
		t.Errorf("an archive from months ago still holds the name: %q", got)
	}
	// Track "pan"'s second archive is named like track pan-2's, but holds only "pan".
	foldBack(t)
	os.MkdirAll(archiveDir(), 0o755)
	os.WriteFile(filepath.Join(archiveDir(), "2026-11-16-pan.md"), []byte("---\nname: pan\n---\n"), 0o644)
	os.WriteFile(filepath.Join(archiveDir(), "2026-11-17-pan-2.md"), []byte("---\nname: pan\n---\n"), 0o644)
	if got := freeName("pan", nil); got != "pan-2" {
		t.Errorf("pan: %q", got)
	}
}

// startFrom opens the form and types the request.
func startFrom(t *testing.T, m *paneModel, req string) {
	t.Helper()
	press(m, "n")
	if m.form == nil {
		t.Fatal("n did not open the form")
	}
	typeText(m, req)
}

func devDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(os.Getenv("HOME"), "Developer")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFormIsOneLine(t *testing.T) {
	setup(t)
	devDir(t)
	m := newPane()
	press(m, "n")
	out := plain(m, 100, 24)
	for _, s := range []string{"What do you want?", requestHolder, requestNote, "enter start", "esc cancel", "ctrl+u clear"} {
		if !strings.Contains(out, s) {
			t.Errorf("form lacks %q:\n%s", s, out)
		}
	}
	for _, s := range []string{"Directory", "Mode", "Tier", "Name"} {
		if strings.Contains(out, s) {
			t.Errorf("form still asks for %q:\n%s", s, out)
		}
	}
	if cmd := press(m, "enter"); cmd != nil || m.form.err != requestRequired {
		t.Errorf("empty enter: %q", m.form.err)
	}
	if out := plain(m, 100, 24); !strings.Contains(strings.Split(out, "\n")[23], requestRequired) {
		t.Errorf("error not in the footer:\n%s", out)
	}
	typeText(m, "i think we should investigate our scanner routines")
	if m.form.err != "" {
		t.Error("typing did not clear the error")
	}
	if out := plain(m, 100, 24); !strings.Contains(out, "→ investigate-scanner-routines · ~/Developer") {
		t.Errorf("no name preview:\n%s", out)
	}
	press(m, "ctrl+u")
	if m.form.ed.String() != "" || m.form.preview != "" {
		t.Errorf("ctrl+u: %q %q", m.form.ed.String(), m.form.preview)
	}
	m.Update(tea.PasteMsg{Content: "line one\nline \x1b[31mtwo\x07"})
	if m.form.ed.String() != "line one line two" {
		t.Errorf("paste: %q", m.form.ed.String())
	}
	typeText(m, strings.Repeat("x", maxRequest+100))
	if n := len([]rune(m.form.ed.String())); n != maxRequest {
		t.Errorf("limit: %d", n)
	}
	press(m, "esc")
	if m.form != nil {
		t.Error("esc did not cancel")
	}
}

// formBox is the form's box in a plain render: its width and the input rows between the
// prompt's blank line and the blank line above the note.
func formBox(t *testing.T, out string) (width, inputRows int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	top := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "╭") })
	bottom := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "╰") })
	if top < 0 || bottom < top {
		t.Fatalf("no box:\n%s", out)
	}
	box := strings.TrimSpace(lines[top])
	return ansi.StringWidth(box), bottom - top - 1 - 4
}

func TestFormUsesThePaneAndGrows(t *testing.T) {
	setup(t)
	devDir(t)
	m := newPane()
	press(m, "n")
	for _, c := range []struct{ w, h, width int }{{200, 40, formMaxWidth}, {120, 30, formMaxWidth}, {100, 30, 96}, {80, 24, 76}} {
		out := plain(m, c.w, c.h)
		width, rows := formBox(t, out)
		if width != c.width || rows != formMinRows {
			t.Errorf("%dx%d empty: width %d rows %d, want %d and %d:\n%s", c.w, c.h, width, rows, c.width, formMinRows, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > c.w {
				t.Errorf("%dx%d: line past the screen: %q", c.w, c.h, l)
			}
		}
	}
	top := func(out string) int {
		return slices.IndexFunc(strings.Split(out, "\n"), func(l string) bool { return strings.Contains(l, "╭") })
	}
	empty := top(plain(m, 80, 24))
	grown := 0
	for i := range 100 { // 500 characters: well past formMaxRows at 80 columns
		typeText(m, "word ")
		_, rows := formBox(t, plain(m, 80, 24))
		if rows < grown || rows > formMaxRows {
			t.Fatalf("after %d words: %d rows (was %d)", i+1, rows, grown)
		}
		grown = rows
	}
	out := plain(m, 80, 24)
	if _, rows := formBox(t, out); rows != formMaxRows {
		t.Errorf("long text shows %d rows, want %d:\n%s", rows, formMaxRows, out)
	}
	if top(out) != empty {
		t.Errorf("the box moved as it grew: top %d, was %d", top(out), empty)
	}
	if !strings.Contains(out, fmt.Sprintf("500/%d", maxRequest)) || !strings.Contains(out, "enter start") || len(strings.Split(out, "\n")) != 24 {
		t.Errorf("no count or keys, or wrong height:\n%s", out)
	}
	// A 16-row terminal still fits the whole box and the footer.
	small := plain(m, 80, 16)
	if _, rows := formBox(t, small); rows > 16-3-6 || !strings.Contains(small, "enter start") {
		t.Errorf("16 rows:\n%s", small)
	}
}

func TestStartFromASentence(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	dev := devDir(t)
	card(t, th, "investigate-scanner-routines", "focus", "", "w", "")
	m := newPane()
	req := "i think we should investigate our scanner routines"
	startFrom(t, m, req)
	cmd := press(m, "enter")
	if cmd == nil || !m.form.busy {
		t.Fatalf("no start: %q", m.form.err)
	}
	if !strings.Contains(plain(m, 100, 24), "Starting…") {
		t.Error("no progress shown")
	}
	m.Update(cmd())
	name := "investigate-scanner-routines-2"
	if m.form != nil || m.cur != name || m.status != "Started "+name {
		t.Fatalf("after start: form=%v cur=%s status=%q", m.form != nil, m.cur, m.status)
	}
	c := loadCardMust(t, name)
	if c.mode() != "background" || c.tier() != "" || c.Meta["dir"] != dev || c.Sections["Goal"] != req ||
		c.Meta["rex_session"] != "session:new-0001" || c.Sections["Where it is"] != "Not started." {
		t.Errorf("card:\n%s", readCard(name))
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	want := "new\n" + name + "\n--cwd=" + dev + "\n--keep-open\n--interactive\n--json\n--\nenv\nTRACKS_HOME=" + th + "\nclaude\n--name\n" + name + "\n/track " + name + "\n"
	if string(args) != want {
		t.Errorf("rex new argv:\n%s\nwant:\n%s", args, want)
	}
	calls := rexCalls(bin)
	if strings.Contains(calls+string(args), "scanner routines") {
		t.Errorf("the request reached rex:\n%s", calls)
	}
	if !strings.Contains(calls, "do session.select session_id=session:new-0001") || !strings.Contains(calls, "block focus -s session:new-0001 block:n1") {
		t.Errorf("not selected:\n%s", calls)
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "rex.bad")); len(b) > 0 {
		t.Errorf("rex without --autostart=false: %s", b)
	}
}

func TestRequestIsCleanedForTheCard(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	devDir(t)
	m := newPane()
	press(m, "n")
	m.Update(tea.PasteMsg{Content: "## Rewrite\n---\nthe \u202edocs\u200b  please $(touch " + th + "/pwned)\\"})
	m.Update(press(m, "enter")())
	goal := "Rewrite --- the docs please $(touch " + th + "/pwned)"
	name := slugName(goal, at1407)
	if !strings.HasPrefix(name, "rewrite-docs-touch") {
		t.Fatalf("name %q", name)
	}
	c := loadCardMust(t, name)
	if g := c.Sections["Goal"]; g != goal {
		t.Errorf("goal %q", g)
	}
	if c.Meta["name"] != name || c.mode() != "background" || len(c.Meta) < 3 {
		t.Errorf("frontmatter broken:\n%s", readCard(name))
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	if strings.Contains(string(args), "pwned") || strings.Contains(rexCalls(bin), "pwned") {
		t.Errorf("the request reached rex:\n%s", args)
	}
	if _, err := os.Stat(filepath.Join(th, "pwned")); err == nil {
		t.Error("the request ran in a shell")
	}
	for _, c := range []struct{ in, goal string }{
		{"# ## investigate our scanner routines", "investigate our scanner routines"},
		{"  #\t## Goal hijack", "Goal hijack"},
		{"# \u3000## x", "x"},
		{"#\u200b# ## \u00a0#y", "y"},
	} {
		if got := cleanRequest(c.in); got != c.goal {
			t.Errorf("cleanRequest(%q) = %q", c.in, got)
		}
		press(m, "n")
		m.Update(tea.PasteMsg{Content: c.in})
		m.Update(press(m, "enter")())
		if g := loadCardMust(t, m.cur).Sections["Goal"]; g != c.goal {
			t.Errorf("%q: goal on the card %q:\n%s", c.in, g, readCard(m.cur))
		}
	}
}

// selectTrack moves the pane's cursor to the track.
func selectTrack(t *testing.T, m *paneModel, name string) {
	t.Helper()
	for range len(m.cards) + 1 {
		if m.cur == name {
			return
		}
		press(m, "down")
	}
	t.Fatalf("no track %s in %q", name, m.cur)
}

// enterOffer presses enter on the track and feeds the result back, as the pane loop would.
func enterOffer(t *testing.T, m *paneModel, name string) {
	t.Helper()
	selectTrack(t, m, name)
	cmd := press(m, "enter")
	if cmd == nil {
		t.Fatal("enter did nothing")
	}
	m.Update(cmd())
	if out := plain(m, 100, 24); !strings.Contains(out, "No agent for "+name+". Start one?  y start · n not now") {
		t.Fatalf("no offer for %s:\n%s", name, out)
	}
}

func TestStartAgentForCardWithoutOne(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	work := filepath.Join(th, "work")
	os.MkdirAll(work, 0o755)
	card(t, th, "bare", "focus", "", "w", "")
	setMeta("bare", "dir", work)
	card(t, th, "dead", "focus", "session:gone-1", "w", "")
	m := newPane()

	enterOffer(t, m, "bare")
	if cmd := press(m, "n"); cmd != nil || m.offer.name != "" || strings.Contains(rexCalls(bin), "new") {
		t.Fatal("n did not cancel the offer")
	}
	enterOffer(t, m, "bare")
	cmd := press(m, "y")
	if cmd == nil || !strings.Contains(plain(m, 100, 24), "Starting an agent for bare…") {
		t.Fatal("y started nothing")
	}
	m.Update(cmd())
	if m.status != "Started bare" || m.cur != "bare" {
		t.Fatalf("status %q", m.status)
	}
	if c := loadCardMust(t, "bare"); c.Meta["rex_session"] != "session:new-0001" || c.Sections["Where it is"] != "w" {
		t.Errorf("card:\n%s", readCard("bare"))
	}
	args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs"))
	want := "new\nbare\n--cwd=" + work + "\n--keep-open\n--interactive\n--json\n--\nenv\nTRACKS_HOME=" + th + "\nclaude\n--name\nbare\n/track bare\n"
	if string(args) != want {
		t.Errorf("argv:\n%s", args)
	}

	// A card whose session Rex no longer has is offered too, and the dead id is replaced.
	enterOffer(t, m, "dead")
	m.Update(press(m, "y")())
	if c := loadCardMust(t, "dead"); c.Meta["rex_session"] != "session:new-0001" {
		t.Errorf("dead:\n%s", readCard("dead"))
	}

	// A session started elsewhere between the offer and y is not replaced.
	card(t, th, "late", "focus", "", "w", "")
	m.reload()
	enterOffer(t, m, "late")
	setMeta("late", "rex_session", "session:someone-else")
	m.Update(press(m, "y")())
	if !strings.Contains(m.status, "has a session now") || loadCardMust(t, "late").Meta["rex_session"] != "session:someone-else" {
		t.Errorf("status %q", m.status)
	}
}

func TestRetryAfterAFailedStart(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	os.WriteFile(filepath.Join(bin, "new.fail"), nil, 0o644)
	m := newPane()
	startFrom(t, m, "flaky start")
	m.Update(press(m, "enter")())
	if !strings.Contains(m.status, "(card kept) · enter to retry") {
		t.Fatalf("status %q", m.status)
	}
	if out := plain(m, 160, 24); !strings.Contains(out, "(card kept) · enter to retry") {
		t.Errorf("not in the footer:\n%s", out)
	}
	os.Remove(filepath.Join(bin, "new.fail"))
	enterOffer(t, m, "flaky-start")
	m.Update(press(m, "y")())
	if m.status != "Started flaky-start" || loadCardMust(t, "flaky-start").Meta["rex_session"] != "session:new-0001" {
		t.Errorf("retry: %q", m.status)
	}
	if _, err := os.Stat(cardPath("flaky-start-2")); err == nil {
		t.Error("retry made a second card")
	}
}

// A start that timed out after Rex made the session leaves a live session labelled with the
// track's name; enter adopts it instead of offering a second agent for the same card.
func TestEnterAdoptsASessionFromATimedOutStart(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	os.WriteFile(filepath.Join(bin, "new.fail"), nil, 0o644)
	m := newPane()
	startFrom(t, m, "new") // the stub lists session:new-0001 labelled "new"
	m.Update(press(m, "enter")())
	if loadCardMust(t, "new").Meta["rex_session"] != "" {
		t.Fatal("the failed start wrote a session")
	}
	os.Remove(filepath.Join(bin, "new.fail"))
	os.Remove(filepath.Join(bin, "rex.newargs"))
	selectTrack(t, m, "new")
	cmd := press(m, "enter")
	// A form opened while Rex answers keeps its text: the answer is a plain enter result.
	press(m, "n")
	for _, r := range "half typed" {
		press(m, string(r))
	}
	m.Update(cmd())
	if m.form == nil || m.form.ed.String() != "half typed" {
		t.Fatal("the adopt answer closed or changed the form")
	}
	if strings.Contains(m.status, "Start one") || m.offer.name != "" {
		t.Fatalf("offered a second agent: %q", m.status)
	}
	if got := loadCardMust(t, "new").Meta["rex_session"]; got != "session:new-0001" {
		t.Errorf("rex_session %q", got)
	}
	if _, err := os.Stat(filepath.Join(bin, "rex.newargs")); err == nil {
		t.Error("started a second agent")
	}
}

func TestDefaultDir(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	m := newPane()
	startFrom(t, m, "anything at all")
	if cmd := press(m, "enter"); cmd != nil || !strings.Contains(m.form.err, "~/Developer is not a directory") {
		t.Errorf("missing ~/Developer: %q", m.form.err)
	}
	t.Setenv("TRACKS_DEFAULT_DIR", th+"/nope")
	if cmd := press(m, "enter"); cmd != nil || !strings.Contains(m.form.err, "TRACKS_DEFAULT_DIR is not a directory") {
		t.Errorf("bad TRACKS_DEFAULT_DIR: %q", m.form.err)
	}
	t.Setenv("TRACKS_DEFAULT_DIR", th+"/")
	m.Update(press(m, "enter")())
	if c := loadCardMust(t, "anything-all"); c.Meta["dir"] != th {
		t.Errorf("dir %q", c.Meta["dir"])
	}
	if args, _ := os.ReadFile(filepath.Join(bin, "rex.newargs")); !strings.Contains(string(args), "--cwd="+th+"\n") {
		t.Errorf("argv:\n%s", args)
	}
}

func TestAgentArgvWithoutTracksHome(t *testing.T) {
	t.Setenv("TRACKS_HOME", "")
	got := rexNewArgs(trackSpec{name: "x", dir: "/d"})
	if strings.Join(got, "|") != "new|x|--cwd=/d|--keep-open|--interactive|--json|--|claude|--name|x|/track x" {
		t.Errorf("%q", got)
	}
}

func TestCreateKeepsCardWhenRexFails(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	os.WriteFile(filepath.Join(bin, "new.fail"), nil, 0o644)
	m := newPane()
	startFrom(t, m, "Goal here")
	m.Update(press(m, "enter")())
	if m.form != nil || !strings.Contains(m.status, "could not start a Rex session for goal-here (card kept)") || !strings.Contains(m.status, "no server") {
		t.Fatalf("status %q", m.status)
	}
	c, err := loadCard("goal-here")
	if err != nil || c.Meta["rex_session"] != "" || c.Sections["Goal"] != "Goal here" || m.cur != "goal-here" {
		t.Errorf("card %+v %v cur %s", c, err, m.cur)
	}
	if !strings.Contains(plain(m, 100, 20), "could not start") {
		t.Error("failure not in the footer")
	}
}

func TestCreateNeverReplacesACard(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	m := newPane()
	startFrom(t, m, "race")
	cmd := press(m, "enter")
	// Another writer creates the card between the preview and the write.
	card(t, th, "race", "background", "", "theirs", "")
	m.Update(cmd())
	if m.form != nil || m.cur != "race-2" {
		t.Fatalf("form %+v cur %s", m.form, m.cur)
	}
	if c, _ := loadCard("race"); c.Sections["Where it is"] != "theirs" {
		t.Error("replaced the other card")
	}
	if c := loadCardMust(t, "race-2"); c.Sections["Goal"] != "race" {
		t.Errorf("race-2:\n%s", readCard("race-2"))
	}
	matches, _ := filepath.Glob(filepath.Join(th, "cards", ".tmp-*"))
	if len(matches) > 0 {
		t.Errorf("temp files left: %q", matches)
	}
}

func TestCardWriteFailureKeepsTheForm(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // a read-only cards folder fails the write only on files; with tracks.db only the view fails
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	os.MkdirAll(filepath.Join(th, "cards"), 0o755)
	os.Chmod(filepath.Join(th, "cards"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(th, "cards"), 0o755) })
	m := newPane()
	startFrom(t, m, "read only")
	m.Update(press(m, "enter")())
	if m.form == nil || m.form.busy || !strings.HasPrefix(m.form.err, "could not write the card") {
		t.Fatalf("form %+v", m.form)
	}
	if m.form.ed.String() != "read only" {
		t.Error("the request was lost")
	}
	if strings.Contains(rexCalls(bin), "new") {
		t.Error("started a session without a card")
	}
}

func TestFloatClosesAfterCreate(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	t.Setenv("TRACKS_DEFAULT_DIR", th)
	m := newPane()
	m.float = true
	startFrom(t, m, "G")
	press(m, "esc")
	if m.form != nil {
		t.Fatal("esc in the form should close the form, not the float")
	}
	startFrom(t, m, "G")
	_, cmd := m.Update(press(m, "enter")())
	if cmd == nil {
		t.Fatal("no quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("float stayed open after create")
	}
}

func TestFormFitsSmallScreens(t *testing.T) {
	setup(t)
	devDir(t)
	m := newPane()
	press(m, "n")
	for _, text := range []string{"", strings.Repeat("a long request ", 40)} {
		typeText(m, text)
		for _, w := range []int{24, 40, 60, 100} {
			out := plain(m, w, 24)
			for _, l := range strings.Split(out, "\n") {
				if len([]rune(l)) > w {
					t.Errorf("width %d: line over width: %q", w, l)
				}
			}
			if len(strings.Split(out, "\n")) != 24 {
				t.Errorf("width %d: height", w)
			}
		}
	}
}
