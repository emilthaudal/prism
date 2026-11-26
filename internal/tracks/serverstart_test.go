package tracks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// serverStart sets up a rex that reports server instance inst and makes the waits short.
func serverStart(t *testing.T, bin, inst string) {
	t.Helper()
	resumeStub(t, bin)
	writeFile(t, filepath.Join(bin, "status.json"), `{"pid":1,"instance_id":"`+inst+`"}`)
	oldWait, oldPoll, oldSettle, oldBetween := serverWait, serverPoll, resumeSettle, betweenPasses
	serverWait, serverPoll, resumeSettle, betweenPasses = 200*time.Millisecond, 20*time.Millisecond, 0, func() {}
	t.Cleanup(func() { serverWait, serverPoll, resumeSettle, betweenPasses = oldWait, oldPoll, oldSettle, oldBetween })
}

func TestResumeOncePerServerInstance(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	metaCard(t, "p", "parked", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	out, code := resumeDo("--all", "--once-per-server")
	if code != 0 || !strings.Contains(out, "Rex server inst-1 started") || !strings.Contains(out, "\na: resumed (") ||
		!strings.Contains(out, "\np: skipped (parked") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	// A config reload loads init.lua again under the same server: nothing is typed.
	os.Remove(filepath.Join(bin, "rex.typed"))
	os.Remove(filepath.Join(bin, "rex.keys"))
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	if out, code := resumeDo("--all", "--once-per-server"); code != 0 || out != "already resumed for Rex server inst-1\n" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	noTyping(t, bin)
	// A restarted server has a new instance: it resumes again and forgets the old one.
	writeFile(t, filepath.Join(bin, "status.json"), `{"instance_id":"inst-2"}`)
	if out, code := resumeDo("--all", "--once-per-server"); code != 0 || !strings.Contains(out, "\na: resumed (") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	ents, _ := os.ReadDir(filepath.Join(th, "resumed"))
	if len(ents) != 1 || ents[0].Name() != "inst-2" {
		t.Errorf("resumed dir: %v", ents)
	}
}

func TestResumeOncePerServerRetriesShellNotReady(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	// The restored pane does not say what runs in it yet; by the next pass its shell is up.
	panes(t, bin, rsSess, map[string]string{rsBlock: ""})
	passes := 0
	betweenPasses = func() {
		passes++
		writeFile(t, filepath.Join(bin, "fg."+strings.TrimPrefix(rsBlock, "block:")), shellFG)
	}
	out, code := resumeDo("--all", "--once-per-server")
	if code != 0 || passes != 1 || !strings.Contains(out, "\na: resumed (") || strings.Contains(out, "skipped") {
		t.Fatalf("exit %d, %d waits:\n%s", code, passes, out)
	}
}

func TestResumeOncePerServerGivesUpAfterPasses(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: vimFG})
	passes := 0
	betweenPasses = func() { passes++ }
	out, code := resumeDo("--all", "--once-per-server")
	if code != 0 || passes != resumePasses-1 || !strings.Contains(out, "\na: skipped (no idle shell: vim runs") {
		t.Fatalf("exit %d, %d waits:\n%s", code, passes, out)
	}
	noTyping(t, bin)
}

func TestManualResumeRefusesWhileAutomaticRunHoldsLock(t *testing.T) {
	th, bin := setup(t)
	resumeStub(t, bin)
	metaCard(t, "x", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: shellFG})
	unlock, err := lockResume()
	if err != nil {
		t.Fatal(err)
	}
	out, code := resumeDo("x")
	if code != 4 || out != "x: skipped ("+errResumeBusy.Error()+")\n" {
		t.Fatalf("by name: exit %d:\n%s", code, out)
	}
	out, code = resumeDo("--all")
	if code != 1 || !strings.Contains(out, "skipped (another resume is running") {
		t.Fatalf("--all: exit %d:\n%s", code, out)
	}
	noTyping(t, bin)
	unlock()
	if out, code := resumeDo("x"); code != 0 || !strings.Contains(out, "x: resumed (") {
		t.Fatalf("after unlock: exit %d:\n%s", code, out)
	}
}

func TestOncePerServerTrimsResumeLog(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	var old strings.Builder
	for i := 1; i <= resumeLogKeep+50; i++ {
		old.WriteString("old line " + strconv.Itoa(i) + "\n")
	}
	logPath := filepath.Join(th, "resume.log")
	writeFile(t, logPath, old.String())
	if out, code := resumeDo("--all", "--once-per-server"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	got := readFile(logPath)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) > resumeLogKeep+2 || lines[0] != "old line 51" || !strings.Contains(got, "Rex server inst-1 started") {
		t.Fatalf("%d lines, first %q", len(lines), lines[0])
	}
	ents, _ := os.ReadDir(th)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".resume.log.") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
	if st, err := os.Stat(logPath); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("resume.log mode: %v %v", st, err)
	}
}

