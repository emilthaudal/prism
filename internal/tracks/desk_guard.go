package tracks

import (
	"regexp"
	"strings"
)

// The desk asks Emil only through the question queue (tracks decision add), which he answers
// with one key and which keeps each question's D id. A numbered list of questions in chat makes
// him type, and its numbers move as items come and go. The desk skill says so; this guard is
// what catches the desk when it forgets.

var (
	numberedLine = regexp.MustCompile(`^\s*\d+[.)]\s`)
	decisionRef  = regexp.MustCompile(`\bD\d+\b`)
)

// outcomeReasked marks a desk reply prism server hid; Prism and the TUI show it collapsed.
const outcomeReasked = "reasked"

// reaskNotice is the line that opens the desk's next turn after prism server hid a reply.
const reaskNotice = "Prism: your last reply numbered questions in chat, which Prism hid from Emil. Raise each with tracks decision add (or --desk when no track fits), then reply naming the D ids."

// numbersQuestions is whether a desk reply numbers a question for Emil in chat: a numbered
// line that asks (a ? or a recommendation) and names no D id. A numbered list of facts, or one
// that points at D ids, passes. One line is enough: on 25 Nov most "Open:" lists held a single
// question, and no plain report had a numbered line that asks.
func numbersQuestions(text string) bool {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if !numberedLine.MatchString(l) || decisionRef.MatchString(l) {
			continue
		}
		if strings.Contains(l, "?") || strings.Contains(strings.ToLower(l), "recommend") {
			n++
		}
	}
	return n > 0
}
