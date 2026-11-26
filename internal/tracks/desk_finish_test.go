package tracks

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestPrismReopensATrackTheDeskClosedFromTheHandledLog(t *testing.T) {
	deskStarted(t)
	if out, code := do("desk", "finish", "cleanup", "merged worktree-cleanup into main"); code != 0 {
		t.Fatalf("finish: %d %q", code, out)
	}
	_, c := serveHub(t, newLiveSource(newDeskRunner(io.Discard)))
	id := "e" + strconv.FormatInt(lastEntry(t).ID, 10)
	var h apiHandled
	for _, e := range c.snapshot().Handled {
		if e.ID == id {
			h = e
		}
	}
	if h.Kind != "closed" || h.Undone {
		t.Fatalf("handled entry %+v", h)
	}
	code, v := c.post("/v1/handled/"+id+"/undo", "")
	if code != 200 || v["status"] != "Reopened cleanup: its card is back, enter opens it" {
		t.Fatalf("undo: %d %v", code, v)
	}
	if v, err := withView("cleanup"); err != nil || v.Closed {
		t.Errorf("cleanup is still closed: %v", err)
	}
	if code, _ := c.post("/v1/handled/"+id+"/undo", ""); code != http.StatusConflict {
		t.Errorf("a second undo: %d", code)
	}
	if code, _ := c.post("/v1/handled/e99999/undo", ""); code != http.StatusNotFound {
		t.Errorf("unknown entry: %d", code)
	}
	for _, e := range c.snapshot().Handled {
		if e.ID == id && !e.Undone {
			t.Errorf("the snapshot still shows the close as standing: %+v", e)
		}
	}
}

func TestTheDeskFinishesItsOwnTrackAndEmilReopensIt(t *testing.T) {
	deskStarted(t)
	out, code := do("desk", "finish", "cleanup", "merged worktree-cleanup into main")
	if code != 0 || !strings.HasPrefix(out, "Closed cleanup") {
		t.Fatalf("finish: %d %q", code, out)
	}
	if v, err := withView("cleanup"); err != nil || !v.Closed {
		t.Fatalf("cleanup is not closed: %v %+v", err, v.Track)
	}
	e := lastEntry(t)
	if e.Kind != "closed" || e.Track != "cleanup" || e.Did != "merged worktree-cleanup into main" {
		t.Fatalf("logged %+v", e)
	}
	if got := undoEntry(e, ""); got != "Reopened cleanup: its card is back, enter opens it" {
		t.Fatalf("undo: %q", got)
	}
	if v, err := withView("cleanup"); err != nil || v.Closed {
		t.Errorf("cleanup is still closed: %v %+v", err, v.Track)
	}
	if !exists(cardPath("cleanup")) {
		t.Error("the reopened track has no card file")
	}
	if again := undoEntry(lastEntry(t), ""); again != "That was taken back already" {
		t.Errorf("a second undo: %q", again)
	}
	// Reopened, it is Emil's: the desk doesn't close it again.
	if out, code := do("desk", "finish", "cleanup", "done after all"); code == 0 || !strings.Contains(out, "Emil reopened cleanup") {
		t.Errorf("finish again: %d %q", code, out)
	}
}

func TestTheDeskDoesNotFinishATrackThatIsNotDone(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, id string)
		want string
	}{
		{"Emil took it over", func(t *testing.T, id string) {
			bgHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "prompt": "I'll take this one"})
		}, "cleanup is Emil's"},
		{"it has an open ask", func(t *testing.T, _ string) {
			if out, code := cardIn("asks:\nMerge it? Recommend: yes\n", "card", "cleanup"); code != 0 {
				t.Fatal(out)
			}
		}, "cleanup has 1 ask open"},
		{"it waits on a check", func(t *testing.T, _ string) {
			if out, code := cardIn("check: pr 12 merged\n", "card", "cleanup"); code != 0 {
				t.Fatal(out)
			}
		}, "cleanup waits on check: pr 12 merged"},
		{"no outcome", nil, "the outcome is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, _ := deskStarted(t)
			if c.prep != nil {
				c.prep(t, id)
			}
			outcome := "merged it"
			if c.prep == nil {
				outcome = " "
			}
			out, code := do("desk", "finish", "cleanup", outcome)
			if code == 0 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q, want %q", code, out, c.want)
			}
			if v, err := withView("cleanup"); err != nil || v.Closed {
				t.Errorf("a refused finish closed it: %v", err)
			}
		})
	}
}

func TestOnlyTheDeskFinishesATrack(t *testing.T) {
	deskStarted(t)
	deskAgentIs(t, 999999, nil)
	if out, code := do("desk", "finish", "cleanup", "merged it"); code == 0 || !strings.Contains(out, "does not run under the desk's agent") {
		t.Errorf("%d %s", code, out)
	}
}

// A track Emil started never ran from the desk, so the desk leaves it to him.
func TestTheDeskDoesNotFinishEmilsTrack(t *testing.T) {
	actsHome(t, "2: product code")
	asDesk(t)
	if out, code := do("desk", "finish", "solo", "merged it"); code == 0 || !strings.Contains(out, "not the desk's track") {
		t.Errorf("%d %s", code, out)
	}
}
