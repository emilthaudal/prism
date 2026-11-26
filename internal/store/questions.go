package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// questionsSchema is the schema that keyed a question to the card ask it mirrors.
const questionsSchema = 10

// attachmentsSchema is the schema that gave a question its attachments column.
const attachmentsSchema = 12

// deliverySchema is the schema from which an answered agent question with no delivered_at is an
// answer still waiting to be typed into its track's agent; earlier answers were all typed at once.
const deliverySchema = 13

const (
	maxOptions    = 9
	recommendMark = "Recommend:"
	maxLabelRunes = 80
)

var (
	ErrNoQuestion     = errors.New("no such question")
	ErrQuestionClosed = errors.New("the question is closed already")
	ErrNoOptions      = errors.New("a question needs at least one option")
	ErrTooManyOptions = errors.New("a question has at most 9 options")
	ErrNoRecommend    = errors.New("a question needs a recommended option")
	ErrBadOption      = errors.New("no such option")
	ErrBadFrom        = errors.New("from must be desk, agent or check")
	ErrEmptyAnswer    = errors.New("an answer is an option or some words")
)

// QOption is one answer a question offers.
type QOption struct {
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Attachment  *Attachment `json:"attachment,omitempty"`
}

// Evidence is one fact behind a recommendation, with where it comes from.
type Evidence struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

// Question is a questions row. Recommended is the 1-based number of the recommended option, 0 for
// none; Recommendation is the recommendation in words, which a question mirrored from an ask keeps
// when the ask had no options to point at. AskID is the card ask it mirrors, 0 for none. Attachments
// are what the question as a whole points at; an option's own is on the option.
type Question struct {
	ID                    int64
	Track                 string
	TrackID               int64
	SessionID             string
	From                  string
	Question              string
	Options               []QOption
	Recommended           int
	Recommendation        string
	Evidence              []Evidence
	Links                 []string
	Attachments           []Attachment
	AskID                 int64
	CreatedAt             time.Time
	AnswerOption          int
	AnswerText            string
	AnsweredBy            string
	AnsweredAt            time.Time
	DeliveredAt, ClosedAt time.Time
}

func (q Question) Open() bool { return q.ClosedAt.IsZero() }

// Answer is the answer in words: the option's label, or the text given.
func (q Question) Answer() string {
	if q.AnswerOption >= 1 && q.AnswerOption <= len(q.Options) {
		return q.Options[q.AnswerOption-1].Label
	}
	return q.AnswerText
}

// QuestionSpec is what tracks decision add gives.
type QuestionSpec struct {
	Track, SessionID, From, Question string
	Options                          []QOption
	Recommended                      int
	Evidence                         []Evidence
	Links                            []string
}

const questionCols = `q.id, COALESCE(t.name, ''), COALESCE(q.track_id, 0), COALESCE(q.session_id, ''), q.from_kind, q.question,
  q.options, COALESCE(q.recommended, 0), q.recommendation, q.evidence, q.links, COALESCE(q.ask_id, 0), q.created_at,
  COALESCE(q.answer_option, 0), q.answer_text, q.answered_by, q.answered_at, q.delivered_at, q.closed_at, q.attachments`

const questionFrom = ` FROM questions q LEFT JOIN tracks t ON t.id = q.track_id`

func scanQuestion(r scanner) (Question, error) {
	var q Question
	var opts, ev, links, att string
	var created, answered, delivered, closed sql.NullString
	err := r.Scan(&q.ID, &q.Track, &q.TrackID, &q.SessionID, &q.From, &q.Question, &opts, &q.Recommended, &q.Recommendation,
		&ev, &links, &q.AskID, &created, &q.AnswerOption, &q.AnswerText, &q.AnsweredBy, &answered, &delivered, &closed, &att)
	if err != nil {
		return q, err
	}
	if err := json.Unmarshal([]byte(att), &q.Attachments); err != nil {
		return q, fmt.Errorf("question %d attachments: %w", q.ID, err)
	}
	if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
		return q, fmt.Errorf("question %d options: %w", q.ID, err)
	}
	if err := json.Unmarshal([]byte(ev), &q.Evidence); err != nil {
		return q, fmt.Errorf("question %d evidence: %w", q.ID, err)
	}
	for _, l := range strings.Split(links, ",") {
		if l = strings.TrimSpace(l); l != "" {
			q.Links = append(q.Links, l)
		}
	}
	return q, parseTimes(created, &q.CreatedAt, answered, &q.AnsweredAt, delivered, &q.DeliveredAt, closed, &q.ClosedAt)
}

