package tracks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type lsOut struct {
	Schema      int              `json:"schema"`
	GeneratedAt string           `json:"generated_at"`
	Tracks      []map[string]any `json:"tracks"`
}

func lsFixture(t *testing.T) string {
	t.Helper()
	th, _ := setup(t)
	write := func(path, text string) {
		foldBack(t)
		if err := writeAtomic(path, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	write(cardPath("alpha"), "---\nname: alpha\nmode: focus   # c\ntier: T2\ndir: "+th+"\nrex_session: s1\nwaiting_on:\ncheck: pr 7 merged\nlinks: EMI-289, https://github.com/o/r/pull/7, junk words\nupdated: 2026-11-19T10:00:00Z\n---\n## Goal\nShip ls\n\n## Where it is\n\nParser done.\nSecond line.\n\n## Needs me\n1. Pick a schema\n2) Check \"pr 7 merged\" is now yes. Recommend: set mode to focus.\n")
	write(cardPath("beta"), "---\nname: beta\nmode: background\ntier: x\ndir: "+th+"\nupdated: 2026-11-18\n---\n## Goal\nQuiet\n\n## Where it is\nWaiting.\n\n## Later\nstuff\n")
	write(cardPath("broken"), "name: broken\nmode: focus\n## Goal\nno frontmatter\n")
	write(cardPath("unclosed"), "---\nname: unclosed\nmode: focus\n## Goal\nx\n")
	write(filepath.Join(archiveDir(), "old.md"), "---\nname: old\n---\n## Outcome\nx\n")
	// updated and since come from the file's change time, not from updated:.
	at, _ := time.Parse(time.RFC3339, "2026-11-19T12:30:00Z")
	if err := os.Chtimes(cardPath("alpha"), at, at); err != nil {
		t.Fatal(err)
	}
	return th
}

func lsJSON(t *testing.T) lsOut {
	t.Helper()
	s, code := do("ls", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, s)
	}
	var d lsOut
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, s)
	}
	return d
}

func TestLsJSON(t *testing.T) {
	th := lsFixture(t)
	noImport(t) // broken card files exist only without tracks.db; TestLsJSONIsTheSameFromTheDatabase covers the store
	d := lsJSON(t)
	if d.Schema != 1 || d.GeneratedAt == "" {
		t.Errorf("schema %d, generated_at %q", d.Schema, d.GeneratedAt)
	}
	names := map[string]map[string]any{}
	var order []string
	for _, tr := range d.Tracks {
		names[tr["name"].(string)] = tr
		order = append(order, tr["name"].(string))
	}
	if strings.Join(order, " ") != "alpha beta broken unclosed" {
		t.Errorf("order: %v", order)
	}
	if len(names) != 4 || names["old"] != nil {
		t.Fatalf("tracks %v: want alpha beta broken unclosed, archive excluded", names)
	}
	a := names["alpha"]
	if a["dir"] != th || a["mode"] != "focus" || a["tier"] != float64(2) || a["rex_session"] != "s1" || a["check"] != "pr 7 merged" || a["waiting_on"] != "" {
		t.Errorf("alpha fields: %v", a)
	}
	if a["goal"] != "Ship ls" || a["where"] != "Parser done." || a["updated"] != "2026-11-19T12:30:00Z" {
		t.Errorf("alpha text: goal %q where %q updated %q", a["goal"], a["where"], a["updated"])
	}
	links := a["links"].([]any)
	if len(links) != 2 || links[0].(map[string]any)["url"] != "https://linear.app/example/issue/EMI-289" || links[1].(map[string]any)["label"] != "PR #7" {
		t.Errorf("links: %v", links)
	}
	asks := a["asks"].([]any)
	if len(asks) != 2 {
		t.Fatalf("asks: %v", asks)
	}
	a1, a2 := asks[0].(map[string]any), asks[1].(map[string]any)
	if a1["number"] != float64(1) || a1["text"] != "Pick a schema" || a1["is_check"] != false || a1["since"] != "2026-11-19T12:30:00Z" {
		t.Errorf("ask 1: %v", a1)
	}
	if a2["number"] != float64(2) || a2["is_check"] != true || a2["text"] != `Check "pr 7 merged" is now yes. Recommend: set mode to focus.` {
		t.Errorf("ask 2: %v", a2)
	}
	b := names["beta"]
	if v, ok := b["tier"]; !ok || v != nil {
		t.Errorf("beta tier must be present and null: %v", b)
	}
	if asks := b["asks"].([]any); len(asks) != 0 {
		t.Errorf("beta has no Needs me: %v", asks)
	}
	if b["links"] == nil || len(b["links"].([]any)) != 0 {
		t.Errorf("beta links: %v", b["links"])
	}
}

