package tracks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

// portfolioHome makes the tracks given as name=mode on tracks.db.
func portfolioHome(t *testing.T, tracks ...string) string {
	t.Helper()
	th, _ := setup(t)
	for _, tm := range tracks {
		name, mode, _ := strings.Cut(tm, "=")
		card(t, th, name, mode, "", "w", "")
	}
	importNow(t)
	return th
}

func setCard(t *testing.T, name, in string) string {
	t.Helper()
	out, code := cardIn(in, "card", name)
	if code != 0 {
		t.Fatalf("tracks card %s: exit %d: %s", name, code, out)
	}
	return out
}

func TestCardSetsThePortfolioFieldsAndShowsThemOnTheCardAndInLs(t *testing.T) {
	portfolioHome(t, "a=focus", "b=background", "c=background")
	setCard(t, "a", "rank: 2\ntoday: PR 9 merged and deployed to staging\nblocked_by: b, c\n")
	b, err := os.ReadFile(cardPath("a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rank: 2 ", "today: PR 9 merged and deployed to staging ", "blocked_by: b, c ", "(Emil and the desk set this)"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("card view lacks %q:\n%s", want, b)
		}
	}
	a := lsTrackNamed(t, "a")
	if a["rank"] != float64(2) || a["today"] != "PR 9 merged and deployed to staging" {
		t.Errorf("ls rank %v today %v", a["rank"], a["today"])
	}
	if bl, _ := json.Marshal(a["blocked_by"]); string(bl) != `["b","c"]` {
		t.Errorf("ls blocked_by %s", bl)
	}
	c := lsTrackNamed(t, "c")
	if bl, _ := json.Marshal(c["blocked_by"]); c["rank"] != nil || c["today"] != "" || string(bl) != "[]" {
		t.Errorf("an unset track: rank %v today %q blocked_by %s", c["rank"], c["today"], bl)
	}
	setCard(t, "a", "rank:\ntoday:\nblocked_by:\n")
	a = lsTrackNamed(t, "a")
	if bl, _ := json.Marshal(a["blocked_by"]); a["rank"] != nil || a["today"] != "" || string(bl) != "[]" {
		t.Errorf("after clearing: rank %v today %q blocked_by %s", a["rank"], a["today"], bl)
	}
}

func TestATracksOwnCardWriteAndAModeChangeKeepThePortfolioFields(t *testing.T) {
	portfolioHome(t, "a=focus", "b=background")
	setCard(t, "a", "rank: 1\ntoday: ship it\nblocked_by: b\n")
	setCard(t, "a", "where: halfway there\nasks:\n1. Merge?\n")
	if out, code := do("mode", "a", "background"); code != 0 {
		t.Fatalf("mode: %s", out)
	}
	a := lsTrackNamed(t, "a")
	if bl, _ := json.Marshal(a["blocked_by"]); a["rank"] != float64(1) || a["today"] != "ship it" || string(bl) != `["b"]` {
		t.Errorf("after the track's own write and a mode change: rank %v today %q blocked_by %s", a["rank"], a["today"], bl)
	}
}

func TestCardRefusesABlockedByThatIsNotAnotherOpenTrack(t *testing.T) {
	portfolioHome(t, "a=focus", "b=background", "gone=background")
	if out, code := do("close", "gone"); code != 0 {
		t.Fatalf("close: %s", out)
	}
	setCard(t, "b", "blocked_by: a\n")
	for _, c := range []struct{ in, want string }{
		{"blocked_by: nosuch\n", "no track nosuch"},
		{"blocked_by: gone\n", "gone is closed"},
		{"blocked_by: a\n", "cannot wait on itself"},
		{"blocked_by: b\n", "b already waits on a"},
		{"rank: 0\n", "rank must be a whole number from 1"},
		{"rank: first\n", "rank must be a whole number from 1"},
		{"blocked_by: b; rm\n", "blocked_by takes track names"},
	} {
		out, code := cardIn(c.in, "card", "a")
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%q: exit %d, output %q, want a refusal with %q", c.in, code, out, c.want)
		}
	}
	if a := lsTrackNamed(t, "a"); len(a["blocked_by"].([]any)) != 0 {
		t.Errorf("a refused write changed blocked_by: %v", a["blocked_by"])
	}
}

func TestThePortfolioFieldsNeedTheDatabase(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	noImport(t)
	for _, in := range []string{"rank: 1\n", "today: x\n", "blocked_by: a\n"} {
		out, code := cardIn(in, "card", "a")
		if code == 0 || !strings.Contains(out, "needs tracks.db") {
			t.Errorf("%q on files: exit %d, output %q", in, code, out)
		}
	}
}

func TestBlockedByFollowsARenameOfTheBlocker(t *testing.T) {
	portfolioHome(t, "a=focus", "b=background")
	setCard(t, "a", "blocked_by: b\n")
	if out, code := do("rename", "b", "b2"); code != 0 {
		t.Fatalf("rename: %s", out)
	}
	if bl := lsTrackNamed(t, "a")["blocked_by"].([]any); len(bl) != 1 || bl[0] != "b2" {
		t.Errorf("blocked_by after the rename: %v", bl)
	}
	if b, _ := os.ReadFile(cardPath("a")); !strings.Contains(string(b), "blocked_by: b2 ") {
		t.Errorf("a's view was not rendered again:\n%s", b)
	}
}

type readyOut struct {
	Schema int          `json:"schema"`
	Tracks []readyTrack `json:"tracks"`
}

