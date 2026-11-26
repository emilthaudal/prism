package tracks

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	sessA = "0b9a6c1e-1111-4a4a-8888-aaaaaaaaaaaa"
	sessB = "0b9a6c1e-2222-4a4a-8888-bbbbbbbbbbbb"
)

func sessionRow(t *testing.T, id string) store.Session {
	t.Helper()
	st, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	list, err := st.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no session %s in %+v", id, list)
	return store.Session{}
}

func hookJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return hook(t, string(b))
}

func TestHookKeepsTheSessionRowCurrent(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	card(t, th, "tr", "focus", "s5", "w", "")
	importNow(t)
	ev := func(name string, more map[string]any) string {
		t.Helper()
		f := map[string]any{"hook_event_name": name, "session_id": sessA, "cwd": "/work/tr"}
		for k, v := range more {
			f[k] = v
		}
		return hookJSON(t, f)
	}
	check := func(what string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}

	ev("SessionStart", map[string]any{"source": "startup", "model": "claude-haiku-5-5"})
	s := sessionRow(t, sessA)
	check("track", s.Track, "tr")
	check("host", s.Host, "rex")
	check("status", s.Status, "idle")
	check("model", s.Model, "claude-haiku-5-5")
	check("owner", s.Owner, "emil")

	ev("UserPromptSubmit", map[string]any{"prompt": "merge it"})
	check("after a prompt", sessionRow(t, sessA).Status, "working")
	evs := readEvents()
	if len(evs) == 0 || evs[len(evs)-1].Event != "prompt" || evs[len(evs)-1].Track != "tr" {
		t.Errorf("the attention log lost the prompt: %+v", evs)
	}

	ev("Notification", map[string]any{"notification_type": "idle_prompt", "message": "Claude is waiting for your input"})
	check("after an idle notification", sessionRow(t, sessA).Status, "working")

	ev("Notification", map[string]any{"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	s = sessionRow(t, sessA)
	check("after a permission notification", s.Status, "blocked")
	check("waiting for", s.WaitingFor, "Claude needs your permission to use Bash")

	out := ev("PermissionRequest", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git push\n  origin emi-376", "description": "push"}})
	check("PermissionRequest output", out, "")
	s = sessionRow(t, sessA)
	check("after a permission request", s.Status, "blocked")
	check("waiting for", s.WaitingFor, "permission: Bash git push origin emi-376")

	ev("Stop", map[string]any{"last_assistant_message": "Merged.\nPR #12 is in."})
	s = sessionRow(t, sessA)
	check("after stop", s.Status, "done")
	check("waiting for after stop", s.WaitingFor, "")
	check("last answer", s.LastAnswer, "Merged.\nPR #12 is in.")

	ev("SessionEnd", map[string]any{"reason": "other"})
	check("after SessionEnd", sessionRow(t, sessA).Host, "stopped")

	ev("SessionStart", map[string]any{"source": "resume"})
	s = sessionRow(t, sessA)
	check("host after resume", s.Host, "rex")
	check("status after resume", s.Status, "idle")
}

func TestHookMarksABackgroundSession(t *testing.T) {
	setup(t)
	t.Setenv("CLAUDE_JOB_DIR", filepath.Join(t.TempDir(), "job"))
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	importNow(t)
	hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": sessA, "source": "startup", "cwd": "/w"})
	if h := sessionRow(t, sessA).Host; h != "background" {
		t.Errorf("host %q, want background", h)
	}
}

// writeSchema7 puts a database as schema 7 left it at the home's tracks.db.
func writeSchema7(t *testing.T) {
	t.Helper()
	noImport(t)
	sqlText, err := os.ReadFile(filepath.Join("..", "store", "testdata", "schema7.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A database the store made is in WAL mode, which persists; two opens that both switch a
	// rollback-journal file to WAL can get SQLITE_BUSY without waiting.
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatal(err)
	}
}

func sessionsJSON(t *testing.T) sessionsDoc {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"sessions", "--json"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("tracks sessions --json: exit %d: %s", code, errb.String())
	}
	var doc sessionsDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return doc
}

func TestFirstOpenOfASchema7DatabaseBacksUpTheHome(t *testing.T) {
	th, _ := setup(t)
	writeSchema7(t)
	doc := sessionsJSON(t)
	if len(doc.Sessions) != 1 || doc.Sessions[0].Track == nil || *doc.Sessions[0].Track != "alpha" {
		t.Fatalf("sessions after the migration: %+v", doc.Sessions)
	}
	backups, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v7-*.tar.gz"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	files, err := listBackup(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range dbFiles {
		if _, ok := files[f]; ok {
			t.Errorf("the archive holds %s, which only SQLite may read while it is open", f)
		}
	}
	copies, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v7-*.db"))
	if len(copies) != 1 {
		t.Fatalf("database copies: %v", copies)
	}
	if err := store.CheckCopy(copies[0]); err != nil {
		t.Error(err)
	}
	sessionsJSON(t)
	again, _ := filepath.Glob(filepath.Join(th, backupDirName, "*.tar.gz"))
	if len(again) != 1 {
		t.Errorf("a second run took another backup: %v", again)
	}
}

func TestSessionsShowsUnlistedSessionsAsStopped(t *testing.T) {
	_, bin := setup(t)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	importNow(t)
	for _, id := range []string{sessA, sessB, sessC} {
		hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": id, "source": "startup", "cwd": "/w"})
	}
	claude := filepath.Join(bin, "claude")
	// sessC is a background session that died without SessionEnd: claude agents still lists
	// it, with pid 0, so that it can be resumed (EMI-430).
	agents := `[{"kind":"interactive","sessionId":"` + sessA + `","pid":4242,"status":"idle"},` +
		`{"kind":"background","sessionId":"` + sessC + `","pid":0,"status":"idle"}]`
	if err := os.WriteFile(claude, []byte("#!/bin/sh\n[ \"$1 $2\" = \"agents --json\" ] && echo '"+agents+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := func(doc sessionsDoc, id string) string {
		for _, s := range doc.Sessions {
			if s.ID == id {
				return s.Host
			}
		}
		return "missing"
	}
	doc := sessionsJSON(t)
	if !doc.Liveness || host(doc, sessA) != "rex" || host(doc, sessB) != "stopped" {
		t.Fatalf("liveness %v, %s %s, %s %s", doc.Liveness, sessA, host(doc, sessA), sessB, host(doc, sessB))
	}
	if h := sessionRow(t, sessB).Host; h != "stopped" {
		t.Errorf("the row of the unlisted session says %s", h)
	}
	if h := host(doc, sessC); h != "stopped" {
		t.Errorf("a background session listed with pid 0 is %s, want stopped", h)
	}

	if err := os.WriteFile(claude, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc = sessionsJSON(t)
	if doc.Liveness || host(doc, sessA) != "rex" {
		t.Errorf("with claude agents failing: liveness %v, %s %s", doc.Liveness, sessA, host(doc, sessA))
	}
}

func TestTwoProcessesOpeningASchema7DatabaseMigrateOnce(t *testing.T) {
	th, _ := setup(t)
	writeSchema7(t)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			st, err := openDB()
			if err == nil {
				err = st.Close()
			}
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("open: %v", err)
		}
	}
	st, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Sessions(); err != nil {
		t.Errorf("after both opens: %v", err)
	}
	copies, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v7-*.db"))
	archives, _ := filepath.Glob(filepath.Join(th, backupDirName, "pre-migrate-v7-*.tar.gz"))
	if len(copies) != 1 || len(archives) != 1 {
		t.Errorf("backups: %v %v", copies, archives)
	}
}

func TestHookStaysQuietWhileAnotherProcessMigrates(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	writeSchema7(t)
	f, err := os.OpenFile(filepath.Join(th, "migrate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if out := hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": sessA, "source": "startup"}); out != "" {
		t.Errorf("hook output %q", out)
	}
	if _, err := os.Stat(hookErrPath()); err == nil {
		b, _ := os.ReadFile(hookErrPath())
		t.Errorf("hook.err written: %s", b)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != 7 {
		t.Errorf("schema %d while another process held the migration lock", v)
	}
}