func TestLsJSONMalformedCard(t *testing.T) {
	lsFixture(t)
	d := lsJSON(t)
	var good map[string]any
	for _, tr := range d.Tracks {
		if tr["name"] == "alpha" {
			good = tr
		}
	}
	for _, tr := range d.Tracks {
		n := tr["name"].(string)
		if len(tr) != len(good) {
			t.Errorf("%s has %d keys, alpha has %d: %v", n, len(tr), len(good), tr)
		}
		for k := range good {
			if _, ok := tr[k]; !ok {
				t.Errorf("%s lacks key %q", n, k)
			}
		}
		if n == "alpha" || n == "beta" {
			if tr["error"] != nil {
				t.Errorf("%s error should be null: %v", n, tr["error"])
			}
			continue
		}
		if e, _ := tr["error"].(string); e == "" {
			t.Errorf("%s: want an error, got %v", n, tr)
		}
		if tr["mode"] != "" || tr["tier"] != nil || tr["updated"] != nil || len(tr["asks"].([]any)) != 0 || len(tr["links"].([]any)) != 0 {
			t.Errorf("%s: want zero values, got %v", n, tr)
		}
	}
}

func TestLsModeNormalisedAndOrdered(t *testing.T) {
	th, _ := setup(t)
	for name, mode := range map[string]string{"c-up": "Focus", "a-none": "", "b-park": "parked", "d-wait": "waiting"} {
		txt := "---\nname: " + name + "\nmode: " + mode + "\n---\n## Goal\nx\n"
		if mode == "" {
			txt = "---\nname: " + name + "\n---\n## Goal\nx\n"
		}
		foldBack(t)
		if err := writeAtomic(cardPath(name), []byte(txt)); err != nil {
			t.Fatal(err)
		}
	}
	foldBack(t)
	if err := writeAtomic(filepath.Join(th, "cards", ".hidden.md"), []byte("---\n---\n")); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tr := range lsJSON(t).Tracks {
		got = append(got, tr["name"].(string)+"="+tr["mode"].(string))
	}
	want := "a-none=background b-park=parked c-up=background d-wait=waiting"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestLsSkipsVanishedCard(t *testing.T) {
	th, _ := setup(t)
	if err := os.Symlink(filepath.Join(th, "nowhere.md"), cardPath("ghost")); err != nil {
		// cards dir may not exist yet
		if err := os.MkdirAll(filepath.Dir(cardPath("ghost")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(th, "nowhere.md"), cardPath("ghost")); err != nil {
			t.Fatal(err)
		}
	}
	if d := lsJSON(t); len(d.Tracks) != 0 {
		t.Errorf("a vanished card must be skipped: %v", d.Tracks)
	}
}

func TestLsUnreadableCardKeepsShape(t *testing.T) {
	lsFixture(t)
	noImport(t) // an unreadable card file exists only without tracks.db
	if err := os.Chmod(cardPath("beta"), 0); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(cardPath("beta")); err == nil {
		f.Close()
		t.Skip("running as a user who can read mode 000 files")
	}
	for _, tr := range lsJSON(t).Tracks {
		if tr["name"] != "beta" {
			continue
		}
		if e, _ := tr["error"].(string); e == "" || len(tr) != 21 {
			t.Errorf("beta: %v", tr)
		}
		return
	}
	t.Error("beta missing")
}

func TestLsTable(t *testing.T) {
	lsFixture(t)
	noImport(t) // broken card files exist only without tracks.db
	s, code := do("ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, s)
	}
	for _, want := range []string{"NAME", "alpha", "focus", "beta", "background", "broken", "error:"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "old") {
		t.Errorf("archived card listed:\n%s", s)
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "alpha") && !strings.Contains(l, " 2 ") {
			t.Errorf("alpha asks count: %q", l)
		}
	}
}

func TestLsRejectsArgs(t *testing.T) {
	setup(t)
	if s, code := do("ls", "--bogus"); code != 1 || !strings.Contains(s, "usage: tracks ls") {
		t.Errorf("code %d: %s", code, s)
	}
}
