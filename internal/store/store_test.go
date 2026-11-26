package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var epoch = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

// testClock ticks one millisecond per call, so every write has its own time.
func testClock() func() time.Time {
	var n atomic.Int64
	return func() time.Time { return epoch.Add(time.Duration(n.Add(1)) * time.Millisecond) }
}

func openAt(t *testing.T, path string, now func() time.Time) *Store {
	t.Helper()
	s, err := Open(path, WithClock(now), WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "tracks.db"), testClock())
}

func mustTrack(t *testing.T, s *Store, name string) Track {
	t.Helper()
	tr, err := s.NewTrack(NewTrackSpec{Name: name, Dir: "/work/" + name})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func get(t *testing.T, s *Store, name string) View {
	t.Helper()
	v, err := s.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ptr(s string) *string { return &s }

func texts(asks []Ask) []string {
	out := make([]string, len(asks))
	for i, a := range asks {
		out[i] = a.Text
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func eventNames(t *testing.T, s *Store, name string) []string {
	t.Helper()
	evs, err := s.Events(name)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Event
	}
	return out
}

func TestOpenCreatesFileWithMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "alpha")
	for _, p := range []string{path, path + "-wal"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
		}
	}
}

func TestOpenUsesWALAndBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s, err := Open(path, WithBusyTimeout(500*time.Millisecond), WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode string
	var busy, fk int
	s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if mode != "wal" || busy != 500 || fk != 1 {
		t.Errorf("journal_mode=%s busy_timeout=%d foreign_keys=%d, want wal 500 1", mode, busy, fk)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s, err := Open(path, WithCreate())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a database with a newer schema")
	}
}

func TestOpenTwiceKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	s := openAt(t, path, testClock())
	mustTrack(t, s, "alpha")
	s2 := openAt(t, path, testClock())
	eq(t, "name", get(t, s2, "alpha").Track.Name, "alpha")
}

func TestNewTrackRefusesTakenClosedAndRetiredNames(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	mustTrack(t, s, "beta")
	mustTrack(t, s, "gamma")
	if err := s.CloseTrack("beta"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("gamma", "delta"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "ALPHA", "beta", "Beta", "gamma", "delta"} {
		if _, err := s.NewTrack(NewTrackSpec{Name: name}); !errors.Is(err, ErrNameTaken) {
			t.Errorf("NewTrack(%q) = %v, want ErrNameTaken", name, err)
		}
	}
	for _, name := range []string{"", "-x", "a b", "a/b"} {
		if _, err := s.NewTrack(NewTrackSpec{Name: name}); !errors.Is(err, ErrBadName) {
			t.Errorf("NewTrack(%q) = %v, want ErrBadName", name, err)
		}
	}
	if _, err := s.NewTrack(NewTrackSpec{Name: "x", Mode: "sleeping"}); !errors.Is(err, ErrBadMode) {
		t.Errorf("bad mode = %v, want ErrBadMode", err)
	}
}

func TestNewTrackDefaults(t *testing.T) {
	s := newStore(t)
	tr := mustTrack(t, s, "alpha")
	eq(t, "mode", tr.Mode, "background")
	eq(t, "goal", tr.Goal, DefaultGoal)
	eq(t, "where", tr.Where, "Not started.")
	if !tr.UpdatedAt.IsZero() || tr.ChangedAt.IsZero() || tr.CreatedAt.IsZero() || tr.Version != 1 {
		t.Errorf("stamps: updated=%v changed=%v created=%v version=%d", tr.UpdatedAt, tr.ChangedAt, tr.CreatedAt, tr.Version)
	}
}

func TestBind(t *testing.T) {
	s := newStore(t)
	r, err := s.Bind("alpha", "session:1", "/work/alpha")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "created name", r.Track.Name, "alpha")
	eq(t, "rex_session", r.Track.RexSession, "session:1")
	eq(t, "dir", r.Track.Dir, "/work/alpha")

	// Binding again from the same session writes nothing.
	again, err := s.Bind("alpha", "session:1", "/elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "version after same bind", again.Track.Version, r.Track.Version)
	eq(t, "dir kept", again.Track.Dir, "/work/alpha")

	// A new session takes over.
	moved, err := s.Bind("ALPHA", "session:2", "")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "rex_session after rebind", moved.Track.RexSession, "session:2")
	eq(t, "no rename for case", moved.RenamedTo, "")
}

