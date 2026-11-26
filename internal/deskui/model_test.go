package deskui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// pump delivers the fixture's changes the window has not seen, as the event stream would.
func (tm *testModel) pump() {
	tm.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var evs []Event
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_ = tm.f.Events(ctx, tm.snap.Seq, func(ev Event) bool { evs = append(evs, ev); return true })
	for _, ev := range evs {
		tm.send(eventMsg{ev})
	}
}

func titles(screen string) []string {
	top := strings.SplitN(screen, "\n", 2)[0]
	var out []string
	for _, part := range strings.Split(top, "╭─ ")[1:] {
		out = append(out, strings.Fields(part)[0])
	}
	return out
}

func TestLayoutAtThreeWidths(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want []string
		// after tab
		next []string
	}{
		{200, 50, []string{"Sessions", "Prism", "Board"}, []string{"Sessions", "Prism", "Board"}},
		{120, 40, []string{"Prism", "Board"}, []string{"Prism", "Board"}},
		{80, 30, []string{"Prism"}, []string{"Board"}},
	} {
		tm := newTest(t, tc.w, tc.h)
		s := tm.screen()
		if got := titles(s); !slices.Equal(got, tc.want) {
			t.Errorf("%d cols: columns %v, want %v\n%s", tc.w, got, tc.want, s)
		}
		for i, l := range strings.Split(s, "\n") {
			if w := width(l); w > tc.w {
				t.Errorf("%d cols: line %d is %d wide", tc.w, i, w)
			}
		}
		if n := len(strings.Split(s, "\n")); n != tc.h {
			t.Errorf("%d cols: %d lines, want %d", tc.w, n, tc.h)
		}
		tm.press("tab")
		if got := titles(tm.screen()); !slices.Equal(got, tc.next) {
			t.Errorf("%d cols after tab: columns %v, want %v", tc.w, got, tc.next)
		}
		tm.press("tab")
		want := []string{"Sessions", "Prism", "Board"}
		switch tc.w {
		case 120:
			want = []string{"Sessions", "Prism"}
		case 80:
			want = []string{"Sessions"}
		}
		if got := titles(tm.screen()); !slices.Equal(got, want) {
			t.Errorf("%d cols with the sessions focused: columns %v, want %v", tc.w, got, want)
		}
	}
}

func TestSessionsColumn(t *testing.T) {
	tm := newTest(t, 200, 50)
	s := tm.screen()
	for _, want := range []string{
		"● Prism", "FOCUS", "recipe-search  ? Question  2m", "bun-rollout          lead · 3",
		"├ pantry-api           ◐ now", "└ app-template-node ○ queued", "▲ Perm  6m",
		"weekly-brief      emil ● done", "invoice-export      Alex · 3h", "PARKED 2 ▸",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sessions column lacks %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "deploy-bot") && !strings.Contains(s, "LANDED") {
		t.Error("a landed session shows in the sessions column")
	}
}

func TestBoardShowsNeedsYouInFlightReadyLanded(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab") // board
	s := tm.screen()
	for _, want := range []string{
		"NEEDS YOU", "◆ D14 recipe-search", "★1 Merge now  2 Wait for Alex", "3 Hold until tomorrow",
		"✓ CI green", "CircleCI #48211", "▲ D15 scanner-skips-shop-api",
		"IN FLIGHT", "◐ pantry-api", "+88 −61 · 9 files", "bun test --shard 2/3",
		"READY TO LAND", "● queue-api", "LANDED", "3 ▸",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("board lacks %q\n%s", want, s)
		}
	}
	for range 20 {
		tm.press("down") // to LANDED, the last row
	}
	tm.press("enter")
	if s := tm.screen(); !strings.Contains(s, "✓ deploy-bot") {
		t.Errorf("enter on LANDED does not unfold it\n%s", s)
	}
}

