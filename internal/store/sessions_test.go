package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// schema7DB writes a database as schema 7 left it: three tracks, two with a Claude session,
// one of those closed.
func schema7DB(t *testing.T) string {
	t.Helper()
	sqlText, err := os.ReadFile(filepath.Join("testdata", "schema7.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tracks.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatal(err)
	}
	return path
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateFromSchema7BacksUpThenAddsSessions(t *testing.T) {
	path := schema7DB(t)
	var backups []int
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	backup := func(from int, copyDB func(string) error) error {
		// The backup sees the database before any schema 8 write.
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if err != nil {
			return err
		}
		defer db.Close()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'sessions'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Error("the sessions table existed when the backup ran")
		}
		backups = append(backups, from)
		if len(backups) > 1 {
			return nil
		}
		return copyDB(copyPath)
	}
	s, err := Open(path, WithBackup(backup), WithClock(testClock()))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "backups", backups, []int{7})
	eq(t, "user_version", userVersion(t, s), schemaVersion)
	if err := CheckCopy(copyPath); err != nil {
		t.Errorf("the backup copy: %v", err)
	}
	cp, err := sql.Open("sqlite", "file:"+copyPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var cv int
	cp.QueryRow(`PRAGMA user_version`).Scan(&cv)
	cp.Close()
	eq(t, "the copy's schema", cv, 7)
	list, err := s.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("sessions after backfill = %+v, want alpha's only", list)
	}
	got := list[0]
	eq(t, "id", got.ID, "11111111-1111-1111-1111-111111111111")
	eq(t, "track", got.Track, "alpha")
	eq(t, "owner/host/status/cwd", []string{got.Owner, got.Host, got.Status, got.Cwd}, []string{"emil", "rex", "idle", "/work/alpha"})
	var asks, closedRows int
	s.db.QueryRow(`SELECT count(*) FROM desk_asks`).Scan(&asks)
	s.db.QueryRow(`SELECT count(*) FROM sessions WHERE id LIKE '3333%'`).Scan(&closedRows)
	eq(t, "desk_asks kept", asks, 1)
	eq(t, "closed track's session", closedRows, 0)
	for _, table := range []string{"questions", "messages"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		eq(t, table+" rows", n, 0)
	}
	s.Close()

	s2, err := Open(path, WithBackup(backup))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	eq(t, "backups after a second open", backups, []int{7})
	list, _ = s2.Sessions()
	eq(t, "sessions after a second open", len(list), 1)
}

func TestMigrateStopsWhenTheBackupFails(t *testing.T) {
	path := schema7DB(t)
	if _, err := Open(path, WithBackup(func(int, func(string) error) error { return errors.New("disk full") })); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Open = %v, want the backup's error", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eq(t, "migrated without a backup option", userVersion(t, s), schemaVersion)
}

func TestFreshDatabaseTakesNoBackup(t *testing.T) {
	called := false
	s, err := Open(filepath.Join(t.TempDir(), "tracks.db"), WithCreate(), WithBackup(func(int, func(string) error) error { called = true; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if called {
		t.Error("a new database was backed up")
	}
}

func session(t *testing.T, s *Store, id string) Session {
	t.Helper()
	list, err := s.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("no session %s in %+v", id, list)
	return Session{}
}

func hookSession(t *testing.T, s *Store, e SessionEvent) {
	t.Helper()
	if err := s.SessionHook(e); err != nil {
		t.Fatal(err)
	}
}

func TestSessionHookKeepsTheRowCurrent(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Bind("alpha", "rex-1", "/work/alpha"); err != nil {
		t.Fatal(err)
	}
	const id = "aaaa"
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: id, Source: "startup", RexSession: "rex-1", Cwd: "/work/alpha", Model: "claude-haiku"})
	x := session(t, s, id)
	eq(t, "start", []string{x.Track, x.Owner, x.Host, x.Status, x.Cwd, x.Model}, []string{"alpha", "emil", "rex", "idle", "/work/alpha", "claude-haiku"})

	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: id, Prompt: "go"})
	eq(t, "prompt", session(t, s, id).Status, "working")

	hookSession(t, s, SessionEvent{Event: SessionBlocked, ID: id, WaitingFor: "permission: Bash git push"})
	x = session(t, s, id)
	eq(t, "blocked", []string{x.Status, x.WaitingFor}, []string{"blocked", "permission: Bash git push"})

	long := strings.Repeat("é", lastAnswerMax+50)
	hookSession(t, s, SessionEvent{Event: SessionStop, ID: id, LastAnswer: long})
	x = session(t, s, id)
	eq(t, "stop", []string{x.Status, x.WaitingFor}, []string{"done", ""})
	eq(t, "last answer clipped", len([]rune(x.LastAnswer)), lastAnswerMax)

	hookSession(t, s, SessionEvent{Event: SessionEnd, ID: id})
	x = session(t, s, id)
	eq(t, "end host", x.Host, "stopped")
	if x.EndedAt.IsZero() {
		t.Error("ended_at not set")
	}

	hookSession(t, s, SessionEvent{Event: SessionStart, ID: id, Source: "resume", Background: true})
	x = session(t, s, id)
	eq(t, "resume", []string{x.Host, x.Status, x.Cwd}, []string{"background", "idle", "/work/alpha"})
	if !x.EndedAt.IsZero() {
		t.Error("a resumed session kept ended_at")
	}
}

