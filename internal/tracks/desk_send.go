package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	permissionWaitEnv = "TRACKS_PERMISSION_WAIT"
	// defaultPermissionWait keeps a prompt answerable in Prism for as long as a session would
	// sit on it: a prompt that times out leaves its session blocked, answerable only by claude
	// attach.
	defaultPermissionWait = 55 * time.Minute
	// permissionHookTimeout is the seconds Claude Code gives tracks permission: past the wait.
	permissionHookTimeout = 3600
	// notedQuiet is how long after a note's delivery the watch says nothing of its session
	// stopping: delivering stops the session and resumes it.
	notedQuiet = time.Minute
	// closedQuiet is how far back the watch looks for questions that closed.
	closedQuiet = 10 * time.Minute

	optAllowOnce    = "Allow once"
	optAllowSession = "Allow for this session"
	optDeny         = "Deny"

	permissionAnswerUsage = "usage: tracks permission-answer ID allow|deny|session [--text '…']"
)

// permissionPoll is how often tracks permission reads its question for an answer.
var permissionPoll = time.Second

// deskTarget is the session the desk started that target names: its id, its short id, or its
// track's name, a running session before a stopped one. ok is false when none does.
func deskTarget(target string) (s store.Session, ok bool, err error) {
	var list []store.Session
	if err := withBareStore(func(st *store.Store) (err error) { list, err = st.DeskSessions(); return err }); err != nil {
		return s, false, err
	}
	var byTrack []store.Session
	for _, x := range list {
		if x.ID == target || len(target) >= len(shortID(x.ID)) && strings.HasPrefix(x.ID, target) {
			return x, true, nil
		}
		if x.Track != "" && strings.EqualFold(x.Track, target) {
			byTrack = append(byTrack, x)
		}
	}
	if i := slices.IndexFunc(byTrack, func(x store.Session) bool { return x.Host != "stopped" }); i >= 0 {
		return byTrack[i], true, nil
	}
	if len(byTrack) > 0 {
		return byTrack[0], true, nil
	}
	return s, false, nil
}

// deskSendCmd is tracks send to a session the desk started: the text joins the session's held
// note, which is delivered now when the session is neither working, blocked nor waiting on its
// subagents, or with now (--now) at once, for a session confirmed stuck.
func deskSendCmd(s store.Session, text string, replace, now bool, out io.Writer) error {
	label := sessionLabel(s.Track, s.Name, s.ID)
	from := "emil"
	if p := os.Getenv("CLAUDE_CODE_SESSION_ID"); p != "" && p == s.ParentID {
		from = "desk"
	}
	var n store.NoteSend
	err := withBareStore(func(st *store.Store) (err error) { n, err = st.SendNote(s.ID, from, text, replace, now); return err })
	if errors.Is(err, store.ErrEmils) {
		return fmt.Errorf("refused: %s is Emil's", label)
	}
	if err != nil {
		return err
	}
	if !n.Deliver {
		fmt.Fprintf(out, "held for %s: delivered when it stops\n", label)
		return nil
	}
	if !now && holdForCompaction(s) {
		if err := withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) }); err != nil {
			return err
		}
		fmt.Fprintf(out, "held for %s: its context is compacted first, then the watch delivers it\n", label)
		return nil
	}
	if err := deliverNote(s, n.Text); err != nil {
		rerr := withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) })
		return fmt.Errorf("could not deliver to %s, so the note is held for the desk's watch: %w", label, errors.Join(err, rerr))
	}
	fmt.Fprintf(out, "delivered to %s\n", label)
	return nil
}

// bgSession is the session the desk started for track, which runs as claude --bg with no Rex
// pane: an answer reaches it as a note, the way tracks send does. ok is false when the desk
// started none for track.
func bgSession(track string) (s store.Session, ok bool) {
	if !storeOn() {
		return s, false
	}
	s, ok, err := deskTarget(track)
	if err != nil || !ok || !strings.EqualFold(s.Track, track) {
		return store.Session{}, false
	}
	return s, true
}