func TestQuestionComposer(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab") // the board, cursor on D14
	s := tm.screen()
	for _, want := range []string{"D14 recipe-search · Merge PR #418 in pantry-api now?", "^1 Merge now, squash", "recommended", "^2 Wait for Alex", "Or answer in your own words…", "1 of 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("question composer lacks %q\n%s", want, s)
		}
	}
	tm.press("esc")
	tm.press("shift+tab") // the conversation
	tm.press("esc")
	if s := tm.screen(); !strings.Contains(s, "Message Prism") {
		t.Errorf("esc in the composer does not go back to the desk\n%s", s)
	}
}

func TestTheDeskLineSitsUnderTheQuestionAndAlwaysMessagesTheDesk(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab") // the board, cursor on D14
	if s := tm.screen(); !strings.Contains(s, "Message Prism") || !strings.Contains(s, "D14 recipe-search") {
		t.Fatalf("the question has no desk line under it\n%s", s)
	}
	tm.press("shift+tab") // the conversation
	tm.typeText("how long will this take")
	tm.press("enter")
	if got := tm.f.Posts(); len(got) == 0 || got[len(got)-1] != "/v1/desk/messages" {
		t.Fatalf("typed text with a question selected did not go to the desk: %v", got)
	}
	if q, _ := tm.f.snap.question("D14"); !q.Open() {
		t.Fatal("a message to the desk answered the question")
	}
	tm.press("enter") // an empty box: the question
	if s := tm.screen(); !strings.Contains(s, "sending in 5 s") {
		t.Fatalf("enter on an empty box did not take the recommendation\n%s", s)
	}
}

func TestCountdownAndUndo(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab", "enter") // the board: take D14's recommendation
	if s := tm.screen(); !strings.Contains(s, "Merge now, squash · sending in 5 s · z undo") || !strings.Contains(s, "sending in 5 s · z") {
		t.Fatalf("no countdown after enter\n%s", s)
	}
	tm.advance(2 * time.Second)
	if s := tm.screen(); !strings.Contains(s, "sending in 3 s") {
		t.Fatalf("the countdown does not count\n%s", s)
	}
	tm.press("z")
	tm.advance(10 * time.Second)
	if p := tm.f.Posts(); len(p) != 0 {
		t.Fatalf("z did not stop the answer: %v", p)
	}
	if s := tm.screen(); strings.Contains(s, "sending in") || !strings.Contains(s, "◆ D14") {
		t.Fatalf("after z D14 should be open again\n%s", s)
	}

	tm.press("2") // pick option 2 on the board
	if s := tm.screen(); !strings.Contains(s, "Wait for Alex · sending in 5 s") {
		t.Fatalf("2 does not pick option 2\n%s", s)
	}
	tm.advance(4 * time.Second)
	if p := tm.f.Posts(); len(p) != 0 {
		t.Fatalf("sent before the countdown ended: %v", p)
	}
	tm.advance(time.Second)
	if p := tm.f.Posts(); !slices.Equal(p, []string{"/v1/questions/D14/answer"}) {
		t.Fatalf("posts %v, want the D14 answer", p)
	}
	tm.pump()
	s := tm.screen()
	if strings.Contains(s, "◆ D14") || !strings.Contains(s, "You answered D14: Wait for Alex") {
		t.Fatalf("D14 should leave the board and the chat say the answer\n%s", s)
	}
	q, _ := tm.f.snap.question("D14")
	if string(q.Answer) == "" || !strings.Contains(string(q.Answer), `"option":1`) {
		t.Fatalf("fixture answer %s", q.Answer)
	}
}

