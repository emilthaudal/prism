package tracks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/emilthaudal/prism/internal/store"
)

// deskPaneHome is a store with two tracks, alpha and beta, and nothing on the desk yet.
func deskPaneHome(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = setup(t)
	card(t, th, "alpha", "focus", "", "w", "")
	card(t, th, "beta", "background", "", "w", "")
	importNow(t)
	ranOnTheStore(t)
	return th, bin
}

func onDesk(t *testing.T, fn func(st *store.Store), opts ...store.Option) {
	t.Helper()
	if err := withStore(func(st *store.Store) error { fn(st); return nil }, opts...); err != nil {
		t.Fatal(err)
	}
}

// bandTime is the layout tracks.db stores times in.
const bandTime = "2006-01-02T15:04:05.000000000Z"

// onDeskDB runs q on tracks.db, as the pane's band wrote desk asks before the desk asked in chat.
func onDeskDB(t *testing.T, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// addAsk stores a desk ask as the band raised them.
func addAsk(t *testing.T, track, question, rec string, evidence ...string) {
	t.Helper()
	ev, _ := json.Marshal(append([]string{}, evidence...))
	onDeskDB(t, `INSERT INTO desk_asks (track, question, recommendation, evidence, created_at) VALUES (?, ?, ?, ?, ?)`,
		track, question, rec, string(ev), time.Now().UTC().Format(bandTime))
}

// answerAsk records Emil's band answer to desk ask id.
func answerAsk(t *testing.T, id int, kind, text string) {
	t.Helper()
	at := time.Now().UTC().Format(bandTime)
	onDeskDB(t, `UPDATE desk_asks SET answer_kind = ?, answer = ?, answered_at = ?, closed_at = ? WHERE id = ?`, kind, text, at, at, id)
}

func logEntry(t *testing.T, track, kind, asked, did string, opts ...store.Option) {
	t.Helper()
	onDesk(t, func(st *store.Store) {
		if _, err := st.LogDesk(track, kind, asked, did); err != nil {
			t.Fatal(err)
		}
	}, opts...)
}

func twoAsks(t *testing.T) {
	t.Helper()
	addAsk(t, "alpha", "merge #212?", "yes", "CI green", "approved by Ana")
	addAsk(t, "beta", "sandbox or separate user?", "separate user", "the sandbox shares a role")
}

var (
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func TestHeaderReadsDeskWithTracksForYouAndHandledToday(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "alpha", "focus", "", "w", "")
	card(t, th, "beta", "background", "", "w", "")
	card(t, th, "gamma", "focus", "", "w", "1. which branch?")
	importNow(t)
	ranOnTheStore(t)
	addAsk(t, "alpha", "merge #212?", "yes")
	logEntry(t, "alpha", "nudged", "idle 40m", "lint is red on #212")
	logEntry(t, "beta", "started", "", "started beta")
	out := plain(newPane(), 120, 24)
	head := strings.Split(out, "\n")[0]
	for _, want := range []string{" Desk", "3 tracks", "◆ 1 for you", "handled 2 today"} {
		if !strings.Contains(head, want) {
			t.Errorf("header lacks %q: %q", want, head)
		}
	}
	if strings.Contains(head, "Tracks") {
		t.Errorf("the old title is back: %q", head)
	}
}

// A desk ask stored before the desk talked in chat stays in the store and in tracks desk, but
// the pane shows no band for it, does not count it, and tab and y do not reach it.
func TestTheBandIsGoneAndStoredDeskAsksStayOutOfThePane(t *testing.T) {
	deskPaneHome(t)
	twoAsks(t)
	logEntry(t, "beta", "started", "", "started beta")
	m := newPane()
	out := plain(m, 120, 30)
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[1], nothingNeedsYou) || !strings.HasPrefix(lines[2], "─") || !strings.Contains(lines[3], "FOCUS") {
		t.Errorf("a row under the header other than the empty Needs you band and its rule before the list:\n%s", strings.Join(lines[:5], "\n"))
	}
	for _, gone := range []string{"merge #212?", "→ yes", "y accept", "◆", "handled: started beta"} {
		if strings.Contains(out, gone) {
			t.Errorf("the pane still shows %q:\n%s", gone, out)
		}
	}
	if _, needs := m.counts(); needs != 0 {
		t.Errorf("%d need you, want none: desk asks no longer count", needs)
	}
	tabKey(m, false)
	if m.flash != nothingNeedsYou {
		t.Errorf("tab with only desk asks stored: cursor %q, flash %q", m.cur, m.flash)
	}
	key(m, "y")
	key(m, "1")
	if doc := readDesk(t); len(doc.Asks) != 2 || len(doc.Answers) != 0 {
		t.Errorf("a key answered a stored desk ask: %+v", doc)
	}
	text, _ := do("desk")
	if !strings.Contains(text, "[1] alpha: merge #212?") || !strings.Contains(text, "recommend: yes") || !strings.Contains(text, "- CI green") {
		t.Errorf("tracks desk no longer shows the stored asks:\n%s", text)
	}
	if out, code := do("next"); code != 0 || out != nothingNeedsYou+"\n" {
		t.Errorf("tracks next: %d %q", code, out)
	}
}

