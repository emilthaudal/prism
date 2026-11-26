package tracks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// importNow builds tracks.db from the test's files whatever the run, so a test of the store
// path runs in both runs.
func importNow(t *testing.T) {
	t.Helper()
	noImport(t)
	st, err := store.Open(dbPath(), store.WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := readSource()
	if _, err := st.Import(src.importData()); err != nil {
		t.Fatal(err)
	}
}

// holdWriteLock takes the database's write lock on a connection of its own for d, the way
// another tracks process mid-write holds it. It returns once the lock is held.
func holdWriteLock(t *testing.T, d time.Duration) <-chan struct{} {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath())
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(d)
		c.ExecContext(context.Background(), `COMMIT`)
		c.Close()
		db.Close()
		close(done)
	}()
	return done
}

func TestHookLogsUnderASecondWhileAnotherWriterHoldsTheDatabase(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "1. q?")
	importNow(t)
	done := holdWriteLock(t, 400*time.Millisecond)
	start := time.Now()
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 yes","cwd":"/c"}`)
	took := time.Since(start)
	<-done
	if took >= time.Second {
		t.Errorf("the hook took %v with a writer holding the database for 400ms", took)
	}
	if took < 300*time.Millisecond {
		t.Errorf("the hook took %v: it did not wait for the writer, so the test proves nothing", took)
	}
	evs := readEvents()
	if len(evs) == 0 || evs[len(evs)-1].Event != "prompt" || evs[len(evs)-1].Track != "tr" {
		t.Fatalf("prompt not logged: %+v", evs)
	}
	if loadCardMust(t, "tr").Meta["answered"] == "" {
		t.Error("the prompt over an open ask did not stamp answered")
	}
	t.Logf("hook with a 400ms writer ahead of it: %v", took)
}

// A writer holding the database past the hook's first lock wait still lets the whole hook land
// within its budget: the event and the answered stamp both.
func TestHookLandsWholeBehindAWriterPastItsLockWait(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "1. q?")
	importNow(t)
	done := holdWriteLock(t, 700*time.Millisecond)
	start := time.Now()
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 yes","cwd":"/c"}`)
	took := time.Since(start)
	<-done
	if took < 600*time.Millisecond {
		t.Errorf("the hook took %v: it did not wait for the writer, so the test proves nothing", took)
	}
	evs := readEvents()
	if len(evs) == 0 || evs[len(evs)-1].Event != "prompt" || evs[len(evs)-1].Track != "tr" {
		t.Fatalf("the prompt event was lost: %+v", evs)
	}
	if loadCardMust(t, "tr").Meta["answered"] == "" {
		t.Error("the prompt over an open ask did not stamp answered")
	}
	if r, failing := hookFailure(); failing {
		t.Errorf("hook.err: %+v", r)
	}
}

