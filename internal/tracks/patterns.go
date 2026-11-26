package tracks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const patternsUsage = "usage: tracks patterns [--json] [--days N] [--min N]"

// patternSource is the evidence source of a desk decision that proposes a pattern as a standing
// order, followed by the kind: tracks patterns finds its own earlier proposals by it.
const patternSource = "tracks patterns: "

// patternKind is one kind of decision Emil keeps being asked. Match is tried on the question's
// first line, lower-cased. Rule is the rule a yes would write. Skill says where it goes: every
// kind here lets someone do more without asking, and tracks orders can only hold sessions back,
// so a yes is a skill rule Emil approves, never an order. Keep, when set, is why the kind stays
// an ask whatever the history says.
type patternKind struct {
	Key, Label string
	Match      *regexp.Regexp
	Skill      string
	Rule       string
	Keep       string
}

var patternKinds = []patternKind{
	{Key: "allow", Label: "Permission prompts (Allow Bash, Allow a tool)", Match: regexp.MustCompile(`^allow \S+:`),
		Keep: "each prompt is a different command, and a blanket allow would let any session run it"},
	{Key: "deploy-production", Label: "Deploy to production", Match: regexp.MustCompile(`\bto production\b|\bto prod\b`),
		Keep: "production deploys stay Emil's (his CLAUDE.md, tier 3)"},
	{Key: "delete", Label: "Delete or remove data", Match: regexp.MustCompile(`^(delete|remove|drop)\b`),
		Keep: "deleting data can't be undone"},
	{Key: "install", Label: "Install a finished change", Match: regexp.MustCompile(`^(install|land and install)\b`),
		Skill: "desk", Rule: "The desk installs a merged tracks change, or holds it, as it would recommend, without asking; installs that migrate tracks.db and Prism.app stay Emil's."},
	{Key: "close-track", Label: "Close this track?", Match: regexp.MustCompile(`^close\b`),
		Skill: "track", Rule: "When an Emil track's goal is met and nothing is left (no ask, check, PR or install waiting), it closes itself instead of asking \"Close this track?\"."},
	{Key: "start-track", Label: "Start a track or the next slice", Match: regexp.MustCompile(`^start\b`),
		Skill: "desk", Rule: "The desk starts a track for a Prism Linear issue Emil filed, or the next slice of one he started, without asking, when it has a free slot."},
	{Key: "merge", Label: "Merge a finished branch", Match: regexp.MustCompile(`^(merge|fast-forward)\b`),
		Skill: "desk", Rule: "The desk merges a finished tracks branch into main without asking, as The desk merges tracks main already says; drop this kind if that rule covers it."},
	{Key: "deploy-staging", Label: "Deploy to staging", Match: regexp.MustCompile(`\bdeploy\b.*\bto staging\b`),
		Keep: "Gatekeeper's confirm page is the ask, and no rule can click it"},
	{Key: "ci-gate", Label: "Approve a CircleCI hold", Match: regexp.MustCompile(`\bapprove\b.*\b(gate|hold|publish)\b|\bon hold\b`),
		Keep: "only Emil can approve a CircleCI hold"},
}

// kindOf is the kind of a question, nil when it fits none.
func kindOf(q store.Question) *patternKind {
	first, _, _ := strings.Cut(strings.TrimSpace(q.Question), "\n")
	first = strings.ToLower(first)
	for i := range patternKinds {
		if patternKinds[i].Match.MatchString(first) {
			return &patternKinds[i]
		}
	}
	return nil
}

// followedRecommendation is whether Emil's answer was the recommended option, or "go with your
// recommendation" in words.
func followedRecommendation(q store.Question) bool {
	if q.Recommended > 0 && q.AnswerOption == q.Recommended {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(q.AnswerText)), "go with your recommendation")
}

