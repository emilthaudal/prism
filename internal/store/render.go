package store

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// GeneratedLine marks a card file as a view of the database.
const GeneratedLine = "generated: tracks.db (write with tracks card)"

const (
	modeComment    = "# focus | background | waiting | parked (Emil sets this)"
	tierComment    = "# 1 | 2 | 3: the riskiest thing this track touches (you set it)"
	waitingComment = "# when mode is waiting: who or what, and how to check it"
	checkComment   = "# when waiting: pr N approved | pr N merged | pr N green | ci BRANCH green"
	linksComment   = "# ticket keys and PR URLs, comma-separated: EMI-273, PAN-213, https://github.com/org/repo/pull/7"
	rankComment    = "# order within its mode, lower first (Emil and the desk set this)"
	todayComment   = "# when focus: what done today means (Emil and the desk set this)"
	blockedComment = "# open tracks that must close first (Emil and the desk set this)"
	tierWidth      = 13
)

// gap is the run of spaces between a frontmatter value and its comment.
func gap(n int) string { return strings.Repeat(" ", n) }

func commented(key, val, comment string, width int) string {
	if val == "" {
		return key + ":" + gap(width) + comment
	}
	return key + ": " + val + gap(width) + comment
}

func plain(key, val string) string {
	if val == "" {
		return key + ":"
	}
	return key + ": " + val
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(time.RFC3339)
}

// Render is the card text of t with its open asks, in the format cardText writes and the
// card editors keep. asks must be in order; they are numbered from 1.
func Render(t Track, asks []Ask) string { return render(t, asks, false) }

// RenderView is Render plus the generated line a card file carries.
func RenderView(t Track, asks []Ask) string { return render(t, asks, true) }

func render(t Track, asks []Ask, generated bool) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	line("---")
	line("name: " + t.Name)
	line(commented("mode", t.Mode, modeComment, 8))
	if t.Rank > 0 {
		line(commented("rank", fmt.Sprint(t.Rank), rankComment, 8))
	}
	if t.Today != "" {
		line(commented("today", t.Today, todayComment, 8))
	}
	if len(t.BlockedBy) > 0 {
		line(commented("blocked_by", strings.Join(t.BlockedBy, blockerSep), blockedComment, 8))
	}
	if t.Tier == "" {
		line("tier: " + gap(tierWidth) + tierComment)
	} else {
		line("tier: " + t.Tier + gap(max(tierWidth-len(t.Tier), 1)) + tierComment)
	}
	line("dir: " + t.Dir)
	line(plain("rex_session", t.RexSession))
	line(commented("waiting_on", t.WaitingOn, waitingComment, 8))
	line(commented("check", t.Check, checkComment, 13))
	line(commented("links", t.Links, linksComment, 13))
	line(plain("updated", stamp(t.UpdatedAt)))
	if t.ClaudeSession != "" {
		line("claude_session: " + t.ClaudeSession)
	}
	if t.ClaudeCwd != "" {
		line("claude_cwd: " + t.ClaudeCwd)
	}
	if !t.AnsweredAt.IsZero() {
		line("answered: " + stamp(t.AnsweredAt))
	}
	for _, k := range slices.Sorted(maps.Keys(t.Extra.Meta)) {
		line(plain(k, t.Extra.Meta[k]))
	}
	if t.Closed() {
		line("closed: " + stamp(t.ClosedAt))
	}
	if generated {
		line(GeneratedLine)
	}
	line("---")

	where := t.Where
	if t.TierWhy != "" && t.Tier != "" {
		if where != "" {
			where += "\n"
		}
		where += fmt.Sprintf("Tier %s: %s", t.Tier, t.TierWhy)
	}
	whereTitle := "Where it is"
	if t.Closed() {
		whereTitle = "Outcome"
	}
	numbered := make([]string, len(asks))
	for i, a := range asks {
		numbered[i] = fmt.Sprintf("%d. %s", i+1, a.Text)
	}
	secs := [][2]string{
		{"Goal", t.Goal},
		{whereTitle, where},
		{"Needs me", strings.Join(numbered, "\n")},
		{"Standing orders", t.Standing},
		{"Later", t.Later},
	}
	for _, k := range slices.Sorted(maps.Keys(t.Extra.Sections)) {
		secs = append(secs, [2]string{k, t.Extra.Sections[k]})
	}
	// A section is its heading and body lines; a blank line separates it from the next.
	parts := make([]string, len(secs))
	for i, s := range secs {
		parts[i] = "## " + s[0] + "\n"
		if s[1] != "" {
			parts[i] += s[1] + "\n"
		}
	}
	b.WriteString(strings.Join(parts, "\n"))
	return b.String()
}
