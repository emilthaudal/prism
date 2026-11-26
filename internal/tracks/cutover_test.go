package tracks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// migrateHome is a test home holding the migrate fixture on the files, in both runs, with no
// pane running.
func migrateHome(t *testing.T) string {
	t.Helper()
	th, _ := setup(t)
	writeFixture(t, th)
	noImport(t)
	fakePanes(t, nil)
	return th
}

func fakePanes(t *testing.T, pids []string) {
	t.Helper()
	prev := panesRunning
	panesRunning = func() ([]string, error) { return pids, nil }
	t.Cleanup(func() { panesRunning = prev })
}

// lsComparable is ls --json without generated_at and without each ask's since: the two keys
// migrating may change.
func lsComparable(t *testing.T, home string) string {
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
	for _, tr := range doc["tracks"].([]any) {
		for _, a := range tr.(map[string]any)["asks"].([]any) {
			delete(a.(map[string]any), "since")
		}
	}
	b, _ := json.MarshalIndent(doc, "", " ")
	return string(b)
}

// withoutLocks drops the lock files a run creates, and the home folder that holds them, from a
// tree snapshot.
func withoutLocks(snap map[string]fileStat) map[string]fileStat {
	for p := range snap {
		if p == "." || strings.HasSuffix(p, ".lock") {
			delete(snap, p)
		}
	}
	return snap
}

