package tracks

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const ansSess = "session:0002-bbbbbb-ab12cd"

// sendStub is a rex that answers like the real CLI for the send and create paths. The agent
// runs in block:bbb with the program_status in bin/status; bin/status.fail makes the status
// call fail and bin/new.fail makes rex new fail. It records stdin of send in bin/rex.sent and
// the argv of send and new one per line.
func sendStub(t *testing.T, bin string) {
	t.Helper()
	body := `#!/bin/sh
[ "$1" = "--autostart=false" ] || echo "no --autostart=false: $*" >> ` + bin + `/rex.bad
shift
echo "$@" >> ` + bin + `/rex.calls
[ -f ` + bin + `/rex.down ] && { echo "server not running" >&2; exit 1; }
case "$1" in
session) [ "$2" = ls ] && echo '{"sessions":[{"session_id":"` + ansSess + `","label":"t"},{"session_id":"session:new-0001","label":"new"}]}';;
block)
  case "$2" in
  ls) echo '{"blocks":[{"block_id":"block:aaa"},{"block_id":"block:bbb"}]}';;
  call)
    [ -f ` + bin + `/status.fail ] && { echo down >&2; exit 1; }
    # A flip file changes the agent's state once the text has been typed.
    if [ -f ` + bin + `/rex.sent ] && [ -f ` + bin + `/flip ]; then mv ` + bin + `/flip ` + bin + `/status; fi
    if [ -f ` + bin + `/rex.sent ] && [ -f ` + bin + `/fg.flip ]; then mv ` + bin + `/fg.flip ` + bin + `/fg; fi
    if [ "$7" = process ]; then
      if [ -f ` + bin + `/fg ]; then cat ` + bin + `/fg; else echo '{"foreground":{"pid":4242,"name":"2.1.326"}}'; fi
    elif [ "$6" = block:bbb ] && [ -f ` + bin + `/status ]; then cat ` + bin + `/status; else echo '{"records":[]}'; fi;;
  esac;;
send) cat > ` + bin + `/rex.sent; printf '%s\n' "$@" > ` + bin + `/rex.sendargs;;
capture) printf 'screen of %s\n' "$*";;
send-key) if [ -f ` + bin + `/sendkey.fail ]; then echo "timed out" >&2; exit 1; fi;;
split)
  printf '%s\n' "$@" > ` + bin + `/rex.splitargs
  [ -f ` + bin + `/split.fail ] && { echo "no such block" >&2; exit 1; }
  echo '{"block_ids":["block:p1"]}';;
new)
  printf '%s\n' "$@" > ` + bin + `/rex.newargs
  [ -f ` + bin + `/new.fail ] && { echo "no server" >&2; exit 1; }
  echo '{"session_id":"session:new-0001","initial_windows":[{"window_id":"w","block_ids":["block:n1"]}]}';;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	answerEnterDelay = 0
}

// ownedJSON is a Claude Code record that names its process, pid 4242, as Rex reports it.
const ownedJSON = `{"records":[{"state":"%s","kind":"%s","app":"claude-code","effective_app":"claude-code","updated_at":"2026-11-16T22:38:27+01:00","owner":{"pid":4242,"name":"2.1.326"}}]}`

func agentIs(t *testing.T, bin, state, kind string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, "status"), []byte(fmt.Sprintf(ownedJSON, state, kind)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agentFlipsTo makes the agent's state change once the text has been typed.
func agentFlipsTo(t *testing.T, bin, state, kind string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, "flip"), []byte(fmt.Sprintf(ownedJSON, state, kind)), 0o644); err != nil {
		t.Fatal(err)
	}
}

var namedKeys = map[string]tea.KeyPressMsg{
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"backspace": {Code: tea.KeyBackspace},
	"left":      {Code: tea.KeyLeft},
	"right":     {Code: tea.KeyRight},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"home":      {Code: tea.KeyHome},
	"end":       {Code: tea.KeyEnd},
	"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
	"ctrl+w":    {Code: 'w', Mod: tea.ModCtrl},
	"ctrl+s":    {Code: 's', Mod: tea.ModCtrl},
	"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
}

// press sends one key and returns the command the pane gave back.
func press(m *paneModel, k string) tea.Cmd {
	msg, ok := namedKeys[k]
	if !ok {
		msg = tea.KeyPressMsg{Text: k, Code: []rune(k)[0]}
	}
	_, cmd := m.Update(msg)
	return cmd
}

func typeText(m *paneModel, s string) {
	for _, r := range s {
		press(m, string(r))
	}
}

func TestLineEditorKeys(t *testing.T) {
	var e lineEditor
	k := func(s string) {
		msg, ok := namedKeys[s]
		if !ok {
			msg = tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
		}
		e.key(msg)
	}
	for _, r := range "hello big world" {
		k(string(r))
	}
	k("ctrl+w")
	if e.String() != "hello big " || e.pos != 10 {
		t.Fatalf("ctrl+w: %q %d", e.String(), e.pos)
	}
	k("backspace")
	k("left")
	k("left")
	k("X")
	if e.String() != "hello bXig" {
		t.Fatalf("left and insert: %q", e.String())
	}
	k("right")
	k("backspace")
	if e.String() != "hello bXg" {
		t.Fatalf("right and backspace: %q", e.String())
	}
	k("home")
	k(">")
	k("end")
	k("!")
	if e.String() != ">hello bXg!" {
		t.Fatalf("home and end: %q", e.String())
	}
	k("left")
	k("ctrl+u")
	if e.String() != "!" || e.pos != 0 {
		t.Fatalf("ctrl+u keeps what is after the cursor: %q %d", e.String(), e.pos)
	}
	if e.key(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}) || e.String() != "!" {
		t.Error("an unknown ctrl key typed something")
	}
	e.set("a\x1b[31mb\nc\x03")
	if e.String() != "ab c" {
		t.Errorf("insert drops controls and joins lines: %q", e.String())
	}
	e.set(strings.Repeat("x", 50) + "END")
	if v := ansi.Strip(e.view(10, true)); ansi.StringWidth(v) != 10 || !strings.Contains(v, "END") {
		t.Errorf("view does not follow the cursor: %q", v)
	}
	if v := e.view(10, true); !strings.Contains(v, "\x1b[7m") {
		t.Errorf("no visible cursor: %q", v)
	}
	e.set(strings.Repeat("abcde", 5))
	rows := e.wrapped(10, 6, true)
	if len(rows) != 3 || ansi.Strip(rows[0]) != "abcdeabcde" || ansi.Strip(rows[2]) != "abcde " || !strings.Contains(rows[2], "\x1b[7m") {
		t.Errorf("wrapped: %q", rows)
	}
	if rows := e.wrapped(10, 2, true); len(rows) != 2 || !strings.Contains(rows[1], "\x1b[7m") {
		t.Errorf("wrapped keeps the cursor row: %q", rows)
	}
	e.set("on 1 and 2 please")
	if rows := e.wrapped(10, 6, false); strings.Join(rows, "|") != "on 1 and |2 please" {
		t.Errorf("wrapped breaks at spaces: %q", rows)
	}
}

func TestAcceptText(t *testing.T) {
	text := map[int]string{1: "Ship it? Recommend: yes.", 3: "24h or 72h? Recommend: 72h.", 5: "Icon?"}
	for _, c := range []struct {
		asks []int
		want string
	}{
		{[]int{1}, `Emil's answers: Emil's answer to your ask 1, "Ship it?": go with your recommendation.`},
		{[]int{1, 3, 5}, `Emil's answers: Emil's answer to your ask 1, "Ship it?": go with your recommendation. ` +
			`Emil's answer to your ask 3, "24h or 72h?": go with your recommendation. ` +
			`Emil's answer to your ask 5, "Icon?": go with your recommendation.`},
	} {
		if got := acceptText(c.asks, text); got != c.want {
			t.Errorf("%v: %q", c.asks, got)
		}
	}
}

