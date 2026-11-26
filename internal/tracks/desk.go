package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
	"github.com/emilthaudal/prism/internal/store"
)

const (
	deskUsage = `usage: tracks desk [--json] [--idle D]
       tracks desk start
       tracks desk answer TRACK N "answer"
       tracks desk log TRACK nudged|noted "what the desk did" [--asked TEXT]
       tracks desk finish TRACK "outcome"   (close a finished track the desk started)
       tracks desk close N   (close an ask left from the pane's band)
       tracks desk restart   (prism server starts a fresh headless desk; the desk's own restart waits for its turn to end)`

	// deskAsksGone is why tracks desk ask and pickup refuse.
	deskAsksGone = "refused: the desk no longer raises asks in the pane: it brings Emil's decisions to him in its chat"

	deskSchema = 2

	// deskAnswersFor is how far back tracks desk shows Emil's answers: a desk that was away
	// for a few days still finds them.
	deskAnswersFor = 7 * 24 * time.Hour
)

// deskDoc is tracks desk --json.
type deskDoc struct {
	Schema      int             `json:"schema"`
	GeneratedAt string          `json:"generated_at"`
	Session     string          `json:"session"`
	IdleAfter   string          `json:"idle_after"`
	Asks        []deskAskDoc    `json:"asks"`
	Handled     []deskLogDoc    `json:"handled"`
	Answers     []deskAnswerDoc `json:"answers"`
	Stalls      []Stall         `json:"stalls"`
}

type deskAskDoc struct {
	ID             int64    `json:"id"`
	Track          string   `json:"track"`
	Question       string   `json:"question"`
	Recommendation string   `json:"recommendation"`
	Evidence       []string `json:"evidence"`
	Since          string   `json:"since"`
}

// deskAnswerDoc is an ask Emil has answered that the desk has not picked up: Kind is accepted
// (Answer is the recommendation he took) or reply (Answer is his text). The ask is no longer in asks.
type deskAnswerDoc struct {
	ID             int64    `json:"id"`
	Track          string   `json:"track"`
	Question       string   `json:"question"`
	Recommendation string   `json:"recommendation"`
	Evidence       []string `json:"evidence"`
	Kind           string   `json:"kind"`
	Answer         string   `json:"answer"`
	At             string   `json:"at"`
}

type deskLogDoc struct {
	ID    int64  `json:"id"`
	At    string `json:"at"`
	Track string `json:"track"`
	Kind  string `json:"kind"`
	Asked string `json:"asked"`
	Did   string `json:"did"`
}

// repeatFlags is leadFlags where a flag may be given again: each value is kept, in order.
func repeatFlags(args []string, want ...string) (pos []string, flags map[string][]string, err error) {
	flags = map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		if !slices.Contains(want, name) {
			return nil, nil, fmt.Errorf("unknown flag %s", name)
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			val = args[i]
		}
		flags[name] = append(flags[name], val)
	}
	return pos, flags, nil
}

func lastOf(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// deskCmd is tracks desk: read the desk, or write its answers, log and session.
func deskCmd(args []string, out io.Writer) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, rest := args[0], args[1:]
		if !storeOn() {
			return errors.New("tracks desk " + needsStore)
		}
		switch sub {
		case "ask", "pickup":
			return errors.New(deskAsksGone)
		case "close":
			return deskClose(rest, out)
		case "finish":
			return deskFinish(rest, out)
		case "answer":
			return deskAnswer(rest, out)
		case "log":
			return deskLog(rest, out)
		case "start":
			return deskStart(rest, out)
		case "restart":
			return deskRestart(rest, out)
		}
		return errors.New(deskUsage)
	}
	return deskShow(args, out)
}

// deskRestart is prism desk restart: it asks prism server to end the headless desk's session and
// start a fresh one. Run by the desk, it carries the desk's secret, so the server restarts once
// the desk's turn ends instead of refusing a desk that is mid-turn.
func deskRestart(args []string, out io.Writer) error {
	if len(args) > 0 {
		return errors.New(deskUsage)
	}
	h, err := deskui.NewHTTP()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := h.Post(ctx, "/v1/desk/restart", map[string]string{"desk_token": os.Getenv(deskTokenEnv)})
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	var reply struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(b, &reply) != nil || reply.Status == "" {
		reply.Status = "the desk is restarting"
	}
	fmt.Fprintln(out, reply.Status)
	return nil
}