func TestReloadDuringRunKeepsTheRunsLogLines(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	var old strings.Builder
	for i := 1; i <= resumeLogKeep+50; i++ {
		old.WriteString("old line " + strconv.Itoa(i) + "\n")
	}
	logPath := filepath.Join(th, "resume.log")
	writeFile(t, logPath, old.String())
	panes(t, bin, rsSess, map[string]string{rsBlock: ""})
	betweenPasses = func() {
		if out, code := resumeDo("--all", "--once-per-server"); code != 0 || !strings.Contains(out, "already resumed") {
			t.Errorf("reload: exit %d:\n%s", code, out)
		}
		writeFile(t, filepath.Join(bin, "fg."+strings.TrimPrefix(rsBlock, "block:")), shellFG)
	}
	if out, code := resumeDo("--all", "--once-per-server"); code != 0 || !strings.Contains(out, "\na: resumed (") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	got := readFile(logPath)
	if !strings.Contains(got, "Rex server inst-1 started") || !strings.Contains(got, "\na: resumed (") || !strings.Contains(got, "already resumed for Rex server inst-1") {
		t.Fatalf("log lost lines:\n%s", got)
	}
}

func TestResumeOncePerServerStopsWhenServerReplaced(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: ""})
	// While this run waits for the shell, the server restarts and the new server's run claims.
	betweenPasses = func() {
		if ok, err := claimInstance("inst-2"); !ok || err != nil {
			t.Fatalf("claim inst-2: %v %v", ok, err)
		}
		writeFile(t, filepath.Join(bin, "fg."+strings.TrimPrefix(rsBlock, "block:")), shellFG)
	}
	out, code := resumeDo("--all", "--once-per-server")
	if code != 0 || !strings.Contains(out, "Rex server inst-1 was replaced") || strings.Contains(out, "resumed (") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	noTyping(t, bin)
}

func TestResumeOncePerServerNoRetryWhenClaudeRuns(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: claudFG})
	passes := 0
	betweenPasses = func() { passes++ }
	out, code := resumeDo("--all", "--once-per-server")
	if code != 0 || passes != 0 || !strings.Contains(out, "\na: skipped (Claude already runs") {
		t.Fatalf("exit %d, %d waits:\n%s", code, passes, out)
	}
}

func TestResumeOncePerServerParkedBetweenPasses(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	metaCard(t, "a", "focus", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
	panes(t, bin, rsSess, map[string]string{rsBlock: ""})
	betweenPasses = func() {
		metaCard(t, "a", "parked", "rex_session: "+rsSess, "claude_session: "+rsID, "claude_cwd: "+th)
		writeFile(t, filepath.Join(bin, "fg."+strings.TrimPrefix(rsBlock, "block:")), shellFG)
	}
	if out, code := resumeDo("--all", "--once-per-server"); code != 0 || strings.Contains(out, "resumed (") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	noTyping(t, bin)
}

func TestResumeOncePerServerNoServer(t *testing.T) {
	th, bin := setup(t)
	serverStart(t, bin, "inst-1")
	// The error goes to resume.log, and stderr stays empty so resume.err holds no copy of it.
	logHas := func(want string) bool { return strings.Contains(readFile(filepath.Join(th, "resume.log")), want) }
	// No server: rex server status fails until the wait runs out.
	os.Remove(filepath.Join(bin, "status.json"))
	if out, code := resumeDo("--all", "--once-per-server"); code == 0 || strings.Contains(out, "did not answer within") || !logHas("did not answer within") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	writeFile(t, filepath.Join(bin, "status.json"), "")
	if out, code := resumeDo("--all", "--once-per-server"); code == 0 || strings.Contains(out, "no usable instance_id") || !logHas("no usable instance_id") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, inst := range []string{"../x", ""} {
		writeFile(t, filepath.Join(bin, "status.json"), `{"instance_id":"`+inst+`"}`)
		if out, code := resumeDo("--all", "--once-per-server"); code == 0 {
			t.Errorf("instance %q accepted: %s", inst, out)
		}
	}
}

func TestResumeOncePerServerUsage(t *testing.T) {
	setup(t)
	for _, args := range [][]string{{"--once-per-server"}, {"a", "--once-per-server"}, {"--all", "--once-per-server", "--dry-run"}} {
		if out, code := resumeDo(args...); code == 0 {
			t.Errorf("%q accepted: %s", args, out)
		}
	}
}
