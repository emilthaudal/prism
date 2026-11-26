package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldens are the tracks behind testdata/*.md. internal/tracks writes the same files from
// cardText (TestCardTextMatchesStoreGoldens), so a render that equals them equals cardText.
var goldens = []struct {
	file                  string
	name, mode, tier, dir string
	goal                  string
}{
	{"fresh.md", "fresh-track", "focus", "", "/Users/emil/Developer/tracks", DefaultGoal},
	{"tiered.md", "tiered.track_2", "background", "2", "/Users/emil/Developer/acme/pantry-api", "Ship the thing."},
	{"nodir.md", "nodir", "parked", "3", "", DefaultGoal},
}

func TestRenderEqualsCardTextFixtures(t *testing.T) {
	for _, g := range goldens {
		want, err := os.ReadFile(filepath.Join("testdata", g.file))
		if err != nil {
			t.Fatal(err)
		}
		fromValue := Render(Track{Name: g.name, Mode: g.mode, Tier: g.tier, Dir: g.dir, Goal: g.goal, Where: "Not started."}, nil)
		if fromValue != string(want) {
			t.Errorf("%s: Render of a Track differs from cardText's text:\n--- got\n%s\n--- want\n%s", g.file, fromValue, want)
		}

		s := newStore(t)
		if _, err := s.NewTrack(NewTrackSpec{Name: g.name, Mode: g.mode, Tier: g.tier, Dir: g.dir, Goal: g.goal}); err != nil {
			t.Fatal(err)
		}
		v := get(t, s, g.name)
		if got := Render(v.Track, v.Asks); got != string(want) {
			t.Errorf("%s: a stored track renders differently from cardText's text:\n--- got\n%s\n--- want\n%s", g.file, got, want)
		}
	}
}

func TestRenderViewAddsOnlyTheGeneratedLine(t *testing.T) {
	tr := Track{Name: "alpha", Mode: "focus", Goal: DefaultGoal, Where: "Not started."}
	plain := Render(tr, nil)
	view := RenderView(tr, nil)
	if got := strings.Replace(view, GeneratedLine+"\n", "", 1); got != plain {
		t.Errorf("view minus generated line differs from the plain card:\n%s", got)
	}
	if !strings.Contains(view, "updated:\n"+GeneratedLine+"\n---\n") {
		t.Errorf("generated line is not the last frontmatter line:\n%s", view)
	}
}

func TestRenderFullCard(t *testing.T) {
	stamp := time.Date(2026, 11, 20, 23, 48, 34, 0, time.FixedZone("CET", 3600))
	closed := time.Date(2026, 11, 21, 8, 0, 0, 0, time.FixedZone("CET", 3600))
	tr := Track{
		Name: "circleci-local-proof", Mode: "waiting", Tier: "2", TierWhy: "CI template change", Dir: "/d",
		RexSession: "session:1", ClaudeSession: "c-1", ClaudeCwd: "/c", WaitingOn: "a review", Check: "pr 179 approved",
		Links: "INC-154, OPS-1640", Goal: "Know.", Where: "Line one.\nLine two.", Standing: "- Commit.", Later: "- Revisit.\n- Again.",
		Extra:      Extra{Meta: map[string]string{"zeta": "z", "alpha": "a"}, Sections: map[string]string{"Notes": "kept"}},
		AnsweredAt: stamp, UpdatedAt: stamp,
	}
	asks := []Ask{{Text: "first"}, {Text: `Check "pr 179 approved" is now yes. Recommend: set mode to focus.`}}
	got := Render(tr, asks)
	ts := stamp.Local().Format(time.RFC3339)
	want := `---
name: circleci-local-proof
mode: waiting        # focus | background | waiting | parked (Emil sets this)
tier: 2            # 1 | 2 | 3: the riskiest thing this track touches (you set it)
dir: /d
rex_session: session:1
waiting_on: a review        # when mode is waiting: who or what, and how to check it
check: pr 179 approved             # when waiting: pr N approved | pr N merged | pr N green | ci BRANCH green
links: INC-154, OPS-1640             # ticket keys and PR URLs, comma-separated: EMI-273, PAN-213, https://github.com/org/repo/pull/7
updated: ` + ts + `
claude_session: c-1
claude_cwd: /c
answered: ` + ts + `
alpha: a
zeta: z
---
## Goal
Know.

## Where it is
Line one.
Line two.
Tier 2: CI template change

## Needs me
1. first
2. Check "pr 179 approved" is now yes. Recommend: set mode to focus.

## Standing orders
- Commit.

## Later
- Revisit.
- Again.

## Notes
kept
`
	if got != want {
		t.Errorf("--- got\n%s\n--- want\n%s", got, want)
	}

	tr.ClosedAt = closed
	out := Render(tr, nil)
	if !strings.Contains(out, "zeta: z\nclosed: "+closed.Local().Format(time.RFC3339)+"\n---\n") || !strings.Contains(out, "## Outcome\nLine one.") || strings.Contains(out, "## Where it is") {
		t.Errorf("closed card:\n%s", out)
	}
}

func TestRenderedAsksComeFromTheStoreInOrder(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"one", "two"}, Where: ptr("Here."), Tier: ptr("1"), TierWhy: "docs"}); err != nil {
		t.Fatal(err)
	}
	text, err := s.Render("alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{GeneratedLine + "\n", "## Where it is\nHere.\nTier 1: docs\n", "## Needs me\n1. one\n2. two\n\n## Standing orders\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered card lacks %q:\n%s", want, text)
		}
	}
	if _, err := s.Render("nobody"); err == nil {
		t.Error("Render of an unknown track succeeded")
	}
}