func questionByID(q querier, id int64) (Question, error) {
	v, err := scanQuestion(q.QueryRow(`SELECT `+questionCols+questionFrom+` WHERE q.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNoQuestion
	}
	return v, err
}

func queryQuestions(q querier, where string, args ...any) ([]Question, error) {
	rows, err := q.Query(`SELECT `+questionCols+questionFrom+` WHERE `+where+` ORDER BY q.created_at, q.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		v, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// AddQuestion is tracks decision add: it refuses a question with no options or no recommendation.
func (s *Store) AddQuestion(sp QuestionSpec) (Question, error) {
	switch {
	case strings.TrimSpace(sp.Question) == "":
		return Question{}, errors.New("the question is empty")
	case len(sp.Options) == 0:
		return Question{}, ErrNoOptions
	case len(sp.Options) > maxOptions:
		return Question{}, ErrTooManyOptions
	case sp.Recommended == 0:
		return Question{}, ErrNoRecommend
	case sp.Recommended < 1 || sp.Recommended > len(sp.Options):
		return Question{}, fmt.Errorf("--recommend %d: %w", sp.Recommended, ErrBadOption)
	case sp.From != "desk" && sp.From != "agent" && sp.From != "check":
		return Question{}, ErrBadFrom
	}
	if sp.Evidence == nil {
		sp.Evidence = []Evidence{}
	}
	var out Question
	err := s.tx(func(tx *wtx) error {
		// A desk question with no track (an install, a repo-wide call) keeps track_id NULL.
		var track any
		if sp.Track != "" || sp.From != "desk" {
			r, err := resolve(tx, sp.Track)
			if err != nil {
				return err
			}
			if r.Closed {
				return ErrClosed
			}
			track = r.Track.ID
		}
		var sess any
		if sp.SessionID != "" {
			sess = sp.SessionID
		}
		res, err := tx.Exec(`INSERT INTO questions (track_id, session_id, from_kind, question, options, recommended, evidence, links, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, track, sess, sp.From, strings.TrimSpace(sp.Question), jsonOf(sp.Options),
			sp.Recommended, jsonOf(sp.Evidence), strings.Join(sp.Links, ","), fmtTime(s.now()))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		out, err = questionByID(tx, id)
		return err
	})
	return out, err
}

// GetQuestion reads one question, open or not.
func (s *Store) GetQuestion(id int64) (Question, error) {
	var out Question
	err := s.read(func(q querier) (err error) {
		out, err = questionByID(q, id)
		return err
	})
	return out, err
}

// OpenQuestions are the questions not closed yet, oldest first.
func (s *Store) OpenQuestions() ([]Question, error) {
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.closed_at IS NULL`)
		return err
	})
	return out, err
}

// AnsweredQuestions are the questions answered since since, oldest first: what tracks patterns
// reads for the kinds Emil always answers with the recommendation.
func (s *Store) AnsweredQuestions(since time.Time) ([]Question, error) {
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.answered_at IS NOT NULL AND q.answered_at >= ?`, fmtTime(since))
		return err
	})
	return out, err
}

// CloseQuestion is tracks decision close:the question leaves the list unanswered, and note says why.
func (s *Store) CloseQuestion(id int64, note string) error {
	return s.tx(func(tx *wtx) error {
		q, err := questionByID(tx, id)
		if err != nil {
			return err
		}
		if !q.Open() {
			return ErrQuestionClosed
		}
		_, err = tx.Exec(`UPDATE questions SET closed_at = ?, answer_text = ? WHERE id = ?`, fmtTime(s.now()), strings.TrimSpace(note), id)
		return err
	})
}

// Decide records the answer to an open question, which closes it: option is 1-based, or text
// is the answer in words. by is emil or desk.
func (s *Store) Decide(id int64, option int, text, by string) (Question, error) {
	return s.decide(id, option, text, by, false)
}

// DecideDelivered is Decide for an answer that already reached whoever asked, so it is
// recorded as delivered in the same write.
func (s *Store) DecideDelivered(id int64, option int, text, by string) (Question, error) {
	return s.decide(id, option, text, by, true)
}

func (s *Store) decide(id int64, option int, text, by string, delivered bool) (Question, error) {
	got, err := s.DecideMany([]Decision{{ID: id, Option: option, Text: text}}, by, delivered)
	if err != nil {
		return Question{}, err
	}
	return got[0], nil
}

// Decision is one answer of DecideMany: option is 1-based, or Text is the answer in words.
type Decision struct {
	ID     int64
	Option int
	Text   string
}

// DecideMany records several answers in one write, all stamped with the same time, so the answers
// of one batch can be told apart from separate answers; nothing is recorded when any is refused.
func (s *Store) DecideMany(ds []Decision, by string, delivered bool) ([]Question, error) {
	var out []Question
	err := s.tx(func(tx *wtx) error {
		now := fmtTime(s.now())
		out = out[:0]
		for _, d := range ds {
			text := strings.TrimSpace(d.Text)
			if (d.Option == 0) == (text == "") {
				return ErrEmptyAnswer
			}
			q, err := questionByID(tx, d.ID)
			if err != nil {
				return err
			}
			if !q.Open() {
				return ErrQuestionClosed
			}
			if d.Option != 0 && (d.Option < 1 || d.Option > len(q.Options)) {
				return fmt.Errorf("%d: %w", d.Option, ErrBadOption)
			}
			var opt any
			if d.Option != 0 {
				opt = d.Option
			}
			var deliveredAt any
			if delivered {
				deliveredAt = now
			}
			if _, err := tx.Exec(`UPDATE questions SET answer_option = ?, answer_text = ?, answered_by = ?, answered_at = ?, delivered_at = ?, closed_at = ? WHERE id = ?`,
				opt, text, by, now, deliveredAt, now, d.ID); err != nil {
				return err
			}
			got, err := questionByID(tx, d.ID)
			if err != nil {
				return err
			}
			out = append(out, got)
		}
		return nil
	})
	return out, err
}

// UndeliveredAnswers are answered questions nobody delivered yet that a track's agent is not the
// asker of: what the desk reads with tracks wait.
func (s *Store) UndeliveredAnswers() ([]Question, error) {
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.from_kind != 'agent' AND q.answered_at IS NOT NULL AND q.delivered_at IS NULL`)
		return err
	})
	return out, err
}

