package tracks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Only the skill's "Tier N:" line is hidden; prose that starts with "Tier 3" stays.
func TestTierLineHidesOnlyTheSkillFormat(t *testing.T) {
	got := strings.Join(whereLines("Tier 3 migration is blocked on the DBA.\nTier 2: product code.", 76), "\n")
	if !strings.Contains(got, "Tier 3 migration is blocked") {
		t.Fatalf("prose line hidden:\n%s", got)
	}
	if strings.Contains(got, "Tier 2:") {
		t.Fatalf("tier line shown:\n%s", got)
	}
}

// A note after a blank line below the asks is its own paragraph, not part of the last ask.
func TestNoteAfterAsksIsNotGluedToLastAsk(t *testing.T) {
	text := "1. Ship it? Recommend: yes.\n2. Ten? Recommend: no\n\nI will wait for you on all of these."
	out := strings.Join(needsLines(text, 76, lipgloss.NewStyle()), "\n")
	if strings.Contains(out, "no I will wait") {
		t.Fatalf("note glued to the arrow line:\n%s", out)
	}
	if !strings.Contains(out, "  I will wait for you on all of these.") {
		t.Fatalf("note missing:\n%s", out)
	}
	// A continuation line with no blank line before it still belongs to the ask.
	out = strings.Join(needsLines("1. Ship it\nbehind the flag? Recommend: yes.", 76, lipgloss.NewStyle()), "\n")
	if !strings.Contains(out, "Ship it behind the flag?") {
		t.Fatalf("continuation lost:\n%s", out)
	}
}

// A line that continues an ask after its Recommend: is a note under the arrow, not part of the pick.
func TestContinuationAfterRecommendIsANote(t *testing.T) {
	out := ansi.Strip(strings.Join(needsLines("1. Merge it? Recommend: yes.\n   CI was green at 10:02.", 76, lipgloss.NewStyle()), "\n"))
	if strings.Contains(out, "yes. CI was green") {
		t.Fatalf("continuation glued to the pick:\n%s", out)
	}
	if !strings.Contains(out, "→ yes.\n   CI was green at 10:02.") {
		t.Fatalf("note not under the arrow:\n%s", out)
	}
	// A quoted Recommend: on the first line does not steal the real one on the next.
	out = ansi.Strip(strings.Join(needsLines("1. The old card said \"Recommend: drop the flag\". Keep it?\n   Recommend: keep the flag.", 76, lipgloss.NewStyle()), "\n"))
	if !strings.Contains(out, "→ keep the flag.") {
		t.Fatalf("wrong pick:\n%s", out)
	}
}

// A list cut off at the bottom never ends on a group heading, and says how many tracks are below.
func TestCutListEndsOnATrack(t *testing.T) {
	lines := []string{" FOCUS", " a", " b", "", " PARKED", " p", " q"}
	kinds := []byte{lineGap, lineTrack, lineTrack, lineGap, lineGap, lineTrack, lineTrack}
	got := windowList(lines, kinds, 1, 5)
	if last := got[len(got)-2]; last != " b" {
		t.Fatalf("list ends on %q, want the last track before the cut:\n%s", last, strings.Join(got, "\n"))
	}
	if !strings.Contains(got[len(got)-1], "↓ 2 more") {
		t.Fatalf("no count of the hidden tracks:\n%s", strings.Join(got, "\n"))
	}
}

// A broken worktree ("not a git repository: PATH") is a real error, not "outside git".
func TestBrokenWorktreeIsARealError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "wt")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "x"},
		{"-C", repo, "worktree", "add", "-q", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.RemoveAll(filepath.Join(repo, ".git", "worktrees")); err != nil {
		t.Fatal(err)
	}
	_, err := sinceLog(&Card{Name: "x", Meta: map[string]string{"dir": wt}}, nil)
	if err == nil || err == errNotGit {
		t.Fatalf("broken worktree: got %v, want a real git error", err)
	}
	_, err = sinceLog(&Card{Name: "x", Meta: map[string]string{"dir": root}}, nil)
	if err != errNotGit {
		t.Fatalf("plain dir: got %v, want errNotGit", err)
	}
}
