package tracks

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// checkedDeskTrack is the desk-started track cleanup waiting on PR 12's approval, with a check
// that answers what ans says.
func checkedDeskTrack(t *testing.T) (id, bin string, ans *string, eval evaluator) {
	t.Helper()
	id, bin = deskStarted(t)
	if out, code := cardIn("where: PR 12 waits on review\ncheck: pr 12 approved\n", "card", "cleanup"); code != 0 {
		t.Fatalf("card: %d %s", code, out)
	}
	a := no
	return id, bin, &a, func(string, check) (string, error) { return a, nil }
}

func cleanupAsks(t *testing.T) []store.Ask {
	t.Helper()
	v, err := getView("cleanup")
	if err != nil {
		t.Fatal(err)
	}
	return v.Asks
}

// EMI-431: a desk track whose PR gets approved is woken by the next check run, with a note in
// its session and no ask for Emil.
func TestApprovedCheckWakesAnIdleDeskSession(t *testing.T) {
	id, bin, ans, eval := checkedDeskTrack(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "PR 12 is in the queue."})
	checks(t, false, eval)
	*ans = yes
	out, errs := checks(t, false, eval)
	if !strings.Contains(out, `cleanup: "pr 12 approved" is yes, woke its session`) || errs != "" {
		t.Fatalf("check: %q %q", out, errs)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != "[check] pr 12 approved\n<end>\n" {
		t.Errorf("delivered %q", n)
	}
	if n := notes(t, id); len(n) != 1 || n[0].From != "system" || !n[0].Delivered {
		t.Errorf("notes %+v", n)
	}
	if a := cleanupAsks(t); len(a) != 0 {
		t.Errorf("Emil got an ask: %+v", a)
	}
	// The same answer again wakes nobody; a flip back says so.
	if out, _ := checks(t, false, eval); !strings.Contains(out, "unchanged") {
		t.Errorf("repeat: %q", out)
	}
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Merged."})
	*ans = no
	checks(t, false, eval)
	if n := fileText(t, filepath.Join(bin, "claude.notes")); !strings.HasSuffix(n, "[check] pr 12 approved: no longer true\n<end>\n") {
		t.Errorf("flip back delivered %q", n)
	}
}

// While the desk's session works, the note waits for it to stop, as tracks send's does.
func TestCheckNoteToAWorkingDeskSessionIsHeld(t *testing.T) {
	id, bin, ans, eval := checkedDeskTrack(t)
	*ans = yes
	if out, _ := checks(t, false, eval); !strings.Contains(out, "the note waits until its session stops") {
		t.Fatalf("check: %q", out)
	}
	if exists(filepath.Join(bin, "claude.calls")) {
		t.Errorf("woke a working session")
	}
	if n := notes(t, id); len(n) != 1 || n[0].Text != "[check] pr 12 approved" || !n[0].Held {
		t.Errorf("notes %+v", n)
	}
	if a := cleanupAsks(t); len(a) != 0 {
		t.Errorf("Emil got an ask: %+v", a)
	}
}

// A track whose session Emil took over keeps today's behaviour: the ask is his.
func TestCheckOnATrackEmilTookOverAsksEmil(t *testing.T) {
	id, bin, ans, eval := checkedDeskTrack(t)
	if err := withBareStore(func(st *store.Store) error { return st.SetOwner(id, "emil") }); err != nil {
		t.Fatal(err)
	}
	*ans = yes
	if out, _ := checks(t, false, eval); !strings.Contains(out, "added an ask") {
		t.Fatalf("check: %q", out)
	}
	if a := cleanupAsks(t); len(a) != 1 || !strings.HasPrefix(a[0].Text, `Check "pr 12 approved" is now yes.`) {
		t.Errorf("asks %+v", a)
	}
	if exists(filepath.Join(bin, "claude.notes")) || len(notes(t, id)) != 0 {
		t.Errorf("sent a note to Emil's session")
	}
}

