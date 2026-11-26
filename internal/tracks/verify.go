package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	verifyUsage = "usage: tracks verify NAME [--timeout D]   (a Claude Code Stop hook: reads the hook's JSON on stdin)"

	defaultVerifyTimeout = 10 * time.Minute
	// hookSlack is how much longer Claude Code waits for the hook than the hook waits for the
	// checks, so the hook always gets to record a timeout itself.
	hookSlack       = time.Minute
	verifyTailLines = 40
	verifyKeepBytes = 64 << 10
	verifiedKey     = "verified"
	exitBlockStop   = exitCode(2) // Claude Code's Stop hook code for "keep working", with stderr as the reason
)

// lefthookConfigs are the files lefthook reads its hooks from, in a repo's root; the -local
// ones overlay the main config, so a pre-push hook in either counts.
var lefthookConfigs = []string{
	"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml",
	"lefthook.toml", ".lefthook.toml", "lefthook.json", ".lefthook.json",
	"lefthook-local.yml", "lefthook-local.yaml", ".lefthook-local.yml", ".lefthook-local.yaml",
	"lefthook-local.toml", ".lefthook-local.toml", "lefthook-local.json", ".lefthook-local.json",
}

var (
	prePushYAML = regexp.MustCompile(`(?m)^["']?pre-push["']?\s*:`)
	prePushTOML = regexp.MustCompile(`(?m)^\s*\[\s*["']?pre-push["']?\s*[\].]`)
	prePushJSON = regexp.MustCompile(`"pre-push"\s*:`)
)

// stopInput is the part of Claude Code's Stop hook input that verify reads.
type stopInput struct {
	StopHookActive bool   `json:"stop_hook_active"`
	LastAnswer     string `json:"last_assistant_message"`
}

// verifyStep is one command verify runs, in dir.
type verifyStep struct {
	label string
	dir   string
	argv  []string
}

// gitRoot is the top of the git work tree dir is in, or dir itself outside one.
func gitRoot(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	root := strings.TrimSpace(string(out))
	if st, serr := os.Stat(root); err != nil || !filepath.IsAbs(root) || serr != nil || !st.IsDir() {
		return dir
	}
	return root
}

// verifyChecks are what tracks verify runs for a thread working in dir: lefthook's pre-push,
// in the repo's root, when that root has a lefthook config with that hook, then the card's
// verify: command in dir. why is set when a pre-push hook exists but there is no lefthook to
// run it.
func verifyChecks(dir, verify string) (checks []verifyStep, why string) {
	root := gitRoot(dir)
	for _, f := range lefthookConfigs {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		re := prePushYAML
		switch filepath.Ext(f) {
		case ".toml":
			re = prePushTOML
		case ".json":
			re = prePushJSON
		}
		if !re.Match(b) {
			continue
		}
		bin := filepath.Join(root, "node_modules", ".bin", "lefthook")
		if _, err := os.Stat(bin); err != nil {
			if bin, err = exec.LookPath("lefthook"); err != nil {
				return nil, filepath.Join(root, f) + " has a pre-push hook, but lefthook is in neither node_modules/.bin nor PATH"
			}
		}
		checks = append(checks, verifyStep{"lefthook pre-push", root, []string{bin, "run", "pre-push"}})
		break
	}
	if verify != "" {
		checks = append(checks, verifyStep{"verify: " + verify, dir, []string{"/bin/sh", "-c", verify}})
	}
	return checks, ""
}

// tailBuffer keeps the last verifyKeepBytes written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - verifyKeepBytes; over > 0 {
		t.b = t.b[over:]
	}
	return len(p), nil
}