type patternExample struct {
	ID       string `json:"id"`
	Track    string `json:"track,omitempty"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Followed bool   `json:"followed"`
}

// pattern is one kind and how Emil answered it. Propose is whether the desk should ask to make it
// a standing order: at least min answers, every one the recommendation, and no proposal open or
// answered since. Why says why not, when it isn't.
type pattern struct {
	Key      string           `json:"kind"`
	Label    string           `json:"label"`
	Answered int              `json:"answered"`
	Followed int              `json:"followed"`
	Propose  bool             `json:"propose"`
	Why      string           `json:"why,omitempty"`
	Skill    string           `json:"skill,omitempty"`
	Rule     string           `json:"rule,omitempty"`
	Examples []patternExample `json:"examples"`
}

// findPatterns groups the questions Emil answered by kind. A desk decision whose evidence names
// patternSource+kind is an earlier proposal: while it is open the kind is not proposed again, and
// once answered only answers after it count, so a "no" holds until min new ones agree.
func findPatterns(qs []store.Question, min int) []pattern {
	lastProposal := map[string]store.Question{}
	proposals := map[int64]bool{}
	seen := map[int64]bool{}
	for _, q := range qs {
		for _, e := range q.Evidence {
			if k, ok := strings.CutPrefix(e.Source, patternSource); ok {
				lastProposal[strings.TrimSpace(k)] = q
				proposals[q.ID] = true
			}
		}
	}
	byKey := map[string]*pattern{}
	for _, q := range qs {
		// A question with no recommendation, such as a Gatekeeper confirm, says nothing about
		// whether Emil trusts one.
		noRec := q.Recommended == 0 && strings.TrimSpace(q.Recommendation) == ""
		if q.AnsweredBy != "emil" || q.AnsweredAt.IsZero() || noRec || proposals[q.ID] || seen[q.ID] {
			continue
		}
		seen[q.ID] = true
		k := kindOf(q)
		if k == nil {
			continue
		}
		if p, ok := lastProposal[k.Key]; ok && !p.AnsweredAt.IsZero() && !q.AnsweredAt.After(p.AnsweredAt) {
			continue
		}
		p := byKey[k.Key]
		if p == nil {
			p = &pattern{Key: k.Key, Label: k.Label, Skill: k.Skill, Rule: k.Rule, Examples: []patternExample{}}
			byKey[k.Key] = p
		}
		ok := followedRecommendation(q)
		p.Answered++
		if ok {
			p.Followed++
		}
		p.Examples = append(p.Examples, patternExample{decisionLabel(q.ID), q.Track, clipRunes(oneLine(q.Question), 100), clipRunes(oneLine(q.Answer()), 60), ok})
	}
	var out []pattern
	for _, k := range patternKinds {
		p := byKey[k.Key]
		if p == nil {
			continue
		}
		prop, open := lastProposal[k.Key]
		switch {
		case k.Keep != "":
			p.Why = "kept as an ask: " + k.Keep
		case open && prop.AnsweredAt.IsZero():
			p.Why = "proposed already in " + decisionLabel(prop.ID) + ", still open"
		case p.Answered < min:
			p.Why = fmt.Sprintf("only %d answered, fewer than %d", p.Answered, min)
		case p.Followed < p.Answered:
			p.Why = fmt.Sprintf("Emil chose otherwise %d of %d times", p.Answered-p.Followed, p.Answered)
		default:
			p.Propose = true
		}
		out = append(out, *p)
	}
	return out
}

func patternsCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("patterns", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "")
	days := fl.Int("days", 30, "")
	min := fl.Int("min", 3, "")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *days < 1 || *min < 1 {
		return errors.New(patternsUsage)
	}
	if !storeOn() {
		return errors.New("tracks patterns " + needsStore)
	}
	var qs []store.Question
	if err := withBareStore(func(st *store.Store) (err error) {
		qs, err = st.AnsweredQuestions(time.Now().AddDate(0, 0, -*days))
		if err != nil {
			return err
		}
		// An open proposal isn't answered, so read the open questions too.
		open, err := st.OpenQuestions()
		qs = append(qs, open...)
		return err
	}); err != nil {
		return err
	}
	ps := findPatterns(qs, *min)
	if *asJSON {
		if ps == nil {
			ps = []pattern{}
		}
		b, err := json.MarshalIndent(map[string]any{"days": *days, "min": *min, "patterns": ps}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	if len(ps) == 0 {
		fmt.Fprintf(out, "no answered decisions of a known kind in the last %d days\n", *days)
		return nil
	}
	for _, p := range ps {
		mark := "  "
		if p.Propose {
			mark = "→ "
		}
		fmt.Fprintf(out, "%s%s: %d of %d took the recommendation\n", mark, p.Label, p.Followed, p.Answered)
		if p.Propose {
			fmt.Fprintf(out, "    propose a %s-skill rule: %s\n", p.Skill, p.Rule)
		} else {
			fmt.Fprintf(out, "    not proposed: %s\n", p.Why)
		}
		for i, e := range p.Examples {
			if i == 3 {
				fmt.Fprintf(out, "    … %d more\n", len(p.Examples)-3)
				break
			}
			fmt.Fprintf(out, "    %s %s → %s\n", e.ID, e.Question, e.Answer)
		}
	}
	return nil
}
