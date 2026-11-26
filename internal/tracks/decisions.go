package tracks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	decisionUsage = `usage: tracks decision add (TRACK | --desk) --question Q --option "label|description" (1-9 times) --recommend N [--evidence "text|source"]... [--link URL]... [--session ID] [--from desk|agent|check]
       tracks decision close ID [--note TEXT]`
	decideUsage    = "usage: tracks decide ID (N | --text '…') [--by emil|desk]"
	decisionsUsage = "usage: tracks decisions [--json]"
	decisionsDoc   = 1
	questionCol    = 60
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// parseMixed parses flags that may come after the positional arguments.
func parseMixed(fl *flag.FlagSet, args []string) (pos []string, err error) {
	for {
		if err := fl.Parse(args); err != nil {
			return nil, err
		}
		if fl.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fl.Arg(0))
		args = fl.Args()[1:]
	}
}

// questionID reads D12 or 12.
func questionID(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(s, "D"), "d"), 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("not a decision id: %q", s)
	}
	return n, nil
}

func decisionLabel(id int64) string { return "D" + strconv.FormatInt(id, 10) }

func splitBar(s string) (string, string) {
	a, b, _ := strings.Cut(s, "|")
	return strings.TrimSpace(a), strings.TrimSpace(b)
}

func decisionCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(decisionUsage)
	}
	if !storeOn() {
		return errors.New("tracks decision " + needsStore)
	}
	fl := flag.NewFlagSet("decision", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	switch args[0] {
	case "add":
		var opts, evidence, links listFlag
		question := fl.String("question", "", "")
		recommend := fl.Int("recommend", 0, "")
		session := fl.String("session", "", "")
		from := fl.String("from", "desk", "")
		fl.Var(&opts, "option", "")
		fl.Var(&evidence, "evidence", "")
		fl.Var(&links, "link", "")
		desk := fl.Bool("desk", false, "")
		pos, err := parseMixed(fl, args[1:])
		// --desk takes no TRACK: an install or a repo-wide question belongs to no one track.
		if err != nil || len(pos) != 1 && !*desk || len(pos) != 0 && *desk || *desk && *from != "desk" {
			return errors.New(decisionUsage)
		}
		track := ""
		if !*desk {
			track = pos[0]
		}
		sp := store.QuestionSpec{Track: track, SessionID: *session, From: *from, Question: *question, Recommended: *recommend, Links: links}
		for _, o := range opts {
			label, desc := splitBar(o)
			sp.Options = append(sp.Options, store.QOption{Label: label, Description: desc})
		}
		for _, e := range evidence {
			text, src := splitBar(e)
			sp.Evidence = append(sp.Evidence, store.Evidence{Text: text, Source: src})
		}
		var q store.Question
		err = withBareStore(func(st *store.Store) (err error) {
			q, err = st.AddQuestion(sp)
			return err
		})
		if err != nil {
			return fmt.Errorf("refused: %w", err)
		}
		fmt.Fprintln(out, decisionLabel(q.ID))
		return nil
	case "close":
		note := fl.String("note", "", "")
		pos, err := parseMixed(fl, args[1:])
		if err != nil || len(pos) != 1 {
			return errors.New(decisionUsage)
		}
		id, err := questionID(pos[0])
		if err != nil {
			return err
		}
		if err := withBareStore(func(st *store.Store) error { return st.CloseQuestion(id, *note) }); err != nil {
			return fmt.Errorf("refused: %w", err)
		}
		fmt.Fprintf(out, "closed %s\n", decisionLabel(id))
		return nil
	}
	return errors.New(decisionUsage)
}

