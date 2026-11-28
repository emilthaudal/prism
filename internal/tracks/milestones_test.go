package tracks

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return out.String() + errb.String(), code
}

func TestMilestonesSetTickUntickList(t *testing.T) {
	liveHome(t)
	if out, code := runCLI(t, "milestones", "solo", "set", "design agreed", "store done", "app shows it"); code != 0 || !strings.Contains(out, "3. [ ] app shows it") {
		t.Fatalf("set: %d %s", code, out)
	}
	if out, code := runCLI(t, "milestones", "solo", "tick", "2"); code != 0 || !strings.Contains(out, "reached 2. store done") {
		t.Fatalf("tick: %d %s", code, out)
	}
	if out, code := runCLI(t, "milestones", "solo", "tick", "design agreed"); code != 0 {
		t.Fatalf("tick by title: %d %s", code, out)
	}
	if out, code := runCLI(t, "milestones", "solo", "untick", "1"); code != 0 || !strings.Contains(out, "not reached 1.") {
		t.Fatalf("untick: %d %s", code, out)
	}
	out, code := runCLI(t, "milestones", "solo", "--json")
	var doc struct {
		Milestones []milestoneJSON `json:"milestones"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Milestones) != 3 || doc.Milestones[0].Done || !doc.Milestones[1].Done {
		t.Fatalf("json: %d %s", code, out)
	}
	if out, code := runCLI(t, "milestones", "solo", "tick", "zzz"); code == 0 {
		t.Errorf("unknown milestone ticked: %s", out)
	}
	if out, code := runCLI(t, "milestones", "ghost", "set", "x"); code == 0 || !strings.Contains(out, "no card") {
		t.Errorf("ghost: %d %s", code, out)
	}
}

func TestMilestonesHelpPrintsUsageAndDashArgsAreRefused(t *testing.T) {
	liveHome(t)
	if _, code := runCLI(t, "milestones", "solo", "set", "keep me"); code != 0 {
		t.Fatal(code)
	}
	for _, args := range [][]string{
		{"milestones", "solo", "set", "--help"},
		{"milestones", "solo", "set", "-h"},
		{"milestones", "solo", "tick", "--help"},
		{"milestones", "--help"},
		{"milestones", "solo", "--help"},
	} {
		if out, code := runCLI(t, args...); code != 0 || !strings.Contains(out, "usage: tracks milestones") {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
	for _, args := range [][]string{
		{"milestones", "solo", "set", "--bogus"},
		{"milestones", "solo", "set", "ok", "-x"},
		{"milestones", "solo", "tick", "-1"},
	} {
		if out, code := runCLI(t, args...); code == 0 || !strings.Contains(out, "unknown option") {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
	out, _ := runCLI(t, "milestones", "solo")
	if !strings.Contains(out, "1. [ ] keep me") || strings.Contains(out, "--") || strings.Contains(out, "2.") {
		t.Errorf("list changed: %s", out)
	}
}

func TestTheSnapshotCarriesMilestonesInTheAppsShape(t *testing.T) {
	_, c := liveHome(t)
	if _, code := runCLI(t, "milestones", "solo", "set", "design agreed", "store done"); code != 0 {
		t.Fatal(code)
	}
	if _, code := runCLI(t, "milestones", "solo", "tick", "1"); code != 0 {
		t.Fatal(code)
	}
	_, b := c.req("GET", "/v1/snapshot", serveToken, "")
	var raw struct {
		Tracks []map[string]any `json:"tracks"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Tracks) == 0 {
		t.Fatal("no tracks")
	}
	for _, tr := range raw.Tracks {
		if tr["name"] != "solo" {
			if _, has := tr["milestones"]; has {
				t.Errorf("%v sends milestones though it has none", tr["name"])
			}
			continue
		}
		want := []any{map[string]any{"title": "design agreed", "done": true}, map[string]any{"title": "store done", "done": false}}
		if got, _ := json.Marshal(tr["milestones"]); string(got) != mustJSON(want) {
			t.Errorf("milestones: %s", got)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestFirstOpenOfASchema15DatabaseBacksUpAndAddsMilestones(t *testing.T) {
	th, _ := setup(t)
	noImport(t)
	st, err := store.Open(dbPath(), store.WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.NewTrack(store.NewTrackSpec{Name: "alpha", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+dbPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE milestones`, `PRAGMA user_version = 15`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if out, code := runCLI(t, "milestones", "alpha", "set", "one", "two"); code != 0 {
		t.Fatalf("set after the migration: %d %s", code, out)
	}
	copies, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v15-*.db"))
	archives, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v15-*.tar.gz"))
	if len(copies) != 1 || len(archives) != 1 {
		t.Fatalf("backups: %v %v", copies, archives)
	}
	if err := store.CheckCopy(copies[0]); err != nil {
		t.Error(err)
	}
	if out, code := runCLI(t, "ls", "--json"); code != 0 || !strings.Contains(out, "alpha") {
		t.Errorf("the track survived: %d %s", code, out)
	}
	var v int
	chk, _ := sql.Open("sqlite", "file:"+dbPath())
	defer chk.Close()
	if err := chk.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 17 {
		t.Errorf("user_version %d %v", v, err)
	}
}
