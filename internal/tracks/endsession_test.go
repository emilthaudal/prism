package tracks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// endStub is a rex that answers session ls with sessions, block ls with blocks, and
// program_status and process per block from files in bin named status.BLOCK and fg.BLOCK.
func endStub(t *testing.T, bin, sessions, blocks string) {
	t.Helper()
	body := `#!/bin/sh
[ "$1" = "--autostart=false" ] || echo "no --autostart=false: $@" >> ` + bin + `/rex.bad
shift
echo "$@" >> ` + bin + `/rex.calls
case "$1 $2" in
"session ls") echo '` + sessions + `';;
"block ls") echo '` + blocks + `';;
"block call")
  f=` + bin + `/status.$(echo "$6" | tr : _)
  [ "$7" = process ] && f=` + bin + `/fg.$(echo "$6" | tr : _)
  if [ -f "$f" ]; then cat "$f"; else echo '{"records":[]}'; fi;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func blockFile(t *testing.T, bin, kind, blk, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, kind+"."+strings.ReplaceAll(blk, ":", "_")), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	oneSession = `{"sessions":[{"session_id":"session:0001-eb6e0e","label":"la"}]}`
	oneBlock   = `{"blocks":[{"block_id":"block:aaa"}]}`
	twoBlocks  = `{"blocks":[{"block_id":"block:aaa"},{"block_id":"block:fff"}]}`
	ownedIdle  = `{"records":[{"state":"idle","app":"claude-code","effective_app":"claude-code","owner":{"pid":42,"name":"claude"},"updated_at":"2026-11-17T10:00:00Z"}]}`
)

// stubChildren makes pid 8 the only process with children.
func stubChildren(t *testing.T) {
	old := hasChildren
	hasChildren = func(_ context.Context, pid int) (bool, error) { return pid == 8, nil }
	t.Cleanup(func() { hasChildren = old })
}

func TestCloseEndsAnIdleAgentsSession(t *testing.T) {
	stubChildren(t)
	for _, tc := range []struct {
		name, blocks, status, fg, self string
		ended                          bool
		note                           string
	}{
		{"idle agent", oneBlock, ownedIdle, `{"foreground":{"pid":42,"name":"claude"}}`, "", true, "ended its Rex session"},
		{"done agent, terminal exited", oneBlock, `{"records":[{"state":"done","app":"claude-code","effective_app":"claude-code"}]}`, `{"last_exit":{"exit_code":0}}`, "", true, "ended"},
		{"working agent", oneBlock, `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"}]}`, "", "", false, "its agent is working"},
		{"blocked agent", oneBlock, `{"records":[{"state":"blocked","kind":"permission","app":"claude-code","effective_app":"claude-code"}]}`, "", "", false, "its agent is blocked"},
		{"agent exited to a shell", oneBlock, ownedIdle, `{"foreground":{"pid":7,"name":"zsh"}}`, "", true, "ended"},
		{"agent exited, server in front", oneBlock, ownedIdle, `{"foreground":{"pid":7,"name":"node"}}`, "", false, "it runs node"},
		{"ownerless idle record, job in front", oneBlock, `{"records":[{"state":"idle","app":"claude-code","effective_app":"claude-code"}]}`, `{"foreground":{"pid":7,"name":"sleep"}}`, "", false, "it runs sleep"},
		{"idle record, its pid exec'd into another program", oneBlock, ownedIdle, `{"foreground":{"pid":42,"name":"sleep"}}`, "", false, "it runs sleep"},
		{"idle record, another process in front", oneBlock, ownedIdle, `{"foreground":{"pid":99,"name":"claude"}}`, "", false, "it runs claude"},
		{"working agent, process unreadable", oneBlock, `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code","owner":{"pid":42}}]}`, "FAIL", "", false, "its agent is working"},
		{"idle agent, process unreadable", oneBlock, ownedIdle, "FAIL", "", false, "could not read it"},
		{"bare shell", oneBlock, "", `{"foreground":{"pid":7,"name":"-zsh"},"ancestors":[{"pid":1,"name":"login"}]}`, "", true, "ended"},
		{"subagent error, root working", oneBlock, `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code","owner":{"pid":42,"name":"claude"}},{"id":"sub1","state":"error","app":"claude-code","effective_app":"claude-code","owner":{"pid":42,"name":"claude"}}]}`, `{"foreground":{"pid":42,"name":"claude"}}`, "", false, "its agent is working"},
		{"a state Rex adds later", oneBlock, `{"records":[{"state":"compacting","app":"claude-code","effective_app":"claude-code"}]}`, "", "", false, "its agent is compacting"},
		{"program_status unreadable", oneBlock, "FAIL", `{"foreground":{"pid":7,"name":"zsh"}}`, "", false, "could not read it"},
		{"program_status without records", oneBlock, `{}`, `{"foreground":{"pid":7,"name":"zsh"}}`, "", false, "could not read it"},
		{"shell script running", oneBlock, "", `{"foreground":{"pid":9,"name":"bash"},"ancestors":[{"pid":7,"name":"zsh"},{"pid":1,"name":"login"}]}`, "", false, "shell script"},
		{"pipeline, no foreground", oneBlock, "", `{"foreground":null,"child":{"pid":1,"name":"login"}}`, "", false, "cannot tell"},
		{"terminal exited", oneBlock, "", `{"last_exit":{"exit_code":0},"foreground":null}`, "", true, "ended"},
		{"process reply of unknown shape", oneBlock, "", `{}`, "", false, "cannot tell"},
		{"shell with a stopped job", oneBlock, "", `{"foreground":{"pid":8,"name":"zsh"},"ancestors":[{"pid":1,"name":"login"}]}`, "", false, "job running or stopped"},
		{"idle record the shell wrote, job behind it", oneBlock, `{"records":[{"state":"idle","app":"claude-code","effective_app":"claude-code","owner":{"pid":8,"name":"zsh"}}]}`, `{"foreground":{"pid":8,"name":"zsh"}}`, "", false, "job running or stopped"},
		{"shell running a job", oneBlock, "", `{"foreground":{"pid":7,"name":"npm"}}`, "", false, "it runs npm"},
		{"another pane open", twoBlocks, ownedIdle, `{"foreground":{"pid":42,"name":"claude"}}`, "", false, "it has 2 panes open"},
		{"the other pane is the float asking", twoBlocks, ownedIdle, `{"foreground":{"pid":42,"name":"claude"}}`, "block:fff", true, "ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th, bin := setup(t)
			endStub(t, bin, oneSession, tc.blocks)
			if tc.status != "" {
				blockFile(t, bin, "status", "block:aaa", tc.status)
			}
			if tc.fg != "" {
				blockFile(t, bin, "fg", "block:aaa", tc.fg)
			}
			card(t, th, "la", "focus", "eb6e0e", "w", "")
			if _, note, err := closeTrack("la", time.Now(), tc.self); err != nil || !strings.Contains(note, tc.note) {
				t.Fatalf("err=%v note=%q", err, note)
			}
			calls := rexCalls(bin)
			if killed := strings.Contains(calls, "session kill session:0001-eb6e0e"); killed != tc.ended {
				t.Errorf("killed=%v want %v:\n%s", killed, tc.ended, calls)
			}
			if marked := strings.Contains(calls, "session rename session:0001-eb6e0e ✓ la"); marked == tc.ended {
				t.Errorf("marked=%v with ended=%v:\n%s", marked, tc.ended, calls)
			}
			if b, err := os.ReadFile(bin + "/rex.bad"); err == nil {
				t.Errorf("%s", b)
			}
		})
	}
}