// noteAnswer gives text to the background session s of track name as tracks send does:
// delivered now when the session is neither working nor blocked, held until it stops otherwise.
// held is true when the text waits; a delivery that failed leaves it held for the desk's watch,
// and failed says why, so the caller can tell that apart from a session that is still working.
func noteAnswer(name string, s store.Session, from, text string) (held bool, failed, err error) {
	var n store.NoteSend
	err = withBareStore(func(st *store.Store) (err error) { n, err = st.SendNote(s.ID, from, text, false, false); return err })
	if errors.Is(err, store.ErrEmils) {
		return false, nil, fmt.Errorf("%s's session is Emil's now: answer it in claude attach %s", name, shortID(s.ID))
	}
	if err != nil {
		return false, nil, err
	}
	if !n.Deliver {
		return true, nil, nil
	}
	if holdForCompaction(s) {
		return true, nil, withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) })
	}
	if err := deliverNote(s, n.Text); err != nil {
		if rerr := withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) }); rerr != nil {
			return false, nil, fmt.Errorf("could not deliver to %s: %w", name, errors.Join(err, rerr))
		}
		return true, err, nil
	}
	return false, nil, nil
}

// withoutDeskEnv is this process's environment without what would make claude, or the
// background service it starts, look like the desk's own session.
func withoutDeskEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(deskOnlyEnv, k)
	})
}

// deliverNote wakes a background session with text as its next prompt. Each turn the desk
// gives a session carries the orders in force now, which may have changed since its tracks
// bind printed them.
func deliverNote(s store.Session, text string) error { return resumeWith(s, withOrders(text)) }

// resumeWith wakes a background session with prompt: claude stop, then claude --bg --resume
// with no other flag, since any other flag starts a copy of the session; -- keeps a prompt that
// starts with - from reading as a flag.
func resumeWith(s store.Session, prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), bgTimeout)
	defer cancel()
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "claude", args...)
		if s.Cwd != "" {
			cmd.Dir = s.Cwd
		}
		cmd.Env = withoutDeskEnv()
		b, err := cmd.CombinedOutput()
		if err != nil {
			if msg := oneLine(string(b)); msg != "" {
				return b, fmt.Errorf("claude %s: %s", args[0], msg)
			}
			return b, fmt.Errorf("claude %s: %w", args[0], err)
		}
		return b, nil
	}
	if _, err := run("stop", shortID(s.ID)); err != nil && s.Host != "stopped" {
		return err
	}
	// claude --resume of a session whose process still runs starts a copy of it under a new
	// id, which gets the note instead (EMI-426: as-1701's 15:08 note went to a copy).
	if err := awaitExit(s.ID); err != nil {
		return err
	}
	b, err := run("--bg", "--resume", s.ID, "--", prompt)
	if err != nil {
		return err
	}
	if m := backgrounded.FindSubmatch(b); m != nil && string(m[1]) != shortID(s.ID) {
		_, _ = run("stop", string(m[1]))
		return fmt.Errorf("claude resumed a copy %s instead of the session, so it was stopped", m[1])
	}
	return nil
}

// exitWait is how long a delivery waits for a stopped session's process to exit.
var exitWait = 15 * time.Second

// awaitExit waits until claude agents no longer lists the session with a process.
func awaitExit(id string) error {
	deadline := time.Now().Add(exitWait)
	for {
		list, err := claudeAgents()
		if err == nil && !slices.ContainsFunc(list, func(a claudeAgent) bool { return a.SessionID == id && a.PID != 0 }) {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("its process still runs after claude stop")
			}
			return fmt.Errorf("not resumed, so no copy starts: %w", err)
		}
		time.Sleep(agentsPoll)
	}
}

// deliverHeld delivers the held notes of the desk's sessions that stopped working, once per
// note per watch: a note whose delivery failed stays held for the next watch or send. A
// session past trackCompactAt is compacted first, and its note waits until that is done.
func deliverHeld(seen map[string]deskSeen) ([]string, error) {
	var notes []store.HeldNote
	if err := withBareStore(func(st *store.Store) (err error) { notes, err = st.DeliverableNotes(); return err }); err != nil {
		return nil, err
	}
	var lines []string
	for _, n := range notes {
		key := "note:" + strconv.FormatInt(n.ID, 10)
		if _, tried := seen[key]; tried {
			continue
		}
		label := sessionLabel(n.Session.Track, n.Session.Name, n.Session.ID)
		running, ended := compactState(n.Session.ID)
		if running {
			continue
		}
		if tokens, _ := transcriptContext(n.Session.ID); !ended && tokens >= trackCompactAt {
			err := compactSession(n.Session)
			if err == nil {
				lines = append(lines, fmt.Sprintf("compacting %s's context (%dk tokens) before its note", label, tokens/1000))
				continue
			}
			lines = append(lines, "could not compact "+label+"'s context, so its note goes in as it is: "+oneLine(err.Error()))
		}
		var took bool
		if err := withBareStore(func(st *store.Store) (err error) { took, err = st.ClaimNote(n.ID); return err }); err != nil || !took {
			continue
		}
		if err := deliverNote(n.Session, n.Text); err != nil {
			seen[key] = deskSeen{status: "failed"}
			rerr := withBareStore(func(st *store.Store) error { return st.ReleaseNote(n.ID) })
			lines = append(lines, "could not deliver note to "+label+", it stays held: "+oneLine(errors.Join(err, rerr).Error()))
			continue
		}
		lines = append(lines, "delivered note to "+label)
	}
	return lines, nil
}

