package tracks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const renSess = "session:0007-cccccc-77aa11"

// renameStub is a rex whose one session, renSess, has the given label. bin/rename.fail makes
// session rename fail.
func renameStub(t *testing.T, bin, label string) {
	t.Helper()
	body := `#!/bin/sh
[ "$1" = "--autostart=false" ] || echo "no --autostart=false: $*" >> ` + bin + `/rex.bad
shift
echo "$@" >> ` + bin + `/rex.calls
case "$1 $2" in
"session ls") echo '{"sessions":[{"session_id":"` + renSess + `","label":"` + label + `"},{"session_id":"session:0008-dddddd-99bb22","label":"emils-own"}]}';;
"session rename") [ -f ` + bin + `/rename.fail ] && { echo "server busy" >&2; exit 1; };;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func renameCalls(bin string) []string {
	var out []string
	for _, l := range strings.Split(rexCalls(bin), "\n") {
		if strings.HasPrefix(l, "session rename") {
			out = append(out, l)
		}
	}
	return out
}

func gone(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(cardPath(name)); !os.IsNotExist(err) {
		t.Errorf("card %s is still there", name)
	}
}

func TestRenameMovesCardRelabelsSessionAndKeepsHistory(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "how-claude-tests-take")
	card(t, th, "how-claude-tests-take", "focus", renSess, "Not started.", "")
	os.Chmod(cardPath("how-claude-tests-take"), 0o600)
	before := readCard("how-claude-tests-take")
	opened := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	appendEvent(Event{At: opened, Event: "open", Track: "how-claude-tests-take", RexSession: renSess})

	out, code := do("rename", "how-claude-tests-take", "claude-code-verification")
	if code != 0 || !strings.Contains(out, "Renamed how-claude-tests-take to claude-code-verification") {
		t.Fatalf("exit %d: %s", code, out)
	}
	gone(t, "how-claude-tests-take")
	after := readCard("claude-code-verification")
	if want := strings.Replace(before, "name: how-claude-tests-take", "name: claude-code-verification", 1); after != want && !(dbExists() && sameCard(after, want)) {
		t.Errorf("card:\n%s\nwant:\n%s", after, want)
	}
	if st, _ := os.Stat(cardPath("claude-code-verification")); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if c := renameCalls(bin); len(c) != 1 || c[0] != "session rename "+renSess+" claude-code-verification" {
		t.Errorf("rex renames: %q", c)
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "rex.bad")); len(b) > 0 {
		t.Errorf("%s", b)
	}
	// History follows the track; the log keeps what it said.
	if at, ok := lastSeen(readEvents(), &Card{Name: "claude-code-verification", Meta: map[string]string{}}); !ok || !at.Equal(opened) {
		t.Errorf("lastSeen after rename: %v %v", at, ok)
	}
	if dbExists() {
		// tracks.db keeps each event's name at the time beside its track.
		var said []string
		_ = withStore(func(st *store.Store) error {
			evs, err := st.AllEvents()
			for _, e := range evs {
				said = append(said, e.Event+" "+e.TrackName)
			}
			return err
		})
		if strings.Join(said, ", ") != "open how-claude-tests-take, rename claude-code-verification" {
			t.Errorf("events: %v", said)
		}
		return
	}
	raw, _ := os.ReadFile(logPath())
	if !strings.Contains(string(raw), `"event":"open","rex_session":"`+renSess+`","track":"how-claude-tests-take"`) ||
		!strings.Contains(string(raw), `"event":"rename","rex_session":"","track":"claude-code-verification"`) {
		t.Errorf("log:\n%s", raw)
	}
}

func TestRenameRefusals(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	card(t, th, "taken", "focus", "", "w", "")
	card(t, th, "was-closed", "focus", "", "w", "")
	if _, _, err := closeTrack("was-closed", time.Now().AddDate(0, -1, 0), ""); err != nil {
		t.Fatal(err)
	}
	card(t, th, "shut", "focus", renSess, "w", "")
	if _, _, err := closeTrack("shut", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(bin, "rex.calls"))
	before := readCard("old")
	for _, c := range []struct{ old, new, why string }{
		{"old", "taken", "a track named taken exists"},
		{"old", "was-closed", "name of a closed track"},
		{"old", "old", "already has that name"},
		{"old", "../escape", "not a valid track name"},
		{"old", "-flag", "not a valid track name"},
		{"old", "two words", "not a valid track name"},
		{"old", "", "not a valid track name"},
		{"shut", "reopened", "is closed"},
		{"missing", "fresh", "no card"},
		{"../old", "fresh", "not a track name"},
	} {
		out, code := do("rename", c.old, c.new)
		if code == 0 || !strings.Contains(out, c.why) {
			t.Errorf("rename %q %q: exit %d %q, want %q", c.old, c.new, code, out, c.why)
		}
	}
	if readCard("old") != before || readCard("taken") == "" {
		t.Error("a refused rename changed a card")
	}
	for _, n := range []string{"reopened", "fresh", "escape"} {
		gone(t, n)
	}
	if rexCalls(bin) != "" {
		t.Errorf("a refused rename called rex:\n%s", rexCalls(bin))
	}
	if strings.Contains(string(must(os.ReadFile(logPath()))), `"rename"`) {
		t.Error("a refused rename was logged")
	}
	if out, code := do("rename", "old"); code == 0 || !strings.Contains(out, "usage: tracks rename NAME NEW") {
		t.Errorf("usage: %d %s", code, out)
	}
}

func must(b []byte, _ error) []byte { return b }

func TestRenameLeavesOtherSessionsAlone(t *testing.T) {
	for _, label := range []string{"emils-own", "✓ old", "Old", ""} {
		th, bin := setup(t)
		renameStub(t, bin, label)
		card(t, th, "old", "focus", renSess, "w", "")
		out, code := do("rename", "old", "new")
		if code != 0 || !strings.Contains(out, "left as it is") {
			t.Errorf("label %q: exit %d %s", label, code, out)
		}
		if c := renameCalls(bin); len(c) != 0 {
			t.Errorf("label %q: renamed %q", label, c)
		}
		if loadCardMust(t, "new").Meta["name"] != "new" {
			t.Errorf("label %q: card not renamed", label)
		}
	}
	// No session on the card: nothing to relabel, nothing said.
	th, bin := setup(t)
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", "", "w", "")
	if out, code := do("rename", "old", "new"); code != 0 || strings.Count(out, "\n") != 1 || rexCalls(bin) != "" {
		t.Errorf("no session: exit %d %q calls %q", code, out, rexCalls(bin))
	}
}

func TestRenameRexFailureKeepsTheCardRenamed(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	os.WriteFile(filepath.Join(bin, "rename.fail"), nil, 0o644)
	out, code := do("rename", "old", "new")
	if code != 0 || !strings.Contains(out, "could not relabel the Rex session "+renSess) || !strings.Contains(out, "server busy") {
		t.Errorf("exit %d: %s", code, out)
	}
	gone(t, "old")
	if loadCardMust(t, "new").Meta["rex_session"] != renSess {
		t.Error("card lost its session")
	}
	// Rex down altogether: same.
	th, bin = setup(t)
	os.WriteFile(filepath.Join(bin, "rex"), []byte("#!/bin/sh\necho 'no server' >&2\nexit 1\n"), 0o755)
	card(t, th, "old", "focus", renSess, "w", "")
	if out, code := do("rename", "old", "new"); code != 0 || !strings.Contains(out, "could not find the Rex session") {
		t.Errorf("rex down: exit %d %s", code, out)
	}
	gone(t, "old")
}

func TestRenameKeepsAWriteThatLandsMidRename(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // the hard-link rename; in tracks.db a rename is one transaction
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	t.Cleanup(func() { beforeTake = func() {} })
	// A writer that skips the cards lock (an agent writing the file by hand) lands after the
	// rename read the card.
	beforeTake = func() {
		if err := writeAtomic(cardPath("old"), []byte(strings.Replace(readCard("old"), "mode: focus", "mode: parked", 1))); err != nil {
			t.Error(err)
		}
	}
	if _, err := renameTrack("old", "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	c := loadCardMust(t, "new")
	if c.mode() != "parked" || c.Meta["name"] != "new" {
		t.Errorf("lost the mid-rename write:\n%s", readCard("new"))
	}
	gone(t, "old")
	// A write after the rename finds no card and recreates nothing.
	beforeTake = func() {}
	if err := setMode("old", "focus"); err == nil {
		t.Error("a write to the old name succeeded")
	}
	gone(t, "old")
	if entries, _ := os.ReadDir(filepath.Join(th, "cards")); len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestRenameOfACardTakenAwayMidRename(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // the hard-link rename; in tracks.db a rename is one transaction
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	t.Cleanup(func() { beforeTake = func() {} })
	// Something that skips the cards lock moves the card away after the rename read it.
	beforeTake = func() {
		if err := os.MkdirAll(archiveDir(), 0o755); err != nil {
			t.Error(err)
		}
		if err := os.Rename(cardPath("old"), filepath.Join(archiveDir(), "2026-11-20-old.md")); err != nil {
			t.Error(err)
		}
	}
	if _, err := renameTrack("old", "new", time.Now()); err == nil || !strings.Contains(err.Error(), "not renamed") {
		t.Fatalf("err %v", err)
	}
	gone(t, "new")
	gone(t, "old")
	if archivedCard("old") == "" {
		t.Error("the archive was undone")
	}
	if calls := renameCalls(bin); len(calls) != 0 {
		t.Errorf("a failed rename relabelled: %q", calls)
	}
}

// Close and tracks card take the cards lock, so they wait for a rename instead of landing
// inside it: the close finds no card under the old name, and the card write follows the rename
// instead of recreating the old card.
func TestLockedWritersWaitForARename(t *testing.T) {
	th, bin := setup(t)
	noImport(t) // the cards lock around the hard-link rename; in tracks.db a rename is one transaction
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	t.Cleanup(func() { beforeTake = func() {} })
	type res struct {
		out string
		err error
	}
	closed, carded := make(chan res, 1), make(chan res, 1)
	beforeTake = func() {
		go func() {
			_, _, err := closeTrack("old", time.Now(), "")
			closed <- res{err: err}
		}()
		go func() {
			out, code := cardIn("where: Written during the rename.\n", "card", "old")
			if code != 0 {
				carded <- res{err: errors.New(out)}
				return
			}
			carded <- res{out: out}
		}()
		time.Sleep(300 * time.Millisecond) // both are waiting for the lock by now
	}
	if _, err := renameTrack("old", "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := <-closed; r.err == nil {
		t.Error("the close ran inside the rename or closed the old name")
	}
	if r := <-carded; r.err != nil || !strings.HasPrefix(r.out, "renamed to new\n") {
		t.Errorf("tracks card: %q %v", r.out, r.err)
	}
	gone(t, "old")
	if archivedCard("old") != "" || archivedCard("new") != "" {
		t.Error("something was archived")
	}
	if w := loadCardMust(t, "new").firstLine("Where it is"); w != "Written during the rename." {
		t.Errorf("new card's where: %q\n%s", w, readCard("new"))
	}
}

func TestAnsweredCheckStaysAnsweredAfterRename(t *testing.T) {
	th, _ := setup(t)
	waitingCard(t, th, "w-old", "pr 7 merged")
	eval := func(string, check) (string, error) { return yes, nil }
	if out, _ := checks(t, false, eval); !strings.Contains(out, "added an ask") {
		t.Fatalf("no ask:\n%s", out)
	}
	// Emil opens the track and the agent clears the ask.
	appendEvent(Event{At: time.Now().UTC(), Event: "open", Track: "w-old"})
	waitingCard(t, th, "w-old", "pr 7 merged")
	if _, err := renameTrack("w-old", "w-new", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, _ := checks(t, false, eval)
	if !strings.Contains(out, "unchanged") || loadCardMust(t, "w-new").asks() != 0 {
		t.Errorf("the answered ask came back:\n%s\n%s", out, readCard("w-new"))
	}
	m, _ := loadChecks()
	if _, ok := m["w-new: pr 7 merged"]; !ok || len(m) != 1 {
		t.Errorf("checks.json: %+v", m)
	}
}

func TestRenamedAwayNameStaysRetired(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "old")
	card(t, th, "old", "focus", renSess, "w", "")
	card(t, th, "other", "focus", "", "w", "")
	if _, err := renameTrack("old", "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	if out, code := do("new", "old", "--dir", th); code == 0 || !strings.Contains(out, "old was renamed to new") {
		t.Errorf("tracks new old: %d %s", code, out)
	}
	gone(t, "old")
	r := loadRetired()
	for _, n := range []string{"old", "OLD", "Old"} {
		if !nameTaken(n, r) || freeName(n, r) != n+"-2" {
			t.Errorf("the pane would reuse %s: %q", n, freeName(n, r))
		}
		if out, code := do("new", n, "--dir", th); code == 0 || !strings.Contains(out, "was renamed to new") {
			t.Errorf("tracks new %s: %d %s", n, code, out)
		}
		if _, err := renameTrack("other", n, time.Now()); err == nil || !strings.Contains(err.Error(), "renamed to new") {
			t.Errorf("rename onto retired %s: %v", n, err)
		}
	}
	// The form reads the renames once, when it opens.
	devDir(t)
	m := newPane()
	press(m, "n")
	typeText(m, "old")
	if !strings.Contains(m.form.preview, "→ old-2") {
		t.Errorf("preview %q", m.form.preview)
	}
}

func TestRenameNeedsTheExactName(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "zzrv-old")
	card(t, th, "zzrv-old", "focus", renSess, "w", "")
	// On a case-insensitive disk ZZRV-OLD.md opens zzrv-old.md; either way it is refused.
	if _, err := renameTrack("ZZRV-OLD", "x", time.Now()); err == nil || !strings.Contains(err.Error(), "no card named exactly") {
		t.Errorf("err %v", err)
	}
	if readCard("zzrv-old") == "" || len(renameCalls(bin)) != 0 {
		t.Error("the card or the session changed")
	}
	gone(t, "x")
	// A card whose name: is not its file name is refused too. Only a card file can be one: a
	// track in tracks.db has one name.
	if !storeMode() {
		os.WriteFile(cardPath("odd"), []byte("---\nname: other\n---\n"), 0o644)
		if _, err := renameTrack("odd", "y", time.Now()); err == nil || !strings.Contains(err.Error(), "no card named exactly") {
			t.Errorf("odd: %v", err)
		}
	}
	// Archived names hold their name whatever the case.
	card(t, th, "Shut", "focus", "", "w", "")
	if _, _, err := closeTrack("Shut", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if archivedCard("shut") == "" || !nameTaken("SHUT", nil) {
		t.Error("archive lookup is case-sensitive")
	}
}

func TestFollowRenames(t *testing.T) {
	ev := func(name, kind string) Event { return Event{Event: name, Track: kind} }
	ren := func(old, name string) Event { return Event{Event: renameEvent, Track: name, Kind: old} }
	evs := []Event{ev("open", "a"), ren("a", "b"), ev("open", "b"), ren("b", "c"), ev("open", "c"),
		ev("open", "x"), ren("x", "a"), ev("open", "a")}
	followRenames(evs)
	var got []string
	for _, e := range evs {
		got = append(got, e.Track)
	}
	if want := "c c c c c a a a"; strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
	if renamedTo(evs, "b") != "c" || renamedTo(evs, "x") != "a" || renamedTo(evs, "z") != "z" {
		t.Error("renamedTo")
	}
}

func TestPaneFollowsARename(t *testing.T) {
	th, bin := setup(t)
	renameStub(t, bin, "zz-old")
	card(t, th, "aa", "focus", "", "w", "")
	card(t, th, "zz-old", "focus", renSess, "w", "")
	m := newPane()
	selectTrack(t, m, "zz-old")
	// The new name sorts first, so a cursor that kept its row would land on aa.
	if _, err := renameTrack("zz-old", "a0-new", time.Now()); err != nil {
		t.Fatal(err)
	}
	m.Update(tickMsg{})
	if m.cur != "a0-new" || m.selected().Name != "a0-new" {
		t.Errorf("cursor on %q", m.cur)
	}
	if out := plain(m, 100, 24); !strings.Contains(out, "▌a0-new") || strings.Contains(out, "zz-old  ") {
		t.Errorf("pane:\n%s", out)
	}
}

// A rename retitles the track's own Claude window and records the title for /resume.
func TestRenameRetitlesClaude(t *testing.T) {
	_, bin := setup(t)
	renameStub(t, bin, "old")
	id := "11111111-2222-3333-4444-555555555555"
	metaCard(t, "old", "focus", "rex_session: "+renSess, "claude_session: "+id)
	tr := transcript(t, "proj", id, "cli", "/w", renSess, time.Now())
	tty := filepath.Join(t.TempDir(), "tty")
	writeFile(t, tty, "")
	asked := ""
	claudeTTY = func(s string) string { asked = s; return tty }
	t.Cleanup(func() { claudeTTY = claudeTTYOf })
	notes, err := renameTrack("old", "new", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.Contains(n, "Claude") || strings.Contains(n, "resume") {
			t.Errorf("note: %s", n)
		}
	}
	if asked != id {
		t.Errorf("asked for the terminal of %q", asked)
	}
	if got := readFile(tty); got != "\x1b]0;new\x07" {
		t.Errorf("terminal got %q", got)
	}
	want := `{"type":"custom-title","customTitle":"new","sessionId":"` + id + `"}` + "\n"
	if got := readFile(tr); !strings.HasSuffix(got, want) {
		t.Errorf("transcript ends:\n%s", got)
	}
}

// Only the Claude that is the card's session gets its terminal retitled.
func TestClaudeTTYNeedsTheCardsSession(t *testing.T) {
	id := "11111111-2222-3333-4444-555555555555"
	for _, c := range []struct{ pid, sess, card string }{
		{"", id, id},
		{"123", "99999999-2222-3333-4444-555555555555", id},
		{"123", id, ""},
		{"12;3", id, id},
	} {
		t.Setenv("CLAUDE_PID", c.pid)
		t.Setenv("CLAUDE_CODE_SESSION_ID", c.sess)
		if got := claudeTTYOf(c.card); got != "" {
			t.Errorf("%+v: %q", c, got)
		}
	}
}