func TestCloseKeepsASessionAnotherTrackUses(t *testing.T) {
	stubChildren(t)
	for _, tc := range []struct{ name, closing, other, note string }{
		{"a live card names it too", "eb6e0e", "session:0001-eb6e0e", "track other uses it"},
		{"a live card names it by a short suffix", "eb6e0e", "0e", "track other uses it"},
		{"the closing card names it by too short an id", "0e", "", "too short an id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th, bin := setup(t)
			endStub(t, bin, oneSession, oneBlock)
			blockFile(t, bin, "fg", "block:aaa", `{"last_exit":{"exit_code":0}}`)
			card(t, th, "la", "focus", tc.closing, "w", "")
			if tc.other != "" {
				card(t, th, "other", "focus", tc.other, "w", "")
			}
			if _, note, err := closeTrack("la", time.Now(), ""); err != nil || !strings.Contains(note, tc.note) {
				t.Fatalf("err=%v note=%q", err, note)
			}
			if strings.Contains(rexCalls(bin), "kill") {
				t.Errorf("killed:\n%s", rexCalls(bin))
			}
		})
	}
}

func TestTheTracksOwnAgentClosingIt(t *testing.T) {
	th, bin := setup(t)
	endStub(t, bin, oneSession, oneBlock)
	blockFile(t, bin, "status", "block:aaa", `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"}]}`)
	card(t, th, "la", "focus", "eb6e0e", "w", "")
	var started []string
	old := startEndWhenIdle
	startEndWhenIdle = func(id string) error { started = append(started, id); return nil }
	t.Cleanup(func() { startEndWhenIdle = old })
	t.Setenv("REX_SESSION", "session:0001-eb6e0e")
	out, code := do("close", "la")
	if code != 0 || !strings.Contains(out, "Closed la; its Rex session ends once nothing in it is running") {
		t.Fatalf("code %d: %s", code, out)
	}
	calls := rexCalls(bin)
	if strings.Contains(calls, "kill") || !strings.Contains(calls, "session rename session:0001-eb6e0e ✓ la") {
		t.Errorf("calls:\n%s", calls)
	}
	if len(started) != 1 || started[0] != "session:0001-eb6e0e" {
		t.Errorf("waiter started for %v", started)
	}
}

