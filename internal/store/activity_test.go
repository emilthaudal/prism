package store

import (
	"path/filepath"
	"testing"
)

func TestASessionFromSchema13GainsActivityAndKeepsItsRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	if err := s.SessionHook(SessionEvent{Event: SessionStart, ID: "0b9a6c1e-1111-4a4a-8888-aaaaaaaaaaaa", Cwd: "/w"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE session_subagents`, `ALTER TABLE sessions DROP COLUMN activity`, `PRAGMA user_version = 13`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s = openAt(t, path, testClock())
	if v := userVersion(t, s); v != schemaVersion {
		t.Fatalf("user_version %d", v)
	}
	if err := s.SessionHook(SessionEvent{Event: SessionTool, ID: "0b9a6c1e-1111-4a4a-8888-aaaaaaaaaaaa", Activity: "Bash: ls"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Sessions()
	if err != nil || len(list) != 1 || list[0].Cwd != "/w" || list[0].Activity != "Bash: ls" {
		t.Fatalf("%+v, %v", list, err)
	}
}
