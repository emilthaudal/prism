package tracks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	rsSess  = "session:0001-res-aaaa"
	rsBlock = "block:r1"
	rsID    = "11111111-2222-3333-4444-555555555555"
	shellFG = `{"foreground":{"pid":700,"name":"zsh","argv0":"-zsh","path":"/bin/zsh"},"ancestors":[{"pid":1,"name":"login"}]}`
	// A bash script waiting on `read` under the pane's zsh.
	scriptFG = `{"foreground":{"pid":710,"name":"bash","argv0":"bash","path":"/bin/bash"},"ancestors":[{"pid":700,"name":"zsh"},{"pid":1,"name":"login"}]}`
	// The pane's zsh with a job stopped or in the background: pid 777 has children.
	jobFG   = `{"foreground":{"pid":777,"name":"zsh","argv0":"-zsh","path":"/bin/zsh"},"ancestors":[{"pid":1,"name":"login"}]}`
	claudFG = `{"foreground":{"pid":800,"name":"2.1.326","argv0":"claude","path":"/Users/x/.local/share/claude/versions/2.1.326","invoked_path":"/Users/x/.local/bin/claude"}}`
	vimFG   = `{"foreground":{"pid":900,"name":"vim","argv0":"vim","path":"/usr/bin/vim"}}`
	// A --keep-open pane whose Claude quit: nothing runs, only how it exited.
	exitedFG = `{"last_exit":{"exit_code":0},"home":"/Users/x"}`
)

