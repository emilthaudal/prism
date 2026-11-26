package tracks

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const alphaCard = `---
name: alpha
mode: waiting        # focus | background | waiting | parked (Emil sets this)
tier: 2            # 1 | 2 | 3: the riskiest thing this track touches (you set it)
dir: /work/alpha
rex_session: session:alpha
waiting_on: PR 7 approval
check: pr 7 approved   # when waiting
links: EMI-307, https://github.com/o/r/pull/7   # ticket keys
updated: 2026-11-20T10:00:00Z
claude_session: abc-123
answered: 2026-11-20T11:00:00Z
answered_asks: 2026-11-20T11:00:05Z 1
---
## Goal
Ship the store.

## Where it is
Slice 2 in review.
Tier 2: touches live state.

## Needs me
1. Pick a driver.
2. Check "pr 7 approved" is now yes. Recommend: set mode to focus.

## Standing orders
Keep it small.

## Later
Slice 3.

## Notes
kept as an extra section
`

const betaCard = `---
name: beta
mode: focus        # focus | background | waiting | parked (Emil sets this)
tier:              # 1 | 2 | 3: the riskiest thing this track touches (you set it)
dir: /work/beta
rex_session:
waiting_on:        # when mode is waiting: who or what, and how to check it
check:             # when waiting: pr N approved | pr N merged | pr N green | ci BRANCH green
links:             # ticket keys and PR URLs, comma-separated: EMI-273, PAN-213, https://github.com/org/repo/pull/7
updated:
---
## Goal
Try beta.

## Where it is
Not started.

## Needs me

## Standing orders

## Later
`

const finalCard = `---
name: c-final
mode: background
tier: 3
dir: /work/c
rex_session:
updated: 2026-11-19T09:30:00Z
---
## Goal
Renamed twice.

## Where it is
Done soon.

## Needs me
1. Look at this.

## Standing orders

## Later
`

const oldCard = `---
name: old-one
mode: focus
tier: 1
dir: /work/old
updated: 2026-11-10T09:00:00Z
closed: 2026-11-10T17:00:00Z
---
## Goal
Was a goal.

## Outcome
Shipped it.

## Needs me

## Standing orders

## Later
`

const noClosedCard = `---
mode: parked
dir: /work/nc
---
## Goal
No closed line, no name line.

## Outcome
Dropped.
`

type fixtureEvent struct {
	ago   time.Duration
	event string
	track string
	kind  string
}

func writeFixture(t *testing.T, home string) {
	t.Helper()
	now := time.Now().UTC()
	mtime := now.Add(-48 * time.Hour).Truncate(time.Second)
	put := func(rel, text string, at time.Time) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	put("cards/alpha.md", alphaCard, mtime)
	put("cards/beta.md", betaCard, mtime.Add(time.Hour))
	put("cards/c-final.md", finalCard, mtime.Add(2*time.Hour))
	put("cards/archive/2026-11-10-old-one.md", oldCard, mtime.Add(-24*time.Hour))
	put("cards/archive/2026-11-11-nc.md", noClosedCard, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))

	checks := `{
  "alpha: pr 7 approved": {
    "answer": true,
    "asked": "2026-11-20T10:30:00.123456Z",
    "ran_at": "2026-11-21T08:00:00.5Z"
  },
  "beta: pr 9 merged": {
    "answer": false
  }
}
`
	put("checks.json", checks, mtime)

	var log bytes.Buffer
	for _, e := range []fixtureEvent{
		{-30 * time.Hour, "prompt", "a-name", "direction"},
		{-29 * time.Hour, "open", "a-name", ""},
		{-28 * time.Hour, "rename", "b-name", "a-name"},
		{-27 * time.Hour, "prompt", "b-name", "news"},
		{-26 * time.Hour, "rename", "c-final", "b-name"},
		{-25 * time.Hour, "open", "c-final", ""},
		{-24 * time.Hour, "prompt", "alpha", "status"},
		{-23 * time.Hour, "stop", "", ""},
		{-22 * time.Hour, "open", "alpha", ""},
	} {
		line, err := json.Marshal(Event{At: now.Add(e.ago).Truncate(time.Microsecond), Event: e.event, RexSession: "session:x",
			Track: e.track, Cwd: "/work", Len: 3, Kind: e.kind})
		if err != nil {
			t.Fatal(err)
		}
		log.Write(line)
		log.WriteByte('\n')
	}
	put("attention.jsonl", log.String(), mtime)
}