func TestSanitizeAnswer(t *testing.T) {
	long := strings.Repeat("ab", 2500)
	for _, c := range []struct{ name, in, want string }{
		{"plain", "yes, go with B", "yes, go with B"},
		{"csi colour", "\x1b[31mred\x1b[0m text", "red text"},
		{"osc title", "a\x1b]0;pwned\x07b", "ab"},
		{"osc st", "a\x1b]7501;state=done\x1b\\b", "ab"},
		{"bracketed paste end", "x\x1b[201~; rm -rf /", "x; rm -rf /"},
		{"ctrl-c", "stop\x03 now", "stop now"},
		{"ctrl-d and bell", "a\x04\x07b", "ab"},
		{"c1 csi", "a\u009b31mb", "a31mb"},
		{"del", "a\x7fb", "ab"},
		{"newlines", "line one\nline two\r\nthree\rfour", "line one line two three four"},
		{"tabs and runs", "a\t\t b   c", "a b c"},
		{"leading dashes", "--help -rf", "--help -rf"},
		{"bidi override", "a\u202eb", "ab"},
		{"invalid utf8", "a\xffb", "ab"},
		{"only controls", "\x1b[2J\x03\n", ""},
		{"5k chars", long, long[:maxAnswer]},
		{"trailing backslash", `yes \`, "yes"},
		{"trailing backslashes after a newline", "yes\\\n\\", "yes"},
		{"inner backslash kept", `a\b`, `a\b`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeAnswer(c.in)
			if got != c.want {
				t.Errorf("%q -> %q, want %q", c.in, got, c.want)
			}
			if strings.ContainsFunc(got, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }) {
				t.Errorf("control left in %q", got)
			}
			if len([]rune(got)) > maxAnswer {
				t.Errorf("over the cap: %d", len([]rune(got)))
			}
		})
	}
}

func TestReplyEditorPrefillDigitsAndHint(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "asks", "focus", "", "w", "1. Ship it? Recommend: yes.\n2. 24h or 72h? Recommend: 72h.")
	card(t, th, "quiet", "focus", "", "w", "")
	m := newPane()
	out := plain(m, 100, 24)
	if !strings.Contains(out, answerKeys) || !strings.Contains(out, "1-2 accept · r reply") {
		t.Fatalf("hint missing with asks:\n%s", out)
	}
	if out := plain(m, 80, 24); !strings.Contains(out, answerKeys) {
		t.Errorf("hint dropped at 80 columns:\n%s", out)
	}
	press(m, "1")
	if m.reply == nil || !strings.Contains(m.reply.ed.String(), `your ask 1, "Ship it?": go with your recommendation.`) {
		t.Fatalf("prefill: %+v", m.reply)
	}
	press(m, "2")
	both := acceptText([]int{1, 2}, m.reply.text)
	if m.reply.ed.String() != both || !strings.Contains(both, `your ask 2, "24h or 72h?"`) {
		t.Fatalf("second digit: %q", m.reply.ed.String())
	}
	if out := plain(m, 100, 24); !strings.Contains(out, editKeys) {
		t.Errorf("footer lacks the editor keys:\n%s", out)
	}
	press(m, "3")
	if m.reply.ed.String() != both || !strings.Contains(m.status, "no ask 3") {
		t.Errorf("ask 3 does not exist: %q %q", m.reply.ed.String(), m.status)
	}
	out = plain(m, 100, 24)
	lines := strings.Split(out, "\n")
	needs, ed := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "Needs me") {
			needs = i
		}
		if strings.Contains(l, "› Emil's answers: Emil's answer to your ask 1") {
			ed = i
		}
	}
	if needs < 0 || ed <= needs || !strings.Contains(out, "enter send · esc cancel") {
		t.Fatalf("editor not under Needs me:\n%s", out)
	}
	press(m, "backspace")
	press(m, "3")
	if !strings.HasSuffix(m.reply.ed.String(), "go with your recommendation3") {
		t.Errorf("after an edit digits are typed: %q", m.reply.ed.String())
	}
	press(m, "down")
	if m.cur != "asks" {
		t.Error("arrow moved the list while editing")
	}
	press(m, "esc")
	if m.reply != nil {
		t.Fatal("esc did not cancel")
	}
	if calls := rexCalls(bin); strings.Contains(calls, "send") {
		t.Errorf("cancel sent something:\n%s", calls)
	}
	press(m, "r")
	if m.reply == nil || m.reply.ed.String() != "" {
		t.Fatal("r does not open an empty reply")
	}
	press(m, "esc")
	press(m, "down")
	if m.cur != "quiet" {
		t.Fatalf("cur %s", m.cur)
	}
	if out := plain(m, 100, 24); strings.Contains(out, answerKeys) {
		t.Errorf("hint shown for a track with no asks and no agent at rest:\n%s", out)
	}
	press(m, "1")
	if m.reply != nil || m.status != "" {
		t.Errorf("a digit without asks did something: %q", m.status)
	}
	press(m, "r")
	if m.reply != nil || !strings.Contains(m.status, "Nothing to answer") {
		t.Errorf("opened a reply with nothing to answer: %q", m.status)
	}
}

func TestHintWhenAgentIdleWithoutAsks(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	agentIs(t, bin, "done", "")
	card(t, th, "a", "focus", ansSess, "w", "")
	m := newPane()
	settle(t, m)
	if out := plain(m, 100, 20); !strings.Contains(out, answerKeys) {
		t.Fatalf("no hint for a done agent:\n%s", out)
	}
	press(m, "r")
	out := plain(m, 100, 20)
	if m.reply == nil || !strings.Contains(out, "Reply") || !strings.Contains(out, "› ") {
		t.Errorf("reply editor not shown without asks:\n%s", out)
	}
	agentIs(t, bin, "working", "")
	m.rex.lastRun = time.Time{}
	press(m, "esc")
	settle(t, m)
	if out := plain(m, 100, 20); strings.Contains(out, answerKeys) {
		t.Errorf("hint shown while working:\n%s", out)
	}
}

// sendFrom opens a reply on the only card, types text, presses enter and runs the send.
func sendFrom(t *testing.T, m *paneModel, text string) answerMsg {
	t.Helper()
	press(m, "r")
	if m.reply == nil {
		t.Fatalf("no editor: %q", m.status)
	}
	m.Update(tea.PasteMsg{Content: text})
	cmd := press(m, "enter")
	if cmd == nil {
		t.Fatalf("enter gave no send: %q", m.status)
	}
	if m.status != "Sending to a…" {
		t.Errorf("status while sending %q", m.status)
	}
	msg := cmd().(answerMsg)
	m.Update(msg)
	return msg
}

func TestSendRefusedUnlessIdleOrDoneNow(t *testing.T) {
	for _, c := range []struct{ name, state, kind, want string }{
		{"working", "working", "", "a is working"},
		{"permission", "blocked", "permission", "waiting on a permission prompt"},
		{"question", "blocked", "question", "waiting on a question prompt"},
		{"error", "error", "", "stopped on an error"},
		{"unknown", "", "", "Rex reports no agent"},
		{"status call fails", "fail", "", "Rex reports no agent"},
		{"rex down", "down", "", "could not ask Rex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			card(t, th, "a", "focus", ansSess, "w", "1. Ok? Recommend: yes.")
			agentIs(t, bin, "idle", "")
			m := newPane()
			settle(t, m)
			if st, _ := m.rex.state(ansSess); st.State != "idle" {
				t.Fatalf("cache %+v", st)
			}
			// The cache says idle; Rex now says otherwise.
			switch c.state {
			case "fail":
				os.WriteFile(filepath.Join(bin, "status.fail"), nil, 0o644)
			case "down":
				os.WriteFile(filepath.Join(bin, "rex.down"), nil, 0o644)
			case "":
				os.Remove(filepath.Join(bin, "status"))
			default:
				agentIs(t, bin, c.state, c.kind)
			}
			msg := sendFrom(t, m, "yes")
			if msg.sent || !strings.Contains(msg.status, c.want) || !strings.HasPrefix(msg.status, "Not sent") {
				t.Errorf("status %q", msg.status)
			}
			if _, err := os.Stat(filepath.Join(bin, "rex.sent")); err == nil || strings.Contains(rexCalls(bin), "send") {
				t.Errorf("sent anyway:\n%s", rexCalls(bin))
			}
			if m.reply == nil || m.reply.ed.String() != "yes" || m.reply.sending {
				t.Errorf("refusal lost the reply: %+v", m.reply)
			}
			if !strings.Contains(plain(m, 100, 20), "Not sent") {
				t.Error("refusal not in the footer")
			}
			for _, e := range readEvents() {
				if e.Event == "answer" {
					t.Errorf("logged a refused answer: %+v", e)
				}
			}
		})
	}
}

func TestSendDeliversSanitizedTextThenEnter(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. Ok? Recommend: yes.")
	agentIs(t, bin, "done", "")
	m := newPane()
	settle(t, m)
	raw := "--help \x1b[201~1 yes,\x03\n2 B\x1b]0;x\x07"
	msg := sendFrom(t, m, raw)
	want := "--help 1 yes, 2 B"
	if !msg.sent || msg.status != "Sent to a" {
		t.Fatalf("not sent: %q", msg.status)
	}
	if got, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); string(got) != want {
		t.Errorf("stdin %q, want %q", got, want)
	}
	if args, _ := os.ReadFile(filepath.Join(bin, "rex.sendargs")); string(args) != "send\n--session="+ansSess+"\n--block=block:bbb\n" {
		t.Errorf("send argv:\n%s", args)
	}
	calls := strings.Split(strings.TrimSpace(rexCalls(bin)), "\n")
	sendAt := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "send --session=") })
	tail := strings.Join(calls[sendAt+1:], "\n")
	want2 := "block call -s " + ansSess + " -b block:bbb program_status\nblock call -s " + ansSess + " -b block:bbb process\nsend-key --session=" + ansSess + " --block=block:bbb enter"
	if sendAt < 0 || tail != want2 {
		t.Errorf("want text, a fresh check of the same live agent, then a separate enter; got:\n%s", strings.Join(calls, "\n"))
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "rex.bad")); len(b) > 0 {
		t.Errorf("rex without --autostart=false: %s", b)
	}
	if m.reply != nil || !strings.Contains(plain(m, 100, 20), "Sent to a") {
		t.Error("editor not closed or footer lacks Sent to")
	}
	var ans []Event
	for _, e := range readEvents() {
		if e.Event == "answer" {
			ans = append(ans, e)
		}
	}
	if len(ans) != 1 || ans[0].Track != "a" || ans[0].RexSession != ansSess || ans[0].RexBlock != "block:bbb" || ans[0].Len != len(want) || ans[0].Kind != classify(want) {
		t.Fatalf("event %+v", ans)
	}
	log, _ := os.ReadFile(logPath())
	for _, part := range []string{"help", "yes", "2 B"} {
		if strings.Contains(string(log), part) {
			t.Errorf("the log holds reply text %q:\n%s", part, log)
		}
	}
	// With the editor closed, enter jumps to the agent again.
	if cmd := press(m, "enter"); cmd == nil || m.hold != "a" {
		t.Error("enter no longer opens the track")
	}
}

func TestSendNeverEmptyAndBlocksDoubleSend(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", ansSess, "w", "1. Ok? Recommend: yes.")
	m := newPane()
	press(m, "r")
	m.Update(tea.PasteMsg{Content: "\x1b[2J\n"})
	if cmd := press(m, "enter"); cmd != nil || !strings.Contains(m.status, "empty") {
		t.Errorf("empty reply: %q", m.status)
	}
	press(m, "ctrl+u")
	typeText(m, "ok")
	if cmd := press(m, "enter"); cmd == nil {
		t.Fatal("no send")
	}
	if cmd := press(m, "enter"); cmd != nil {
		t.Error("second enter while sending started another send")
	}
	press(m, "x")
	if m.reply.ed.String() != "ok" {
		t.Error("typed while sending")
	}
}

func TestReplyDroppedWhenCardCloses(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "", "w", "1. Ok? Recommend: yes.")
	card(t, th, "b", "focus", "", "w", "")
	m := newPane()
	press(m, "1")
	foldBack(t)
	os.Remove(cardPath("a"))
	m.reload()
	if m.reply != nil || !strings.Contains(m.status, "Reply dropped") {
		t.Errorf("reply kept for a gone track: %+v %q", m.reply, m.status)
	}
}

func TestLeftoverRecordsDoNotOutrankTheLiveAgent(t *testing.T) {
	// A crashed agent's done record (owner 33976) and the live agent's idle root (owner 75296).
	b := []byte(`{"records":[{"id":"sub","state":"done","effective_app":"claude-code","title":"old","owner":{"pid":33976}},` +
		`{"state":"idle","effective_app":"claude-code","updated_at":"2026-11-16T22:00:00Z","owner":{"pid":75296}}]}`)
	s, ok := parseProgramStatus(b)
	if !ok || s.State != "idle" || s.Owner != 75296 || s.Msg == "old" {
		t.Errorf("got %+v", s)
	}
}

func TestOnlyTracksCheckShapesAreCheckAsks(t *testing.T) {
	for text, want := range map[string]bool{
		`Check "pr 5 merged" is now yes. Recommend: set mode to focus.`:           true,
		`Check "ci main green" is now no. Recommend: open the track and see why.`: true,
		`Check "the deploy" is now green?`:                                        false,
		`Check "x" is now yes. Recommend: set mode to parked.`:                    false,
	} {
		if isCheckAsk(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
}

func answerEvents() []Event {
	var out []Event
	for _, e := range readEvents() {
		if e.Event == "answer" {
			out = append(out, e)
		}
	}
	return out
}

// TestEnterWaitsForAFreshCheck: the agent changes between the text and Enter.
func TestEnterWaitsForAFreshCheck(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(bin string)
		want  string
	}{
		{"blocked on permission", func(bin string) { agentFlipsTo(t, bin, "blocked", "permission") }, "it is blocked on a permission prompt"},
		{"working", func(bin string) { agentFlipsTo(t, bin, "working", "") }, "it is working"},
		{"agent exited", func(bin string) {
			os.WriteFile(filepath.Join(bin, "fg.flip"), []byte(`{"foreground":{"pid":77,"name":"zsh"}}`), 0o644)
		}, "its agent exited. Clear that input before you run anything there."},
		{"enter fails", func(bin string) { os.WriteFile(filepath.Join(bin, "sendkey.fail"), nil, 0o644) }, "enter failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			th, bin := setup(t)
			sendStub(t, bin)
			card(t, th, "a", "focus", ansSess, "w", "1. Ok? Recommend: yes.")
			agentIs(t, bin, "idle", "")
			c.setup(bin)
			m := newPane()
			settle(t, m)
			msg := sendFrom(t, m, "yes")
			exited := c.name == "agent exited"
			if msg.sent || !msg.typed || !strings.HasPrefix(msg.status, "Typed into a but not sent: "+c.want) || exited == strings.Contains(msg.status, "Press enter") {
				t.Errorf("status %q", msg.status)
			}
			if got, _ := os.ReadFile(filepath.Join(bin, "rex.sent")); string(got) != "yes" {
				t.Errorf("text not typed: %q", got)
			}
			if c.name != "enter fails" && strings.Contains(rexCalls(bin), "send-key") {
				t.Errorf("pressed enter anyway:\n%s", rexCalls(bin))
			}
			if m.reply != nil {
				t.Error("the typed text is still in the editor, ready to be sent twice")
			}
			if len(answerEvents()) != 0 {
				t.Errorf("logged an answer that was not sent: %+v", answerEvents())
			}
			if !strings.Contains(plain(m, 120, 20), "Typed into a but not sent") {
				t.Error("not in the footer")
			}
		})
	}
}

func TestStaleRecordIsExitedAndRefused(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", ansSess, "w", "1. Ok? Recommend: yes.")
	agentIs(t, bin, "idle", "")
	os.WriteFile(filepath.Join(bin, "fg"), []byte(`{"foreground":{"pid":99892,"name":"zsh"}}`), 0o644)
	m := newPane()
	settle(t, m)
	out := plain(m, 100, 20)
	if !strings.Contains(out, "○ exited") || !strings.Contains(out, "zsh runs in its block") || strings.Contains(out, "idle") {
		t.Errorf("stale record not shown as exited:\n%s", out)
	}
	msg := sendFrom(t, m, "touch answered-into-shell")
	if msg.sent || msg.status != "Not sent: a's agent has exited: zsh runs in its block" {
		t.Errorf("status %q", msg.status)
	}
	if strings.Contains(rexCalls(bin), "send") {
		t.Errorf("typed into a shell:\n%s", rexCalls(bin))
	}
	// Rex names the foreground process as owner, so a status written while a shell is in
	// front matches its pid; a shell in front is never the agent.
	os.WriteFile(filepath.Join(bin, "fg"), []byte(`{"foreground":{"pid":4242,"name":"zsh","argv0":"-zsh"}}`), 0o644)
	m.reply = nil
	if msg := sendFrom(t, m, "yes"); msg.sent || msg.status != "Not sent: a's agent has exited: zsh runs in its block" {
		t.Errorf("shell owning the record: %q", msg.status)
	}
	// A record that names no process cannot be checked, so it is not typed into either.
	os.Remove(filepath.Join(bin, "fg"))
	os.WriteFile(filepath.Join(bin, "status"), []byte(fmt.Sprintf(agentJSON, "idle", "", "")), 0o644)
	m.reply = nil
	if msg := sendFrom(t, m, "yes"); msg.sent || !strings.Contains(msg.status, "does not say which process") {
		t.Errorf("ownerless record: %q", msg.status)
	}
}

func TestModeCharactersRefused(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", ansSess, "w", "1. Ok? Recommend: yes.")
	agentIs(t, bin, "idle", "")
	m := newPane()
	settle(t, m)
	for _, text := range []string{"!rm -rf ~", "# remember this", "  !ls"} {
		press(m, "r")
		m.Update(tea.PasteMsg{Content: text})
		if cmd := press(m, "enter"); cmd != nil || m.status != "Not sent: starts with ! or #, which Claude Code treats as bash or memory mode: type it in the agent" {
			t.Errorf("%q: %q", text, m.status)
		}
		if m.reply == nil {
			t.Fatalf("%q: refusal dropped the reply", text)
		}
		press(m, "esc")
	}
	if msg := sendAnswer("a", "#x", "", nil, nil); msg.sent || !strings.Contains(msg.status, "bash or memory mode") {
		t.Errorf("sendAnswer itself: %q", msg.status)
	}
	if strings.Contains(rexCalls(bin), "send") {
		t.Errorf("sent:\n%s", rexCalls(bin))
	}
	if msg := sendFrom(t, m, "/compact"); !msg.sent {
		t.Errorf("slash command refused: %q", msg.status)
	}
}

func TestDigitsMatchAskNumbers(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // asks numbered 2 and 5 exist only on a card file; the store numbers by position
	sendStub(t, bin)
	card(t, th, "a", "focus", "", "w", "2. Ship? Recommend: yes.\n5. Window? Recommend: 72h.")
	m := newPane()
	if out := plain(m, 100, 20); !strings.Contains(out, "2, 5 accept · r reply") {
		t.Errorf("hint names the asks:\n%s", out)
	}
	press(m, "1")
	if m.reply != nil || !strings.Contains(m.status, "no ask 1") {
		t.Errorf("1 is not an ask: %q", m.status)
	}
	press(m, "5")
	press(m, "3")
	press(m, "2")
	if m.reply == nil || m.reply.ed.String() != acceptText([]int{2, 5}, map[int]string{2: "Ship? Recommend: yes.", 5: "Window? Recommend: 72h."}) {
		t.Errorf("prefill: %+v %q", m.reply, m.status)
	}
	if got := classify(m.reply.ed.String()); got != "answers" {
		t.Errorf("classify(prefill) = %s", got)
	}
}

func TestCheckAskAppliesLocally(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // an ask numbered 0 exists only on a card file; TestCheckAskAppliesFromTheDatabase covers the store
	sendStub(t, bin)
	waitingCard(t, th, "w", "pr 5 approved")
	if err := fire("w", "pr 5 approved", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cardPath("w"))
	os.WriteFile(cardPath("w"), []byte(strings.Replace(string(b), "## Needs me\n", "## Needs me\n0) keep me\n", 1)), 0o644)
	m := newPane()
	m.idx = slicesIndex(m, "w")
	m.cur = "w"
	press(m, "1")
	if m.reply != nil || m.status != "Applied: ask 1 cleared; agent not told: w has no tier" {
		t.Fatalf("status %q reply %+v", m.status, m.reply)
	}
	c, _ := loadCard("w")
	if c.mode() != "waiting" || strings.Contains(c.Sections["Needs me"], "Check") || !strings.Contains(c.Sections["Needs me"], "0) keep me") {
		t.Errorf("card after apply:\n%s", readCard("w"))
	}
	if strings.Contains(rexCalls(bin), "send") {
		t.Errorf("sent something:\n%s", rexCalls(bin))
	}
	st := checkState{Answer: true, Asked: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}
	if askLost(c, "pr 5 approved", st, readEvents()) {
		t.Error("tracks check would put the applied ask back")
	}
	// An ask without a mode recommendation is cleared.
	card(t, th, "x", "focus", "", "w", `1. Check "pr 6 green" is now no. Recommend: open the track and see why.`)
	m.reload()
	m.idx, m.cur = slicesIndex(m, "x"), "x"
	press(m, "1")
	if m.status != "Applied: ask 1 cleared" || loadCardMust(t, "x").asks() != 0 || loadCardMust(t, "x").mode() != "focus" {
		t.Errorf("status %q\n%s", m.status, readCard("x"))
	}
}

func loadCardMust(t *testing.T, name string) *Card {
	t.Helper()
	c, err := loadCard(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFooterKeepsQuitAt80To99(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "1. Ok? Recommend: yes.")
	m := newPane()
	for _, w := range []int{80, 90, 99, 120} {
		out := plain(m, w, 20)
		if !strings.Contains(out, "q quit") || !strings.Contains(out, answerKeys) {
			t.Errorf("width %d footer:\n%s", w, out)
		}
	}
}