// resumeStub is a rex that answers from files in bin: sessions.json for `session ls`,
// blocks.<session> for `block ls`, fg.<block> for `process` (none: the call fails) and
// ps.<block> for `program_status`. It records what is typed and which keys are pressed.
func resumeStub(t *testing.T, bin string) {
	t.Helper()
	body := `#!/bin/sh
D=` + bin + `
[ "$1" = "--autostart=false" ] || echo "no --autostart=false: $*" >> $D/rex.bad
shift
echo "$@" >> $D/rex.calls
case "$1" in
session) [ "$2" = ls ] && cat $D/sessions.json;;
server) [ "$2" = status ] && cat $D/status.json;;
block)
  case "$2" in
  ls) cat "$D/blocks.${4#session:}" 2>/dev/null || echo '{"blocks":[]}';;
  close) echo "$@" >> $D/rex.closed;;
  call)
    b=${6#block:}
    if [ "$7" = process ]; then
      [ -f $D/fg.$b ] || { echo "no such block" >&2; exit 1; }
      cat $D/fg.$b
    elif [ -f $D/ps.$b ]; then cat $D/ps.$b; else echo '{"records":[]}'; fi;;
  esac;;
send)
  cat >> $D/rex.typed
  printf '%s\n' "$@" > $D/rex.sendargs
  # A flip file changes what runs in the pane once text has been typed.
  for f in $D/flip.*; do [ -f "$f" ] && mv "$f" "$D/fg.${f##*/flip.}"; done; true;;
send-key)
  echo "$@" >> $D/rex.keys
  # Enter starts Claude in the pane, unless the shell swallows the line (nostart).
  if [ "$4" = enter ] && [ ! -f $D/nostart ]; then cp $D/claude.fg "$D/fg.${3#--block=block:}"; fi;;
run)
  printf '%s\n' "$@" > $D/rex.runargs
  [ -f $D/run.fail ] && { echo "no server" >&2; exit 1; }
  [ -f $D/nostart ] || cp $D/claude.fg $D/fg.n2
  echo '{"anchor_id":"x","block_ids":["block:n2"]}';;
new)
  printf '%s\n' "$@" > $D/rex.newargs
  [ -f $D/new.fail ] && { echo "no server" >&2; exit 1; }
  echo '{"session_id":"session:new-0001","initial_windows":[{"window_id":"w","block_ids":["block:n1"]}]}';;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	rexSessions(t, bin, map[string]string{rsSess: "x"})
	writeFile(t, filepath.Join(bin, "claude.fg"), claudFG)
	answerEnterDelay = 0
	oldKids, oldWait, oldPoll := hasChildren, resumeStartWait, resumeStartPoll
	hasChildren = func(_ context.Context, pid int) (bool, error) { return pid == 777, nil }
	resumeStartWait, resumeStartPoll = 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { hasChildren, resumeStartWait, resumeStartPoll = oldKids, oldWait, oldPoll })
}

func rexSessions(t *testing.T, bin string, labels map[string]string) {
	t.Helper()
	var l struct {
		Sessions []map[string]string `json:"sessions"`
	}
	for id, label := range labels {
		l.Sessions = append(l.Sessions, map[string]string{"session_id": id, "label": label})
	}
	b, _ := json.Marshal(l)
	writeFile(t, filepath.Join(bin, "sessions.json"), string(b))
}

// panes gives the session terminal blocks, each with its foreground process.
func panes(t *testing.T, bin, sess string, fg map[string]string) {
	t.Helper()
	var ids []string
	for id, proc := range fg {
		ids = append(ids, `{"block_id":"`+id+`","flavor":"com.superlogical.terminal.shell"}`)
		if proc != "" {
			writeFile(t, filepath.Join(bin, "fg."+strings.TrimPrefix(id, "block:")), proc)
		}
	}
	writeFile(t, filepath.Join(bin, "blocks."+strings.TrimPrefix(sess, "session:")), `{"blocks":[`+strings.Join(ids, ",")+`]}`)
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	foldBack(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// metaCard writes a card with the given frontmatter lines after name and mode.
func metaCard(t *testing.T, name, mode string, meta ...string) {
	t.Helper()
	txt := "---\nname: " + name + "\nmode: " + mode + "   # keep me\n" + strings.Join(meta, "\n") + "\n---\n## Goal\nG\n"
	writeFile(t, cardPath(name), txt)
}

func readFile(path string) string { b, _ := os.ReadFile(path); return string(b) }

// transcript writes a Claude Code transcript whose opening context names the Rex session.
func transcript(t *testing.T, project, id, entrypoint, cwd, rexSession string, at time.Time) string {
	t.Helper()
	rec := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
	lines := []string{
		rec(map[string]any{"type": "session-owner", "sessionId": id}),
		rec(map[string]any{"type": "attachment", "entrypoint": entrypoint, "isSidechain": false, "cwd": cwd, "sessionId": id,
			"attachment": map[string]any{"content": "You're running in a Rex terminal.\n\n```text\nREX_SESSION=" + rexSession + "\nREX_BLOCK=block:z\n```"}}),
	}
	p := filepath.Join(userHome(), ".claude", "projects", project, id+".jsonl")
	writeFile(t, p, strings.Join(lines, "\n")+"\n")
	os.Chtimes(p, at, at)
	return p
}

func resumeDo(args ...string) (string, int) { return do(append([]string{"resume"}, args...)...) }

func noTyping(t *testing.T, bin string) {
	t.Helper()
	for _, f := range []string{"rex.typed", "rex.keys", "rex.newargs", "rex.runargs", "rex.closed"} {
		if _, err := os.Stat(filepath.Join(bin, f)); err == nil {
			t.Errorf("%s written: %q", f, readFile(filepath.Join(bin, f)))
		}
	}
}

func TestHookRecordsClaudeSession(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("REX_SESSION", rsSess)
	metaCard(t, "tr", "background", "tier: 2", "dir: "+th, "rex_session: "+rsSess, "links: EMI-1")
	before := readFile(cardPath("tr"))
	tp := transcript(t, "-start", rsID, "cli", "/start/dir", rsSess, time.Now())
	if o := hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"go","cwd":"/later/dir","session_id":"`+rsID+`","transcript_path":"`+tp+`"}`); o != "" {
		t.Errorf("output %q", o)
	}
	after := readFile(cardPath("tr"))
	want := strings.Replace(before, "links: EMI-1\n", "links: EMI-1\nclaude_session: "+rsID+"\nclaude_cwd: /start/dir\n", 1)
	if after != want && !(dbExists() && sameCard(after, want)) {
		t.Fatalf("card:\n%s\nwant:\n%s", after, want)
	}
	if evs := readEvents(); len(evs) != 1 || evs[0].Track != "tr" {
		t.Errorf("events: %+v", evs)
	}

	// The same session again leaves the card alone, even from another directory.
	st, _ := os.Stat(cardPath("tr"))
	os.Chtimes(cardPath("tr"), st.ModTime().Add(-time.Hour), st.ModTime().Add(-time.Hour))
	hook(t, `{"hook_event_name":"Stop","cwd":"/other","session_id":"`+rsID+`"}`)
	if st2, _ := os.Stat(cardPath("tr")); !st2.ModTime().Before(st.ModTime()) {
		t.Error("card rewritten for an unchanged session")
	}

	// A new session without a transcript takes the hook's cwd and replaces both values in place.
	hook(t, `{"hook_event_name":"Stop","cwd":"/new/cwd","session_id":"abc-2"}`)
	c := loadCardMust(t, "tr")
	if c.Meta["claude_session"] != "abc-2" || c.Meta["claude_cwd"] != "/new/cwd" || c.mode() != "background" || c.Meta["tier"] != "2" || c.Meta["links"] != "EMI-1" {
		t.Errorf("meta: %+v", c.Meta)
	}
	if n := strings.Count(readFile(cardPath("tr")), "claude_session:"); n != 1 {
		t.Errorf("%d claude_session lines", n)
	}
	if !dbExists() && !strings.Contains(readFile(cardPath("tr")), "mode: background   # keep me") {
		t.Error("mode comment lost")
	}
}

