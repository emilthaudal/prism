package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// An MCP server asks for approval by elicitation: Gatekeeper's request_merge asks Emil to open its
// confirm page and approve there. Claude Code shows it in the terminal, which a session the desk
// started in the background has nobody looking at, so tracks elicitation, its Elicitation hook,
// asks Emil in Prism: Open the page, then Done, continue or Decline. The desk never answers one.

const (
	elicitationWaitEnv     = "TRACKS_ELICITATION_WAIT"
	defaultElicitationWait = 55 * time.Minute
	// elicitationHookTimeout is the seconds Claude Code gives tracks elicitation: past the wait.
	elicitationHookTimeout = 3600

	optElicitDone    = "Done, continue"
	optElicitDecline = "Decline"
)

func elicitationWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(elicitationWaitEnv)); err == nil && d > 0 {
		return d
	}
	return defaultElicitationWait
}

// isElicitation is a question a desk session's MCP approval prompt asked.
func isElicitation(q store.Question) bool {
	return q.From == "agent" && q.SessionID != "" && q.AskID == 0 && len(q.Options) > 0 && q.Options[0].Label == optElicitDone
}

// isHookPrompt is a question a hook of a desk session waits on, which it passes on itself:
// a permission prompt or an MCP approval. Its answer is never typed into the session.
func isHookPrompt(q store.Question) bool { return isPermission(q) || isElicitation(q) }

// promptQuestionMax is the most of a prompt's text a decision keeps. Past it the question says it is cut and
// by how much, so a decision never reads as whole when it is not (EMI-435).
const promptQuestionMax = 2000

// wholePrompt is text as one line with nothing dropped, up to promptQuestionMax runes.
func wholePrompt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > promptQuestionMax {
		return string(r[:promptQuestionMax]) + fmt.Sprintf(" … (cut: %d more characters)", len(r)-promptQuestionMax)
	}
	return s
}

// elicitationQuestion is "SERVER: MESSAGE" with the whole message: Emil approves what it says, so it is
// never guessed at from a cut one. The session's waiting_for line stays short.
func elicitationQuestion(server, message string) string {
	if strings.TrimSpace(message) == "" {
		return server
	}
	return wholePrompt(server + ": " + message)
}

// elicitationWaitingFor is the session's waiting_for while the prompt is open.
func elicitationWaitingFor(server, message string) string {
	return oneShortLine("approval in "+server, message, waitingForMax)
}

// elicitationCmd is the Elicitation hook of a session the desk started. For an approval page
// (mode url) of a session the desk owns, it marks the session blocked, asks Emil with the page
// as the question's link, waits for his answer and prints accept or decline. A form, a session
// Emil took over, or no answer in time prints nothing, so Claude Code's own prompt stays for
// claude attach. It never fails: Claude Code would show a failed hook.
func elicitationCmd(in io.Reader, out io.Writer) {
	defer func() { recover() }()
	start := time.Now()
	var h hookInput
	if json.NewDecoder(in).Decode(&h) != nil || !claudeSessionID.MatchString(h.SessionID) || !storeOn() {
		return
	}
	if h.Mode != "url" || !strings.HasPrefix(h.URL, "https://") && !strings.HasPrefix(h.URL, "http://") || strings.Contains(h.URL, ",") {
		return
	}
	if se, ok := sessionEventOf(h); ok {
		_ = sessionHook(se, start)
	}
	var (
		id int64
		ok bool
	)
	err := withBareStore(func(st *store.Store) (err error) {
		id, ok, err = st.AddPromptQuestion(h.SessionID, elicitationQuestion(h.MCPServer, h.Message), []string{optElicitDone, optElicitDecline}, []string{h.URL})
		return err
	})
	if err != nil || !ok {
		return
	}
	wait, timeoutNote := elicitationWait(), "timed out: answer with claude attach "+shortID(h.SessionID)
	loop := isLoopSession(h.SessionID)
	if loop {
		wait, timeoutNote = loopPermissionWait, loopDenyNote
	}
	q, ok := awaitPromptAnswerNote(h.SessionID, id, wait, timeoutNote)
	if !ok {
		if loop && loopDenied(id) {
			writePromptDecision(out, id, map[string]any{"hookEventName": "Elicitation", "action": "decline"}, map[string]any{"reason": loopDenyText})
		}
		return
	}
	specific, top := elicitationDecision(q)
	writePromptDecision(out, id, specific, top)
}

// elicitationDecision is the Elicitation hook output an answer makes: accept for Done, continue,
// else decline, with an answer in words as the reason.
func elicitationDecision(q store.Question) (specific, top map[string]any) {
	if q.AnswerOption > 0 && q.Answer() == optElicitDone {
		return map[string]any{"hookEventName": "Elicitation", "action": "accept", "content": map[string]any{}}, nil
	}
	reason := "declined by " + q.AnsweredBy
	if q.AnswerOption == 0 && strings.TrimSpace(q.AnswerText) != "" {
		reason = q.AnswerText
	}
	return map[string]any{"hookEventName": "Elicitation", "action": "decline"}, map[string]any{"reason": reason}
}

// answerElicitation records Emil's answer to an MCP approval, which the waiting hook passes on.
// The desk never answers one: approving means Emil opened the page and approved it himself.
func answerElicitation(q store.Question, option int, words, by string) (string, error) {
	if by == "desk" {
		return "", errors.New("an MCP approval is Emil's: he opens its page and approves it himself")
	}
	if option > len(q.Options) {
		return "", fmt.Errorf("%d: %w", option, store.ErrBadOption)
	}
	if (option == 0) == (strings.TrimSpace(words) == "") {
		return "", store.ErrEmptyAnswer
	}
	var got store.Question
	if err := withBareStore(func(st *store.Store) (err error) { got, err = st.Decide(q.ID, option, words, by); return err }); err != nil {
		return "", err
	}
	return "answered " + decisionLabel(q.ID) + ": " + oneLine(got.Answer()), nil
}