func readyJSON(t *testing.T) readyOut {
	t.Helper()
	s, code := do("ready", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, s)
	}
	var d readyOut
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, s)
	}
	return d
}

func TestReadyListsWhatCanMoveFocusFirstThenByRank(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "f-late", "focus", "", "w", "")
	card(t, th, "f-first", "focus", "", "w", "1. Merge #9?")
	card(t, th, "bg-free", "background", "", "w", "")
	card(t, th, "bg-top", "background", "", "w", "1. a?\n2. b?")
	card(t, th, "wait", "waiting", "", "w", "")
	card(t, th, "outside", "waiting", "", "w", "")
	card(t, th, "pk", "parked", "", "w", "")
	card(t, th, "held", "background", "", "w", "")
	card(t, th, "freed", "background", "", "w", "")
	card(t, th, "done", "background", "", "w", "")
	importNow(t)
	setCard(t, "f-late", "rank: 2\ntoday: release notes out\n")
	setCard(t, "f-first", "rank: 1\n")
	setCard(t, "bg-top", "rank: 1\n")
	setCard(t, "wait", "check: pr 7 merged\n")
	setCard(t, "outside", "waiting_on: Anna's review\n")
	setCard(t, "held", "rank: 1\nblocked_by: f-late\n")
	setCard(t, "freed", "blocked_by: done\n")
	if out, code := do("close", "done"); code != 0 {
		t.Fatalf("close: %s", out)
	}
	d := readyJSON(t)
	var got []string
	for _, r := range d.Tracks {
		got = append(got, r.Name+"="+r.WaitsOn)
	}
	want := "f-first=emil f-late=nothing bg-top=emil bg-free=nothing freed=nothing outside=waiting wait=check"
	if strings.Join(got, " ") != want {
		t.Errorf("ready:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	for _, r := range d.Tracks {
		switch r.Name {
		case "f-first":
			if r.Waiting != "1 ask: Merge #9?" {
				t.Errorf("f-first waits on %q", r.Waiting)
			}
		case "f-late":
			if r.Today != "release notes out" || r.Rank == nil || *r.Rank != 2 {
				t.Errorf("f-late: today %q rank %v", r.Today, r.Rank)
			}
		case "wait":
			if r.Waiting != "pr 7 merged" {
				t.Errorf("wait waits on %q", r.Waiting)
			}
		}
	}
	text, code := do("ready")
	if code != 0 || !strings.Contains(text, "WAITS ON") || strings.Contains(text, "held") ||
		!strings.Contains(text, "emil: 2 asks") || strings.Index(text, "f-first") > strings.Index(text, "bg-top") {
		t.Errorf("ready table, exit %d:\n%s", code, text)
	}
}

func TestReadyOnFilesReadsTheCardsWithoutPortfolioFields(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "b", "background", "", "w", "")
	card(t, th, "a", "focus", "", "w", "")
	noImport(t)
	var got []string
	for _, r := range readyJSON(t).Tracks {
		got = append(got, r.Name)
	}
	if strings.Join(got, " ") != "a b" {
		t.Errorf("ready on files: %v", got)
	}
}

func TestPaneSortsEachModeByRankAndShowsAFocusTracksToday(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "f-asks", "focus", "", "w", "1. q?")
	card(t, th, "f-one", "focus", "", "w", "")
	card(t, th, "f-two", "focus", "", "w", "")
	card(t, th, "b-none", "background", "", "w", "1. q?")
	card(t, th, "b-one", "background", "", "w", "")
	importNow(t)
	setCard(t, "f-one", "rank: 1\ntoday: ship the refund fix\n")
	setCard(t, "f-two", "rank: 2\ntoday: write the plan\n")
	setCard(t, "b-one", "rank: 1\ntoday: not shown, it is not in focus\n")
	var names []string
	for _, c := range orderedCards(loadCards()) {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, " "); got != "f-one f-two f-asks b-one b-none" {
		t.Errorf("order %s", got)
	}
	m := newPane()
	out := plain(m, 120, 30)
	if !strings.Contains(out, "TODAY") || !strings.Contains(out, "ship the refund fix") {
		t.Errorf("the selected focus track's today line is not in the detail:\n%s", out)
	}
	if !strings.Contains(out, "FOCUS  3, plan says 2") {
		t.Errorf("the focus warning does not read maxFocus:\n%s", out)
	}
	if rows := todayRows(loadCardMust(t, "b-one"), 60); rows != nil {
		t.Errorf("a background track shows today: %q", rows)
	}
}

func TestMigrateReadsThePortfolioFromCardFiles(t *testing.T) {
	th, _ := setup(t)
	writeFile(t, cardPath("a"), "---\nname: a\nmode: focus\nrank: 2   # c\ntoday: ship it\nblocked_by: b\ndir: "+th+"\n---\n## Goal\nG\n\n## Where it is\nw\n")
	card(t, th, "b", "background", "", "w", "")
	importNow(t)
	a := lsTrackNamed(t, "a")
	if bl, _ := json.Marshal(a["blocked_by"]); a["rank"] != float64(2) || a["today"] != "ship it" || string(bl) != `["b"]` {
		t.Errorf("imported: rank %v today %q blocked_by %s", a["rank"], a["today"], bl)
	}
	var text string
	if err := withStore(func(st *store.Store) error {
		v, err := st.Get("a")
		text = store.Render(v.Track, v.Asks)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "rank:") != 1 || strings.Count(text, "blocked_by:") != 1 {
		t.Errorf("a portfolio field is kept twice, as a column and an extra key:\n%s", text)
	}
}