func TestHookClaudeSessionGuards(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", rsSess)
	metaCard(t, "tr", "focus", "dir: "+th, "rex_session: "+rsSess)
	before := readFile(cardPath("tr"))
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-cli")
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"x","cwd":"/c","session_id":"headless-1"}`)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	for _, in := range []string{
		`{"hook_event_name":"Stop","cwd":"/c","session_id":"../../etc"}`,
		`{"hook_event_name":"Stop","cwd":"/c","session_id":"a\nmode: parked"}`,
		`{"hook_event_name":"Stop","cwd":"/c"}`,
		`{"hook_event_name":"PreToolUse","cwd":"/c","session_id":"ok-1"}`,
		`not json`,
	} {
		hook(t, in)
	}
	if got := readFile(cardPath("tr")); got != before {
		t.Errorf("card changed:\n%s", got)
	}
	// Another Rex session's Claude is not this card's.
	t.Setenv("REX_SESSION", "session:other")
	hook(t, `{"hook_event_name":"Stop","cwd":"/c","session_id":"ok-1"}`)
	if got := readFile(cardPath("tr")); got != before {
		t.Errorf("card changed by another session:\n%s", got)
	}
	// A cwd that cannot sit on a card is left empty rather than mangled.
	t.Setenv("REX_SESSION", rsSess)
	hook(t, `{"hook_event_name":"Stop","cwd":"/a # b","session_id":"ok-2"}`)
	if c := loadCardMust(t, "tr"); c.Meta["claude_session"] != "ok-2" || c.Meta["claude_cwd"] != "" || c.mode() != "focus" {
		t.Errorf("meta: %+v", c.Meta)
	}
	// A card closed meanwhile is not brought back.
	foldBack(t)
	os.Remove(cardPath("tr"))
	if err := recordClaudeSession(&Card{Name: "tr", Meta: map[string]string{}}, "ok-3", "", "/c"); err == nil {
		t.Error("no error for a closed card")
	}
	if _, err := os.Stat(cardPath("tr")); err == nil {
		t.Error("closed card recreated")
	}
}

func TestResumeTypesIntoIdleShell(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	dir := filepath.Join(th, "it's a dir")
	os.MkdirAll(dir, 0o755)
	metaCard(t, "x", "focus", "dir: "+th, "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+dir)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	out, code := resumeDo("x")
	line := "cd '" + th + "/it'\\''s a dir' && env TRACKS_HOME=" + th + " claude --resume " + rsID + " --name x"
	if code != 0 || out != "x: resumed (in "+rsBlock+": "+line+")\n" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := readFile(filepath.Join(bin, "rex.typed")); got != line {
		t.Errorf("typed %q", got)
	}
	if got := readFile(filepath.Join(bin, "rex.sendargs")); got != "send\n--session="+rsSess+"\n--block="+rsBlock+"\n" {
		t.Errorf("send args %q", got)
	}
	if got := readFile(filepath.Join(bin, "rex.keys")); got != "send-key --session="+rsSess+" --block="+rsBlock+" ctrl+u\nsend-key --session="+rsSess+" --block="+rsBlock+" enter\n" {
		t.Errorf("keys %q", got)
	}
	if b := readFile(filepath.Join(bin, "rex.bad")); b != "" {
		t.Error(b)
	}
}

func TestResumeQuotedLineRunsInAShell(t *testing.T) {
	th, _ := setup(t)
	dir := filepath.Join(th, "a b'c $(touch pwned) `x` ; \"q\"")
	os.MkdirAll(dir, 0o755)
	line := "cd " + shellQuote(dir) + " && " + shellJoin([]string{"printf", "%s|", "--resume", "=x", "a'b"})
	got, err := run("", "/bin/sh", "-c", line+" && pwd")
	if err != nil || got != "--resume|=x|a'b|"+dir {
		t.Errorf("%q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(th, "pwned")); err == nil {
		t.Error("command substitution ran")
	}
}

func TestResumeRefusesBusyPanes(t *testing.T) {
	for name, c := range map[string]struct {
		fg, ps, why string
	}{
		"claude":         {claudFG, "", "Claude already runs in " + rsBlock},
		"vim":            {vimFG, "", "no idle shell: vim runs in " + rsBlock},
		"unknown":        {"", "", "cannot tell what runs in " + rsBlock},
		"no foreground":  {`{"foreground":null}`, "", "cannot tell what runs in " + rsBlock},
		"exit and child": {`{"last_exit":{"exit_code":0},"child":{"pid":5,"name":"sleep"}}`, "", "cannot tell what runs in " + rsBlock},
		"login":          {`{"foreground":{"pid":5,"name":"login","argv0":"login"}}`, "", "no idle shell: login runs in " + rsBlock},
		"shell script":   {scriptFG, "", "no idle shell: it runs a shell script in " + rsBlock},
		"shell job":      {jobFG, "", "no idle shell: its shell has a job running or stopped in " + rsBlock},
		"node as claude": {`{"foreground":{"pid":42,"name":"node","argv0":"node"}}`, `{"records":[{"state":"idle","effective_app":"claude-code","owner":{"pid":42}}]}`, "Claude already runs in " + rsBlock},
	} {
		t.Run(name, func(t *testing.T) {
			th, bin := setup(t)
			resumeStub(t, bin)
			metaCard(t, "x", "focus", "dir: "+th, "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
			panes(t, bin, rsSess, map[string]string{rsBlock: c.fg})
			if c.ps != "" {
				writeFile(t, filepath.Join(bin, "ps.r1"), c.ps)
			}
			out, code := resumeDo("x")
			want := 4 // the agent is not back
			if strings.HasPrefix(c.why, "Claude already runs") {
				want = 0
			}
			if code != want || !strings.HasPrefix(out, "x: skipped ("+c.why) {
				t.Errorf("exit %d: %s", code, out)
			}
			noTyping(t, bin)
		})
	}
}

func TestResumeClaudeInAnotherPaneSkips(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG, "block:r2": claudFG})
	if out, _ := resumeDo("x"); out != "x: skipped (Claude already runs in block:r2)\n" {
		t.Errorf("%s", out)
	}
	noTyping(t, bin)
}

func TestResumeDryRun(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	metaCard(t, "gone", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	before := readFile(cardPath("gone"))
	out, code := resumeDo("--all", "--dry-run")
	want := "gone: would recreate (new Rex session gone in " + th + ": env TRACKS_HOME=" + th + " claude --resume " + rsID + " --name gone)\n" +
		"x: would resume (in " + rsBlock + ": cd " + th + " && env TRACKS_HOME=" + th + " claude --resume " + rsID + " --name x)\n"
	if code != 0 || out != want {
		t.Errorf("exit %d:\n%s\nwant:\n%s", code, out, want)
	}
	noTyping(t, bin)
	if readFile(cardPath("gone")) != before {
		t.Error("dry run wrote the card")
	}
}

func TestResumeFallsBackToTranscript(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	good, other := filepath.Join(th, "good"), filepath.Join(th, "other")
	os.MkdirAll(good, 0o755)
	os.MkdirAll(other, 0o755)
	now := time.Now()
	transcript(t, "-good", "aaaa-good", "cli", good, rsSess, now.Add(-time.Hour))
	transcript(t, "-older", "aaaa-older", "cli", other, rsSess, now.Add(-2*time.Hour))
	transcript(t, "-headless", "aaaa-headless", "sdk-cli", other, rsSess, now.Add(-time.Minute))
	transcript(t, "-prefix", "aaaa-prefix", "cli", other, rsSess+"0", now.Add(-time.Minute))
	transcript(t, "-good/aaaa-good/subagents", "agent-1", "cli", other, rsSess, now)
	metaCard(t, "x", "focus", "dir: "+th, "rex_session: "+rsSess)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	out, code := resumeDo("x")
	if code != 0 || !strings.Contains(out, "resumed") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := readFile(filepath.Join(bin, "rex.typed")); got != "cd "+good+" && env TRACKS_HOME="+th+" claude --resume aaaa-good --name x" {
		t.Errorf("typed %q", got)
	}
	if c := loadCardMust(t, "x"); c.Meta["claude_session"] != "aaaa-good" || c.Meta["claude_cwd"] != good || c.mode() != "focus" {
		t.Errorf("meta %+v", c.Meta)
	}
}

func TestResumeCwdFromOwnTranscript(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	transcript(t, "-p", rsID, "cli", th, "session:whatever", time.Now())
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	if out, _ := resumeDo("x"); !strings.Contains(out, "resumed (in "+rsBlock+": cd "+th+" && ") {
		t.Errorf("%s", out)
	}
}

func TestResumeRecreatesGoneSession(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "waiting", "dir: /elsewhere", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	rexSessions(t, bin, map[string]string{"session:unrelated": "y"})
	out, code := resumeDo("x")
	if code != 0 || out != "x: recreated (session:new-0001 in "+th+": env TRACKS_HOME="+th+" claude --resume "+rsID+" --name x)\n" {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := strings.Join([]string{"new", "x", "--cwd=" + th, "--keep-open", "--interactive", "--json", "--", "env", "TRACKS_HOME=" + th, "claude", "--resume", rsID, "--name", "x"}, "\n") + "\n"
	if got := readFile(filepath.Join(bin, "rex.newargs")); got != want {
		t.Errorf("new args:\n%s", got)
	}
	if c := loadCardMust(t, "x"); c.Meta["rex_session"] != "session:new-0001" || c.mode() != "waiting" || c.Meta["dir"] != "/elsewhere" {
		t.Errorf("meta %+v", c.Meta)
	}
	if _, err := os.Stat(filepath.Join(bin, "rex.typed")); err == nil {
		t.Error("typed into a pane")
	}
}

func TestResumeAdoptsSessionByLabel(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	if out, code := resumeDo("x"); code != 0 || !strings.HasPrefix(out, "x: resumed (in "+rsBlock) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if c := loadCardMust(t, "x"); c.Meta["rex_session"] != rsSess {
		t.Errorf("meta %+v", c.Meta)
	}
}

func TestResumeFailuresExitNonZero(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	rexSessions(t, bin, map[string]string{})
	writeFile(t, filepath.Join(bin, "new.fail"), "")
	out, code := resumeDo("x")
	if code == 0 || !strings.Contains(out, "x: failed (could not start a Rex session") || !strings.Contains(out, "1 of 1 tracks failed") {
		t.Errorf("exit %d: %s", code, out)
	}
	if c := loadCardMust(t, "x"); c.Meta["rex_session"] != "session:dead" {
		t.Errorf("card changed: %+v", c.Meta)
	}
}

func TestResumeStopsWhenPaneChangesBeforeEnter(t *testing.T) {
	for name, after := range map[string]string{
		"vim": vimFG,
		// The same pid, now with a shell above it: the pane's shell is running a script.
		"script": `{"foreground":{"pid":700,"name":"zsh","argv0":"zsh"},"ancestors":[{"pid":600,"name":"bash"},{"pid":1,"name":"login"}]}`,
		// The same shell, now with a job.
		"job": strings.Replace(shellFG, `"pid":700`, `"pid":777`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			th, bin := setup(t)
			resumeStub(t, bin)
			metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
			panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
			writeFile(t, filepath.Join(bin, "flip.r1"), after)
			out, code := resumeDo("x")
			if code == 0 || !strings.Contains(out, "x: failed (typed into "+rsBlock+" but not run") {
				t.Errorf("exit %d: %s", code, out)
			}
			if keys := readFile(filepath.Join(bin, "rex.keys")); strings.Contains(keys, "enter") {
				t.Errorf("enter pressed: %q", keys)
			}
		})
	}
}

// A shell at a continuation prompt (zsh's dquote>) takes the line as more input: Enter
// goes through but Claude never starts, and that is a failure, not "resumed".
func TestResumeFailsWhenClaudeDoesNotStart(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	writeFile(t, filepath.Join(bin, "nostart"), "")
	start := time.Now()
	out, code := resumeDo("x")
	if code == 0 || !strings.Contains(out, "x: failed (ran the command in "+rsBlock+" but Claude was not in front within 300ms") {
		t.Errorf("exit %d: %s", code, out)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("waited %s", d)
	}
	if c := loadCardMust(t, "x"); c.Meta["claude_session"] != rsID {
		t.Errorf("meta %+v", c.Meta)
	}
}

func TestResumePicksAgentsShell(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG, "block:r2": shellFG})
	if out, _ := resumeDo("x", "--dry-run"); out != "x: skipped (2 idle shells and none known as the agent's)\n" {
		t.Errorf("%s", out)
	}
	appendEvent(Event{At: time.Now(), Event: "stop", RexSession: rsSess, RexBlock: "block:r2"})
	if out, _ := resumeDo("x", "--dry-run"); !strings.HasPrefix(out, "x: would resume (in block:r2:") {
		t.Errorf("%s", out)
	}
	noTyping(t, bin)
}

// Claude quit in its --keep-open pane, so nothing runs there: a new pane resumes it beside
// the exited one, which is closed once Claude is in front.
func TestResumeReplacesExitedPane(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: exitedFG})
	run := "in " + th + ": env TRACKS_HOME=" + th + " claude --resume " + rsID + " --name x"
	if out, _ := resumeDo("x", "--dry-run"); out != "x: would resume (in a new pane in place of exited "+rsBlock+", "+run+")\n" {
		t.Errorf("dry run: %s", out)
	}
	noTyping(t, bin)
	appendEvent(Event{At: time.Now(), Event: "stop", RexSession: rsSess, RexBlock: rsBlock})
	out, code := resumeDo("x")
	if code != 0 || out != "x: resumed (in block:n2, in place of exited "+rsBlock+", "+run+")\n" {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := strings.Join([]string{"run", "--session=" + rsSess, "--block=" + rsBlock, "--split=right", "--focus=false",
		"--keep-open", "--interactive", "--cwd=" + th, "--json", "--", "env", "TRACKS_HOME=" + th, "claude", "--resume", rsID, "--name", "x"}, "\n") + "\n"
	if got := readFile(filepath.Join(bin, "rex.runargs")); got != want {
		t.Errorf("run args:\n%s", got)
	}
	if got := readFile(filepath.Join(bin, "rex.closed")); got != "block close --session="+rsSess+" "+rsBlock+"\n" {
		t.Errorf("closed %q", got)
	}
	for _, f := range []string{"rex.typed", "rex.keys", "rex.newargs"} {
		if _, err := os.Stat(filepath.Join(bin, f)); err == nil {
			t.Errorf("%s written", f)
		}
	}
	if c := loadCardMust(t, "x"); c.Meta["claude_session"] != rsID || c.Meta["claude_cwd"] != th {
		t.Errorf("meta %+v", c.Meta)
	}
}

// An exited pane no hook ran in may be any finished --keep-open command, so its output stays.
func TestResumeKeepsExitedPaneNotKnownAsAgents(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: exitedFG, "block:r2": vimFG})
	out, code := resumeDo("x")
	if code != 0 || !strings.HasPrefix(out, "x: resumed (in block:n2, in place of exited "+rsBlock) ||
		!strings.HasSuffix(out, ", leaving exited "+rsBlock+" open: not known as the agent's pane)\n") {
		t.Errorf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(bin, "rex.closed")); err == nil {
		t.Error("closed a pane not known as the agent's")
	}
}

// Claude not coming up in the new pane keeps the exited pane, so nothing is lost.
func TestResumeExitedPaneKeptWhenClaudeDoesNotStart(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: exitedFG})
	writeFile(t, filepath.Join(bin, "nostart"), "")
	out, code := resumeDo("x")
	if code == 0 || !strings.Contains(out, "x: failed (opened block:n2 beside exited "+rsBlock+" but Claude was not in front") {
		t.Errorf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(bin, "rex.closed")); err == nil {
		t.Error("closed the exited pane")
	}
	writeFile(t, filepath.Join(bin, "run.fail"), "")
	if out, code := resumeDo("x"); code == 0 || !strings.Contains(out, "x: failed (could not open a pane beside "+rsBlock) {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestResumeExitedPaneAmongShells(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: exitedFG, "block:r2": shellFG})
	if out, _ := resumeDo("x", "--dry-run"); out != "x: skipped (2 idle shells or exited panes and none known as the agent's)\n" {
		t.Errorf("%s", out)
	}
	appendEvent(Event{At: time.Now(), Event: "stop", RexSession: rsSess, RexBlock: rsBlock})
	if out, _ := resumeDo("x", "--dry-run"); !strings.HasPrefix(out, "x: would resume (in a new pane in place of exited "+rsBlock) {
		t.Errorf("%s", out)
	}
	// Claude running anywhere in the session still wins over an exited pane.
	panes(t, bin, rsSess, map[string]string{rsBlock: exitedFG, "block:r2": claudFG})
	if out, _ := resumeDo("x"); out != "x: skipped (Claude already runs in block:r2)\n" {
		t.Errorf("%s", out)
	}
	noTyping(t, bin)
}

func TestResumeAll(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	metaCard(t, "b", "parked", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	metaCard(t, "c", "background", "rex_session:")
	rexSessions(t, bin, map[string]string{rsSess: "a"})
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	out, code := resumeDo("--all")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "a: resumed (") || lines[1] != "b: skipped (parked: resume it by name)" ||
		lines[2] != "c: nothing to resume (no claude_session or rex_session on the card)" {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestResumeUsage(t *testing.T) {
	setup(t)
	for _, args := range [][]string{{}, {"a", "--all"}, {"a", "b"}, {"--force", "a"}, {"../x"}, {"missing"}} {
		if out, code := resumeDo(args...); code == 0 {
			t.Errorf("%q accepted: %s", args, out)
		}
	}
}

func TestResumeRexDownSkips(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	os.Remove(filepath.Join(bin, "sessions.json"))
	if out, code := resumeDo("x"); code != 3 || !strings.HasPrefix(out, "x: skipped (could not ask Rex about "+rsSess) {
		t.Errorf("exit %d: %s", code, out)
	}
	noTyping(t, bin)
}

func TestResumeAmbiguousSessionExits2(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: eb6e0e", "claude_session: "+rsID, "claude_cwd: "+th)
	rexSessions(t, bin, map[string]string{"session:aaaa-11eb6e0e": "a", "session:bbbb-22eb6e0e": "b"})
	if out, code := resumeDo("x"); code != 2 || !strings.HasPrefix(out, "x: skipped (could not ask Rex about eb6e0e") {
		t.Errorf("exit %d: %s", code, out)
	}
	noTyping(t, bin)
}

func TestResumeSkipThatLeavesNoAgentExits4(t *testing.T) {
	_, bin := setup(t)
	resumeStub(t, bin)
	gone := filepath.Join(t.TempDir(), "gone")
	metaCard(t, "x", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+gone)
	rexSessions(t, bin, map[string]string{"session:unrelated": "y"})
	if out, code := resumeDo("x"); code != 4 || !strings.HasPrefix(out, "x: skipped (the Claude session's directory is gone") {
		t.Errorf("exit %d: %s", code, out)
	}
	noTyping(t, bin)
}

func TestResumeNothingToResumeExits2(t *testing.T) {
	setup(t)
	metaCard(t, "x", "focus", "rex_session:")
	out, code := resumeDo("x")
	if code != 2 || out != "x: nothing to resume (no claude_session or rex_session on the card)\n" {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestResumeOtherSkipsExit0(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: claudFG})
	if out, code := resumeDo("x"); code != 0 || !strings.HasPrefix(out, "x: skipped (Claude already runs") {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestResumeAllExits0WhenSomeSkipOrHaveNothing(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "a", "focus", "rex_session:")
	metaCard(t, "b", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: claudFG})
	out, code := resumeDo("--all")
	if code != 0 || !strings.Contains(out, "a: nothing to resume") || !strings.Contains(out, "b: skipped (Claude already runs") {
		t.Errorf("exit %d: %s", code, out)
	}
	os.Remove(filepath.Join(bin, "sessions.json"))
	if out, code := resumeDo("--all"); code != 0 || !strings.Contains(out, "b: skipped (could not ask Rex") {
		t.Errorf("rex down: exit %d: %s", code, out)
	}
	if strings.Contains(rexCalls(bin), "session.select") {
		t.Errorf("--all selected a session:\n%s", rexCalls(bin))
	}
}

func TestResumeSelectsWhatItBroughtBack(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	rexSessions(t, bin, map[string]string{"session:unrelated": "y"})
	if out, code := resumeDo("x"); code != 0 || !strings.HasPrefix(out, "x: recreated") {
		t.Fatalf("exit %d: %s", code, out)
	}
	calls := rexCalls(bin)
	sel, focus := strings.Index(calls, "do session.select session_id=session:new-0001"), strings.Index(calls, "block focus -s session:new-0001 block:n1")
	if sel < 0 || focus < sel || strings.Index(calls, "new x") > sel {
		t.Errorf("want select then focus of the new session after `new`:\n%s", calls)
	}
}

func TestResumeInPlaceSelectsSession(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	if out, code := resumeDo("x"); code != 0 || !strings.HasPrefix(out, "x: resumed") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if calls := rexCalls(bin); !strings.Contains(calls, "do session.select session_id="+rsSess) {
		t.Errorf("session not selected:\n%s", calls)
	}
}

func TestResumeDryRunSelectsNothing(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: session:dead", "claude_session: "+rsID, "claude_cwd: "+th)
	rexSessions(t, bin, map[string]string{})
	if _, code := resumeDo("x", "--dry-run"); code != 0 || strings.Contains(rexCalls(bin), "session.select") {
		t.Errorf("exit %d:\n%s", code, rexCalls(bin))
	}
}
