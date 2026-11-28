package tracks

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/emilthaudal/prism/internal/store"
)

func milestoneNote(w store.WatchedMilestone, old, state string) string {
	if state == store.PRMerged {
		return fmt.Sprintf("[milestone] %d. %s: %s merged, ticked", w.Position, w.Title, w.PR)
	}
	return fmt.Sprintf("[milestone] %d. %s: %s is now %s (was %s)", w.Position, w.Title, w.PR, state, old)
}

// refreshMilestones asks gh for the state of every unreached milestone's PR, the way a pr check
// does, and records it. A merged PR ticks its milestone; a state change wakes the track's desk
// session. A gh error leaves the milestone as it is.
func refreshMilestones(st *store.Store, dry bool, eval evaluator, out, errw io.Writer) {
	list, err := st.WatchedMilestones()
	if err != nil {
		fmt.Fprintf(errw, "milestones: %v\n", err)
		return
	}
	for _, w := range list {
		chk, ok := parseCheck("pr " + w.PR + " merged")
		if !ok {
			fmt.Fprintf(errw, "%s: milestone %d: unknown PR ref %q, not run\n", w.Track, w.Position, w.PR)
			continue
		}
		chk.want = "state"
		dir := w.Dir
		if !filepath.IsAbs(dir) {
			dir = home()
		}
		state, err := eval(dir, chk)
		if err != nil {
			fmt.Fprintf(errw, "%s: milestone %d %q: %s: %v\n", w.Track, w.Position, w.Title, w.PR, err)
			continue
		}
		if state == "" {
			fmt.Fprintf(out, "%s: milestone %d %s checks still running, kept\n", w.Track, w.Position, w.PR)
			continue
		}
		if state == w.PRState {
			fmt.Fprintf(out, "%s: milestone %d %s is %s, unchanged\n", w.Track, w.Position, w.PR, state)
			continue
		}
		if dry {
			fmt.Fprintf(out, "%s: milestone %d %s is %s, would record it\n", w.Track, w.Position, w.PR, state)
			continue
		}
		res, err := st.RecordMilestonePR(w.TrackID, w.Position, w.PR, state)
		switch {
		case err != nil:
			fmt.Fprintf(errw, "%s: milestone %d: %v\n", w.Track, w.Position, err)
		case res.Wake != "":
			fmt.Fprintf(out, "%s: milestone %d %s is %s, %s\n", w.Track, w.Position, w.PR, state, wakeCheck(w.Track, res.Wake, milestoneNote(w, res.Old, state), errw))
		case res.Ticked:
			fmt.Fprintf(out, "%s: milestone %d %s is merged, reached\n", w.Track, w.Position, w.PR)
		default:
			fmt.Fprintf(out, "%s: milestone %d %s is %s\n", w.Track, w.Position, w.PR, state)
		}
	}
}
