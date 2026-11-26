package tracks

import (
	"fmt"
	"slices"
	"strings"

	"github.com/emilthaudal/prism/internal/store"
)

// focusFullError is a refusal to put a third track in focus (EMI-423, Emil's D148). Focus is
// Emil's and holds maxFocus tracks: the one asking must say which of them moves to background.
type focusFullError struct {
	track string
	focus []string // the tracks in focus, older first
}

func (e *focusFullError) Error() string {
	if e.track == "" { // a start: there is no track yet
		return fmt.Sprintf("refused: focus is full with %s: start the track in background and ask Emil which of them moves, then run: tracks mode NAME focus --swap FOCUS_TRACK",
			strings.Join(e.focus, " and "))
	}
	return fmt.Sprintf("refused: focus is full with %s: moving %s to focus needs one of them in background. Ask Emil which, then run: tracks mode %s focus --swap NAME",
		strings.Join(e.focus, " and "), e.track, e.track)
}

// openFocus is the tracks in focus other than except, older first. A thread sits under its
// lead and does not count.
func openFocus(except string) ([]string, error) {
	var names []string
	if storeOn() {
		vs, err := liveViews()
		if err != nil {
			return nil, err
		}
		slices.SortFunc(vs, func(a, b store.View) int { return int(a.Track.ID - b.Track.ID) }) // older first
		for _, v := range vs {
			if t := v.Track; t.Mode == "focus" && t.Parent() == "" && t.Name != except {
				names = append(names, t.Name)
			}
		}
		return names, nil
	}
	for _, c := range loadCards() {
		if c.mode() == "focus" && c.Meta["parent"] == "" && c.Name != except {
			names = append(names, c.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// checkFocusRoom is nil when name may go to focus without a swap: it is there already, or
// focus has room.
func checkFocusRoom(name string) error {
	others, err := openFocus(name)
	if err != nil {
		return err
	}
	if len(others) >= maxFocus {
		return &focusFullError{track: name, focus: others}
	}
	return nil
}

// setModeChecked is setMode with focus capped at maxFocus. A third focus track is refused with
// a focusFullError, unless swap names one of the focus tracks, which then moves to background.
func setModeChecked(name, mode, swap string) error {
	if mode != "focus" {
		return setMode(name, mode)
	}
	others, err := openFocus(name)
	if err != nil {
		return err
	}
	if len(others) < maxFocus {
		return setMode(name, mode)
	}
	if swap == "" {
		return &focusFullError{track: name, focus: others}
	}
	if !slices.Contains(others, swap) {
		return fmt.Errorf("refused: %s is not in focus, so it cannot make room: focus holds %s", swap, strings.Join(others, " and "))
	}
	if err := setMode(swap, "background"); err != nil {
		return err
	}
	if err := setMode(name, mode); err != nil {
		return fmt.Errorf("moved %s to background, but could not put %s in focus: %w", swap, name, err)
	}
	return nil
}