func TestEndWhenIdle(t *testing.T) {
	stubChildren(t)
	oldPoll, oldWait := endPoll, endWait
	endPoll = time.Millisecond
	t.Cleanup(func() { endPoll, endWait = oldPoll, oldWait })
	for _, tc := range []struct {
		name, status, live string
		archived, killed   bool
	}{
		{"agent at rest", ownedIdle, "", true, true},
		{"agent still working", `{"records":[{"state":"working","app":"claude-code","effective_app":"claude-code"}]}`, "", true, false},
		{"a live card names it again", ownedIdle, "session:0001-eb6e0e", true, false},
		{"no closed track names it", ownedIdle, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The waiter ends the session after two calm polls, each a few runs of the rex stub,
			// which can take longer than 200ms on a busy machine. A case that ends the session
			// returns as soon as it does, so it gets ample time; the others wait out endWait.
			endWait = 200 * time.Millisecond
			if tc.killed {
				endWait = 10 * time.Second
			}
			th, bin := setup(t)
			endStub(t, bin, oneSession, oneBlock)
			blockFile(t, bin, "status", "block:aaa", tc.status)
			blockFile(t, bin, "fg", "block:aaa", `{"foreground":{"pid":42,"name":"claude"}}`)
			if tc.archived {
				foldBack(t)
				if err := writeAtomic(filepath.Join(th, "cards", "archive", "2026-11-17-la.md"), []byte("---\nname: la\nrex_session: session:0001-eb6e0e\n---\n## Outcome\nx\n")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.live != "" {
				card(t, th, "other", "focus", tc.live, "w", "")
			}
			if _, code := do("close", "--end-when-idle", "session:0001-eb6e0e"); code != 0 {
				t.Fatalf("code %d", code)
			}
			if killed := strings.Contains(rexCalls(bin), "session kill session:0001-eb6e0e"); killed != tc.killed {
				t.Errorf("killed=%v want %v:\n%s", killed, tc.killed, rexCalls(bin))
			}
		})
	}
	if _, code := do("close", "--end-when-idle", "not-a-session"); code == 0 {
		t.Error("took an id that is not a session's")
	}
}

func TestCloseNeverKillsWhenRexFails(t *testing.T) {
	th, bin := setup(t)
	endStub(t, bin, oneSession, `not json`)
	card(t, th, "la", "focus", "eb6e0e", "w", "")
	if _, note, err := closeTrack("la", time.Now(), ""); err != nil || !strings.Contains(note, "could not read it") {
		t.Fatalf("err=%v note=%q", err, note)
	}
	if strings.Contains(rexCalls(bin), "kill") {
		t.Errorf("killed on a failed read:\n%s", rexCalls(bin))
	}
}

func TestSweepEndsOnlyClosedTracksSessions(t *testing.T) {
	th, bin := setup(t)
	sessions := `{"sessions":[` +
		`{"session_id":"session:s1-aaaaaa","label":"✓ old"},` + // closed, idle: ended
		`{"session_id":"session:s2-bbbbbb","label":"✓ live"},` + // a live card names it again
		`{"session_id":"session:s3-cccccc","label":"✓ never"},` + // no card names it
		`{"session_id":"session:s4-dddddd","label":"old"},` + // no check mark
		`{"session_id":"session:s5-eeeeee","label":"✓ agent-picked-label"},` + // relabelled by its agent: ended
		`{"session_id":"session:s6-ffffff","label":"✓ me"},` + // runs the sweep
		`{"session_id":"session:s7-gggggg","label":"✓ short"}]}` // its card's id is too short a suffix
	endStub(t, bin, sessions, oneBlock)
	blockFile(t, bin, "status", "block:aaa", `{"records":[{"state":"idle","app":"claude-code","effective_app":"claude-code"}]}`)
	blockFile(t, bin, "fg", "block:aaa", `{"last_exit":{"exit_code":0}}`)
	for n, sess := range map[string]string{"old": "aaaaaa", "live": "session:s2-bbbbbb", "relabelled": "session:s5-eeeeee",
		"me": "session:s6-ffffff", "never": "", "plain": "session:s4-dddddd", "short": "ggg"} {
		f := filepath.Join(th, "cards", "archive", "2026-11-16-"+n+".md")
		foldBack(t)
		if err := writeAtomic(f, []byte("---\nname: "+n+"\nrex_session: "+sess+"\n---\n## Outcome\nx\n")); err != nil {
			t.Fatal(err)
		}
	}
	card(t, th, "back", "focus", "bbbbbb", "w", "")
	t.Setenv("REX_SESSION", "session:s6-ffffff")
	os.Remove(bin + "/rex.calls")
	out, code := do("close", "--sweep")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	calls := rexCalls(bin)
	for _, want := range []string{"session kill session:s1-", "session kill session:s5-"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q:\n%s", want, calls)
		}
	}
	for _, not := range []string{"s2", "s3", "s4", "s6", "s7"} {
		if strings.Contains(calls, "kill session:"+not) {
			t.Errorf("killed %s:\n%s", not, calls)
		}
	}
	if !strings.Contains(out, "ended old") || !strings.Contains(out, "ended relabelled") || !strings.Contains(out, "kept me: it runs this command") || !strings.Contains(out, "ended 2 of the closed tracks' sessions") {
		t.Errorf("out:\n%s", out)
	}
}