// lastLines are the last n lines without terminal escapes, which the agent would read as noise.
func (t *tailBuffer) lastLines(n int) string {
	lines := strings.Split(strings.TrimRight(ansi.Strip(string(t.b)), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// runCheck runs c in its dir until ctx ends, killing its whole process group then, so a test
// runner's children cannot outlive the hook.
func runCheck(ctx context.Context, c verifyStep, out io.Writer) error {
	cmd := exec.CommandContext(ctx, c.argv[0], c.argv[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = c.dir, out, out
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// verifyCmd is tracks verify, the Stop hook tracks spawn gives a thread's agent: it runs the
// thread's checks in the card's dir and records pass or fail on the card. A failure exits 2
// with the end of the output on stderr, which Claude Code hands back to the agent to keep it
// working; once Claude Code says the agent is already continuing for a stop hook, it records
// the result and lets the agent stop, so a check the agent cannot fix never loops.
func verifyCmd(args []string, in io.Reader, out, errw io.Writer) (err error) {
	pos, fl, err := leadFlags(args, "--timeout")
	if err != nil || len(pos) != 1 {
		return errors.New(verifyUsage)
	}
	timeout := defaultVerifyTimeout
	if v, ok := fl["--timeout"]; ok {
		if timeout, err = time.ParseDuration(v); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout is a duration such as 90s or 10m: %q", v)
		}
	}
	name := pos[0]
	if !validName.MatchString(name) {
		return fmt.Errorf("not a track name: %q", name)
	}
	var hook stopInput
	b, _ := io.ReadAll(in)
	_ = json.Unmarshal(b, &hook)

	// A hook that cannot find its card lets the agent stop: there is nothing to record on.
	if !storeOn() {
		fmt.Fprintln(errw, "tracks verify: "+needsStore+"; nothing checked")
		return nil
	}
	v, err := getView(name)
	if v.RenamedTo != "" {
		name = v.RenamedTo
		v, err = getView(name)
	}
	if err != nil {
		fmt.Fprintln(errw, "tracks verify: "+err.Error()+"; nothing checked")
		return nil
	}
	// A desk-started session has no REX_SESSION, so this Stop hook is where its where: is written.
	defer func() {
		if err != nil || hook.LastAnswer == "" {
			return
		}
		if c, e := loadCard(name); e == nil {
			if e = stopWhere(c, hook.LastAnswer); e != nil {
				fmt.Fprintln(errw, "tracks verify: could not write where: on "+name+"'s card: "+e.Error())
			}
		}
	}()
	dir := v.Track.Dir
	record := func(s string) {
		if err := setMetas(name, [][2]string{{verifiedKey, s}}); err != nil {
			fmt.Fprintln(errw, "tracks verify: could not write the result on "+name+"'s card: "+err.Error())
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339) + " in " + dir
	fail := func(why, tail string) error {
		note := ""
		if hook.StopHookActive {
			note = " (the agent stopped anyway: it was already sent back once this turn)"
		}
		record("fail " + stamp + ": " + oneLine(why) + note)
		if hook.StopHookActive {
			return nil
		}
		fmt.Fprintf(errw, "Verification failed in %s: %s. Fix it before you stop.\n", dir, why)
		if tail != "" {
			fmt.Fprintf(errw, "The last %d lines of output:\n%s\n", verifyTailLines, tail)
		}
		return exitBlockStop
	}
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		return fail("the card's dir is not a directory: "+dir, "")
	}
	checks, why := verifyChecks(dir, v.Track.Extra.Meta[store.MetaVerify])
	if why != "" {
		return fail(why, "")
	}
	if len(checks) == 0 {
		record("pass " + stamp + ": nothing to run (no lefthook pre-push hook at the repo root, no verify: command)")
		fmt.Fprintln(out, "nothing to verify: no lefthook pre-push hook and no verify: command")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var labels []string
	for _, c := range checks {
		var buf tailBuffer
		err := runCheck(ctx, c, &buf)
		switch {
		case ctx.Err() != nil:
			return fail(fmt.Sprintf("%s did not finish within %s", c.label, timeout), buf.lastLines(verifyTailLines))
		case err != nil:
			return fail(c.label+" failed ("+err.Error()+")", buf.lastLines(verifyTailLines))
		}
		labels = append(labels, c.label)
	}
	record("pass " + stamp + ": " + oneLine(strings.Join(labels, ", ")))
	fmt.Fprintln(out, "verified: "+strings.Join(labels, ", "))
	return nil
}
