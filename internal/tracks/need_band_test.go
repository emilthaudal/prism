package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

var bandBase = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func bandCard(name, mode, sess, extra string, ago time.Duration, needs string) string {
	return "---\nname: " + name + "\nmode: " + mode + "\ndir: %DIR%\nrex_session: " + sess + "\nupdated: " +
		bandBase.Add(-ago).Format(time.RFC3339) + "\n" + extra + "---\n## Goal\nG\n\n## Where it is\nw\n\n## Needs me\n" + needs + "\n"
}

func bandFixture() (map[string]string, map[string]agentState) {
	idle := agentState{State: "idle", At: bandBase.Add(-time.Hour)}
	cards := map[string]string{
		"orders-api": bandCard("orders-api", "focus", "s1", "", time.Hour, ""),
		"billing":    bandCard("billing", "focus", "s2", "", time.Hour, ""),
		"pantry-logs": bandCard("pantry-logs", "focus", "s3", "", 26*time.Hour,
			"1. D7 agent login: record C, Entra SSO built in PAN-141, as the decision? Recommend: yes, since Robin agreed in the review.\n"+
				"2. D3 Returns context: you put A with no name. Record it as yours, or wait for the PAN-92 canvas review? Recommend: wait for Robin; his decision, and it needs an ADR.\n"),
		"deploy-svc": bandCard("deploy-svc", "background", "s4", "", 3*24*time.Hour,
			"1. Should the two deploy logs share one ADR template or keep their own, given that the integration log already has eight decided entries and the platform log has three open ones?\n"),
		"search-tuning": bandCard("search-tuning", "focus", "s5", "", time.Hour, "1. Which index?\n2. Which analyzer?\n"),
		"docs-sync":     bandCard("docs-sync", "background", "s6", "", time.Hour, "1. Publish?\n"),
		"old-spike":     bandCard("old-spike", "parked", "s7", "", time.Hour, "1. Still wanted? Recommend: no\n"),
		"shipped":       bandCard("shipped", "focus", "s8", "", time.Hour, ""),
		"boss":          bandCard("boss", "focus", "s9", "role: lead\n", time.Hour, ""),
		"boss-t1":       bandCard("boss-t1", "focus", "s10", "parent: boss\n", time.Hour, "1. A thread's ask is for its lead? Recommend: yes\n"),
		"boss-t2":       bandCard("boss-t2", "focus", "s11", "parent: boss\n", time.Hour, "1. Another thread ask? Recommend: yes\n"),
	}
	states := map[string]agentState{
		"s1": {State: "blocked", Kind: "permission", At: bandBase.Add(-12 * time.Minute)},
		"s2": {State: "error", At: bandBase.Add(-time.Hour)},
		"s3": idle, "s4": idle, "s7": idle, "s9": idle, "s11": idle,
		"s5":  {State: "working"},
		"s6":  {State: "working"},
		"s8":  {State: "done", At: bandBase.Add(-time.Hour)},
		"s10": {State: "blocked", Kind: "question", At: bandBase.Add(-5 * time.Minute)},
	}
	return cards, states
}

func bandPane(t *testing.T, w, h int) *paneModel {
	t.Helper()
	cards, states := bandFixture()
	m := listPane(t, cards, states, w, h)
	m.now = bandBase
	return m
}

// bandOf is the band's lines of a plain render: from under the header to its closing rule.
func bandOf(out string) []string {
	var band []string
	for _, l := range strings.Split(out, "\n")[1:] {
		band = append(band, strings.TrimRight(l, " "))
		if strings.HasPrefix(l, "─") {
			break
		}
	}
	return band
}