// questionLines prints each open question of the desk's sessions once per watch, and a
// question it printed that closed unanswered.
func questionLines(qs []store.Question, seen map[string]deskSeen) []string {
	var lines []string
	for _, q := range qs {
		key := "q:" + strconv.FormatInt(q.ID, 10)
		old, known := seen[key]
		label := sessionLabel(q.Track, "", q.SessionID)
		switch {
		case q.Open() && !known:
			lines = append(lines, fmt.Sprintf("%s asks: %s %s", label, oneLine(q.Question), decisionLabel(q.ID)))
			seen[key] = deskSeen{status: "open"}
		case !q.Open() && old.status == "open" && q.AnsweredAt.IsZero():
			lines = append(lines, fmt.Sprintf("%s %s closed: %s", label, decisionLabel(q.ID), oneLine(q.AnswerText)))
			seen[key] = deskSeen{status: "closed"}
		case !q.Open():
			seen[key] = deskSeen{status: "closed"}
		}
	}
	return lines
}

// permissionQuestion is "Allow TOOL: INPUT?" with the whole input on one line (see wholePrompt).
func permissionQuestion(tool string, input json.RawMessage) string {
	if full := permissionInputFull(input); full != "" {
		return "Allow " + tool + ": " + wholePrompt(full) + "?"
	}
	return "Allow " + tool + "?"
}

func permissionWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(permissionWaitEnv)); err == nil && d > 0 {
		return d
	}
	return defaultPermissionWait
}

// permissionCmd is the PermissionRequest hook of a session the desk started. For a session the
// desk owns it asks the permission as a question, waits for its answer and prints the decision,
// unless Emil took the session over meanwhile; when nobody answers in time it closes the
// question and prints nothing, so the session's own prompt stays for claude attach. It never
// fails: Claude Code would show a failed hook.
func permissionCmd(in io.Reader, out io.Writer) {
	defer func() { recover() }()
	var h hookInput
	if json.NewDecoder(in).Decode(&h) != nil || !claudeSessionID.MatchString(h.SessionID) || !storeOn() {
		return
	}
	opts := []string{optAllowOnce}
	if rules := permissionRules(h.Suggestions); rules != "" {
		opts = append(opts, optAllowSession+": "+rules)
	}
	opts = append(opts, optDeny)
	var (
		id int64
		ok bool
	)
	err := withBareStore(func(st *store.Store) (err error) {
		id, ok, err = st.AddPermissionQuestion(h.SessionID, permissionQuestion(h.ToolName, h.ToolInput), opts)
		return err
	})
	if err != nil || !ok {
		return
	}
	wait, timeoutNote := permissionWait(), "timed out: answer with claude attach "+shortID(h.SessionID)
	loop := isLoopSession(h.SessionID)
	if loop {
		wait, timeoutNote = loopPermissionWait, loopDenyNote
	}
	q, ok := awaitPromptAnswerNote(h.SessionID, id, wait, timeoutNote)
	if !ok {
		if loop && loopDenied(id) {
			writePromptDecision(out, id, map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]any{"behavior": "deny", "message": loopDenyText}}, nil)
		}
		return
	}
	writePromptDecision(out, id, map[string]any{"hookEventName": "PermissionRequest", "decision": permissionDecision(q, h.Suggestions)}, nil)
}

// awaitPromptAnswer waits for the answer to question id, which a hook of session asked. ok is
// false when Emil took the session over, the question closed unanswered, or nobody answered
// before wait ran out: then it closes the question, so the session's own prompt stays for
// claude attach. Emil taking the session over closes it at once.
func awaitPromptAnswer(session string, id int64, wait time.Duration) (q store.Question, ok bool) {
	return awaitPromptAnswerNote(session, id, wait, "timed out: answer with claude attach "+shortID(session))
}