func runTracks(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("TRACKS_HOME", home)
	var out, errw bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errw)
	return out.String(), errw.String(), code
}

// lsStable is ls --json without generated_at.
func lsStable(t *testing.T, home string) string {
	t.Helper()
	out, _, code := runTracks(t, home, "ls", "--json")
	if code != 0 {
		t.Fatalf("ls --json exited %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "generated_at")
	b, _ := json.MarshalIndent(doc, "", " ")
	return string(b)
}

func TestMigrateCheckMatchesFixture(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	out, errw, code := runTracks(t, home, "migrate", "--check")
	if code != 0 {
		t.Fatalf("migrate --check exited %d\nstdout: %s\nstderr: %s", code, out, errw)
	}
	if !strings.Contains(out, "checked 3 live and 2 archived cards, 2 check records, 9 events") || !strings.Contains(out, "no differences") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestMigrateCheckNamesTheCardThatDiffers(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	p := filepath.Join(home, "cards", "alpha.md")
	// An unnumbered line under Needs me is not an ask, so the database cannot carry it.
	edited := strings.Replace(alphaCard, "1. Pick a driver.", "- a loose note\n1. Pick a driver.", 1)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := runTracks(t, home, "migrate", "--check")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "differs: card alpha: section Needs me") {
		t.Errorf("the difference does not name the card and section:\n%s", out)
	}
	if strings.Contains(out, "card beta") || strings.Contains(out, "c-final") {
		t.Errorf("untouched cards are reported:\n%s", out)
	}
}

func TestMigrateCheckNamesABrokenCard(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	if err := os.WriteFile(filepath.Join(home, "cards", "broken.md"), []byte("no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := runTracks(t, home, "migrate", "--check")
	if code != 1 || !strings.Contains(out, "card broken: no frontmatter") {
		t.Errorf("exit %d, want 1 naming broken:\n%s", code, out)
	}
}

type fileStat struct {
	size  int64
	mtime time.Time
	mode  fs.FileMode
}

func snapshotTree(t *testing.T, root string) map[string]fileStat {
	t.Helper()
	snap := map[string]fileStat{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		snap[rel] = fileStat{info.Size(), info.ModTime(), info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestMigrateCheckTouchesNothing(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	before := snapshotTree(t, home)
	if _, _, code := runTracks(t, home, "migrate", "--check"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	after := snapshotTree(t, home)
	if len(before) != len(after) {
		t.Errorf("entries: %d before, %d after", len(before), len(after))
	}
	for p, b := range before {
		a, ok := after[p]
		if !ok {
			t.Errorf("%s is gone", p)
		} else if a != b {
			t.Errorf("%s changed: %+v -> %+v", p, b, a)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s is new", p)
		}
	}
}

func TestExportRefusesWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	before := snapshotTree(t, home)
	_, errw, code := runTracks(t, home, "export")
	if code != 1 || !strings.Contains(errw, "nothing to export") {
		t.Errorf("exit %d, stderr %q", code, errw)
	}
	if _, err := os.Stat(filepath.Join(home, dbFileName)); err == nil {
		t.Error("export created tracks.db")
	}
	if len(snapshotTree(t, home)) != len(before) {
		t.Error("export without a database wrote something")
	}
}

func TestImportThenExportKeepsWhatTheFileCodePrints(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	wantLs := lsStable(t, home)
	wantArchive, _, _ := runTracks(t, home, "archive")
	wantStats, _, _ := runTracks(t, home, "stats", "--days", "30")
	wantLog, _ := os.ReadFile(filepath.Join(home, "attention.jsonl"))
	wantChecks, _ := os.ReadFile(filepath.Join(home, "checks.json"))
	if !strings.Contains(wantArchive, "old-one") || !strings.Contains(wantStats, "Opens") && !strings.Contains(wantStats, "opens") {
		t.Fatalf("fixture prints nothing useful:\n%s\n%s", wantArchive, wantStats)
	}

	copyHome := t.TempDir()
	t.Setenv("TRACKS_HOME", home)
	notes, diffs, err := buildDB(filepath.Join(copyHome, dbFileName), readSource())
	if err != nil || len(diffs) != 0 || len(notes) != 0 {
		t.Fatalf("import: err %v, diffs %v, notes %v", err, diffs, notes)
	}

	out, errw, code := runTracks(t, copyHome, "export")
	if code != 0 {
		t.Fatalf("export exited %d: %s %s", code, out, errw)
	}
	if _, err := os.Stat(filepath.Join(copyHome, dbFileName)); err == nil {
		t.Error("tracks.db is still in place after export")
	}
	if m, _ := filepath.Glob(filepath.Join(copyHome, "tracks.db.rolled-back-*")); len(m) != 1 {
		t.Errorf("rolled-back files: %v", m)
	}

	if got := lsStable(t, copyHome); got != wantLs {
		t.Errorf("ls --json differs after the round trip\nbefore:\n%s\nafter:\n%s", wantLs, got)
	}
	if got, _, _ := runTracks(t, copyHome, "archive"); got != wantArchive {
		t.Errorf("archive differs\nbefore:\n%s\nafter:\n%s", wantArchive, got)
	}
	if got, _, _ := runTracks(t, copyHome, "stats", "--days", "30"); got != wantStats {
		t.Errorf("stats differs\nbefore:\n%s\nafter:\n%s", wantStats, got)
	}
	gotLog, _ := os.ReadFile(filepath.Join(copyHome, "attention.jsonl"))
	if string(gotLog) != string(wantLog) {
		t.Errorf("attention.jsonl differs\nbefore:\n%s\nafter:\n%s", wantLog, gotLog)
	}
	for _, name := range []string{"a-name", "b-name"} {
		if got := renamedTo(readEvents(), name); got != "c-final" {
			t.Errorf("renamedTo(%s) = %s after export, want c-final", name, got)
		}
		if got := renamedAway(name); got != "c-final" {
			t.Errorf("renamedAway(%s) = %q after export, want c-final", name, got)
		}
	}
	gotChecks, _ := os.ReadFile(filepath.Join(copyHome, "checks.json"))
	if string(gotChecks) != string(wantChecks) {
		t.Errorf("checks.json differs (asked and ran_at included)\nbefore:\n%s\nafter:\n%s", wantChecks, gotChecks)
	}
}

func TestLsDiffsFindsADifference(t *testing.T) {
	since, other := "a", "b"
	base := func() []lsTrack {
		return []lsTrack{{Name: "x", Mode: "focus", Links: []lsLink{}, Asks: []lsAsk{{Number: 1, Text: "q", Since: &since}}}}
	}
	if d := lsDiffs(base(), base()); len(d) != 0 {
		t.Errorf("equal lists differ: %v", d)
	}
	changedSince := base()
	changedSince[0].Asks[0].Since = &other
	if d := lsDiffs(base(), changedSince); len(d) != 0 {
		t.Errorf("a different since is reported: %v", d)
	}
	changedMode := base()
	changedMode[0].Mode = "parked"
	if d := lsDiffs(base(), changedMode); len(d) != 1 || !strings.Contains(d[0], "x: mode") {
		t.Errorf("a different mode: %v", d)
	}
	if d := lsDiffs(base(), nil); len(d) != 1 || !strings.Contains(d[0], "x is listed from the files") {
		t.Errorf("a missing track: %v", d)
	}
	if d := lsDiffs(nil, base()); len(d) != 1 || !strings.Contains(d[0], "x is listed from the database") {
		t.Errorf("an extra track: %v", d)
	}
}

// seenCheckHome is one waiting card with a check ask that Emil saw: the hook logs his prompt
// and then rewrites the card, so the card's mtime is later than the event that saw the ask.
func seenCheckHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	asked := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	seenAt := asked.Add(time.Minute)
	card := strings.Replace(betaCard, "## Needs me\n",
		"## Needs me\n1. Check \"pr 9 merged\" is now yes. Recommend: open the track.\n", 1)
	files := map[string]string{
		"cards/beta.md":   card,
		"checks.json":     `{"beta: pr 9 merged": {"answer": true, "asked": "` + asked.Format(time.RFC3339Nano) + `"}}`,
		"attention.jsonl": `{"at":"` + seenAt.Format(time.RFC3339Nano) + `","event":"prompt","rex_session":"s","track":"beta","cwd":"/","len":1,"kind":"direction"}` + "\n",
	}
	for rel, text := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mt := seenAt.Add(time.Second)
	if err := os.Chtimes(filepath.Join(home, "cards/beta.md"), mt, mt); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestImportKeepsASeenCheckAskSeen(t *testing.T) {
	home := seenCheckHome(t)
	if out, _, code := runTracks(t, home, "migrate", "--check"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	path := filepath.Join(t.TempDir(), dbFileName)
	if _, _, err := buildDB(path, readSource()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.Get("beta")
	if err != nil || len(v.Asks) != 1 {
		t.Fatalf("%v %v", v.Asks, err)
	}
	if !v.Asks[0].CreatedAt.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)) || v.Asks[0].SeenAt.IsZero() {
		t.Errorf("ask created %v seen %v: want created at the record's asked, and seen", v.Asks[0].CreatedAt, v.Asks[0].SeenAt)
	}
}

func TestMigrateCheckReportsASeenStateThatDiffers(t *testing.T) {
	home := seenCheckHome(t)
	t.Setenv("TRACKS_HOME", home)
	src := readSource()
	data := src.importData()
	data.Tracks[0].Asks[0].SeenAt = time.Time{}
	st, err := store.Open(filepath.Join(t.TempDir(), dbFileName), store.WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Import(data); err != nil {
		t.Fatal(err)
	}
	views, _ := st.AllTracks()
	d := seenDiffs(src.cards[0], views[0], src.keptFrom)
	if len(d) != 1 || !strings.Contains(d[0], "card beta: ask 1: the file code says seen is true") {
		t.Errorf("seen diffs: %v", d)
	}
}

func seenAskOf(t *testing.T, home string) store.Ask {
	t.Helper()
	t.Setenv("TRACKS_HOME", home)
	path := filepath.Join(t.TempDir(), dbFileName)
	if _, _, err := buildDB(path, readSource()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.Get("beta")
	if err != nil || len(v.Asks) != 1 {
		t.Fatalf("%v %v", v.Asks, err)
	}
	return v.Asks[0]
}

// A record without a readable asked: the old code never counts its ask seen, so the import
// must not fall back to the card's mtime and mark it seen by a later prompt.
func TestImportCheckAskWithoutAskedStaysUnseen(t *testing.T) {
	home := seenCheckHome(t)
	if err := os.WriteFile(filepath.Join(home, "checks.json"), []byte(`{"beta: pr 9 merged": {"answer": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	early := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) // before the prompt
	if err := os.Chtimes(filepath.Join(home, "cards/beta.md"), early, early); err != nil {
		t.Fatal(err)
	}
	if out, _, code := runTracks(t, home, "migrate", "--check"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if a := seenAskOf(t, home); !a.SeenAt.IsZero() || a.CheckID == 0 {
		t.Errorf("ask = %+v, want unseen but still linked to its record, so a flip closes it", a)
	}
}

// Right after a rename the record still sits under the old name until tracks check moves it.
// That legitimate state must not fail the check.
func TestMigrateCheckAcceptsARecordUnderTheOldName(t *testing.T) {
	home := seenCheckHome(t)
	asked := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	checks := `{"old-beta: pr 9 merged": {"answer": true, "asked": "` + asked.Format(time.RFC3339Nano) + `"}}`
	if err := os.WriteFile(filepath.Join(home, "checks.json"), []byte(checks), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(home, "attention.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rename := `{"at":"` + asked.Add(30*time.Second).Format(time.RFC3339Nano) + `","event":"rename","track":"beta","kind":"old-beta"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "attention.jsonl"), append([]byte(rename), log...), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, code := runTracks(t, home, "migrate", "--check"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if a := seenAskOf(t, home); a.SeenAt.IsZero() || a.CheckID == 0 {
		t.Errorf("ask = %+v, want linked to the record and seen", a)
	}
}

// Two renames between check runs leave the record two names back; it must still reach the
// track's current name.
func TestImportFollowsARenameChainForChecks(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	if err := os.WriteFile(filepath.Join(home, "checks.json"), []byte(`{"a-name: pr 1 merged": {"answer": false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runTracks(t, home, "migrate", "--check")
	if strings.Contains(out, "no track has a card") {
		t.Fatalf("the chain was not followed:\n%s", out)
	}
	t.Setenv("TRACKS_HOME", home)
	path := filepath.Join(t.TempDir(), dbFileName)
	if _, _, err := buildDB(path, readSource()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if cs, err := st.Checks("c-final"); err != nil || len(cs) != 1 {
		t.Errorf("checks of c-final: %+v, %v: want the record from a-name", cs, err)
	}
}

const twoNameChecks = `{"b-name: pr 1 merged": {"answer": true, "asked": "2026-11-19T10:00:00Z"}, "c-final: pr 1 merged": {"answer": false}, "ghost: pr 2 merged": {"answer": true}}`

func TestImportKeepsTheCheckUnderTheCurrentName(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	if err := os.WriteFile(filepath.Join(home, "checks.json"), []byte(twoNameChecks), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := runTracks(t, home, "migrate", "--check")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{
		`differs: check "pr 1 merged" of c-final: stored under b-name and c-final, kept the record under c-final`,
		`differs: check "pr 2 merged" of ghost: no track has a card of that name`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	path := filepath.Join(t.TempDir(), dbFileName)
	if _, _, err := buildDB(path, readSource()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cs, err := st.Checks("c-final")
	if err != nil || len(cs) != 1 || cs[0].Answer {
		t.Errorf("checks of c-final: %+v, %v: want one record, answer no", cs, err)
	}
}

func TestImportBuildsTheRenameChainAndLinksAsks(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, home)
	t.Setenv("TRACKS_HOME", home)
	path := filepath.Join(t.TempDir(), dbFileName)
	if _, _, err := buildDB(path, readSource()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, old := range []string{"a-name", "b-name", "C-FINAL"} {
		r, err := st.Get(old)
		if err != nil || r.Track.Name != "c-final" {
			t.Fatalf("Get(%s) = %q, %v: want the track c-final", old, r.Track.Name, err)
		}
	}
	if r, _ := st.Get("a-name"); r.RenamedTo != "c-final" {
		t.Errorf("a-name resolves with RenamedTo %q", r.RenamedTo)
	}
	evs, err := st.Events("c-final")
	if err != nil || len(evs) != 6 {
		t.Errorf("events of c-final: %d, %v, want 6 (two before each rename, the renames and the open)", len(evs), err)
	}
	v, err := st.Get("alpha")
	if err != nil || len(v.Asks) != 2 || v.Asks[0].Kind != "agent" || v.Asks[1].Kind != "check" || v.Asks[1].CheckID == 0 {
		t.Errorf("alpha asks: %+v, %v", v.Asks, err)
	}
	if v.Track.Tier != "2" || v.Track.TierWhy != "touches live state." || strings.Contains(v.Track.Where, "Tier 2") {
		t.Errorf("tier %q why %q where %q", v.Track.Tier, v.Track.TierWhy, v.Track.Where)
	}
	if v.Asks[0].SeenAt.IsZero() {
		t.Error("an ask created before Emil's later open of alpha is not marked seen")
	}
}
