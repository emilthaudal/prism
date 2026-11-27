package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const milestonesUsage = "usage: tracks milestones NAME [--json] | milestones NAME set TITLE... | milestones NAME tick N|TITLE | milestones NAME untick N|TITLE"

type milestoneJSON struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
	At    string `json:"reached_at,omitempty"`
}

func milestonesDoc(list []store.Milestone) []milestoneJSON {
	doc := []milestoneJSON{}
	for i, m := range list {
		d := milestoneJSON{N: i + 1, Title: m.Title, Done: m.Done()}
		if m.Done() {
			d.At = m.ReachedAt.UTC().Format(time.RFC3339)
		}
		doc = append(doc, d)
	}
	return doc
}

func printMilestones(list []store.Milestone, out io.Writer) {
	if len(list) == 0 {
		fmt.Fprintln(out, "no milestones")
	}
	for i, m := range list {
		box := "[ ]"
		if m.Done() {
			box = "[x]"
		}
		fmt.Fprintf(out, "%d. %s %s\n", i+1, box, m.Title)
	}
}

// milestonesCmd is a track's own list of steps to its goal: the track's agent sets the whole
// ordered list, ticks one as reached, unticks it, and lists them.
func milestonesCmd(args []string, out io.Writer) error {
	if !storeOn() {
		return errors.New("tracks milestones " + needsStore)
	}
	if len(args) == 0 || !validName.MatchString(args[0]) {
		return errors.New(milestonesUsage)
	}
	name, rest := args[0], args[1:]
	explain := func(err error) error {
		switch {
		case errors.Is(err, store.ErrNoTrack):
			return fmt.Errorf("no card for %s: run tracks bind %s first", name, name)
		case errors.Is(err, store.ErrClosed):
			return fmt.Errorf("%s is closed", name)
		}
		return fmt.Errorf("refused: %w", err)
	}
	switch {
	case len(rest) == 0 || len(rest) == 1 && rest[0] == "--json":
		var list []store.Milestone
		if err := withBareStore(func(st *store.Store) (err error) { list, err = st.Milestones(name); return err }); err != nil {
			return explain(err)
		}
		if len(rest) == 1 {
			b, err := json.MarshalIndent(map[string]any{"milestones": milestonesDoc(list)}, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s\n", b)
			return err
		}
		printMilestones(list, out)
		return nil
	case rest[0] == "set":
		var list []store.Milestone
		if err := withStore(func(st *store.Store) (err error) { list, err = st.SetMilestones(name, rest[1:]); return err }); err != nil {
			return explain(err)
		}
		printMilestones(list, out)
		return nil
	case (rest[0] == "tick" || rest[0] == "untick") && len(rest) == 2:
		var (
			m store.Milestone
			n int
		)
		done := rest[0] == "tick"
		if err := withStore(func(st *store.Store) (err error) { m, n, err = st.MarkMilestone(name, rest[1], done); return err }); err != nil {
			return explain(err)
		}
		if done {
			fmt.Fprintf(out, "reached %d. %s\n", n, m.Title)
		} else {
			fmt.Fprintf(out, "not reached %d. %s\n", n, m.Title)
		}
		return nil
	}
	return errors.New(milestonesUsage)
}