func TestAnAnswerToABusyAgentShowsAsQueuedNotAsAnError(t *testing.T) {
	tm := newTest(t, 200, 50)
	for i := range tm.f.snap.Questions {
		if tm.f.snap.Questions[i].ID == "D14" {
			tm.f.snap.Questions[i].From = "agent"
			sid := tm.f.snap.Questions[i].Session
			for j := range tm.f.snap.Sessions {
				if tm.f.snap.Sessions[j].ID == sid {
					tm.f.snap.Sessions[j].Status = "working"
				}
			}
		}
	}
	snap, _ := tm.f.Snapshot(context.Background())
	tm.send(snapshotMsg{snap})
	tm.press("tab", "2")
	tm.advance(5 * time.Second)
	tm.pump()
	q, _ := tm.f.snap.question("D14")
	if !q.Queued() {
		t.Fatalf("fixture did not queue: %+v", q)
	}
	tm.send(snapshotMsg{tm.f.snap})
	s := tm.screen()
	for _, want := range []string{"Queued for recipe-search: sent when its agent stops", "ANSWERED, WAITING TO SEND", "→ Wait for Alex · sent when its agent stops"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "Not sent") || strings.Contains(s, "◆ D14") {
		t.Errorf("a queued answer shows as an error or still needs Emil\n%s", s)
	}
}

func TestAnswerInWordsUndoRestoresText(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab", "down", "down", "down", "up", "up", "up", "r") // board, back on D14, answer in words
	tm.typeText("ask Alex first")
	tm.press("enter")
	if s := tm.screen(); !strings.Contains(s, "“ask Alex first” · sending in 5 s") {
		t.Fatalf("no countdown for an answer in words\n%s", s)
	}
	tm.press("ctrl+z")
	if got := string(tm.text); got != "ask Alex first" {
		t.Fatalf("undo left %q in the composer", got)
	}
	tm.press("enter")
	tm.advance(undoWindow)
	tm.pump()
	q, _ := tm.f.snap.question("D14")
	if !strings.Contains(string(q.Answer), `"text":"ask Alex first"`) {
		t.Fatalf("fixture answer %s", q.Answer)
	}
}

// composerRow is the screen row the composer's text is on.
func composerRow(s, text string) int {
	for i, l := range strings.Split(s, "\n") {
		if strings.Contains(l, text) && strings.Contains(l, "│ │") {
			return i
		}
	}
	return -1
}