func sameTree(t *testing.T, before, after map[string]fileStat) {
	t.Helper()
	for p, b := range before {
		if a, ok := after[p]; !ok {
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

func TestMigrateSwitchesToTheDatabase(t *testing.T) {
	home := migrateHome(t)
	files := map[string]int64{}
	for p, st := range snapshotTree(t, home) {
		if st.mode.IsRegular() {
			files[filepath.ToSlash(p)] = st.size
		}
	}
	lsBefore := lsComparable(t, home)
	out, errw, code := runTracks(t, home, "migrate")
	if code != 0 {
		t.Fatalf("migrate exited %d\nstdout: %s\nstderr: %s", code, out, errw)
	}
	for _, want := range []string{"imported 3 live and 2 archived cards, 2 check records, 9 events into tracks.db",
		"backup: ", "renamed checks.json and attention.jsonl to *.imported", "rendered every card view"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !dbExists() {
		t.Fatal("no tracks.db")
	}
	if _, err := os.Stat(dbPath() + ".new"); err == nil {
		t.Error("tracks.db.new is left")
	}

	backups, _ := filepath.Glob(filepath.Join(home, backupDirName, "pre-store-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	listed, err := listBackup(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	for name := range listed {
		if strings.HasSuffix(name, ".lock") {
			delete(listed, name) // migrate made them before the backup
		}
	}
	for name, size := range files {
		if listed[name] != size {
			t.Errorf("the backup lists %s with %d bytes, the file had %d", name, listed[name], size)
		}
	}
	if len(listed) != len(files) {
		t.Errorf("the backup lists %d files, the home held %d", len(listed), len(files))
	}

	for _, f := range []string{"checks.json", "attention.jsonl"} {
		if _, err := os.Stat(filepath.Join(home, f)); err == nil {
			t.Errorf("%s is still there", f)
		}
		if _, err := os.Stat(filepath.Join(home, f+importedExt)); err != nil {
			t.Errorf("%s%s: %v", f, importedExt, err)
		}
	}

	st, err := store.Open(dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	live, err := st.LiveTracks()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 3 {
		t.Fatalf("%d live tracks", len(live))
	}
	for _, v := range live {
		b, err := os.ReadFile(cardPath(v.Track.Name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "\ngenerated: ") {
			t.Errorf("view %s has no generated line:\n%s", v.Track.Name, b)
		}
		if hashText(b) != v.Track.RenderHash {
			t.Errorf("view %s: its hash is not the stored render_hash", v.Track.Name)
		}
	}

	if after := lsComparable(t, home); after != lsBefore {
		t.Errorf("ls --json changed beyond generated_at and since:\n--- before\n%s\n--- after\n%s", lsBefore, after)
	}
	if edited := editedViews(); len(edited) > 0 {
		t.Errorf("views kept aside as hand edits: %v", edited)
	}
}

// A backup never replaces or removes an archive already under its name.
func TestBackupLeavesAnArchiveUnderItsNameAlone(t *testing.T) {
	home := migrateHome(t)
	now := time.Now()
	taken := filepath.Join(home, backupDirName, "pre-store-"+now.UTC().Format("20060102T150405Z")+".tar.gz")
	if err := os.MkdirAll(filepath.Dir(taken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taken, []byte("an earlier backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := backupHome("pre-store", now); err == nil {
		t.Error("backup wrote over an archive under its name")
	}
	if b, err := os.ReadFile(taken); err != nil || string(b) != "an earlier backup" {
		t.Errorf("the earlier archive: %q, %v", b, err)
	}
}

func TestMigrateTwiceSaysAlreadyMigrated(t *testing.T) {
	home := migrateHome(t)
	if out, errw, code := runTracks(t, home, "migrate"); code != 0 {
		t.Fatalf("first run exited %d: %s%s", code, out, errw)
	}
	before := snapshotTree(t, home)
	out, errw, code := runTracks(t, home, "migrate")
	if code != 0 || strings.TrimSpace(out) != "already migrated" {
		t.Errorf("second run: exit %d, stdout %q, stderr %q", code, out, errw)
	}
	sameTree(t, before, snapshotTree(t, home))
}

// A migrate killed after the switch left the logs in place and a view unrendered; the re-run
// says already migrated and finishes both.
func TestMigrateRerunFinishesACrashedSwitch(t *testing.T) {
	home := migrateHome(t)
	if out, errw, code := runTracks(t, home, "migrate"); code != 0 {
		t.Fatalf("first run exited %d: %s%s", code, out, errw)
	}
	for _, name := range []string{"checks.json", "attention.jsonl"} {
		p := filepath.Join(home, name)
		if err := os.Rename(p+".imported", p); err != nil {
			t.Fatal(err)
		}
	}
	views, _ := filepath.Glob(filepath.Join(home, "cards", "*.md"))
	if len(views) == 0 {
		t.Fatal("no views")
	}
	b, err := os.ReadFile(views[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(views[0], bytes.Replace(b, []byte("\ngenerated: "), []byte("\nxgenerated: "), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errw, code := runTracks(t, home, "migrate")
	if code != 0 || !strings.Contains(out, "already migrated") || !strings.Contains(out, "rendered every card view") {
		t.Fatalf("re-run: exit %d, stdout %q, stderr %q", code, out, errw)
	}
	for _, name := range []string{"checks.json", "attention.jsonl"} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			t.Errorf("%s still in place", name)
		}
		if _, err := os.Stat(filepath.Join(home, name+".imported")); err != nil {
			t.Errorf("%s.imported: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(views[0]); !bytes.Contains(b, []byte("\ngenerated: ")) {
		t.Errorf("view not re-rendered:\n%s", b)
	}
}

// The archive takes its real name only once it verifies, and a crashed run's .partial is
// cleared rather than blocking the next.
func TestBackupNeverLeavesAnUnverifiedArchiveUnderItsName(t *testing.T) {
	home := migrateHome(t)
	t.Setenv("TRACKS_HOME", home)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(home, "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "pre-store-" + now.Format("20060102T150405Z") + ".tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name+".partial"), []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, n, err := backupHome("pre-store", now)
	if err != nil || n == 0 || filepath.Base(path) != name {
		t.Fatalf("backup: %q %d %v", path, n, err)
	}
	if _, err := os.Stat(path + ".partial"); err == nil {
		t.Error(".partial left behind")
	}
	if _, err := verifyBackup(path); err != nil {
		t.Errorf("archive does not verify: %v", err)
	}
}

func TestMigrateRefusesWhileAPaneRuns(t *testing.T) {
	home := migrateHome(t)
	fakePanes(t, []string{"4242"})
	before := snapshotTree(t, home)
	_, errw, code := runTracks(t, home, "migrate")
	if code != 1 || !strings.Contains(errw, "quit the pane, then run again") || !strings.Contains(errw, "4242") {
		t.Errorf("exit %d, stderr %q", code, errw)
	}
	sameTree(t, before, snapshotTree(t, home))
}

// fakePaneEnv makes the test binary a stand-in pane; ps cannot read the environment of a system
// binary such as cat, but it can read a Go binary's, as it can the real pane's.
const fakePaneEnv = "TRACKS_FAKE_PANE"

// The real pane lookup finds a process started as tracks on this home, and not one on another.
func TestFindPanesSeesOnlyThisHomesPane(t *testing.T) {
	home := migrateHome(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(trackHome string) *exec.Cmd {
		cmd := exec.Command("/bin/bash", "-c", `exec -a tracks "$0"`, self)
		cmd.Env = append(os.Environ(), "TRACKS_HOME="+trackHome, fakePaneEnv+"=1")
		if _, err := cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd
	}
	other := strconv.Itoa(start(t.TempDir()).Process.Pid)
	ours := strconv.Itoa(start(home).Process.Pid)
	var pids []string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var err error
		if pids, err = findPanes(); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(pids, ours) {
			break
		}
	}
	if !slices.Contains(pids, ours) {
		t.Errorf("the pane on this home (pid %s) is not found: %v", ours, pids)
	}
	if slices.Contains(pids, other) {
		t.Errorf("the pane on another home (pid %s) is found: %v", other, pids)
	}
}

func TestMigrateAbortsOnADifferenceAndTouchesNothing(t *testing.T) {
	home := migrateHome(t)
	// An unnumbered line under Needs me is not an ask, so the database cannot carry it.
	edited := strings.Replace(alphaCard, "1. Pick a driver.", "- a loose note\n1. Pick a driver.", 1)
	p := filepath.Join(home, "cards", "alpha.md")
	st, _ := os.Stat(p)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)
	out, _, code := runTracks(t, home, "migrate")
	if code != 1 || !strings.Contains(out, "differs: card alpha: section Needs me") || !strings.Contains(out, "nothing changed") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	sameTree(t, withoutLocks(before), withoutLocks(snapshotTree(t, home)))
}

func TestMigrateRebuildsALeftoverNewDatabase(t *testing.T) {
	home := migrateHome(t)
	if err := os.WriteFile(dbPath()+".new", []byte("half a database from a run that stopped"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errw, code := runTracks(t, home, "migrate"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errw)
	}
	if _, err := os.Stat(dbPath() + ".new"); err == nil {
		t.Error("tracks.db.new is left")
	}
	if c, err := loadCard("beta"); err != nil || c.Sections["Goal"] != "Try beta." {
		t.Errorf("beta from the rebuilt database: %+v, %v", c, err)
	}
}

// startDuringMigrate runs write while migrate holds its locks, and lets migrate go on only once
// write is waiting for the lock at lockPath.
func startDuringMigrate(t *testing.T, lockPath string, write func()) <-chan struct{} {
	t.Helper()
	waiting, done := make(chan struct{}, 1), make(chan struct{})
	prevWaiting, prevHolding := waitingForLock, migrateHolding
	waitingForLock = func(p string) {
		if p == lockPath {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}
	migrateHolding = func() {
		go func() {
			defer close(done)
			write()
		}()
		select {
		case <-waiting:
		case <-time.After(3 * time.Second):
			t.Error("the write never waited for the lock")
		}
	}
	t.Cleanup(func() { waitingForLock, migrateHolding = prevWaiting, prevHolding })
	return done
}

// A tracks card that read the switch as off, and then waited for the cards lock migrate holds,
// lands in tracks.db, not on a view that the database overwrites.
func TestMigrateTakesACardWriteThatWaitedForTheLock(t *testing.T) {
	home := migrateHome(t)
	var code int
	var wout, werr bytes.Buffer
	done := startDuringMigrate(t, cardsLockPath(), func() {
		code = Run([]string{"card", "beta"}, strings.NewReader("where:\nwritten during migrate\n"), &wout, &werr)
	})
	if out, errw, c := runTracks(t, home, "migrate"); c != 0 {
		t.Fatalf("migrate exited %d: %s%s", c, out, errw)
	}
	<-done
	if code != 0 {
		t.Fatalf("tracks card exited %d: %s%s", code, wout.String(), werr.String())
	}
	st, err := store.Open(dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.Get("beta")
	if err != nil {
		t.Fatal(err)
	}
	if v.Track.Where != "written during migrate" {
		t.Errorf("tracks.db has where %q: the write was lost", v.Track.Where)
	}
	if b, _ := os.ReadFile(cardPath("beta")); !strings.Contains(string(b), "written during migrate") || !strings.Contains(string(b), "\ngenerated: ") {
		t.Errorf("the view is not the database's render with the write:\n%s", b)
	}
	if edited := editedViews(); len(edited) > 0 {
		t.Errorf("views kept aside as hand edits: %v", edited)
	}
}

// A hook that waited for the events lock migrate holds logs its event in tracks.db, and makes
// its card change there, instead of appending to an attention.jsonl that is already imported.
func TestMigrateTakesAHookEventThatWaitedForTheLock(t *testing.T) {
	home := migrateHome(t)
	t.Setenv("REX_SESSION", "session:alpha")
	start := time.Now().UTC().Truncate(time.Second)
	done := startDuringMigrate(t, eventsLockPath(), func() {
		Run([]string{"hook"}, strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"1 yes","cwd":"/c"}`), &bytes.Buffer{}, &bytes.Buffer{})
	})
	if out, errw, c := runTracks(t, home, "migrate"); c != 0 {
		t.Fatalf("migrate exited %d: %s%s", c, out, errw)
	}
	<-done
	if r, failing := hookFailure(); failing {
		t.Errorf("hook.err: %+v", r)
	}
	if _, err := os.Stat(logPath()); err == nil {
		b, _ := os.ReadFile(logPath())
		t.Errorf("the hook wrote attention.jsonl after the switch:\n%s", b)
	}
	evs := readEvents()
	if len(evs) != 10 || evs[9].Event != "prompt" || evs[9].Track != "alpha" {
		t.Fatalf("the prompt is not the tenth event in tracks.db: %+v", evs)
	}
	st, err := store.Open(dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.Get("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if v.Track.AnsweredAt.Before(start) {
		t.Errorf("answered_at %v is not this prompt's", v.Track.AnsweredAt)
	}
}

// A card whose updated: stamp is later than its file's mtime imports its content time as the
// mtime, so a mode key, which moves changed_at past the stamp, leaves ls --json's updated alone.
func TestImportedStampLaterThanTheMtimeSurvivesAModeKey(t *testing.T) {
	th, _ := setup(t)
	mtime := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	stamp := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	card(t, th, "late", "focus", "", "w", "")
	b, _ := os.ReadFile(cardPath("late"))
	if err := os.WriteFile(cardPath("late"), []byte(strings.Replace(string(b), "dir:", "updated: "+stamp+"\ndir:", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cardPath("late"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	importNow(t)
	updated := func() string {
		for _, tr := range storeLs() {
			if tr.Name == "late" && tr.Updated != nil {
				return *tr.Updated
			}
		}
		t.Fatal("late is not listed")
		return ""
	}
	want := mtime.UTC().Format(time.RFC3339)
	if got := updated(); got != want {
		t.Fatalf("updated after the import is %s, want the mtime %s", got, want)
	}
	if err := setMode("late", "parked"); err != nil {
		t.Fatal(err)
	}
	if got := updated(); got != want {
		t.Errorf("updated after a mode key is %s, want the mtime %s", got, want)
	}
}