// QueuedAnswers are the answers to a track agent's questions that are recorded and not typed
// into the agent yet, oldest answer first. It includes a permission prompt's answer, which a
// hook passes on and nothing types; the caller leaves those out.
func (s *Store) QueuedAnswers() ([]Question, error) {
	var out []Question
	err := s.read(func(q querier) (err error) {
		out, err = queryQuestions(q, `q.from_kind = 'agent' AND q.track_id IS NOT NULL AND q.answered_at IS NOT NULL AND q.delivered_at IS NULL`)
		return err
	})
	slices.SortStableFunc(out, func(a, b Question) int { return a.AnsweredAt.Compare(b.AnsweredAt) })
	return out, err
}

// ReopenQuestion takes back an answer that could not be delivered: the question is open again,
// with note among its evidence so Emil sees why. It does nothing to a question whose answer
// was delivered or that is not answered.
func (s *Store) ReopenQuestion(id int64, note string) error {
	return s.tx(func(tx *wtx) error {
		q, err := questionByID(tx, id)
		if err != nil {
			return err
		}
		if q.AnsweredAt.IsZero() || !q.DeliveredAt.IsZero() {
			return nil
		}
		ev := append(slices.Clone(q.Evidence), Evidence{Text: note, Source: "prism server"})
		_, err = tx.Exec(`UPDATE questions SET answer_option = NULL, answer_text = '', answered_by = '', answered_at = NULL, closed_at = NULL, evidence = ? WHERE id = ?`,
			jsonOf(ev), id)
		return err
	})
}