// A writer holding the database for nearly all of the hook's budget costs the hook its card
// change, never its event: the event is logged on its own with what is left of the budget.
func TestHookKeepsItsEventBehindALongWriter(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "tr", "focus", "s5", "w", "1. q?")
	importNow(t)
	hold := hookBudget - hookLogReserve/2
	done := holdWriteLock(t, hold)
	start := time.Now()
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"1 yes","cwd":"/c"}`)
	took := time.Since(start)
	<-done
	if took < hold-100*time.Millisecond {
		t.Errorf("the hook took %v: it did not wait for the writer, so the test proves nothing", took)
	}
	if took >= hookBudget+200*time.Millisecond {
		t.Errorf("the hook took %v, past its budget", took)
	}
	evs := readEvents()
	if len(evs) == 0 || evs[len(evs)-1].Event != "prompt" || evs[len(evs)-1].Track != "tr" {
		t.Fatalf("the prompt event was lost: %+v", evs)
	}
	if r, failing := hookFailure(); !failing || !strings.Contains(r.Error, "card not updated") {
		t.Errorf("hook.err does not say the card change was dropped: %+v", r)
	}
}

// tracks export can move tracks.db between the hook seeing it and opening it: the hook then
// writes the files, as for a write that found the database moved.
func TestHookFindingNoDatabaseFallsBackToFiles(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "e", "focus", "s5", "w", "")
	importNow(t)
	if err := os.Rename(dbPath(), dbPath()+".gone"); err != nil {
		t.Fatal(err)
	}
	moved, err := hookStore(Event{Event: "stop", RexSession: "s5"}, "", "", "/c")
	if !moved || err != nil {
		t.Errorf("moved %v, err %v", moved, err)
	}
}

// Reading never waits for a writer: ls answers while another process holds the write lock.
func TestLsDoesNotWaitForAWriter(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	importNow(t)
	done := holdWriteLock(t, 3*time.Second)
	start := time.Now()
	out, code := do("ls", "--json")
	took := time.Since(start)
	<-done
	if code != 0 || !strings.Contains(out, `"name": "a"`) {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	if took > time.Second {
		t.Errorf("ls took %v behind a writer", took)
	}
}

// Meridian watches cards/ and reads tracks ls --json: a card write lands in the file at once.
func TestCardWriteRendersTheView(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "m", "focus", "", "w", "")
	importNow(t)
	before, _ := os.Stat(cardPath("m"))
	start := time.Now()
	if out, code := cardIn("where: Rendered now.\nasks:\nShip it?\n", "card", "m"); code != 0 {
		t.Fatalf("card: exit %d: %s", code, out)
	}
	after, err := os.Stat(cardPath("m"))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("render took %v", took)
	}
	if os.SameFile(before, after) {
		t.Error("the view was not replaced, so a watch on cards/ sees no new file")
	}
	text := readCard("m")
	if !strings.Contains(text, "Rendered now.") || !strings.Contains(text, "1. Ship it?") || !strings.Contains(text, store.GeneratedLine) {
		t.Errorf("view:\n%s", text)
	}
}

// lsDoc parses ls --json and blanks what may differ between the modes.
func lsNormal(t *testing.T, out string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	delete(doc, "generated_at")
	for _, tr := range doc["tracks"].([]any) {
		for _, a := range tr.(map[string]any)["asks"].([]any) {
			delete(a.(map[string]any), "since")
		}
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b)
}

// ls --json is Meridian's contract: the database prints what the files print, but each ask's
// since (its real creation time now).
func TestLsJSONIsTheSameFromTheDatabase(t *testing.T) {
	th, _ := setup(t)
	noImport(t)
	writeFile(t, cardPath("full"), "---\nname: full\nmode: waiting      # c\ntier: 2             # c\ndir: "+th+"\nrex_session: session:x-sa\n"+
		"waiting_on: review\ncheck: pr 7 merged\nlinks: EMI-1, https://github.com/o/r/pull/7\nupdated: 2026-11-20T10:00:00+01:00\n"+
		"answered: 2026-11-20T10:05:00+01:00\n---\n## Goal\nShip it.\n\n## Where it is\nIn review.\nDetail.\nTier 2: touches the pane.\n\n"+
		"## Needs me\n1. Merge?\n2. Check \"pr 7 merged\" is now yes. Recommend: set mode to focus.\n\n## Standing orders\nBe brief.\n\n## Later\n- one\n")
	card(t, th, "bare", "focus", "", "w", "")
	card(t, th, "parked", "parked", "s", "p", "1. later?")
	files, code := do("ls", "--json")
	if code != 0 {
		t.Fatal(files)
	}
	// The file times ls reports become each track's created_at on import.
	importNow(t)
	db, code := do("ls", "--json")
	if code != 0 {
		t.Fatal(db)
	}
	if lsNormal(t, files) != lsNormal(t, db) {
		t.Errorf("ls --json differs\nfiles:\n%s\ndatabase:\n%s", lsNormal(t, files), lsNormal(t, db))
	}
}

// ls --json's updated is the last content change, as a card file's mtime was: a mode key does
// not move it (Meridian would mark the track unread), a card write does, and export hands the
// same value back to the files.
func TestModeKeyLeavesUpdatedAlone(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "u", "focus", "", "w", "")
	card(t, th, "v", "focus", "", "w", "")
	hourAgo := time.Now().Add(-time.Hour)
	for _, n := range []string{"u", "v"} {
		if err := os.Chtimes(cardPath(n), hourAgo, hourAgo); err != nil {
			t.Fatal(err)
		}
	}
	importNow(t)
	updated := func() map[string]string {
		t.Helper()
		out, code := do("ls", "--json")
		if code != 0 {
			t.Fatal(out)
		}
		var doc struct{ Tracks []lsTrack }
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, tr := range doc.Tracks {
			m[tr.Name] = *tr.Updated
		}
		return m
	}
	before := updated()
	if want := timeStr(hourAgo.UTC()); *want != before["u"] {
		t.Fatalf("updated after import %s, the file said %s", before["u"], *want)
	}
	if err := setMode("u", "parked"); err != nil {
		t.Fatal(err)
	}
	if out, code := cardIn("where: Moved on.\n", "card", "v"); code != 0 {
		t.Fatal(out)
	}
	db := updated()
	if db["u"] != before["u"] {
		t.Errorf("a mode key moved updated from %s to %s", before["u"], db["u"])
	}
	if db["v"] == before["v"] {
		t.Error("a card write did not move updated")
	}
	if out, code := do("export"); code != 0 {
		t.Fatal(out)
	}
	if files := updated(); files["u"] != db["u"] || files["v"] != db["v"] {
		t.Errorf("updated after export %v, from the database %v", files, db)
	}
}

// A hand edit to a view is kept aside, never overwritten, and the view is rendered again.
func TestHandEditedViewIsKeptAside(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "h", "focus", "", "w", "")
	importNow(t)
	if out, code := cardIn("where: First.\n", "card", "h"); code != 0 {
		t.Fatal(out)
	}
	edited := strings.Replace(readCard("h"), "First.", "Typed by hand.", 1)
	if err := os.WriteFile(cardPath("h"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := cardIn("where: Second.\n", "card", "h"); code != 0 {
		t.Fatal(out)
	}
	kept := editedViews()
	if len(kept) != 1 || !strings.HasPrefix(filepath.Base(kept[0]), ".edited-h-") {
		t.Fatalf("kept: %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != edited {
		t.Errorf("the kept file is not the hand edit:\n%s", b)
	}
	if v := readCard("h"); !strings.Contains(v, "Second.") || strings.Contains(v, "Typed by hand.") {
		t.Errorf("view after the edit:\n%s", v)
	}
	if loadCardMust(t, "h").firstLine("Where it is") != "Second." {
		t.Error("the hand edit reached the database")
	}
	// The next write has nothing to keep: the view is the one it rendered.
	if out, code := cardIn("where: Third.\n", "card", "h"); code != 0 {
		t.Fatal(out)
	}
	if len(editedViews()) != 1 {
		t.Errorf("a rendered view was kept as a hand edit: %v", editedViews())
	}
	m := newPane()
	if !strings.Contains(m.footer(), ".edited-h-") {
		t.Errorf("the pane does not say a view was edited: %q", m.footer())
	}
}

// A card edited by hand after the import, before any write rendered it, is kept aside by the
// next render, and neither tracks render nor a mode key loses it.
func TestHandEditAfterImportIsKeptAside(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "h", "focus", "", "w", "")
	importNow(t)
	edited := readCard("h") + "\n## Standing orders\nNever push on a Friday.\n"
	if err := os.WriteFile(cardPath("h"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check"}, {"render"}} {
		if out, code := do(args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
	}
	if err := setMode("h", "parked"); err != nil {
		t.Fatal(err)
	}
	kept := editedViews()
	if len(kept) != 1 || !strings.HasPrefix(filepath.Base(kept[0]), ".edited-h-") {
		t.Fatalf("kept: %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != edited {
		t.Errorf("the kept file is not the hand edit:\n%s", b)
	}
	if v := readCard("h"); strings.Contains(v, "Never push") || !strings.Contains(v, "mode: parked") {
		t.Errorf("view after the mode key:\n%s", v)
	}
}

// A view that cannot be written leaves the write committed and a note the pane shows, until
// tracks render succeeds.
func TestARenderThatFailsLeavesATrace(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "f", "focus", "", "w", "")
	importNow(t)
	cards := filepath.Dir(cardPath("f"))
	if err := os.Chmod(cards, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cards, 0o755) })
	if out, code := cardIn("where: Committed.\n", "card", "f"); code != 0 {
		t.Fatalf("card: exit %d: %s", code, out)
	}
	if loadCardMust(t, "f").firstLine("Where it is") != "Committed." {
		t.Fatal("the write did not commit")
	}
	if strings.Contains(readCard("f"), "Committed.") {
		t.Fatal("the view was written, so the test proves nothing")
	}
	if m := newPane(); !strings.Contains(m.footer(), "was not rendered") {
		t.Errorf("the pane does not say a view failed: %q", m.footer())
	}
	if err := os.Chmod(cards, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := do("render"); code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(readCard("f"), "Committed.") {
		t.Errorf("render did not repair the view:\n%s", readCard("f"))
	}
	if m := newPane(); strings.Contains(m.footer(), "was not rendered") {
		t.Errorf("the note outlived a render that worked: %q", m.footer())
	}
}

// The pane reloads on another connection's commit, through data_version.
func TestPaneStampFollowsCommits(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "p", "focus", "", "w", "")
	importNow(t)
	s1 := paneStamp()
	if s2 := paneStamp(); s2 != s1 {
		t.Fatalf("stamp moved with no write: %q, %q", s1, s2)
	}
	if err := setMode("p", "parked"); err != nil {
		t.Fatal(err)
	}
	if s3 := paneStamp(); s3 == s1 {
		t.Error("a commit did not change the pane's stamp")
	}
}

// Export holds the write lock from its reads through moving tracks.db away: a writer that was
// waiting finds the database gone and writes the files instead, so its write is not lost.
func TestExportLosesNoWriteThatWaitedForIt(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "e", "focus", "s5", "w", "")
	card(t, th, "gone", "focus", "", "w", "")
	importNow(t)
	if _, err := renameTrack("gone", "kept", time.Now()); err != nil {
		t.Fatal(err)
	}
	// A view left behind under the name the database renamed away: export takes it away.
	if err := os.WriteFile(cardPath("gone"), []byte(readCard("kept")), 0o644); err != nil {
		t.Fatal(err)
	}
	waiting, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer waiting.Close()
	const held = 300 * time.Millisecond
	type result struct {
		err  error
		took time.Duration
	}
	hooked := make(chan result, 1)
	exportHolding = func() {
		// The write starts while export holds the lock, and export goes on only once it waits.
		go func() {
			start := time.Now()
			err := waiting.Hook(store.HookEvent{Event: "prompt", RexSession: "s5"})
			hooked <- result{err, time.Since(start)}
		}()
		time.Sleep(held)
	}
	t.Cleanup(func() { exportHolding = func() {} })
	if out, code := do("export"); code != 0 {
		t.Fatal(out)
	}
	r := <-hooked
	if !errors.Is(r.err, store.ErrMoved) {
		t.Fatalf("a write that waited for export: err %v, want ErrMoved", r.err)
	}
	if r.took < held/2 {
		t.Fatalf("the write took %v: it did not wait for export, so the test proves nothing", r.took)
	}
	hook(t, `{"hook_event_name":"UserPromptSubmit","prompt":"go","cwd":"/c"}`)
	evs := readEvents()
	if len(evs) == 0 || evs[len(evs)-1].Event != "prompt" || evs[len(evs)-1].Track != "e" {
		t.Errorf("the hook after export did not reach attention.jsonl: %+v", evs)
	}
	gone(t, "gone")
	loadCardMust(t, "kept")
}

// The pane applies a check ask by its id: the agent's own ask stays, and applying the same ask
// again finds it gone instead of hitting whatever took its number.
func TestCheckAskAppliesFromTheDatabase(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	waitingCard(t, th, "w", "pr 5 approved")
	importNow(t)
	if err := fire("w", "pr 5 approved", true); err != nil {
		t.Fatal(err)
	}
	if out, code := cardIn("asks:\nKeep me?\n", "card", "w"); code != 0 || !strings.HasPrefix(out, "1. Keep me?\n2. Check ") {
		t.Fatalf("card: exit %d: %q", code, out)
	}
	checkAsk := loadCardMust(t, "w").askList()[1]
	if checkAsk.id == 0 {
		t.Fatal("a card read from tracks.db carries no ask ids")
	}
	m := newPane()
	m.idx, m.cur = slicesIndex(m, "w"), "w"
	press(m, "2")
	if m.reply != nil || m.status != "Applied: ask 2 cleared; agent not told: w has no tier" {
		t.Fatalf("status %q reply %+v", m.status, m.reply)
	}
	c := loadCardMust(t, "w")
	if c.mode() != "waiting" || c.asks() != 1 || c.askList()[0].text != "Keep me?" {
		t.Errorf("card after apply:\n%s", readCard("w"))
	}
	if status, ok := applyCheckAsk("w", checkAsk); ok || status != "could not apply ask 2: it changed on the card" {
		t.Errorf("second apply: %q %v", status, ok)
	}
	var applied bool
	for _, e := range readEvents() {
		applied = applied || e.Event == "answer" && e.Kind == "applied" && e.Track == "w"
	}
	if !applied {
		t.Error("no answer event for the applied ask")
	}
}

// An answer accepts the asks the pane showed when Emil pressed their digits: an agent that
// rewrites its asks before Enter does not move answered_at to whatever took those numbers.
func TestAnswerStampsTheAsksThePaneShowed(t *testing.T) {
	th, bin := setup(t)
	sendStub(t, bin)
	card(t, th, "a", "focus", "ab12cd", "w", "1. X?\n2. Y?")
	agentIs(t, bin, "done", "")
	importNow(t)
	m := newPane()
	settle(t, m)
	press(m, "2")
	if m.reply == nil || !m.reply.prefilled() {
		t.Fatalf("no prefilled reply: %q", m.status)
	}
	if out, code := cardIn("asks:\nZ?\nX?\nY?\n", "card", "a"); code != 0 {
		t.Fatal(out)
	}
	cmd := press(m, "enter")
	if cmd == nil {
		t.Fatalf("no send: %q", m.status)
	}
	if msg := cmd().(answerMsg); !msg.sent || msg.status != "Sent to a" {
		t.Fatalf("not sent: %q", msg.status)
	}
	var v store.View
	if err := withStore(func(st *store.Store) (err error) { v, err = st.Get("a"); return err }); err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Asks {
		if answered := !a.AnsweredAt.IsZero(); answered != (a.Text == "Y?") {
			t.Errorf("ask %d %q: answered %v", a.N, a.Text, answered)
		}
	}
}

// An import keeps the asks of a closed card open, as the archive file shows them; nothing that
// lists what needs Emil may count them.
func TestAsksOfAClosedTrackNeverNeedEmil(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "live", "focus", "", "w", "")
	writeFile(t, filepath.Join(archiveDir(), "2026-11-01-old.md"), "---\nname: old\nmode: focus\nclosed: 2026-11-01T10:00:00Z\n---\n## Goal\nG\n\n## Outcome\nDone.\n\n## Needs me\n1. Still open?\n")
	importNow(t)
	var openOnClosed int
	_ = withStore(func(st *store.Store) error {
		vs, err := st.AllTracks()
		for _, v := range vs {
			if v.Closed {
				openOnClosed += len(v.Asks)
			}
		}
		return err
	})
	if openOnClosed != 1 {
		t.Fatalf("the fixture should leave one open ask on the closed track, got %d", openOnClosed)
	}
	if out, _ := do("ls", "--json"); strings.Contains(out, "Still open") || strings.Contains(out, `"old"`) {
		t.Errorf("ls lists the closed track's ask:\n%s", out)
	}
	if out, code := do("next"); code != 0 || out != nothingNeedsYou+"\n" {
		t.Errorf("next: %d %q", code, out)
	}
	if len(loadCards()) != 1 {
		t.Errorf("cards: %d", len(loadCards()))
	}
}

// answered_asks imports to the asks' answered_at, not as an extra key rendered forever.
func TestAnsweredAsksImportToTheAsks(t *testing.T) {
	setup(t)
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second).Format(time.RFC3339)
	writeFile(t, cardPath("a"), "---\nname: a\nmode: focus\nanswered: "+at+"\nanswered_asks: "+at+" 2\nupdated: 2026-11-19T10:00:00Z\n---\n## Goal\nG\n\n## Needs me\n1. One?\n2. Two?\n")
	importNow(t)
	v, err := func() (v store.View, err error) {
		err = withStore(func(st *store.Store) error { v, err = st.Get("a"); return err })
		return v, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !v.Asks[0].AnsweredAt.IsZero() || v.Asks[1].AnsweredAt.Format(time.RFC3339) != at {
		t.Errorf("answered_at: %v, %v", v.Asks[0].AnsweredAt, v.Asks[1].AnsweredAt)
	}
	if _, kept := v.Track.Extra.Meta[answeredAsksKey]; kept {
		t.Error("answered_asks kept as an extra key")
	}
	if got := cardAnsweredAsks(loadCardMust(t, "a")); len(got) != 1 || got[0] != 2 {
		t.Errorf("readers see answered asks %v", got)
	}
	if out, code := cardIn("where: x\n", "card", "a"); code != 0 || strings.Contains(readCard("a"), answeredAsksKey) {
		t.Errorf("rendered view keeps answered_asks: %s\n%s", out, readCard("a"))
	}
}

// tracks card refuses on the store what it refuses on the files: a line that would start a
// section, a value the view would cut at " # ", a dir that is not absolute.
func TestCardRefusalsHoldOnTheStore(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "r", "focus", "", "Before.", "")
	importNow(t)
	before := readCard("r")
	for in, want := range map[string]string{
		"where: x\n## Injected\n":     "cannot start with ##",
		"asks:\n## Needs me\n":        "cannot start with ##",
		"waiting_on: PR # 4 or # 5\n": `which the card reads as a comment`,
		"dir: relative/path\n":        "must be an absolute path",
	} {
		if out, code := cardIn(in, "card", "r"); code == 0 || !strings.Contains(out, want) {
			t.Errorf("%q: exit %d: %s", in, code, out)
		}
	}
	if readCard("r") != before || loadCardMust(t, "r").firstLine("Where it is") != "Before." {
		t.Error("a refused write changed the track")
	}
}

// tracks render puts back a view that is missing or edited by hand, and leaves a right one alone.
func TestRenderRepairsViews(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "a", "focus", "", "w", "")
	card(t, th, "b", "focus", "", "w", "")
	importNow(t)
	if out, code := cardIn("where: Rendered.\n", "card", "a"); code != 0 {
		t.Fatal(out)
	}
	if out, code := cardIn("where: Also rendered.\n", "card", "b"); code != 0 {
		t.Fatal(out)
	}
	right := readCard("b")
	if err := os.Remove(cardPath("a")); err != nil {
		t.Fatal(err)
	}
	stB, _ := os.Stat(cardPath("b"))
	if out, code := do("render"); code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(readCard("a"), "Rendered.") {
		t.Errorf("missing view not put back:\n%s", readCard("a"))
	}
	if st, _ := os.Stat(cardPath("b")); readCard("b") != right || !os.SameFile(st, stB) {
		t.Error("a view that was right was written again")
	}
}

// Closing renders the archive file once, with the asks the track had, and takes the live view.
func TestCloseRendersTheArchiveWithItsAsks(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "c", "focus", "", "Almost.", "1. Last question?")
	importNow(t)
	dest, _, err := closeTrack("c", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	gone(t, "c")
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	a := parseCard("c", string(b))
	if a.Meta["closed"] == "" || a.Sections["Outcome"] != "Almost." || a.Sections["Needs me"] != "1. Last question?" {
		t.Errorf("archive %s:\n%s", filepath.Base(dest), b)
	}
	if got := readArchive(); len(got) != 1 || got[0].Name != "c" || got[0].File != dest || got[0].Outcome != "Almost." {
		t.Errorf("archive list: %+v", got)
	}
}

// A session's last card write after its track closed is kept whole in the handled log (EMI-436).
func TestACardWriteAfterCloseKeepsTheLastReport(t *testing.T) {
	th, _ := setup(t)
	card(t, th, "late", "focus", "", "Almost.", "")
	importNow(t)
	if _, _, err := closeTrack("late", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	report := "PR 12 is up and CI is green; " + strings.Repeat("a long detail ", 40)
	out, code := cardIn("where: "+report+"\n", "card", "late")
	if code != 0 || !strings.HasPrefix(out, "closed\n") || !strings.Contains(out, "kept") {
		t.Fatalf("exit %d: %q", code, out)
	}
	var log []store.DeskLogEntry
	if err := withBareStore(func(st *store.Store) (err error) { log, err = st.DeskLogRecent(5); return err }); err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].Track != "late" || log[0].Kind != "noted" || !strings.Contains(log[0].Did, strings.TrimSpace(report)) {
		t.Errorf("handled log: %+v", log)
	}
}

// A hook that waited behind export lands in the files: hookStore reports the move.
func TestHookAfterExportFallsBackToFiles(t *testing.T) {
	th, _ := setup(t)
	t.Setenv("REX_SESSION", "s5")
	card(t, th, "e", "focus", "s5", "w", "")
	importNow(t)
	if out, code := do("export"); code != 0 {
		t.Fatal(out)
	}
	if dbExists() {
		t.Fatal("tracks.db is still there")
	}
	hook(t, `{"hook_event_name":"Stop","cwd":"/c"}`)
	b, _ := os.ReadFile(logPath())
	if !strings.Contains(string(b), `"event":"stop"`) {
		t.Errorf("stop not in attention.jsonl:\n%s", b)
	}
}