// Prism's row shows the check and its last answer.
func TestSnapshotTrackCarriesItsCheck(t *testing.T) {
	_, _, ans, eval := checkedDeskTrack(t)
	snapCheck := func() *apiCheck {
		t.Helper()
		s, err := newLiveSource(nil).snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range s.(apiSnapshot).Tracks {
			if tr.Name == "cleanup" {
				return tr.Check
			}
		}
		t.Fatal("no cleanup in the snapshot")
		return nil
	}
	if c := snapCheck(); c == nil || c.Text != "pr 12 approved" || c.Answer != nil || c.RanAt != nil {
		t.Errorf("before a run: %+v", c)
	}
	*ans = yes
	checks(t, false, eval)
	if c := snapCheck(); c == nil || c.Answer == nil || *c.Answer != "yes" || c.RanAt == nil {
		t.Errorf("after a yes: %+v", c)
	}
}

// EMI-439: a check that fails three runs in a row asks, once, so a track is never left with
// nothing watching.
func TestFailingCheckAsksAfterThreeRuns(t *testing.T) {
	checkedDeskTrack(t)
	boom := func(string, check) (string, error) { return "", errors.New("not a git repository") }
	for i := 0; i < 5; i++ {
		checks(t, false, boom)
	}
	asks := cleanupAsks(t)
	if len(asks) != 1 || !strings.Contains(asks[0].Text, "failed 3 runs in a row") {
		t.Fatalf("asks %+v", asks)
	}
}

// EMI-439: a check that was already true when the session stopped wakes it, once per stop.
func TestAlreadyTrueCheckWakesAStoppedSession(t *testing.T) {
	id, _, ans, eval := checkedDeskTrack(t)
	*ans = yes
	checks(t, false, eval) // the first yes finds the session working: the note is held
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Waiting."})
	// The flip already sent its note, so stopping again must not wake it every run.
	for i := 0; i < 3; i++ {
		if out, _ := checks(t, false, eval); !strings.Contains(out, "unchanged") {
			t.Fatalf("run %d woke again: %q", i, out)
		}
	}
	if n := notes(t, id); len(n) != 1 {
		t.Errorf("notes %+v", n)
	}
}

func TestPRCheckNamesItsRepo(t *testing.T) {
	t.Setenv("TRACKS_GH_ACCOUNTS", "acme=me-at-acme, /Users/e/Developer/acme=me-at-acme")
	c, ok := parseCheck("pr acme/pantry#191 approved")
	if !ok || c != (check{"pr", "acme/pantry#191", "approved"}) {
		t.Fatalf("%+v %v", c, ok)
	}
	if got := strings.Join(c.ghArgs(), " "); got != "pr view 191 --repo acme/pantry --json state,reviewDecision,statusCheckRollup" {
		t.Errorf("args %q", got)
	}
	if ghAccountFor("/x", c, "") != "me-at-acme" || ghAccountFor("/Users/e/Developer/acme", check{"pr", "1", "green"}, "") != "me-at-acme" || ghAccountFor("/Users/e/Developer/acme-not", check{"pr", "1", "green"}, "") != "" || ghAccountFor("/Users/e/Developer/tracks", check{"pr", "1", "green"}, "") != "" {
		t.Error("accounts")
	}
	if _, ok := parseCheck("pr -x/y#1 approved"); ok {
		t.Error("leading dash accepted")
	}
}

func TestStaleCheckStalls(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	p := stallProbe{Name: "t", Mode: "waiting", Session: true, Answered: true, State: "idle", At: now.Add(-3 * time.Hour), CheckStuck: `"pr 1 green" is already yes`}
	got := detectStalls(now, 30*time.Minute, []stallProbe{p})
	if len(got) != 1 || !strings.Contains(got[0].Detail, "already yes") {
		t.Errorf("%+v", got)
	}
	p.CheckStuck = ""
	if got := detectStalls(now, 30*time.Minute, []stallProbe{p}); len(got) != 0 {
		t.Errorf("a healthy waiting track stalled: %+v", got)
	}
}