func TestComposerKeepsTextWhileEntriesArrive(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.typeText("hello desk")
	before := tm.screen()
	row := composerRow(before, "hello desk▌")
	if row < 0 {
		t.Fatalf("typed text not in the composer\n%s", before)
	}
	for i, text := range []string{"Opened PR 419 for queue-api.", "pantry-api shard 2 passed.", "Started app-template-node."} {
		if err := tm.f.Emit("conversation", "upsert", Entry{ID: "x" + string(rune('a'+i)), At: fixtureNow.Add(time.Minute), Role: "desk", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tm.f.Emit("question", "upsert", Question{ID: "D16", Track: "121", Question: "Open the PR?", Options: []Option{{Label: "Yes"}, {Label: "No"}}, CreatedAt: fixtureNow}); err != nil {
		t.Fatal(err)
	}
	tm.pump()
	after := tm.screen()
	if got := composerRow(after, "hello desk▌"); got != row {
		t.Fatalf("the composer moved from row %d to %d\n%s", row, got, after)
	}
	if string(tm.text) != "hello desk" {
		t.Fatalf("composer text is %q", string(tm.text))
	}
	if !strings.Contains(after, "▾ 3 new") || strings.Contains(after, "Opened PR 419") {
		t.Fatalf("new entries should wait under a 3 new marker\n%s", after)
	}
	if !strings.Contains(after, "◆ D16") {
		t.Fatalf("the board did not take the new question\n%s", after)
	}
	tm.typeText(" more")
	tm.press("enter")
	tm.pump()
	s := tm.screen()
	if strings.Contains(s, "3 new") || !strings.Contains(s, "Started app-template-node.") || !strings.Contains(s, "hello desk more") {
		t.Fatalf("after send the new entries and the message should show\n%s", s)
	}
	if len(tm.text) != 0 {
		t.Fatalf("composer not cleared after send: %q", string(tm.text))
	}
}

func TestScrollDownShowsNew(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.typeText("draft")
	_ = tm.f.Emit("conversation", "upsert", Entry{ID: "y1", At: fixtureNow, Role: "desk", Text: "Something new."})
	tm.pump()
	if !strings.Contains(tm.screen(), "▾ 1 new") {
		t.Fatal("no new marker")
	}
	tm.press("down")
	s := tm.screen()
	if strings.Contains(s, "1 new") || !strings.Contains(s, "Something new.") || string(tm.text) != "draft" {
		t.Fatalf("scrolling down should show the new entry and keep the draft\n%s", s)
	}
}

func TestBackgroundSessionReadOnly(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.typeText("my draft")
	tm.press("tab", "tab")           // sessions
	tm.press("down", "down", "down") // recipe-search, bun-rollout, pantry-api
	s := tm.screen()
	for _, want := range []string{"pantry-api · transcript", "Shard 1/3 passed. Shard 2/3 is running.", "▸ 12 steps", "Read-only. t takes it over", "Your draft to Prism is kept."} {
		if !strings.Contains(s, want) {
			t.Errorf("transcript view lacks %q\n%s", want, s)
		}
	}
	tm.press("t")
	if p := tm.f.Posts(); !slices.Contains(p, "/v1/sessions/3f91c2aa-6b7c-4d8e-9f0a-1b2c3d4e5f61/takeover") {
		t.Fatalf("t did not take over: %v", p)
	}
	tm.pump()
	if s := tm.screen(); !strings.Contains(s, "run claude attach 3f91c2aa") || !strings.Contains(s, "emil ◐ now") {
		t.Fatalf("takeover not shown\n%s", s)
	}
	tm.press("g")
	tm.pump()
	tm.press("esc")
	tm.press("tab") // conversation
	s = tm.screen()
	if !strings.Contains(s, "You took over pantry-api. It no longer reports here.") || !strings.Contains(s, "You gave pantry-api back") || !strings.Contains(s, "my draft") {
		t.Fatalf("notes or draft missing\n%s", s)
	}
}

func TestOpenLinkAndHelp(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab", "o")
	if !slices.Equal(tm.opened, []string{"https://github.com/acme/pantry-api/pull/418"}) {
		t.Fatalf("o opened %v", tm.opened)
	}
	tm.press("?")
	if s := tm.screen(); !strings.Contains(s, "take back an answer within 5 s") {
		t.Fatalf("? shows no keys\n%s", s)
	}
	tm.press("x")
	if tm.help {
		t.Fatal("any key should close the help")
	}
}

func TestAttachmentsListAsLinksAndOpen(t *testing.T) {
	tm := newTest(t, 200, 60)
	for i, q := range tm.snap.Questions {
		if q.ID == "D14" {
			q.Links = nil
			q.Options[0].Attachment = &Attachment{Label: "owl.png", URL: "file:///tmp/owl.png", Kind: "image"}
			q.Attachments = []Attachment{{Label: "sheet.html", URL: "https://example.com/sheet.html", Kind: "page"}}
			tm.snap.Questions[i] = q
		}
	}
	tm.press("tab")
	s := tm.screen()
	for _, want := range []string{"▣ owl.png · o opens", "◎ sheet.html · o opens", "o https://example.com/sheet.html", "o file:///tmp/owl.png"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q\n%s", want, s)
		}
	}
	tm.press("o")
	if !slices.Equal(tm.opened, []string{"https://example.com/sheet.html"}) {
		t.Fatalf("o opened %v", tm.opened)
	}
}

func TestSeenOnOpen(t *testing.T) {
	tm := newTest(t, 200, 50)
	tm.press("tab", "tab", "down", "down", "down", "down") // queue-api, done and unseen
	if p := tm.f.Posts(); !slices.Contains(p, "/v1/sessions/b25e44d1-7c8d-4e9f-8a1b-2c3d4e5f6a72/seen") {
		t.Fatalf("opening a done session does not mark it seen: %v", p)
	}
}