func TestNeedBandRowsOrderAndExclusions(t *testing.T) {
	m := bandPane(t, 170, 40)
	band := bandOf(ansi.Strip(m.render()))
	if len(band) != 9 || !strings.HasPrefix(band[0], " NEEDS YOU 6") {
		t.Fatalf("band is %d lines, want a heading with 6 decisions, 6 rows and a rule:\n%s", len(band), strings.Join(band, "\n"))
	}
	for i, want := range []struct{ name, text string }{
		{"orders-api", "permission"},
		{"boss-t1", "question"},
		{"billing", "error"},
		{"pantry-logs", "D7 agent login"},
		{"", "D3 Returns context"},
		{"deploy-svc", "Should the two deploy logs"},
	} {
		row := band[i+1]
		if !strings.Contains(row, want.text) || want.name != "" && !strings.Contains(row, want.name) {
			t.Errorf("row %d is %q, want %q and %q", i, row, want.name, want.text)
		}
	}
	joined := strings.Join(band, "\n")
	for _, gone := range []string{"old-spike", "shipped", "boss-t2", "A thread's ask", "search-tuning", "docs-sync", "boss "} {
		if strings.Contains(strings.Join(band[:7], "\n"), gone) {
			t.Errorf("%q does not need him but the band shows it:\n%s", gone, joined)
		}
	}
	if strings.Count(joined, "pantry-logs") != 1 {
		t.Errorf("a track's name belongs on its first row only:\n%s", joined)
	}
	if !strings.Contains(band[4], "→ yes") || strings.Contains(band[4], "since") || strings.Contains(band[4], "Recommend") {
		t.Errorf("the recommendation is the pick, cut where its reasons start: %q", band[4])
	}
	if !strings.Contains(band[5], "→ wait for Robin") || strings.Contains(band[5], "ADR") {
		t.Errorf("the recommendation is cut at its first ';': %q", band[5])
	}
	if strings.Contains(band[6], "→") {
		t.Errorf("a question without a recommendation shows an arrow: %q", band[6])
	}
	for i, age := range []string{"12m", "5m", "1h", "26h", "26h", "3d"} {
		if !strings.HasSuffix(band[i+1], age) {
			t.Errorf("row %d should end with its age %s: %q", i, age, band[i+1])
		}
	}
	if _, needs := m.counts(); needs != 5 {
		t.Errorf("counts() is %d tracks, want 5", needs)
	}
	if h := ansi.Strip(m.header()); !strings.Contains(h, "◆ 6 for you") {
		t.Errorf("the header counts the band's 6 rows: %q", h)
	}
	if !strings.Contains(joined, "not yours yet") {
		t.Errorf("no not-yours-yet line: %s", joined)
	}
}

func TestNeedBandNotYoursYetLine(t *testing.T) {
	m := bandPane(t, 170, 40)
	var line string
	for _, l := range strings.Split(ansi.Strip(m.render()), "\n") {
		if strings.Contains(l, "not yours yet") {
			line = strings.TrimSpace(l)
		}
	}
	if line != "not yours yet  docs-sync ◆1 · search-tuning ◆2" && line != "not yours yet  search-tuning ◆2 · docs-sync ◆1" {
		t.Errorf("not-yours-yet line %q", line)
	}
	if strings.Contains(line, "old-spike") || strings.Contains(line, "boss") {
		t.Errorf("parked and thread asks are not listed: %q", line)
	}
	if !strings.Contains(m.render(), dimSt.Render("not yours yet")[:3]) {
		t.Error("the line is not dim")
	}
}

func TestNeedBandKeepsSelectionMarkerOnTheSelectedTrack(t *testing.T) {
	m := bandPane(t, 170, 40)
	for i, c := range m.cards {
		if c.Name == "pantry-logs" {
			m.idx, m.cur = i, c.Name
		}
	}
	for _, l := range bandOf(ansi.Strip(m.render())) {
		isPantry := strings.Contains(l, "D7 agent login") || strings.Contains(l, "D3 Returns")
		if storeMode() { // only the selected question's row carries the marker
			isPantry = strings.Contains(l, "D7 agent login")
		}
		if got := strings.HasPrefix(l, "▌"); got != isPantry {
			t.Errorf("marker %v on %q, want %v", got, l, isPantry)
		}
	}
}

func TestListAskColumnIsDimBecauseTheBandCarriesTheColour(t *testing.T) {
	m := bandPane(t, 170, 40)
	raw := m.render()
	if !strings.Contains(raw, dimSt.Render(" ◆2")) || strings.Contains(raw, warnSt.Bold(true).Render(" ◆2")) {
		t.Errorf("the list's ◆N is not dim")
	}
}