// awaitPromptAnswerNote is awaitPromptAnswer with the note a question that timed out closes with.
func awaitPromptAnswerNote(session string, id int64, wait time.Duration, timeoutNote string) (q store.Question, ok bool) {
	deadline := time.Now().Add(wait)
	for {
		time.Sleep(permissionPoll)
		var owner string
		err := withBareStore(func(st *store.Store) (err error) {
			if q, err = st.GetQuestion(id); err == nil {
				owner, _, err = st.SessionOwner(session)
			}
			return err
		})
		switch {
		case err != nil:
		case !q.AnsweredAt.IsZero():
			return q, owner == "desk"
		case !q.Open():
			return q, false
		case owner != "desk":
			_ = withBareStore(func(st *store.Store) error {
				_, err := st.TimeOutQuestion(id, store.EmilsNowNote)
				return err
			})
			return q, false
		case time.Now().After(deadline):
			var closed bool
			err := withBareStore(func(st *store.Store) (err error) {
				closed, err = st.TimeOutQuestion(id, timeoutNote)
				return err
			})
			if err != nil || closed {
				return q, false
			}
		}
	}
}

// writePromptDecision prints a hook's decision for the answer to question id and marks the
// answer delivered; top holds fields beside hookSpecificOutput, such as a reason.
func writePromptDecision(out io.Writer, id int64, specific map[string]any, top map[string]any) {
	m := map[string]any{"hookSpecificOutput": specific}
	for k, v := range top {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	fmt.Fprintf(out, "%s\n", b)
	_ = withBareStore(func(st *store.Store) error { return st.MarkDelivered([]int64{id}) })
}

// permissionDecision is the PermissionRequest decision an answer makes: allow once, allow with
// the prompt's suggested rules kept for the session, or deny with the answer's words.
func permissionDecision(q store.Question, suggestions json.RawMessage) map[string]any {
	label := ""
	if q.AnswerOption > 0 {
		label = q.Answer()
	}
	switch {
	case label == optAllowOnce:
		return map[string]any{"behavior": "allow"}
	case strings.HasPrefix(label, optAllowSession):
		d := map[string]any{"behavior": "allow"}
		if perms := sessionPermissions(suggestions); len(perms) > 0 {
			d["updatedPermissions"] = perms
		}
		return d
	}
	msg := "denied by " + q.AnsweredBy
	if q.AnswerOption == 0 {
		msg = q.AnswerText
	}
	return map[string]any{"behavior": "deny", "message": msg}
}

// sessionPermissions are the permission prompt's suggestions, each kept for this session only.
func sessionPermissions(suggestions json.RawMessage) []map[string]any {
	var list []map[string]any
	if json.Unmarshal(suggestions, &list) != nil {
		return nil
	}
	for _, p := range list {
		p["destination"] = "session"
	}
	return list
}

// permissionRules is what the suggestions would allow, as Claude's settings write it:
// Bash(gh pr:*) for a rule, the folder for an added directory, the mode for a mode change.
func permissionRules(suggestions json.RawMessage) string {
	var list []struct {
		Type  string `json:"type"`
		Mode  string `json:"mode"`
		Rules []struct {
			ToolName    string `json:"toolName"`
			RuleContent string `json:"ruleContent"`
		} `json:"rules"`
		Directories []string `json:"directories"`
	}
	if json.Unmarshal(suggestions, &list) != nil {
		return ""
	}
	var parts []string
	for _, p := range list {
		for _, r := range p.Rules {
			if r.RuleContent != "" {
				parts = append(parts, r.ToolName+"("+r.RuleContent+")")
			} else {
				parts = append(parts, r.ToolName)
			}
		}
		for _, d := range p.Directories {
			parts = append(parts, "files in "+d)
		}
		if p.Mode != "" {
			parts = append(parts, "mode "+p.Mode)
		}
		if len(p.Rules) == 0 && len(p.Directories) == 0 && p.Mode == "" && p.Type != "" {
			parts = append(parts, p.Type)
		}
	}
	return strings.Join(parts, ", ")
}

// isPermission is a question a desk session's permission prompt asked.
func isPermission(q store.Question) bool {
	return q.From == "agent" && q.SessionID != "" && q.AskID == 0 && len(q.Options) > 0 && q.Options[0].Label == optAllowOnce
}

// answerPermission records the answer to a permission question, which the waiting hook passes
// on. The desk answers only Allow once or Deny, only for a session it still owns, and only on
// a tier 1 or 2 track; everything else is Emil's.
func answerPermission(q store.Question, option int, words, by string) (string, error) {
	if option > len(q.Options) {
		return "", fmt.Errorf("%d: %w", option, store.ErrBadOption)
	}
	if by == "desk" {
		if why := deskPermissionRefusal(q, option); why != "" {
			return "", errors.New(why)
		}
	}
	var got store.Question
	if err := withBareStore(func(st *store.Store) (err error) { got, err = st.Decide(q.ID, option, words, by); return err }); err != nil {
		return "", err
	}
	answer := oneLine(got.Answer())
	if by == "desk" && q.Track != "" {
		if err := withStore(func(st *store.Store) error {
			_, err := st.LogDesk(q.Track, "answered", q.Question, "answered "+decisionLabel(q.ID)+": "+answer)
			return err
		}); err != nil {
			return "", fmt.Errorf("answered %s, but could not log it: %w", decisionLabel(q.ID), err)
		}
	}
	return "answered " + decisionLabel(q.ID) + ": " + answer, nil
}

func deskPermissionRefusal(q store.Question, option int) string {
	var owner string
	if err := withBareStore(func(st *store.Store) (err error) { owner, _, err = st.SessionOwner(q.SessionID); return err }); err != nil {
		return err.Error()
	}
	if owner != "desk" {
		return "the session is Emil's now: he answers its prompt himself"
	}
	if option > 0 {
		if l := q.Options[option-1].Label; l != optAllowOnce && l != optDeny {
			return "the desk answers a permission with Allow once or Deny only: " + decisionLabel(q.ID) + " is Emil's"
		}
	}
	if q.Track == "" {
		return "the session has no track, so no tier: " + decisionLabel(q.ID) + " is Emil's"
	}
	v, err := getView(q.Track)
	if err != nil {
		return err.Error()
	}
	if tier := strings.TrimSpace(v.Track.Tier); tier != "1" && tier != "2" {
		return q.Track + " is not tier 1 or 2: " + decisionLabel(q.ID) + " is Emil's"
	}
	return ""
}

// permissionAnswerCmd is tracks permission-answer: Emil's or the desk's answer to a
// permission question, as tracks decide records it. Deny with --text is an answer in words,
// which the session gets as the reason.
func permissionAnswerCmd(args []string, out io.Writer) error {
	pos, fl, err := leadFlags(args, "--text")
	if err != nil || len(pos) != 2 {
		return errors.New(permissionAnswerUsage)
	}
	id, err := questionID(pos[0])
	if err != nil {
		return err
	}
	text := strings.TrimSpace(fl["--text"])
	if text != "" && pos[1] != "deny" {
		return errors.New("refused: --text goes with deny")
	}
	if !storeOn() {
		return errors.New("tracks permission-answer " + needsStore)
	}
	var (
		q      store.Question
		parent string
	)
	if err := withBareStore(func(st *store.Store) (err error) {
		if q, err = st.GetQuestion(id); err == nil {
			_, parent, err = st.SessionOwner(q.SessionID)
		}
		return err
	}); err != nil {
		return fmt.Errorf("%s: %w", decisionLabel(id), err)
	}
	if !isPermission(q) {
		return fmt.Errorf("refused: %s is not a permission prompt: answer it with tracks decide", decisionLabel(id))
	}
	option := slices.IndexFunc(q.Options, func(o store.QOption) bool {
		switch pos[1] {
		case "allow":
			return o.Label == optAllowOnce
		case "session":
			return strings.HasPrefix(o.Label, optAllowSession)
		case "deny":
			return o.Label == optDeny
		}
		return false
	}) + 1
	switch {
	case pos[1] != "allow" && pos[1] != "session" && pos[1] != "deny":
		return errors.New(permissionAnswerUsage)
	case option == 0:
		return fmt.Errorf("refused: %s does not offer %s", decisionLabel(id), pos[1])
	case text != "":
		option = 0
	}
	by := "emil"
	if p := os.Getenv("CLAUDE_CODE_SESSION_ID"); p != "" && p == parent {
		by = "desk"
	}
	status, err := answerQuestion(id, option, text, by, "")
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	fmt.Fprintln(out, status)
	return nil
}