type decisionsDocJSON struct {
	Schema      int            `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Decisions   []decisionJSON `json:"decisions"`
}

type decisionOptionJSON struct {
	N           int    `json:"n"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type decisionJSON struct {
	ID             string               `json:"id"`
	N              int64                `json:"n"`
	Track          *string              `json:"track"`
	Session        *string              `json:"session"`
	From           string               `json:"from"`
	Question       string               `json:"question"`
	Options        []decisionOptionJSON `json:"options"`
	Recommended    *int                 `json:"recommended"`
	Recommendation string               `json:"recommendation"`
	Evidence       []store.Evidence     `json:"evidence"`
	Links          []string             `json:"links"`
	CreatedAt      string               `json:"created_at"`
}

func decisionOf(q store.Question) decisionJSON {
	d := decisionJSON{ID: decisionLabel(q.ID), N: q.ID, From: q.From, Question: q.Question, Recommendation: q.Recommendation,
		Options: []decisionOptionJSON{}, Evidence: q.Evidence, Links: q.Links, CreatedAt: q.CreatedAt.UTC().Format(time.RFC3339)}
	if q.Track != "" {
		d.Track = &q.Track
	}
	if q.SessionID != "" {
		d.Session = &q.SessionID
	}
	if q.Recommended > 0 {
		d.Recommended = &q.Recommended
	}
	for i, o := range q.Options {
		d.Options = append(d.Options, decisionOptionJSON{i + 1, o.Label, o.Description})
	}
	if d.Evidence == nil {
		d.Evidence = []store.Evidence{}
	}
	if d.Links == nil {
		d.Links = []string{}
	}
	return d
}

func decisionsCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("decisions", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return errors.New(decisionsUsage)
	}
	if !storeOn() {
		return errors.New("tracks decisions " + needsStore)
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) {
		qs, err = st.OpenQuestions()
		return err
	}); err != nil {
		return err
	}
	if *asJSON {
		doc := decisionsDocJSON{decisionsDoc, time.Now().UTC().Format(time.RFC3339), []decisionJSON{}}
		for _, q := range qs {
			doc.Decisions = append(doc.Decisions, decisionOf(q))
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTRACK\tAGE\tQUESTION\tRECOMMENDED")
	for _, q := range qs {
		track := q.Track
		if track == "" {
			track = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", decisionLabel(q.ID), track, shortDur(time.Since(q.CreatedAt)), clipRunes(oneLine(q.Question), questionCol), recommendedLabel(q))
	}
	return tw.Flush()
}

// recommendedLabel is the recommended option, or the recommendation in words.
func recommendedLabel(q store.Question) string {
	if q.Recommended >= 1 && q.Recommended <= len(q.Options) {
		return fmt.Sprintf("%d %s", q.Recommended, q.Options[q.Recommended-1].Label)
	}
	return shortRec(q.Recommendation, 40)
}

func decideCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("decide", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	text := fl.String("text", "", "")
	by := fl.String("by", "emil", "")
	pos, err := parseMixed(fl, args)
	if err != nil || len(pos) < 1 || len(pos) > 2 || (len(pos) == 2) == (*text != "") {
		return errors.New(decideUsage)
	}
	if *by != "emil" && *by != "desk" {
		return errors.New(decideUsage)
	}
	id, err := questionID(pos[0])
	if err != nil {
		return err
	}
	option := 0
	if len(pos) == 2 {
		if option, err = strconv.Atoi(pos[1]); err != nil || option < 1 {
			return fmt.Errorf("not an option number: %q", pos[1])
		}
	}
	if !storeOn() {
		return errors.New("tracks decide " + needsStore)
	}
	if *by == "emil" {
		// The desk answers --by desk; an answer recorded as Emil's from its own session would
		// pass as his decision.
		deskSess, err := deskSessionNow()
		if err != nil {
			return err
		}
		if caller := callerSession(); caller != "" && caller == deskSess {
			return errors.New("refused: the desk's session answers --by desk; --by emil is for Emil")
		}
	}
	status, err := answerQuestion(id, option, *text, *by, "")
	if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	fmt.Fprintln(out, status)
	return nil
}

// answerText is what a track's agent is told for an answer: the ask it answers, quoted, so the
// agent can't mistake which ask it is (a bare "Ask 1: go with 2, No." was read as answers to two
// asks), then the option picked by its label, or the words given.
func answerText(q store.Question, askN, option int, words string) string {
	what := words
	if what == "" {
		what = strings.TrimSuffix(q.Options[option-1].Label, ".") + "."
	}
	ask := "your ask " + quoteAsk(q.Question)
	if askN > 0 {
		ask = fmt.Sprintf("your ask %d, %s", askN, quoteAsk(q.Question))
	}
	return fmt.Sprintf("Emil's answer to %s: %s", ask, what)
}

// deliverAnswer is the one place an answer leaves for whoever asked. A track agent's question
// gets the answer typed into its pane as the pane's reply does; a question the desk or a check
// raised has no one to type to, and tracks wait hands its answer to the desk. Step 3 replaces
// this with a message to the asker's session.
func deliverAnswer(q store.Question, text, hint string) error {
	if q.From != "agent" || q.Track == "" {
		return nil
	}
	return typeAnswer(q.Track, []store.Question{q}, text, hint)
}

// typeAnswer types text into track's agent as the answer to qs, which are the track's questions
// that mirror a card ask.
func typeAnswer(track string, qs []store.Question, text, hint string) error {
	var asks []int
	var ids []int64
	for _, q := range qs {
		if q.AskID == 0 {
			continue
		}
		v, err := getView(track)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(v.Asks, func(a store.Ask) bool { return a.ID == q.AskID })
		if i < 0 {
			return fmt.Errorf("the ask behind %s is gone from %s's card", decisionLabel(q.ID), track)
		}
		asks, ids = append(asks, v.Asks[i].N), append(ids, q.AskID)
	}
	if hint == "" {
		if v, err := getView(track); err == nil {
			hint = agentBlock(readEvents(), v.Track.RexSession)
		}
	}
	msg := sendAnswer(track, text, hint, asks, ids)
	switch {
	case msg.sent:
		return nil
	case msg.busy && !msg.typed:
		return busyError{msg.status}
	}
	return errors.New(msg.status)
}

// busyError is a delivery refused only because the agent works or waits on a prompt: nothing
// was typed, so the answer can wait for it to stop.
type busyError struct{ status string }

func (e busyError) Error() string { return e.status }

// askNumber is the number of the card ask a question mirrors, 0 when it mirrors none.
func askNumber(q store.Question) int {
	if q.AskID == 0 || q.Track == "" {
		return 0
	}
	if v, err := getView(q.Track); err == nil {
		if i := slices.IndexFunc(v.Asks, func(a store.Ask) bool { return a.ID == q.AskID }); i >= 0 {
			return v.Asks[i].N
		}
	}
	return 0
}

// answerQuestion answers question id with option (1-based) or words, by emil or desk, delivers
// the answer and records it. Nothing is recorded when delivery fails, so the question stays open,
// except that Emil's answer to an agent that is only busy is recorded as queued (see answerQuestionQueued).
func answerQuestion(id int64, option int, words, by, hint string) (string, error) {
	status, _, err := answerQuestionQueued(id, option, words, by, hint)
	return status, err
}

// answerQuestionQueued is answerQuestion that also says whether the answer was only recorded:
// Emil's answer to a track agent's question that the agent was too busy to take is kept with
// delivered_at empty, and prism server types it when the agent stops.
func answerQuestionQueued(id int64, option int, words, by, hint string) (status string, queued bool, err error) {
	var q store.Question
	if err := withBareStore(func(st *store.Store) (err error) {
		q, err = st.GetQuestion(id)
		return err
	}); err != nil {
		return "", false, err
	}
	if !q.Open() {
		return "", false, store.ErrQuestionClosed
	}
	if isPermission(q) {
		status, err := answerPermission(q, option, words, by)
		return status, false, err
	}
	if isElicitation(q) {
		status, err := answerElicitation(q, option, words, by)
		return status, false, err
	}
	if by == "desk" && q.From == "agent" {
		return "", false, errors.New("the desk does not answer a track's own question: Emil does, in the pane, or the desk uses tracks desk answer")
	}
	if option > len(q.Options) {
		return "", false, fmt.Errorf("%d: %w", option, store.ErrBadOption)
	}
	if (option == 0) == (strings.TrimSpace(words) == "") {
		return "", false, store.ErrEmptyAnswer
	}
	text := strings.TrimSpace(words)
	if option > 0 {
		text = answerText(q, askNumber(q), option, "")
	}
	typed := q.From == "agent" && q.Track != ""
	if err := deliverAnswer(q, text, hint); err != nil {
		var wait busyError
		if !errors.As(err, &wait) || by != "emil" {
			return "", false, err
		}
		queued = true
	}
	var got store.Question
	err = withBareStore(func(st *store.Store) (err error) {
		if typed && !queued {
			got, err = st.DecideDelivered(id, option, words, by)
		} else {
			got, err = st.Decide(id, option, words, by)
		}
		return err
	})
	if err != nil {
		return "", false, fmt.Errorf("answered %s, but could not record it: %w", decisionLabel(id), err)
	}
	if queued {
		return queuedStatus(q), true, nil
	}
	return fmt.Sprintf("answered %s: %s", decisionLabel(id), got.Answer()), false, nil
}

func queuedStatus(q store.Question) string {
	return fmt.Sprintf("queued %s: typed into %s when its agent stops", decisionLabel(q.ID), q.Track)
}

// answeredLines are the answers to questions the desk or a check raised that nobody has read
// yet, as tracks wait prints them. Reading them marks them delivered.
func answeredLines() ([]string, error) {
	var lines []string
	err := withBareStore(func(st *store.Store) error {
		qs, err := st.UndeliveredAnswers()
		if err != nil || len(qs) == 0 {
			return err
		}
		var ids []int64
		for _, q := range qs {
			lines = append(lines, fmt.Sprintf("answered %s: %s", decisionLabel(q.ID), oneLine(q.Answer())))
			ids = append(ids, q.ID)
		}
		return st.MarkDelivered(ids)
	})
	return lines, err
}
