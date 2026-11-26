package deskui

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite docs/desk-ui.md from the screens")

const screensIntro = "# prism tui: screens\n\n" +
	"Text captures of `prism tui --fixture mock/serve-fixture.json` at 200×50, and the narrower layouts, drawn by\n" +
	"`TestScreensDoc` in `internal/deskui/screens_test.go`; `go test ./internal/deskui -run TestScreensDoc -update`\n" +
	"redraws them. Colours are stripped. Times are UTC and the clock stands at 13:32, the fixture's `generated_at`.\n" +
	"Keys are in docs/keys.md, under `prism tui`.\n"

type screen struct {
	title, about string
	w, h         int
	steps        func(tm *testModel)
}

var screens = []screen{
	{"Opening", "The desk's chat in the middle with the reports as messages, the composer at the bottom, the board on the right.", 200, 50, func(*testModel) {}},
	{"A question selected", "tab to the board: the cursor is on D14, its evidence and link unfold, and the composer becomes D14's.", 200, 50, func(tm *testModel) { tm.press("tab") }},
	{"Answer, then the countdown", "enter takes the recommendation; it is sent when the 5 s run out, z takes it back. Two seconds in:", 200, 50, func(tm *testModel) {
		tm.press("tab", "enter")
		tm.advance(2 * time.Second)
	}},
	{"Typing while the desk talks", "Emil types; two desk entries arrive. The composer and its text stay put, and the entries wait under \"2 new\".", 200, 50, func(tm *testModel) {
		tm.typeText("merge 418, and hold the queue-api PR until I have looked")
		_ = tm.f.Emit("conversation", "upsert", Entry{ID: "n1", At: fixtureNow, Role: "desk", Text: "pantry-api passed shard 2/3."})
		_ = tm.f.Emit("conversation", "upsert", Entry{ID: "n2", At: fixtureNow, Role: "desk", Text: "Opening the queue-api PR."})
		tm.pump()
	}},
	{"A background session, read-only", "In the sessions column, pantry-api: its transcript with tool calls folded into steps. t takes it over, g gives it back.", 200, 50, func(tm *testModel) {
		tm.press("tab", "tab", "down", "down", "down")
	}},
	{"Two columns (120×40)", "Under 150 columns the sessions fold away: the board stands beside the chat, and the sessions take its place while they have focus.", 120, 40, func(*testModel) {}},
	{"One column (90×30)", "Under 100 columns only the focused column shows; its title bar names where tab goes.", 90, 30, func(tm *testModel) { tm.press("tab") }},
}

func renderScreensDoc(t *testing.T) string {
	var b strings.Builder
	b.WriteString(screensIntro)
	for _, s := range screens {
		tm := newTest(t, s.w, s.h)
		s.steps(tm)
		b.WriteString("\n## " + s.title + "\n\n" + s.about + "\n\n```text\n" + tm.screen() + "\n```\n")
	}
	return b.String()
}

func TestScreensDoc(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "docs", "desk-ui.md")
	got := renderScreensDoc(t)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != got {
		t.Fatalf("docs/desk-ui.md is out of date: run go test ./internal/deskui -run TestScreensDoc -update and read the diff")
	}
}