// MarkDelivered stamps delivered_at on answers that were handed on.
func (s *Store) MarkDelivered(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(func(tx *wtx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE questions SET delivered_at = ? WHERE id = ? AND delivered_at IS NULL`, fmtTime(s.now()), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// RefreshQuestions mirrors every open track's asks again, so open questions follow the parser.
func (s *Store) RefreshQuestions() error {
	if s.stale {
		return nil
	}
	return s.tx(func(tx *wtx) error { return s.mirrorAllOpen(tx) })
}

// mirrorAllOpen gives every open track's open asks their questions, for a database that had none.
func (s *Store) mirrorAllOpen(tx *wtx) error {
	rows, err := tx.Query(`SELECT id FROM tracks WHERE closed_at IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := mirrorAsks(tx, id, s.now()); err != nil {
			return err
		}
	}
	return nil
}

// mirror is mirrorAsks unless another process is migrating, when the table may not be there yet.
func (s *Store) mirror(tx *wtx, trackID int64, now time.Time) error {
	if s.stale {
		return nil
	}
	return mirrorAsks(tx, trackID, now)
}

// mirrorAsks makes the track's open agent asks the questions of the list: each ask gets one
// question, kept for as long as the ask keeps its id, and a question whose ask is closed is
// closed with it. An answered question stays as it is.
func mirrorAsks(tx *wtx, trackID int64, now time.Time) error {
	asks, err := openAsks(tx, trackID)
	if err != nil {
		return err
	}
	for _, a := range asks {
		if a.Kind != kindAgent {
			continue
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM questions WHERE ask_id = ?`, a.ID).Scan(&n); err != nil {
			return err
		}
		p := ParseAsk(a.Text)
		if n > 0 {
			// The parser may have learned to read this ask better since it was mirrored: an
			// unanswered question follows it, so asks open before a parser change get its options.
			if _, err := tx.Exec(`UPDATE questions SET question = ?, options = ?, recommended = ?, recommendation = ?, attachments = ?
				WHERE ask_id = ? AND answered_at IS NULL AND closed_at IS NULL`, p.Question, jsonOf(p.Options), nullInt(p.Recommended),
				p.Recommendation, jsonOf(nonNilAtt(p.Attachments)), a.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT INTO questions (track_id, from_kind, question, options, recommended, recommendation, attachments, ask_id, created_at)
			VALUES (?, 'agent', ?, ?, ?, ?, ?, ?, ?)`, trackID, p.Question, jsonOf(p.Options), nullInt(p.Recommended), p.Recommendation,
			jsonOf(nonNilAtt(p.Attachments)), a.ID, fmtTime(a.CreatedAt)); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE questions SET closed_at = ? WHERE track_id = ? AND ask_id IS NOT NULL AND closed_at IS NULL
		AND ask_id NOT IN (SELECT id FROM asks WHERE track_id = ? AND closed_at IS NULL)`, fmtTime(now), trackID, trackID)
	return err
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// ParsedAsk is an ask's text read as a question. Attachments are the "See:" references that point at
// the question as a whole; an option's own is on the option.
type ParsedAsk struct {
	Question       string
	Options        []QOption
	Recommended    int
	Recommendation string
	Attachments    []Attachment
}

var (
	numberedMark = regexp.MustCompile(`(?:^|\s)\(?(\d)[.)]\s`)
	yesNoStart   = regexp.MustCompile(`(?i)^(yes|no)\b`)
)

// ParseAsk reads an ask's text: the question before its last "Recommend:", the options it names
// in the numbered form (1) a 2) b), and which option the recommendation picks, when it holds one
// option's number. Without numbered options it reads a prose list (see proseOptions), and the
// recommendation picks the option whose label it starts with. An ask with no options whose
// recommendation starts with yes or no is a yes/no question: it gets the options Yes and No, and
// the recommendation picks one. "See: X" references (see takeRefs) leave the text and become
// attachments: "(see: X)" inside an option is that option's, any other is the question's.
func ParseAsk(text string) ParsedAsk {
	text = strings.Join(strings.Fields(text), " ")
	text, refs := takeRefs(text)
	p := ParsedAsk{Question: text}
	if i := strings.LastIndex(text, recommendMark); i >= 0 {
		p.Question, p.Recommendation = strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+len(recommendMark):])
	}
	var spare []Attachment
	p.Options, spare = numberedOptions(p.Question, refs)
	if len(p.Options) > 0 {
		if i := numberedMark.FindStringIndex(p.Question); i != nil && strings.TrimSpace(p.Question[:i[0]]) != "" {
			p.Question = strings.TrimSpace(p.Question[:i[0]])
		}
	}
	var paren, bare []Attachment
	p.Question, paren, bare = resolveRefs(p.Question, refs)
	p.Attachments = append(p.Attachments, append(paren, bare...)...)
	p.Recommendation, paren, bare = resolveRefs(p.Recommendation, refs)
	p.Attachments = append(p.Attachments, append(paren, bare...)...)
	p.Attachments = append(p.Attachments, spare...)
	if len(p.Options) > 0 {
		p.Recommended = pickedOption(p.Options, p.Recommendation)
		return p
	}
	if p.Options = proseOptions(p.Question); len(p.Options) > 0 {
		p.Recommended = pickedByLabel(p.Options, p.Recommendation)
		return p
	}
	if m := yesNoStart.FindString(p.Recommendation); m != "" {
		p.Options = []QOption{{Label: "Yes"}, {Label: "No"}}
		p.Recommended = 1
		if strings.EqualFold(m, "no") {
			p.Recommended = 2
		}
	}
	return p
}

func numberedOptions(q string, refs []Attachment) ([]QOption, []Attachment) {
	marks := numberedMark.FindAllStringSubmatchIndex(q, -1)
	var starts, ends []int
	for _, m := range marks {
		if n, _ := strconv.Atoi(q[m[2]:m[3]]); n == len(starts)+1 {
			starts, ends = append(starts, m[1]), append(ends, m[0])
		}
	}
	if len(starts) < 2 {
		return nil, nil
	}
	var out []QOption
	var spare []Attachment
	for i, st := range starts {
		end := len(q)
		if i+1 < len(starts) {
			end = ends[i+1]
		}
		label, got, bare := resolveRefs(q[st:end], refs)
		spare = append(spare, bare...)
		label = strings.Trim(strings.TrimSpace(label), "?,;. ")
		label = strings.TrimSpace(strings.TrimSuffix(label, " or"))
		if label == "" {
			return nil, nil
		}
		o := QOption{Label: clip(label, maxLabelRunes)}
		if len(got) > 0 {
			o.Attachment, spare = &got[0], append(spare, got[1:]...)
		}
		out = append(out, o)
	}
	return out, spare
}

var wholeNumber = regexp.MustCompile(`\d+`)

// pickedOption is the option a recommendation names: only for numbered options, and only when
// the recommendation holds exactly one number and it is one of them.
func pickedOption(opts []QOption, rec string) int {
	nums := map[string]bool{}
	for _, n := range wholeNumber.FindAllString(rec, -1) {
		nums[n] = true
	}
	if len(opts) == 0 || len(nums) != 1 {
		return 0
	}
	for n := range nums {
		if i, err := strconv.Atoi(n); err == nil && i >= 1 && i <= len(opts) {
			return i
		}
	}
	return 0
}