func TestNeedBandEmptyAndOverflow(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "a", bandCard("a", "focus", "s", "", time.Hour, ""))
	m := newPane()
	m.now = bandBase
	lines := strings.Split(plain(m, 100, 20), "\n")
	if strings.TrimSpace(lines[1]) != nothingNeedsYou || !strings.HasPrefix(lines[2], "─") || !strings.Contains(lines[3], "FOCUS") {
		t.Errorf("empty band:\n%s", strings.Join(lines[:5], "\n"))
	}

	m = bandPane(t, 100, 17) // a third of 14 body lines, less heading, rule and "not yours yet": one row and the note
	band := bandOf(ansi.Strip(m.render()))
	if len(band) != 5 || strings.TrimSpace(band[2]) != "+5 more · tab walks them" {
		t.Errorf("overflow:\n%s", strings.Join(band, "\n"))
	}
	m = bandPane(t, 100, 12) // too short for a band: the list keeps its room
	if out := ansi.Strip(m.render()); strings.Contains(out, "NEEDS YOU") {
		t.Errorf("band on a short pane:\n%s", out)
	}
}

func TestNeedBandHiddenInHelpLogAndForm(t *testing.T) {
	m := bandPane(t, 120, 40)
	m.help = true
	if out := ansi.Strip(m.render()); strings.Contains(out, "NEEDS YOU") {
		t.Errorf("band over help:\n%s", out)
	}
	m.help = false
	key(m, "n")
	if out := ansi.Strip(m.render()); strings.Contains(out, "NEEDS YOU") {
		t.Errorf("band over the new-track form:\n%s", out)
	}
}

// A row gives up the age first, then cuts the question down to bandQuestKeep, then the recommendation.
func TestNeedBandNarrowingOrder(t *testing.T) {
	m := bandPane(t, 200, 40)
	row := bandRow{card: m.cards[0], name: "alpha", ask: true, n: 1, age: "3d",
		quest: strings.Repeat("q", 50), rec: "wait for D2 then merge"}
	// prefix 11, question 50, gap 2, "→ " and 22, age 5
	for _, c := range []struct {
		w                          int
		age, fullRec, fullQuestion bool
	}{
		{92, true, true, true},
		{91, false, true, true},
		{87, false, true, true},
		{86, false, true, false},
		{65, false, true, false}, // the question at bandQuestKeep
		{64, false, false, false},
	} {
		got := ansi.Strip(m.bandLines([]bandRow{row}, c.w)[0])
		if len(got) == 0 || ansi.StringWidth(got) != c.w {
			t.Errorf("w=%d: row is %d wide: %q", c.w, ansi.StringWidth(got), got)
		}
		if has := strings.HasSuffix(strings.TrimRight(got, " "), "3d"); has != c.age {
			t.Errorf("w=%d: age shown %v, want %v: %q", c.w, has, c.age, got)
		}
		if has := strings.Contains(got, "→ wait for D2 then merge"); has != c.fullRec {
			t.Errorf("w=%d: full recommendation %v, want %v: %q", c.w, has, c.fullRec, got)
		}
		if has := strings.Contains(got, strings.Repeat("q", 50)); has != c.fullQuestion {
			t.Errorf("w=%d: full question %v, want %v: %q", c.w, has, c.fullQuestion, got)
		}
		if !c.fullQuestion && !strings.Contains(got, "q…") || c.fullQuestion && !c.fullRec && !strings.Contains(got, "…") {
			t.Errorf("w=%d: a cut needs its ellipsis: %q", c.w, got)
		}
	}
}

func TestShortRec(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"yes.", 32, "yes"},
		{"no; wait for D2, since it moves the table", 32, "no"},
		{"wait for Robin, since his call", 32, "wait for Robin, since his call"},
		{"approve the merge because CI is green", 20, "approve the merge"},
		{"a much longer pick with no reasons at all in it", 12, "a much…"},
	} {
		if got := shortRec(c.in, c.max); got != c.want {
			t.Errorf("shortRec(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// storeSuffix names the goldens of a run on tracks.db, whose band reads questions and shows D ids.
func storeSuffix() string {
	if storeMode() {
		return "_store"
	}
	return ""
}

func TestNeedBandGolden(t *testing.T) {
	for _, w := range []int{170, 80} {
		m := bandPane(t, w, 40)
		got := strings.Join(bandOf(ansi.Strip(m.render())), "\n") + "\n"
		for _, l := range strings.Split(got, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("%d columns: band line is %d wide: %q", w, ansi.StringWidth(l), l)
			}
		}
		golden := filepath.Join("testdata", "need_band_"+map[int]string{170: "170", 80: "80"}[w]+storeSuffix()+".golden")
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != got {
			t.Errorf("band at %d differs from %s (go test -run NeedBandGolden -update):\n%s", w, golden, got)
		}
	}
}