func TestBackgroundSessionIgnoresTheRexSessionItInherited(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Bind("alpha", "rex-1", "/work/alpha"); err != nil {
		t.Fatal(err)
	}
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "bg1", Background: true, RexSession: "rex-1"})
	x := session(t, s, "bg1")
	eq(t, "bg", []string{x.Host, x.Track}, []string{"background", ""})
}

func TestPromptHandsADeskSessionToEmilUnlessTheDeskDeliveredIt(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := openAt(t, filepath.Join(t.TempDir(), "tracks.db"), clock)
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "d1", Background: true})
	if _, err := s.db.Exec(`UPDATE sessions SET owner = 'desk' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	deliver := func(text string, ago time.Duration) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO messages (session_id, from_kind, kind, text, created_at, delivered_at) VALUES ('d1', 'desk', 'note', ?, ?, ?)`,
			text, fmtTime(now.Add(-ago)), fmtTime(now.Add(-ago))); err != nil {
			t.Fatal(err)
		}
	}
	deliver("run the tests", 5*time.Minute)
	deliver("old note", 20*time.Minute)

	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "d1", Prompt: "run the tests\n"})
	eq(t, "a delivered message", session(t, s, "d1").Owner, "desk")

	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "d1", Prompt: "old note"})
	eq(t, "a message delivered 20 minutes ago", session(t, s, "d1").Owner, "emil")

	if _, err := s.db.Exec(`UPDATE sessions SET owner = 'desk' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "d1", Prompt: "stop that, I'll take it"})
	eq(t, "Emil's own prompt", session(t, s, "d1").Owner, "emil")
}

// EMI-404: a desk session whose background review finished stayed the desk's no longer.
func TestHarnessPromptKeepsADeskSessionTheDesks(t *testing.T) {
	s := newStore(t)
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "d1", Background: true})
	if _, err := s.db.Exec(`UPDATE sessions SET owner = 'desk' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"<task-notification>\n<task-id>a1</task-id>\n<status>completed</status>\n</task-notification>",
		"  <system-reminder>Proactive output style is active.</system-reminder>",
		// EMI-404 reopened: a finished subagent's report, as prism-prompt-audit got it mid-turn
		// and prism-cli-followups got it at its stop, flipped both after c1b0267.
		"<agent-message from=\"a3029a6bd9dc10a2d\">\n[Subagent hand-back] The text below is the final report of a subagent.\n</agent-message>",
		"Another Claude session sent a message:\n<agent-message from=\"afb3df94ac3ef98ed\">\n[Subagent hand-back] Review clean.\n</agent-message>",
		// EMI-421: as-1701-oxlint-no-comments flipped three times on 25 Nov as Emil's own session
		// messaged it: at a turn's start with the preamble, and mid-turn as a queued prompt without.
		"Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/65874.sock\" from-name=\"as-1702-publish-beta-packages\" from-mode=\"prompting\">\nYes, that was mine; I've told it to stop.\n</cross-session-message>",
		"<cross-session-message from=\"uds:/tmp/cc-socks/65874.sock\" from-name=\"as-1702-publish-beta-packages\" from-mode=\"prompting\">\nUnderstood. I won't start OPS-1704.\n</cross-session-message>",
	} {
		hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "d1", Prompt: p})
		eq(t, "after "+p[:20], session(t, s, "d1").Owner, "desk")
	}
}