// deskAuthorize is the write check: it reads the desk's session and applies deskCallerRefusal.
func deskAuthorize() error {
	sess, err := deskSessionNow()
	if err != nil {
		return err
	}
	if why := deskCallerRefusal(sess, callerSession()); why != "" {
		return errors.New("refused: " + why)
	}
	return nil
}

func deskSessionNow() (string, error) {
	var sess string
	err := withStore(func(st *store.Store) (err error) {
		sess, err = st.DeskSession()
		return err
	})
	return sess, err
}

// deskLogKinds are the kinds tracks desk log takes. Answers and starts are logged by the
// commands that make them, so u in the pane only ever takes back one that really happened.
var deskLogKinds = []string{"nudged", "noted"}

func deskLog(args []string, out io.Writer) error {
	pos, fl, err := repeatFlags(args, "--asked")
	if err != nil || len(pos) != 3 || len(fl["--asked"]) > 1 {
		return errors.New(deskUsage)
	}
	track, kind, did := pos[0], pos[1], sanitizeAnswer(pos[2])
	switch {
	case !validName.MatchString(track):
		return fmt.Errorf("not a track name: %q", track)
	case !slices.Contains(deskLogKinds, kind):
		return fmt.Errorf("kind must be %s: %q (tracks desk answer logs answers, tracks start logs starts)", strings.Join(deskLogKinds, " or "), kind)
	case did == "":
		return errors.New("refused: what the desk did is empty")
	}
	if err := deskAuthorize(); err != nil {
		return err
	}
	err = withStore(func(st *store.Store) error {
		_, err := st.LogDesk(track, kind, sanitizeAnswer(lastOf(fl["--asked"])), did)
		return err
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "logged %s %s\n", kind, track)
	return nil
}

// deskFinish is tracks desk finish TRACK "outcome": the desk closes a track it started, once its
// work is merged and nothing is left on its card, and logs it as closed, which Emil takes back
// with u in the handled log. A track Emil ever ran or took over is his to close, and a card with
// open asks or a check still waits on something.
func deskFinish(args []string, out io.Writer) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") {
		return errors.New(deskUsage)
	}
	name, did := args[0], sanitizeAnswer(args[1])
	switch {
	case !validName.MatchString(name):
		return fmt.Errorf("not a track name: %q", name)
	case did == "":
		return errors.New("refused: the outcome is empty")
	}
	if err := deskAuthorize(); err != nil {
		return err
	}
	if err := withStore(func(st *store.Store) error { return st.DeskOwnsTrack(name) }); err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	c, err := loadCard(name)
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	if asks, _ := leftovers(c); len(asks) > 0 {
		return fmt.Errorf("refused: %s has %s open", name, plural(len(asks), "ask"))
	}
	if chk := strings.TrimSpace(c.Meta["check"]); chk != "" {
		return fmt.Errorf("refused: %s waits on check: %s", name, chk)
	}
	_, note, err := closeTrack(name, time.Now(), "")
	if err != nil {
		return err
	}
	err = withStore(func(st *store.Store) error {
		_, err := st.LogDesk(name, "closed", "", did)
		return err
	})
	if err != nil {
		return fmt.Errorf("%s, but could not log it: %w", note, err)
	}
	fmt.Fprintln(out, note)
	return nil
}