func TestBindFollowsRenameAndReportsClosed(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "old")
	if err := s.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Bind("old", "session:9", "/x")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "renamed to", r.RenamedTo, "new")
	eq(t, "session untouched", get(t, s, "new").Track.RexSession, "")
	if _, err := s.Get("old"); err != nil {
		t.Errorf("old name no longer resolves: %v", err)
	}

	if err := s.CloseTrack("new"); err != nil {
		t.Fatal(err)
	}
	r, err = s.Bind("new", "session:9", "/x")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Closed {
		t.Error("closed track not reported")
	}
	eq(t, "session untouched on closed", get(t, s, "new").Track.RexSession, "")
	var n int
	s.db.QueryRow(`SELECT count(*) FROM tracks`).Scan(&n)
	eq(t, "tracks after binds to old and closed names", n, 1)
}

func TestCardSetsFieldsAndStamps(t *testing.T) {
	s := newStore(t)
	before := mustTrack(t, s, "alpha")
	res, err := s.Card("alpha", CardUpdate{
		Tier: ptr("2"), TierWhy: "product code", Dir: ptr("/new"), Links: ptr("EMI-1, EMI-2"), Check: ptr("pr 4 merged"),
		WaitingOn: ptr("review"), Goal: ptr("Done."), Where: ptr("Working.\nMore."), Later: ptr("Later thing"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Track
	eq(t, "tier", tr.Tier, "2")
	eq(t, "tier_why", tr.TierWhy, "product code")
	eq(t, "dir", tr.Dir, "/new")
	eq(t, "links", tr.Links, "EMI-1, EMI-2")
	eq(t, "check", tr.Check, "pr 4 merged")
	eq(t, "waiting_on", tr.WaitingOn, "review")
	eq(t, "goal", tr.Goal, "Done.")
	eq(t, "where", tr.Where, "Working.\nMore.")
	eq(t, "later", tr.Later, "Later thing")
	if !tr.UpdatedAt.After(before.UpdatedAt) || !tr.ChangedAt.After(before.ChangedAt) || tr.Version != before.Version+1 {
		t.Errorf("stamps not moved: %+v", tr)
	}

	res, err = s.Card("alpha", CardUpdate{Goal: ptr("Changed.")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "untouched links", res.Track.Links, "EMI-1, EMI-2")
	eq(t, "untouched tier_why", res.Track.TierWhy, "product code")
	eq(t, "untouched mode", res.Track.Mode, "background")
}

func TestCardReconcilesAgentAsks(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	first, err := s.Card("alpha", CardUpdate{Asks: []string{"one", "two", "two", "", "three"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "first asks", texts(first.Asks), []string{"one", "two", "three"})
	for i, a := range first.Asks {
		eq(t, "number", a.N, i+1)
	}

	second, err := s.Card("alpha", CardUpdate{Asks: []string{"three", "four", "one"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "second asks", texts(second.Asks), []string{"three", "four", "one"})
	byText := map[string]Ask{}
	for _, a := range first.Asks {
		byText[a.Text] = a
	}
	for _, a := range second.Asks {
		if old, ok := byText[a.Text]; ok {
			eq(t, a.Text+" keeps its id", a.ID, old.ID)
			if !a.CreatedAt.Equal(old.CreatedAt) {
				t.Errorf("%s: created_at moved", a.Text)
			}
		} else if a.ID <= first.Asks[2].ID {
			t.Errorf("%s: new ask reused an id", a.Text)
		}
	}
	var closedBy string
	s.db.QueryRow(`SELECT closed_by FROM asks WHERE id = ?`, byText["two"].ID).Scan(&closedBy)
	eq(t, "dropped ask closed_by", closedBy, "agent")

	cleared, err := s.Card("alpha", CardUpdate{Asks: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "cleared asks", len(cleared.Asks), 0)

	left, err := s.Card("alpha", CardUpdate{Goal: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "nil Asks leaves asks", len(left.Asks), 0)
}

func TestCardKeepsUnseenCheckAsksAfterAgentsAndDropsSeenOnes(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged"), Asks: []string{"agent ask"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 4 merged", true); err != nil {
		t.Fatal(err)
	}
	res, err := s.Card("alpha", CardUpdate{Asks: []string{"new agent ask", "agent ask"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Asks) != 3 || res.Asks[2].Kind != "check" {
		t.Fatalf("asks = %v, want the check ask last", texts(res.Asks))
	}

	if err := s.LogEvent("alpha", Event{Event: "open"}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Card("alpha", CardUpdate{Asks: []string{"only"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "seen check ask dropped", texts(res.Asks), []string{"only"})
}

// A track read before another write landed cannot be saved over it: the lost update EMI-294
// fixed on files stays impossible here even for a caller that reads outside its transaction.
func TestSaveRefusesAStaleRead(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	stale := get(t, s, "alpha").Track
	if _, err := s.Card("alpha", CardUpdate{Goal: ptr("fresh goal")}); err != nil {
		t.Fatal(err)
	}
	err := s.tx(func(tx *wtx) error {
		stale.Mode = "parked"
		return s.save(tx, &stale, false)
	})
	if !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("stale save: err = %v, want ErrStaleWrite", err)
	}
	v := get(t, s, "alpha")
	eq(t, "goal", v.Track.Goal, "fresh goal")
	eq(t, "mode", v.Track.Mode == "parked", false)
}

// An agent that re-sends the check ask it read on the card gets one line, still the check's own,
// so the pane can still apply it.
func TestCardResendingACheckAskKeepsOneCheckRow(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	res, err := s.RecordCheck("alpha", "pr 4 merged", true)
	if err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "alpha")
	if len(v.Asks) != 1 {
		t.Fatalf("asks = %v after the check fired (record: %+v)", texts(v.Asks), res)
	}
	checkText := v.Asks[0].Text
	got, err := s.Card("alpha", CardUpdate{Asks: []string{"mine", checkText}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "asks", texts(got.Asks), []string{"mine", checkText})
	if got.Asks[1].Kind != "check" || got.Asks[1].ID != v.Asks[0].ID {
		t.Errorf("re-sent ask = %+v, want the check row %d", got.Asks[1], v.Asks[0].ID)
	}
}

// The old check's ask stays while Emil has not seen it, as the file code keeps it; once seen,
// changing check: closes it.
func TestCardChangingCheckSupersedesTheSeenAskOfTheOldCheck(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 4 merged", true); err != nil {
		t.Fatal(err)
	}
	res, err := s.Card("alpha", CardUpdate{Check: ptr("pr 5 merged")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "unseen ask after check changed", len(res.Asks), 1)
	if err := s.LogEvent("alpha", Event{Event: "open"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Card("alpha", CardUpdate{Check: ptr("pr 5 merged")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "seen ask after check changed", len(res.Asks), 0)

	same, err := s.Card("alpha", CardUpdate{Check: ptr("pr 5 merged")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "check", same.Track.Check, "pr 5 merged")
}

func TestCardFollowsRenameAndLeavesClosedAlone(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "old")
	if err := s.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Card("old", CardUpdate{Goal: ptr("via old name")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "renamed to", res.RenamedTo, "new")
	eq(t, "goal written to the track", get(t, s, "new").Track.Goal, "via old name")

	if err := s.CloseTrack("new"); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "new").Track.Version
	res, err = s.Card("new", CardUpdate{Goal: ptr("after close")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Closed {
		t.Error("closed not reported")
	}
	eq(t, "version unchanged", get(t, s, "new").Track.Version, v)
	eq(t, "goal unchanged", get(t, s, "new").Track.Goal, "via old name")

	if _, err := s.Card("nobody", CardUpdate{Goal: ptr("x")}); !errors.Is(err, ErrNoTrack) {
		t.Errorf("unknown track = %v, want ErrNoTrack", err)
	}
}

func TestRename(t *testing.T) {
	s := newStore(t)
	tr := mustTrack(t, s, "old")
	mustTrack(t, s, "other")
	if err := s.Rename("old", "other"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename onto a taken name = %v, want ErrNameTaken", err)
	}
	if err := s.Rename("old", "bad name"); !errors.Is(err, ErrBadName) {
		t.Errorf("rename to a bad name = %v, want ErrBadName", err)
	}
	if err := s.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "old")
	eq(t, "id kept", v.Track.ID, tr.ID)
	eq(t, "current name", v.Track.Name, "new")
	eq(t, "old resolves to", v.RenamedTo, "new")
	eq(t, "new resolves to", get(t, s, "new").RenamedTo, "")
	var retired string
	s.db.QueryRow(`SELECT coalesce(retired_at, '') FROM names WHERE name = 'old'`).Scan(&retired)
	if retired == "" {
		t.Error("old name not retired")
	}
	evs, _ := s.Events("new")
	if len(evs) != 1 || evs[0].Event != "rename" || evs[0].TrackName != "new" || evs[0].Kind != "old" {
		t.Errorf("events = %+v, want one rename event from old to new", evs)
	}
	if err := s.Rename("old", "third"); err == nil {
		t.Error("renaming through a retired name succeeded")
	}
	if err := s.Rename("nobody", "x"); !errors.Is(err, ErrNoTrack) {
		t.Errorf("rename of unknown = %v, want ErrNoTrack", err)
	}
	if err := s.Rename("new", "again"); err != nil {
		t.Fatal(err)
	}
	eq(t, "chain resolves", get(t, s, "old").RenamedTo, "again")
}

func TestCloseTrack(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"one", "two"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "alpha")
	if !v.Closed || v.Track.ClosedAt.IsZero() {
		t.Error("track not closed")
	}
	eq(t, "open asks", len(v.Asks), 0)
	var n int
	s.db.QueryRow(`SELECT count(*) FROM asks WHERE closed_by = 'closed'`).Scan(&n)
	eq(t, "asks closed by close", n, 2)
	eq(t, "events", eventNames(t, s, "alpha"), []string{"close"})
	if err := s.CloseTrack("alpha"); !errors.Is(err, ErrClosed) {
		t.Errorf("second close = %v, want ErrClosed", err)
	}
	if !strings.Contains(RenderView(v.Track, v.Asks), "## Outcome\n") {
		t.Error("closed card has no Outcome section")
	}
}

func TestSetModeMovesChangedAtButNotUpdatedAt(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	carded, err := s.Card("alpha", CardUpdate{Goal: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("alpha", "waiting"); err != nil {
		t.Fatal(err)
	}
	tr := get(t, s, "alpha").Track
	eq(t, "mode", tr.Mode, "waiting")
	if !tr.UpdatedAt.Equal(carded.Track.UpdatedAt) {
		t.Error("updated_at moved with a mode write")
	}
	if !tr.ChangedAt.After(carded.Track.ChangedAt) {
		t.Error("changed_at did not move")
	}
	eq(t, "events", eventNames(t, s, "alpha"), []string{"mode"})
	if err := s.SetMode("alpha", "asleep"); !errors.Is(err, ErrBadMode) {
		t.Errorf("bad mode = %v, want ErrBadMode", err)
	}
	if err := s.SetMode("nobody", "focus"); !errors.Is(err, ErrNoTrack) {
		t.Errorf("unknown track = %v, want ErrNoTrack", err)
	}
}

var checkAskShape = regexp.MustCompile(`^Check ".*" is now (yes|no)\. Recommend: (set mode to focus|open the track|open the track and see why)\.$`)

func TestRecordCheck(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("alpha", "waiting"); err != nil {
		t.Fatal(err)
	}

	// A stored answer that is missing counts as "no": a first "no" is not news.
	res, err := s.RecordCheck("alpha", "pr 4 merged", false)
	if err != nil || res.Fired {
		t.Fatalf("first no: fired=%v err=%v", res.Fired, err)
	}
	res, err = s.RecordCheck("alpha", "pr 4 merged", true)
	if err != nil || !res.Fired {
		t.Fatalf("first yes: fired=%v err=%v", res.Fired, err)
	}
	want := `Check "pr 4 merged" is now yes. Recommend: open the track.`
	eq(t, "ask text", res.Ask.Text, want)
	if !checkAskShape.MatchString(res.Ask.Text) {
		t.Errorf("ask %q does not have the shape the card matcher reads", res.Ask.Text)
	}
	eq(t, "kind", res.Ask.Kind, "check")

	res, err = s.RecordCheck("alpha", "pr 4 merged", true)
	if err != nil || res.Fired {
		t.Fatalf("unchanged yes: fired=%v err=%v", res.Fired, err)
	}
	eq(t, "asks after unchanged", len(get(t, s, "alpha").Asks), 1)

	res, err = s.RecordCheck("alpha", "pr 4 merged", false)
	if err != nil || !res.Fired {
		t.Fatalf("flip to no: fired=%v err=%v", res.Fired, err)
	}
	asks := get(t, s, "alpha").Asks
	eq(t, "only the new ask is open", texts(asks), []string{`Check "pr 4 merged" is now no. Recommend: open the track and see why.`})
	var closedBy string
	s.db.QueryRow(`SELECT closed_by FROM asks WHERE text LIKE '%now yes%'`).Scan(&closedBy)
	eq(t, "old ask closed_by", closedBy, "superseded")

	checks, _ := s.Checks("alpha")
	if len(checks) != 1 || checks[0].Answer || checks[0].AskedAt.IsZero() || checks[0].RanAt.IsZero() {
		t.Errorf("checks = %+v", checks)
	}
	eq(t, "events", eventNames(t, s, "alpha"), []string{"mode", "check", "check"})
}

func TestRecordCheckAddsAfterAgentAsks(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("ci main green"), Asks: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "ci main green", true); err != nil {
		t.Fatal(err)
	}
	asks := get(t, s, "alpha").Asks
	eq(t, "positions", texts(asks)[:2], []string{"a", "b"})
	eq(t, "check ask number", asks[2].N, 3)
	eq(t, "check ask text", asks[2].Text, `Check "ci main green" is now yes. Recommend: open the track.`)
}

func TestRecordCheckRefusesChangedCardAndSkipsParked(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 9 merged", true); !errors.Is(err, ErrCardChanged) {
		t.Errorf("stale check text = %v, want ErrCardChanged", err)
	}
	if err := s.SetMode("alpha", "parked"); err != nil {
		t.Fatal(err)
	}
	res, err := s.RecordCheck("alpha", "pr 4 merged", true)
	if err != nil || res.Fired {
		t.Errorf("parked: fired=%v err=%v", res.Fired, err)
	}
	if checks, _ := s.Checks("alpha"); len(checks) != 0 {
		t.Errorf("parked track recorded a check: %+v", checks)
	}
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 4 merged", true); !errors.Is(err, ErrClosed) {
		t.Errorf("closed track = %v, want ErrClosed", err)
	}
}

func TestApplyCheckAsk(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged"), Asks: []string{"agent"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("alpha", "waiting"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 4 merged", true); err != nil {
		t.Fatal(err)
	}
	asks := get(t, s, "alpha").Asks

	if ok, err := s.ApplyCheckAsk(asks[0].ID); err != nil || ok {
		t.Errorf("applying an agent ask: ok=%v err=%v, want a no-op", ok, err)
	}
	ok, err := s.ApplyCheckAsk(asks[1].ID)
	if err != nil || !ok {
		t.Fatalf("apply: ok=%v err=%v", ok, err)
	}
	v := get(t, s, "alpha")
	eq(t, "mode", v.Track.Mode, "waiting")
	eq(t, "open asks", texts(v.Asks), []string{"agent"})
	var closedBy string
	s.db.QueryRow(`SELECT closed_by FROM asks WHERE id = ?`, asks[1].ID).Scan(&closedBy)
	eq(t, "closed_by", closedBy, "applied")
	eq(t, "events", eventNames(t, s, "alpha"), []string{"mode", "check", "answer"})

	if ok, err := s.ApplyCheckAsk(asks[1].ID); err != nil || ok {
		t.Errorf("second apply: ok=%v err=%v, want a no-op", ok, err)
	}
	if ok, err := s.ApplyCheckAsk(9999); err != nil || ok {
		t.Errorf("unknown ask: ok=%v err=%v, want a no-op", ok, err)
	}
}

func TestApplyCheckAskThatRecommendsOnlyOpening(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if _, err := s.Card("alpha", CardUpdate{Check: ptr("pr 4 merged")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("alpha", "background"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordCheck("alpha", "pr 4 merged", true); err != nil {
		t.Fatal(err)
	}
	ask := get(t, s, "alpha").Asks[0]
	if ok, err := s.ApplyCheckAsk(ask.ID); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	eq(t, "mode unchanged", get(t, s, "alpha").Track.Mode, "background")
}

func TestAnswerSentStampsTheAcceptedAsks(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	res, err := s.Card("alpha", CardUpdate{Asks: []string{"one", "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerSent("alpha", []int64{res.Asks[1].ID}, Event{RexSession: "session:r", RexBlock: "b1", Len: 3, Kind: "answers"}); err != nil {
		t.Fatal(err)
	}
	asks := get(t, s, "alpha").Asks
	if !asks[0].AnsweredAt.IsZero() || asks[1].AnsweredAt.IsZero() {
		t.Errorf("answered_at = %v, %v; want only the second set", asks[0].AnsweredAt, asks[1].AnsweredAt)
	}
	if asks[0].SeenAt.IsZero() || asks[1].SeenAt.IsZero() {
		t.Error("an answer event should mark the open asks seen")
	}
	eq(t, "events", eventNames(t, s, "alpha"), []string{"answer"})
	evs, _ := s.Events("alpha")
	if e := evs[0]; e.RexSession != "session:r" || e.RexBlock != "b1" || e.Len != 3 || e.Kind != "answers" {
		t.Errorf("answer event lost its fields: %+v", e)
	}

	// A later answer that accepts the first ask describes a new answer: the second loses its stamp.
	if err := s.AnswerSent("alpha", []int64{res.Asks[0].ID}, Event{}); err != nil {
		t.Fatal(err)
	}
	asks = get(t, s, "alpha").Asks
	if asks[0].AnsweredAt.IsZero() || !asks[1].AnsweredAt.IsZero() {
		t.Errorf("after the second answer: answered_at = %v, %v; want only the first set", asks[0].AnsweredAt, asks[1].AnsweredAt)
	}
}

func TestSeenAtSetByOpenAnswerAndUserPromptEventsOnly(t *testing.T) {
	cases := []struct {
		event HookEvent
		open  string
		seen  bool
	}{
		{HookEvent{Event: "prompt", Kind: "user", RexSession: "s"}, "", true},
		{HookEvent{Event: "prompt", Kind: "system", RexSession: "s"}, "", false},
		{HookEvent{Event: "stop", RexSession: "s"}, "", false},
		{HookEvent{Event: "notify", RexSession: "s"}, "", false},
		{HookEvent{}, "open", true},
	}
	for _, c := range cases {
		s := newStore(t)
		if _, err := s.Bind("alpha", "s", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Card("alpha", CardUpdate{Asks: []string{"q"}}); err != nil {
			t.Fatal(err)
		}
		if c.open != "" {
			if err := s.LogEvent("alpha", Event{Event: c.open}); err != nil {
				t.Fatal(err)
			}
		} else if err := s.Hook(c.event); err != nil {
			t.Fatal(err)
		}
		if got := !get(t, s, "alpha").Asks[0].SeenAt.IsZero(); got != c.seen {
			t.Errorf("%s/%s/%s: seen = %v, want %v", c.event.Event, c.event.Kind, c.open, got, c.seen)
		}
	}
}

func TestSeenAtIgnoresAsksCreatedAfterTheEvent(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if err := s.LogEvent("alpha", Event{Event: "open"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Card("alpha", CardUpdate{Asks: []string{"later"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Asks[0].SeenAt.IsZero() {
		t.Error("an ask created after the open event is seen")
	}
}

func TestHookAnsweredStampAndSession(t *testing.T) {
	s := newStore(t)
	if _, err := s.Bind("alpha", "rex:1", ""); err != nil {
		t.Fatal(err)
	}

	prompt := HookEvent{Event: "prompt", Kind: "user", Len: 12, RexSession: "rex:1", RexBlock: "b1", Cwd: "/w", ClaudeSession: "claude-1", ClaudeCwd: "/w"}
	if err := s.Hook(prompt); err != nil {
		t.Fatal(err)
	}
	tr := get(t, s, "alpha").Track
	if !tr.AnsweredAt.IsZero() {
		t.Error("answered stamp set with no open asks")
	}
	eq(t, "claude_session", tr.ClaudeSession, "claude-1")
	eq(t, "claude_cwd", tr.ClaudeCwd, "/w")

	if _, err := s.Card("alpha", CardUpdate{Asks: []string{"q"}}); err != nil {
		t.Fatal(err)
	}
	system := prompt
	system.Kind = "system"
	if err := s.Hook(system); err != nil {
		t.Fatal(err)
	}
	if !get(t, s, "alpha").Track.AnsweredAt.IsZero() {
		t.Error("a system prompt marked the track answered")
	}
	if err := s.Hook(prompt); err != nil {
		t.Fatal(err)
	}
	if get(t, s, "alpha").Track.AnsweredAt.IsZero() {
		t.Fatal("a prompt over open asks did not mark the track answered")
	}

	if err := s.Hook(HookEvent{Event: "stop", RexSession: "rex:1"}); err != nil {
		t.Fatal(err)
	}
	if !get(t, s, "alpha").Track.AnsweredAt.IsZero() {
		t.Error("stop did not clear the answered stamp")
	}

	if err := s.Hook(prompt); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Card("alpha", CardUpdate{})
	if err := s.AnswerSent("alpha", []int64{res.Asks[0].ID}, Event{}); err != nil {
		t.Fatal(err)
	}
	before := eventNames(t, s, "alpha")
	if err := s.Hook(HookEvent{Event: "session_start", RexSession: "rex:1"}); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "alpha")
	if !v.Track.AnsweredAt.IsZero() || !v.Asks[0].AnsweredAt.IsZero() {
		t.Error("session_start did not clear the answered stamps")
	}
	eq(t, "session_start logs no event", eventNames(t, s, "alpha"), before)

	evs, _ := s.Events("alpha")
	first := evs[0]
	if first.Event != "prompt" || first.Len != 12 || first.RexBlock != "b1" || first.Cwd != "/w" || first.Kind != "user" || first.TrackName != "alpha" {
		t.Errorf("first event = %+v", first)
	}
}

func TestHookWithoutATrackStillLogsTheEvent(t *testing.T) {
	s := newStore(t)
	if err := s.Hook(HookEvent{Event: "prompt", Kind: "user", RexSession: "nobody", Len: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Hook(HookEvent{Event: "stop"}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM events WHERE track_id IS NULL`).Scan(&n)
	eq(t, "events without a track", n, 2)
}

func TestHookIgnoresClosedTracks(t *testing.T) {
	s := newStore(t)
	if _, err := s.Bind("alpha", "rex:1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseTrack("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.Hook(HookEvent{Event: "prompt", Kind: "user", RexSession: "rex:1", ClaudeSession: "c"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "claude_session on closed", get(t, s, "alpha").Track.ClaudeSession, "")
}

func TestSetSession(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	if err := s.SetSession("alpha", "rex:7", "claude-7", "/cwd"); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "alpha").Track.Version
	if err := s.SetSession("alpha", "", "", ""); err != nil {
		t.Fatal(err)
	}
	tr := get(t, s, "alpha").Track
	eq(t, "rex_session", tr.RexSession, "rex:7")
	eq(t, "claude_session", tr.ClaudeSession, "claude-7")
	eq(t, "claude_cwd", tr.ClaudeCwd, "/cwd")
	eq(t, "no write for no change", tr.Version, v)
}
