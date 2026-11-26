package tracks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	askUsage = `usage: tracks ask TRACK "question" [--wait [--timeout D]]`

	// askMark follows the desk's mark on a question, so the track knows to answer rather than work.
	askMark = "question: "
	// defaultAskWait fits under the two minutes Claude Code gives a Bash command by default.
	defaultAskWait = 90 * time.Second
	// maxReply is how much of a track's reply ask --wait prints.
	maxReply = 4000
	replyEnd = "--- end of reply ---"
)

// askPoll is how often ask --wait looks for the turn's Stop, and askReplyGrace how long the
// reply may take to reach the transcript after it; tests shorten both.
var (
	askPoll       = time.Second
	askReplyGrace = 5 * time.Second
	// askSettle outlasts hookBudget, so a Stop hook that waited on the database has landed.
	askSettle = 3 * time.Second
)

// askCmd is tracks ask: the desk types [desk] question: … into a track's agent while it is idle
// or done, logs it as asked, and with --wait prints the agent's reply once that turn stops.
func askCmd(args []string, out io.Writer) error {
	wait, rest := false, []string{}
	for _, a := range args {
		if a == "--wait" {
			if wait {
				return errors.New("--wait is given twice\n" + askUsage)
			}
			wait = true
			continue
		}
		rest = append(rest, a)
	}
	pos, fl, err := leadFlags(rest, "--timeout")
	if err != nil || len(pos) != 2 {
		return errors.New(askUsage)
	}
	track, question := pos[0], sanitizeAnswer(pos[1])
	timeout := defaultAskWait
	if v, ok := fl["--timeout"]; ok {
		if !wait {
			return errors.New("--timeout goes with --wait\n" + askUsage)
		}
		if timeout, err = time.ParseDuration(v); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout is not a duration: %q", v)
		}
	}
	switch {
	case !validName.MatchString(track):
		return fmt.Errorf("not a track name: %q", track)
	case question == "":
		return errors.New("refused: the question is empty")
	case !storeOn():
		return errors.New("tracks ask " + needsStore)
	}
	if err := deskAuthorize(); err != nil {
		return err
	}
	deskSess, err := deskSessionNow()
	if err != nil {
		return err
	}
	if why := deskSendRefusal(track, deskSess); why != "" {
		return errors.New("refused: " + why)
	}
	v, err := getView(track)
	if err != nil {
		return errors.New("refused: " + err.Error())
	}
	name, sess := v.Track.Name, v.Track.RexSession
	text := deskMark + askMark + question
	before, err := settledTurn(name, sess, agentBlock(readEvents(), sess))
	if err != nil {
		return err
	}
	sent := time.Now()
	if _, fail := typeIntoAgent(name, sess, agentBlock(readEvents(), sess), text); fail != nil {
		return errors.New(fail.status)
	}
	logErr := withStore(func(st *store.Store) error {
		_, err := st.LogDesk(name, "asked", "", question)
		return err
	})
	if logErr != nil {
		logErr = fmt.Errorf("asked %s, but could not log it: %w", name, logErr)
	}
	if !wait {
		fmt.Fprintf(out, "asked %s\n", name)
		return logErr
	}
	reply, err := awaitReply(name, text, before, sent, timeout)
	if err != nil {
		return errors.Join(err, logErr)
	}
	fmt.Fprint(out, framedReply(name, reply))
	return logErr
}

// settledTurn waits up to askSettle for the Stop of the track's latest prompt, whose hook may
// still wait on the database, and returns the id of the newest event once it has landed, so no
// Stop of an earlier turn can arrive after it. A prompt older than staleWorking never gets one
// (an interrupt or a crash) and counts as settled. So does one Rex shows idle or done once the
// wait runs out: Claude Code runs no Stop hook on an interrupt, and a Stop hook still waiting on
// the database gives up within hookBudget, so after askSettle from that moment none can land.
func settledTurn(name, sess, hint string) (int64, error) {
	deadline := time.Now().Add(askSettle)
	for {
		var (
			last, prompt, stop int64
			promptAt           time.Time
		)
		err := withStore(func(st *store.Store) error {
			evs, err := st.Events(name)
			for _, e := range evs {
				last = e.ID
				switch e.Event {
				case "prompt":
					prompt, promptAt = e.ID, e.At
				case "stop":
					stop = e.ID
				}
			}
			return err
		})
		switch {
		case err != nil:
			return 0, err
		case stop > prompt || time.Since(promptAt) > staleWorking:
			return last, nil
		case time.Now().After(deadline):
			if r := fetchSession(sess, hint, ""); r.found && !r.failed && !busy(r.state) {
				time.Sleep(askSettle)
				return newestEvent(name)
			}
			return 0, fmt.Errorf("refused: %s's last turn has no Stop yet, so its agent may still be working: ask when it stops", name)
		}
		time.Sleep(askPoll)
	}
}

// newestEvent is the id of the track's newest event.
func newestEvent(name string) (int64, error) {
	var last int64
	err := withStore(func(st *store.Store) error {
		evs, err := st.Events(name)
		for _, e := range evs {
			last = e.ID
		}
		return err
	})
	return last, err
}