func TestDeskNotesShowTheDesksLogEntriesForTheSelectedTrackOnly(t *testing.T) {
	deskPaneHome(t)
	logEntry(t, "alpha", "nudged", "idle 40m", "lint is red on #212, fixing")
	logEntry(t, "beta", "started", "", "started beta")
	logEntry(t, "Alpha", "answered", "", "version is 4.6.0")
	m := newPane()
	out := plain(m, 120, 40)
	for _, want := range []string{"Desk notes", "nudged: lint is red on #212, fixing (idle 40m)", "answered: version is 4.6.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("alpha lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "started beta") && strings.Contains(strings.SplitN(out, "│", 2)[1], "started beta") {
		t.Errorf("another track's entry in the notes:\n%s", out)
	}
	if strings.Index(out, "nudged: lint") > strings.Index(out, "answered: version") {
		t.Errorf("notes are not oldest first:\n%s", out)
	}
	key(m, "j")
	if out := plain(m, 120, 40); !strings.Contains(out, "started: started beta") || strings.Contains(out, "lint is red") {
		t.Errorf("beta's notes:\n%s", out)
	}
}

func TestDeskNotesKeepTheNewestFiveAndSayHowManyAreEarlier(t *testing.T) {
	deskPaneHome(t)
	for i := range 7 {
		logEntry(t, "alpha", "noted", "", fmt.Sprint("note ", i))
	}
	out := plain(newPane(), 120, 40)
	if strings.Contains(out, "note 0") || strings.Contains(out, "note 1") || !strings.Contains(out, "note 2") || !strings.Contains(out, "note 6") || !strings.Contains(out, "2 earlier · h opens the log") {
		t.Errorf("notes:\n%s", out)
	}
}

func TestNoDeskNotesSectionWhenTheDeskLoggedNothingForTheTrack(t *testing.T) {
	deskPaneHome(t)
	logEntry(t, "beta", "noted", "", "kept it")
	if out := plain(newPane(), 120, 40); strings.Contains(out, "Desk notes") {
		t.Errorf("an empty section:\n%s", out)
	}
}

func TestHOpensTheFullHandledLogNewestFirstIncludingEarlierDays(t *testing.T) {
	deskPaneHome(t)
	old := func() time.Time { return time.Now().AddDate(0, 0, -3) }
	logEntry(t, "beta", "started", "", "started beta", store.WithClock(old))
	logEntry(t, "alpha", "nudged", "idle", "lint is red")
	m := newPane()
	key(m, "h")
	out := plain(m, 120, 20)
	i, j := strings.Index(out, "nudged alpha: lint is red (idle)"), strings.Index(out, "started beta: started beta")
	if m.logv == nil || i < 0 || j < 0 || i > j {
		t.Errorf("log:\n%s", out)
	}
	if !strings.Contains(out, old().Format("Jan 2")) || !strings.Contains(out, "enter open the track") {
		t.Errorf("the earlier day or the footer is missing:\n%s", out)
	}
	key(m, "down")
	if _, cmd := m.Update(keyEnter); cmd == nil || m.cur != "beta" || m.logv == nil {
		t.Errorf("enter in the log: cmd %v cur %q open %v", cmd, m.cur, m.logv != nil)
	}
	key(m, "esc")
	if m.logv != nil {
		t.Error("esc leaves the log open")
	}
}

func TestHandledLogScrollsToEveryEntry(t *testing.T) {
	deskPaneHome(t)
	for i := range 40 {
		logEntry(t, "alpha", "noted", "", fmt.Sprintf("entry %02d", i))
	}
	m := newPane()
	key(m, "h")
	out := plain(m, 100, 12)
	if !strings.Contains(out, "entry 39") || strings.Contains(out, "entry 00") || !strings.Contains(out, "more") {
		t.Fatalf("the top of the log:\n%s", out)
	}
	for range 39 {
		key(m, "j")
	}
	out = plain(m, 100, 12)
	if !strings.Contains(out, "entry 00") || !strings.Contains(out, "▌") {
		t.Errorf("the oldest entry is out of reach:\n%s", out)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.logv.idx >= 39 {
		t.Errorf("pgup stayed at %d", m.logv.idx)
	}
}

func TestHandledLogWithNothingSaysSo(t *testing.T) {
	deskPaneHome(t)
	m := newPane()
	key(m, "h")
	if out := plain(m, 100, 12); !strings.Contains(out, "Nothing handled yet") {
		t.Errorf("empty log:\n%s", out)
	}
	key(m, "h")
	if m.logv != nil {
		t.Error("h does not close the log")
	}
}

func TestHelpListsTheDeskKeys(t *testing.T) {
	deskPaneHome(t)
	m := newPane()
	key(m, "?")
	out := plain(m, 100, 24)
	for _, want := range []string{"1-9  r", "h  d  ?  esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestDeskJSONAnswersListWhatEmilAnsweredAndTheTextViewSaysSo(t *testing.T) {
	deskPaneHome(t)
	twoAsks(t)
	answerAsk(t, 1, "accepted", "yes")
	doc := readDesk(t)
	if len(doc.Asks) != 1 || doc.Asks[0].Track != "beta" {
		t.Errorf("open asks %+v", doc.Asks)
	}
	if len(doc.Answers) != 1 || doc.Answers[0].Recommendation != "yes" || strings.Join(doc.Answers[0].Evidence, "|") != "CI green|approved by Ana" {
		t.Errorf("answers %+v", doc.Answers)
	}
	out, _ := do("desk")
	if !strings.Contains(out, "answered by Emil") || !strings.Contains(out, "[1] alpha: merge #212?: accepted yes") {
		t.Errorf("desk:\n%s", out)
	}
	if out, _ := do("desk", "--json"); !strings.Contains(out, `"answers": [`) {
		t.Errorf("no answers key:\n%s", out)
	}
}

func TestDeskJSONAnswersIsAnEmptyListNotNull(t *testing.T) {
	deskHome(t)
	if out, _ := do("desk", "--json"); !strings.Contains(out, `"answers": []`) {
		t.Errorf("desk --json:\n%s", out)
	}
}
