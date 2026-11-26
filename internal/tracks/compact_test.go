package tracks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deskCall is one call of the desk's turn: an assistant line whose usage puts the desk's
// context at tokens.
func deskCall(r *deskRunner, tokens int, text string) {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"usage":   map[string]int{"input_tokens": 2, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": tokens - 1002},
	}})
	r.handle(r.gen, b)
}

func deskResult(r *deskRunner) { r.handle(r.gen, []byte(`{"type":"result","subtype":"success"}`)) }

// A desk past its threshold is compacted once its turn ends, never inside it, and what queued
// meanwhile goes in after the compaction; a desk under it gets its next batch at once.
func TestADeskPastItsThresholdIsCompactedBetweenTurns(t *testing.T) {
	decisionHome(t, "alpha")
	var stdin bytes.Buffer
	desk := newDeskRunner(io.Discard)
	desk.stdin, desk.alive, desk.session, desk.since = nopCloser{&stdin}, true, fakeDeskSession, time.Now()

	desk.event("one")
	stdin.Reset()
	deskCall(desk, 120_000, "reading")
	deskCall(desk, deskCompactAt+10_000, "still working")
	desk.event("solo done: PR 7 open")
	if stdin.Len() != 0 {
		t.Fatalf("written to the desk mid-turn: %s", stdin.String())
	}
	deskResult(desk)
	if got := stdin.String(); got != string(userMessage(compactPrompt)) {
		t.Fatalf("after a turn past %d: %q", deskCompactAt, got)
	}
	if !desk.busy {
		t.Fatal("the compaction is not a turn: the batch could go in during it")
	}
	stdin.Reset()
	desk.event("bun-rollout done")
	if stdin.Len() != 0 {
		t.Fatalf("written to the desk while it compacts: %s", stdin.String())
	}
	deskResult(desk)
	if got := stdin.String(); !strings.Contains(got, "solo done: PR 7 open") || !strings.Contains(got, "bun-rollout done") || strings.Contains(got, compactPrompt) {
		t.Fatalf("after the compaction: %q", got)
	}

	// Under the threshold: no compaction, the next batch goes in at once.
	stdin.Reset()
	deskCall(desk, deskCompactAt-10_000, "done")
	desk.event("later")
	deskResult(desk)
	if got := stdin.String(); strings.Contains(got, compactPrompt) || !strings.Contains(got, "later") {
		t.Errorf("a desk under the threshold: %q", got)
	}
}

// writeContext writes the Claude transcript of session id with an assistant call at tokens
// of context, as Claude Code keeps it under ~/.claude/projects.
func writeContext(t *testing.T, id string, tokens int) string {
	t.Helper()
	path := filepath.Join(transcriptsDir(), "-proj", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","isSidechain":false,"message":{"role":"user","content":"/track cleanup"}}`,
		fmt.Sprintf(`{"type":"assistant","isSidechain":true,"message":{"usage":{"input_tokens":1,"cache_read_input_tokens":%d}}}`, 900_000),
		fmt.Sprintf(`{"type":"assistant","isSidechain":false,"message":{"usage":{"input_tokens":3,"cache_creation_input_tokens":2000,"cache_read_input_tokens":%d,"output_tokens":400}}}`, tokens-2003),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// A desk session past its threshold is compacted when it stops and a note waits for it, never
// while it works; the note goes in once the compaction is in its transcript.
func TestATrackSessionPastItsThresholdIsCompactedBeforeItsNextNote(t *testing.T) {
	id, bin := deskStarted(t)
	path := writeContext(t, id, trackCompactAt+20_000)
	calls := filepath.Join(bin, "claude.calls")

	bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "/track cleanup"})
	if out, code := do("send", "cleanup", "review found two things"); code != 0 || out != "held for cleanup: delivered when it stops\n" {
		t.Fatalf("send: %d %q", code, out)
	}
	if out := waitDesk(t); exists(calls) {
		t.Fatalf("touched a working session:\n%s\n%s", out, fileText(t, calls))
	}

	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Opened PR 12."})
	if out := waitDesk(t); !strings.Contains(out, "compacting cleanup's context") || strings.Contains(out, "delivered") {
		t.Fatalf("wait --desk at the stop:\n%s", out)
	}
	if c := fileText(t, calls); c != "stop aaaaaaa1\nresume "+id+" -- 5\n" {
		t.Errorf("claude calls:\n%s", c)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != compactPrompt+"\n<end>\n" {
		t.Errorf("resumed with %q", n)
	}
	if n := notes(t, id); len(n) != 1 || !n[0].Held {
		t.Fatalf("the note went before the compaction: %+v", n)
	}

	// Compacting: the resume stopped the session for a moment; nothing goes in, nothing is reported.
	bgHook(t, map[string]any{"hook_event_name": "SessionEnd", "session_id": id})
	bgHook(t, map[string]any{"hook_event_name": "SessionStart", "session_id": id, "source": "resume"})
	if out := waitDesk(t); strings.Contains(out, "delivered") || strings.Contains(out, "stopped") || strings.Contains(out, "compacting") {
		t.Fatalf("while it compacts:\n%s", out)
	}

	appendLine(t, path, `{"type":"system","subtype":"compact_boundary","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","compactMetadata":{"trigger":"manual"}}`)
	if out := waitDesk(t); out != "delivered note to cleanup\n" {
		t.Fatalf("after the compaction:\n%s", out)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); !strings.HasSuffix(n, "review found two things\n<end>\n") {
		t.Errorf("delivered %q", n)
	}
	if n := notes(t, id); len(n) != 1 || n[0].Held || !n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
}

// tracks send to an idle session past its threshold holds the note for the watch, which
// compacts first; under it the note goes in at once.
func TestSendHoldsANoteForABigSessionAndDeliversToASmallOne(t *testing.T) {
	id, bin := deskStarted(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "ok"})
	writeContext(t, id, trackCompactAt-20_000)
	if out, code := do("send", "cleanup", "small enough"); code != 0 || out != "delivered to cleanup\n" {
		t.Fatalf("under the threshold: %d %q", code, out)
	}
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "ok"})
	writeContext(t, id, trackCompactAt+20_000)
	if out, code := do("send", "cleanup", "now big"); code != 0 || !strings.HasPrefix(out, "held for cleanup: its context is compacted first") {
		t.Fatalf("past the threshold: %d %q", code, out)
	}
	if n := notes(t, id); len(n) != 2 || !n[1].Held || n[1].Delivered {
		t.Fatalf("notes %+v", n)
	}
	if out := waitDesk(t); !strings.Contains(out, "compacting cleanup's context") {
		t.Errorf("the watch did not compact:\n%s", out)
	}
	if c := fileText(t, filepath.Join(bin, "claude.notes")); c != "small enough\n<end>\n"+compactPrompt+"\n<end>\n" {
		t.Errorf("resumed with %q", c)
	}
}
