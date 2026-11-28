package tracks

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

// milestoneStates is a faked gh: the state each PR ref is in, or an error.
type milestoneStates map[string]string

func (m milestoneStates) eval(_ string, c check) (string, error) {
	if c.want != "state" {
		return no, nil
	}
	s, ok := m[c.arg]
	if !ok || s == "error" {
		return "", errors.New("gh: HTTP 502")
	}
	return s, nil
}

func milestoneTrack(t *testing.T) (id, bin string) {
	t.Helper()
	id, bin = deskStarted(t)
	if out, code := do("milestones", "cleanup", "set", "api change", "client change", "docs"); code != 0 {
		t.Fatal(out)
	}
	for i, pr := range []string{"acme/api#4", "acme/api#9", "acme/web#2"} {
		if out, code := do("milestones", "cleanup", "pr", string(rune('1'+i)), pr); code != 0 {
			t.Fatal(out)
		}
	}
	return id, bin
}

func milestoneList(t *testing.T) []store.Milestone {
	t.Helper()
	var l []store.Milestone
	if err := withBareStore(func(st *store.Store) (err error) { l, err = st.Milestones("cleanup"); return err }); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMilestonePRRefsSetClearAndList(t *testing.T) {
	milestoneTrack(t)
	out, _ := do("milestones", "cleanup")
	if !strings.Contains(out, "1. [ ] api change  (acme/api#4)") {
		t.Errorf("list: %s", out)
	}
	if out, _ := do("milestones", "cleanup", "--json"); !strings.Contains(out, `"pr": "acme/web#2"`) {
		t.Errorf("json: %s", out)
	}
	if out, code := do("milestones", "cleanup", "pr", "1", "not a ref"); code == 0 {
		t.Errorf("bad ref accepted: %s", out)
	}
	if out, code := do("milestones", "cleanup", "unpr", "docs"); code != 0 {
		t.Fatal(out)
	}
	if l := milestoneList(t); l[2].PR != "" {
		t.Errorf("cleared: %+v", l[2])
	}
	// Rewriting the list keeps the PR of a title that stays.
	do("milestones", "cleanup", "set", "client change", "api change")
	if l := milestoneList(t); l[0].PR != "acme/api#9" || l[1].PR != "acme/api#4" {
		t.Errorf("after set: %+v", l)
	}
}

func TestCheckRefreshesMilestonePRsTicksMergedAndWakesOnChange(t *testing.T) {
	id, bin := milestoneTrack(t)
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "PRs are up."})
	gh := milestoneStates{"acme/api#4": "open", "acme/api#9": "open", "acme/web#2": "open"}

	// First look records open silently.
	out, errs := checks(t, false, gh.eval)
	if errs != "" || strings.Contains(out, "woke") || exists(filepath.Join(bin, "claude.notes")) {
		t.Fatalf("first look: %q %q", out, errs)
	}
	if l := milestoneList(t); l[0].PRState != "open" || l[0].Done() {
		t.Fatalf("recorded: %+v", l)
	}

	// Unchanged wakes nobody.
	out, _ = checks(t, false, gh.eval)
	if strings.Count(out, "unchanged") != 3 || exists(filepath.Join(bin, "claude.notes")) {
		t.Fatalf("unchanged: %q", out)
	}

	// A gh error ticks nothing and leaves the state.
	gh["acme/api#4"] = "error"
	out, errs = checks(t, false, gh.eval)
	if !strings.Contains(errs, "acme/api#4") || milestoneList(t)[0].Done() || milestoneList(t)[0].PRState != "open" || exists(filepath.Join(bin, "claude.notes")) {
		t.Fatalf("gh error: %q %q %+v", out, errs, milestoneList(t))
	}

	// open to merged ticks the milestone and wakes the session.
	gh["acme/api#4"] = "merged"
	out, errs = checks(t, false, gh.eval)
	l := milestoneList(t)
	if errs != "" || !strings.Contains(out, "acme/api#4 is merged, woke its session") || !l[0].Done() || l[0].PRState != "merged" || l[1].Done() {
		t.Fatalf("merged: %q %q %+v", out, errs, l)
	}
	if n := fileText(t, filepath.Join(bin, "claude.notes")); n != "[milestone] 1. api change: acme/api#4 merged, ticked\n<end>\n" {
		t.Errorf("delivered %q", n)
	}

	// Another change wakes too; the ticked milestone is not asked about again.
	bgHook(t, map[string]any{"hook_event_name": "Stop", "session_id": id, "last_assistant_message": "Noted."})
	gh["acme/web#2"] = "approved"
	gh["acme/api#4"] = "error"
	out, errs = checks(t, false, gh.eval)
	if errs != "" || !strings.Contains(out, "acme/web#2 is approved, woke its session") || strings.Contains(out, "acme/api#4") {
		t.Fatalf("approved: %q %q", out, errs)
	}
}

func TestDryCheckWritesNoMilestoneState(t *testing.T) {
	milestoneTrack(t)
	gh := milestoneStates{"acme/api#4": "merged", "acme/api#9": "open", "acme/web#2": "open"}
	out, _ := checks(t, true, gh.eval)
	if !strings.Contains(out, "would record it") {
		t.Errorf("dry: %q", out)
	}
	if l := milestoneList(t); l[0].Done() || l[0].PRState != "" {
		t.Errorf("dry wrote: %+v", l[0])
	}
}

func TestPRStateFromGhJSON(t *testing.T) {
	cases := []struct {
		state, review string
		rollup        []rollupItem
		want          string
	}{
		{"MERGED", "APPROVED", nil, "merged"},
		{"CLOSED", "", nil, "closed"},
		{"OPEN", "APPROVED", nil, "approved"},
		{"OPEN", "", []rollupItem{{Status: "COMPLETED", Conclusion: "SUCCESS", Name: "build"}}, "green"},
		{"OPEN", "", []rollupItem{{Status: "COMPLETED", Conclusion: "FAILURE", Name: "build"}}, "open"},
		{"OPEN", "", nil, "open"},
		{"OPEN", "", []rollupItem{{Status: "IN_PROGRESS", Name: "build"}}, ""},
		{"OPEN", "APPROVED", []rollupItem{{Status: "IN_PROGRESS", Name: "build"}}, "approved"},
	}
	for _, c := range cases {
		if got := prState(c.state, c.review, c.rollup); got != c.want {
			t.Errorf("%v: %s", c, got)
		}
	}
}