// deskClose is tracks desk close N: it closes a desk ask left from the pane's band, which
// nothing else shows or closes now.
func deskClose(args []string, out io.Writer) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New(deskUsage)
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("not an ask number: %q", args[0])
	}
	err = withStore(func(st *store.Store) error { return st.CloseDeskAsk(id) })
	if errors.Is(err, store.ErrNoDeskAsk) {
		return fmt.Errorf("refused: %w: %d", err, id)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "closed desk ask %d\n", id)
	return nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func deskShow(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("desk", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print one JSON document")
	idleFlag := fs.String("idle", "", "how long an agent may sit idle with no ask")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(deskUsage)
	}
	fl := map[string]string{}
	if *idleFlag != "" {
		fl["--idle"] = *idleFlag
	}
	idle, err := deskIdle(fl)
	if err != nil {
		return err
	}
	if !storeOn() {
		return errors.New("tracks desk " + needsStore)
	}
	now := time.Now()
	var v store.DeskView
	var answers []store.DeskAsk
	if err := withStore(func(st *store.Store) (err error) {
		if v, err = st.Desk(startOfDay(now)); err != nil {
			return err
		}
		answers, err = st.DeskAnswers(now.Add(-deskAnswersFor))
		return err
	}); err != nil {
		return err
	}
	stalls, err := openStalls(now, idle)
	if err != nil {
		return err
	}
	if *asJSON {
		doc := deskDoc{Schema: deskSchema, GeneratedAt: now.UTC().Format(time.RFC3339), Session: v.Session, IdleAfter: idle.String(),
			Asks: []deskAskDoc{}, Handled: []deskLogDoc{}, Answers: []deskAnswerDoc{}, Stalls: stalls}
		if doc.Stalls == nil {
			doc.Stalls = []Stall{}
		}
		for _, a := range v.Asks {
			doc.Asks = append(doc.Asks, deskAskDoc{a.ID, a.Track, a.Question, a.Recommendation, nonNil(a.Evidence), a.CreatedAt.UTC().Format(time.RFC3339)})
		}
		for _, a := range answers {
			doc.Answers = append(doc.Answers, deskAnswerDoc{a.ID, a.Track, a.Question, a.Recommendation, nonNil(a.Evidence), a.AnswerKind, a.Answer, a.AnsweredAt.UTC().Format(time.RFC3339)})
		}
		for _, e := range v.Log {
			doc.Handled = append(doc.Handled, deskLogDoc{e.ID, e.At.UTC().Format(time.RFC3339), e.Track, e.Kind, e.Asked, e.Did})
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	fmt.Fprintf(out, "desk: %d open asks, %d handled today, %d stalled\n", len(v.Asks), len(v.Log), len(stalls))
	if exe, err := os.Executable(); err == nil {
		si, serr := readServeInfo()
		running := serr == nil && si.PID > 0 && syscall.Kill(si.PID, 0) == nil
		fmt.Fprintln(out, serveBuildLine(si, running, buildLabel(exe)))
	}
	if len(v.Asks) > 0 {
		fmt.Fprintln(out, "\nasks for Emil from the band (the desk now asks in its chat)")
		for _, a := range v.Asks {
			fmt.Fprintf(out, "[%d] %s: %s\n", a.ID, a.Track, a.Question)
			if a.Recommendation != "" {
				fmt.Fprintf(out, "    recommend: %s\n", a.Recommendation)
			}
			for _, l := range a.Evidence {
				fmt.Fprintf(out, "    - %s\n", l)
			}
		}
	}
	if len(answers) > 0 {
		fmt.Fprintln(out, "\nanswered by Emil")
		for _, a := range answers {
			fmt.Fprintf(out, "[%d] %s: %s: %s %s\n", a.ID, a.Track, a.Question, a.AnswerKind, a.Answer)
		}
	}
	if len(stalls) > 0 {
		fmt.Fprintln(out, "\nstalled")
		for _, s := range stalls {
			fmt.Fprintf(out, "%s: %s\n", s.Track, s.Detail)
		}
	}
	if len(v.Log) > 0 {
		fmt.Fprintln(out, "\nhandled today")
		for _, e := range v.Log {
			line := fmt.Sprintf("%s %s %s", e.At.Local().Format("15:04"), e.Kind, e.Track)
			if e.Asked != "" {
				line += " (" + e.Asked + ")"
			}
			fmt.Fprintf(out, "%s: %s\n", line, e.Did)
		}
	}
	return nil
}

// nonNil is s, or an empty slice for nil, so JSON shows [] and not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