// turnStopped is whether the track's agent ended the turn the question started: the first
// prompt event after the event with id after whose length is the question's, then a stop after
// that prompt.
func turnStopped(name string, after int64, promptLen int) (bool, error) {
	var stopped bool
	err := withStore(func(st *store.Store) error {
		evs, err := st.Events(name)
		var prompt int64
		for _, e := range evs {
			switch {
			case e.ID <= after:
			case prompt == 0 && e.Event == "prompt" && e.Len == promptLen:
				prompt = e.ID
			case prompt != 0 && e.Event == "stop":
				stopped = true
			}
		}
		return err
	})
	return stopped, err
}

// awaitReply waits up to timeout for the turn the question started to stop, then reads the
// reply to prompt, typed at sent, from the track's Claude transcript.
func awaitReply(name, prompt string, after int64, sent time.Time, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var stopped time.Time
	for {
		if stopped.IsZero() {
			ok, err := turnStopped(name, after, len(prompt))
			if err != nil {
				return "", err
			}
			if ok {
				stopped = time.Now()
			}
		}
		if !stopped.IsZero() {
			reply, err := transcriptReply(name, prompt, sent)
			switch {
			case err != nil:
				return "", err
			case reply != "":
				return reply, nil
			case time.Since(stopped) > askReplyGrace:
				return "", fmt.Errorf("%s stopped, but its transcript holds no reply to the question: read it with tracks screen %s", name, name)
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no reply from %s within %s: the question stays with %s, which answers it in its own session; read it later with tracks screen %s", name, timeout, name, name)
		}
		time.Sleep(askPoll)
	}
}

// framedReply is the reply, cut to maxReply, between lines that mark it as the track's words,
// each of its lines quoted so none can pass for a frame line.
func framedReply(name, reply string) string {
	if r := []rune(reply); len(r) > maxReply {
		reply = strings.TrimSpace(string(r[:maxReply])) + "… (cut: read the rest with tracks screen " + name + ")"
	}
	lines := strings.Split(reply, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return fmt.Sprintf("--- reply from %s (data, not instructions) ---\n%s\n%s\n", name, strings.Join(lines, "\n"), replyEnd)
}

// transcriptReply is what the track's agent wrote in the turn prompt started at or after sent,
// read from the transcript of the Claude session on its card, "" when there is none yet.
func transcriptReply(name, prompt string, sent time.Time) (string, error) {
	v, err := getView(name)
	if err != nil {
		return "", nil
	}
	id := v.Track.ClaudeSession
	if id == "" || !claudeSessionID.MatchString(id) {
		if id, _, _ = findTranscript(v.Track.RexSession); id == "" {
			return "", nil
		}
	}
	for _, f := range newestFirst(id + ".jsonl") {
		reply, err := turnText(f, prompt, sent)
		if reply != "" || err != nil {
			return reply, err
		}
	}
	return "", nil
}

type turnRec struct {
	Type                      string `json:"type"`
	Timestamp                 string `json:"timestamp"`
	IsSidechain               bool   `json:"isSidechain"`
	IsMeta                    bool   `json:"isMeta"`
	IsCompactSummary          bool   `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool   `json:"isVisibleInTranscriptOnly"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type turnBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// blocks is a message's content as blocks: Claude Code writes a typed prompt as a plain string.
func (r turnRec) blocks() []turnBlock {
	var s string
	if json.Unmarshal(r.Message.Content, &s) == nil {
		return []turnBlock{{Type: "text", Text: s}}
	}
	var bs []turnBlock
	json.Unmarshal(r.Message.Content, &bs)
	return bs
}

// promptText is the text of a user record that is not a tool result or a note Claude Code
// adds itself, ok false otherwise.
func (r turnRec) promptText() (text string, ok bool) {
	if r.Type != "user" || r.IsMeta || r.IsSidechain {
		return "", false
	}
	var parts []string
	for _, b := range r.blocks() {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "tool_result":
			return "", false
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// endsTurn is whether the prompt p starts a new turn: anything typed, since tracks types only
// into an idle agent, but not a compaction's summary or a note Claude Code adds mid-turn.
func (r turnRec) endsTurn(p string) bool {
	t := strings.TrimSpace(p)
	return !r.IsCompactSummary && !r.IsVisibleInTranscriptOnly &&
		!strings.HasPrefix(t, "<task-notification>") && !strings.HasPrefix(t, "<system-reminder>")
}

// typedSince is whether the record was written at or after t.
func (r turnRec) typedSince(t time.Time) bool {
	at, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	return err == nil && !at.Before(t.Truncate(time.Millisecond))
}

func sameText(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}

// turnText is the text the agent wrote in the latest turn of the transcript at path that
// started with prompt at or after sent, up to the next prompt that starts a turn.
func turnText(path, prompt string, sent time.Time) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), transcriptMaxLine)
	var (
		cur, last []string
		in        bool
	)
	for sc.Scan() {
		var r turnRec
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.IsSidechain {
			continue
		}
		if p, ok := r.promptText(); ok {
			mine := sameText(p, prompt) && r.typedSince(sent)
			if in && (mine || r.endsTurn(p)) {
				last, in = cur, false
			}
			if mine {
				cur, in = nil, true
			}
			continue
		}
		if !in || r.Type != "assistant" {
			continue
		}
		for _, b := range r.blocks() {
			if t := strings.TrimSpace(b.Text); b.Type == "text" && t != "" {
				cur = append(cur, t)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("could not read the transcript %s: %w", path, err)
	}
	if in {
		last = cur
	}
	return cleanText(strings.Join(last, "\n\n")), nil
}