// EMI-406: a session killed mid-turn read working forever, so notes for it were held forever.
func TestASessionThatEndsMidTurnIsNoLongerWorking(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	s := openAt(t, filepath.Join(t.TempDir(), "tracks.db"), func() time.Time { return now })
	for _, id := range []string{"killed", "ended", "idle"} {
		hookSession(t, s, SessionEvent{Event: SessionStart, ID: id, Background: true})
	}
	hookSession(t, s, SessionEvent{Event: SessionPrompt, ID: "killed", Prompt: "go"})
	hookSession(t, s, SessionEvent{Event: SessionBlocked, ID: "ended", WaitingFor: "permission: Bash"})
	if err := s.MarkStopped([]string{"killed", "idle"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	hookSession(t, s, SessionEvent{Event: SessionEnd, ID: "ended"})
	x := session(t, s, "killed")
	eq(t, "killed", []string{x.Host, x.Status}, []string{"stopped", "error"})
	x = session(t, s, "ended")
	eq(t, "ended", []string{x.Host, x.Status, x.WaitingFor}, []string{"stopped", "error", ""})
	x = session(t, s, "idle")
	eq(t, "idle", []string{x.Host, x.Status}, []string{"stopped", "idle"})
}

func TestMarkStoppedSparesSessionsTouchedAfterTheLivenessRead(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := openAt(t, filepath.Join(t.TempDir(), "tracks.db"), clock)
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "old"})
	before := now.Add(time.Second)
	now = now.Add(2 * time.Second)
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "new"})
	if err := s.MarkStopped([]string{"old", "new"}, before); err != nil {
		t.Fatal(err)
	}
	eq(t, "old", session(t, s, "old").Host, "stopped")
	eq(t, "new", session(t, s, "new").Host, "rex")
}

func TestSessionsSkipClosedTracks(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if err := s.SetSession("alpha", "", "s-alpha", "/w"); err != nil {
		t.Fatal(err)
	}
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "s-alpha"})
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "loose"})
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Sessions()
	if len(list) != 1 || list[0].ID != "loose" {
		t.Fatalf("sessions = %+v, want only the track-less one", list)
	}
}

func TestOnlyOneProcessMigratesAtATime(t *testing.T) {
	path := schema7DB(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		s, err := Open(path, WithBackup(func(int, func(string) error) error {
			close(entered)
			<-release
			return nil
		}))
		if err == nil {
			s.Close()
		}
		first <- err
	}()
	<-entered
	s, err := Open(path, WithBackup(func(int, func(string) error) error {
		t.Error("a second migration ran while the first held the lock")
		return nil
	}))
	if err != nil {
		t.Fatalf("the second open: %v", err)
	}
	eq(t, "schema while the first migrates", userVersion(t, s), 7)
	if err := s.SessionHook(SessionEvent{Event: SessionStart, ID: "x"}); err != nil {
		t.Errorf("a session write on the old schema: %v", err)
	}
	if _, err := s.Sessions(); !errors.Is(err, ErrStale) {
		t.Errorf("Sessions on the old schema = %v, want ErrStale", err)
	}
	s.Close()
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("the first open: %v", err)
	}
	s = openAt(t, path, testClock())
	eq(t, "schema after the first finished", userVersion(t, s), schemaVersion)
}

func TestDeleteThreadKeepsItsSessionRow(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "lead")
	if _, err := s.Card("lead", CardUpdate{Role: ptr(RoleLead)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Spawn("lead", SpawnSpec{Name: "kid", Dir: "/w", Goal: "g"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSession("kid", "", "s-kid", "/w"); err != nil {
		t.Fatal(err)
	}
	hookSession(t, s, SessionEvent{Event: SessionStart, ID: "s-kid"})
	if err := s.DeleteThread("kid"); err != nil {
		t.Fatal(err)
	}
	x := session(t, s, "s-kid")
	eq(t, "track of the deleted thread's session", x.Track, "")
}
