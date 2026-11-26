package tracks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

const sessC = "0b9a6c1e-3333-4a4a-8888-cccccccccccc"

func liveTrackSessions() ([]store.Session, error) {
	st, err := openDB()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.Sessions()
}

// holderHome is a store with track tr, whose Rex session s5 runs Claude session sessB.
func holderHome(t *testing.T) (transcripts string) {
	t.Helper()
	th, _ := setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	card(t, th, "tr", "focus", "s5", "w", "")
	importNow(t)
	transcripts = t.TempDir()
	t.Setenv("REX_SESSION", "s5")
	hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": sessB, "source": "startup",
		"transcript_path": filepath.Join(transcripts, sessB+".jsonl")})
	if s := sessionRow(t, sessB); s.Track != "tr" || s.Host == "stopped" {
		t.Fatalf("the holder: %+v", s)
	}
	t.Setenv("REX_SESSION", "")
	return transcripts
}

// EMI-427: a second session must not bind a track another live session runs.
func TestBindRefusesATrackAnotherLiveSessionHolds(t *testing.T) {
	holderHome(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", sessA)
	out, code := do("bind", "tr")
	if code == 0 || !strings.Contains(out, "held by Emil's session 0b9a6c1e") || !strings.Contains(out, "tracks bind --force tr") {
		t.Fatalf("bind of a held track: %d %q", code, out)
	}
	if out, code := do("bind", "--force", "tr"); code != 0 || !strings.Contains(out, "## Goal") {
		t.Errorf("bind --force: %d %q", code, out)
	}

	// The holder binds again freely.
	t.Setenv("CLAUDE_CODE_SESSION_ID", sessB)
	if out, code := do("bind", "tr"); code != 0 {
		t.Errorf("the holder's own bind: %d %q", code, out)
	}

	// Once the holder ends, the track is free.
	hookJSON(t, map[string]any{"hook_event_name": "SessionEnd", "session_id": sessB})
	t.Setenv("CLAUDE_CODE_SESSION_ID", sessA)
	if out, code := do("bind", "tr"); code != 0 {
		t.Errorf("bind after the holder ended: %d %q", code, out)
	}
}

func transcriptFile(t *testing.T, dir, id, root string) string {
	t.Helper()
	p := filepath.Join(dir, id+".jsonl")
	body := `{"type":"custom-title","customTitle":"tr","sessionId":"` + id + `"}` + "\n" +
		`{"type":"file-history-snapshot","messageId":"m"}` + "\n" +
		`{"type":"attachment","uuid":"` + root + `","parentUuid":null,"isSidechain":false,"sessionId":"` + id + `"}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// EMI-427: a resumed copy of a session that still runs on a track gets its prompts refused,
// as 96f6d18a, a copy of as-1668's desk session, should have been when Emil typed "continue".
func TestTheHookRefusesAPromptFromACopyOfALiveSession(t *testing.T) {
	dir := holderHome(t)
	transcriptFile(t, dir, sessB, "31a60d67-894c-4312-8dd4-1a15d0b52fbb")
	copyT := transcriptFile(t, dir, sessA, "31a60d67-894c-4312-8dd4-1a15d0b52fbb")
	otherT := transcriptFile(t, dir, sessC, "924b338c-ea4f-453e-aca9-74b944ac7ea6")
	prompt := func(id, transcript string) string {
		return hookJSON(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "continue", "transcript_path": transcript})
	}

	out := prompt(sessA, copyT)
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "copy of 0b9a6c1e, which still runs on track tr") {
		t.Fatalf("the copy's prompt: %q", out)
	}
	if list, err := liveTrackSessions(); err != nil || len(list) != 1 || list[0].ID != sessB {
		t.Errorf("the refused prompt changed the sessions: %+v %v", list, err)
	}
	if out := prompt(sessC, otherT); out != "" {
		t.Errorf("another session's prompt: %q", out)
	}
	if out := prompt(sessB, filepath.Join(dir, sessB+".jsonl")); out != "" {
		t.Errorf("the original's own prompt: %q", out)
	}

	// A copy that got a row on the track still never refuses its older original.
	t.Setenv("REX_SESSION", "s5")
	hookJSON(t, map[string]any{"hook_event_name": "SessionStart", "session_id": sessA, "source": "resume", "transcript_path": copyT})
	t.Setenv("REX_SESSION", "")
	if s := sessionRow(t, sessA); s.Track != "tr" {
		t.Fatalf("the copy's row: %+v", s)
	}
	if out := prompt(sessB, filepath.Join(dir, sessB+".jsonl")); out != "" {
		t.Errorf("the original's prompt beside a copy on the track: %q", out)
	}
	if out := prompt(sessA, copyT); !strings.Contains(out, `"decision":"block"`) {
		t.Errorf("the copy on the track: %q", out)
	}

	// Once the original ends, resuming it is no copy that works beside it.
	hookJSON(t, map[string]any{"hook_event_name": "SessionEnd", "session_id": sessB})
	if out := prompt(sessA, copyT); out != "" {
		t.Errorf("the prompt after the original ended: %q", out)
	}
}
