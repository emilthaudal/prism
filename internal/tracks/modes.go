package tracks

import "github.com/emilthaudal/prism/internal/store"

// effectiveMode is the mode a track is shown in (EMI-423). Emil sets focus, background or
// parked; waiting is computed: a background track with a check: or waiting_on:, no open ask
// and no session working on it. A stored waiting reads as background. A focus track that
// waits stays in focus, because Emil chose it for today: its row says what it waits for.
func effectiveMode(c *Card, working bool) string {
	return shownMode(c.mode(), waits(c, working))
}

// shownMode is the mode shown for a stored mode, given whether the track waits.
func shownMode(stored string, waiting bool) string {
	switch stored {
	case "focus", "parked":
		return stored
	}
	if waiting {
		return "waiting"
	}
	return "background"
}

// waits is whether the track has something to wait on and nothing to do: a check or
// waiting_on, no open ask, and no session working on it.
func waits(c *Card, working bool) bool {
	return idle(c.Meta["check"], c.Meta["waiting_on"], len(c.askList()), working)
}

func idle(check, waitingOn string, openAsks int, working bool) bool {
	return (check != "" || waitingOn != "") && openAsks == 0 && !working
}

// workingTracks is the names of the open tracks a session is working on, by the store's
// sessions. A store that cannot say reads as none working.
func workingTracks() map[string]bool {
	out := map[string]bool{}
	_ = withStore(func(st *store.Store) error {
		ss, err := st.Sessions()
		if err != nil {
			return err
		}
		for _, s := range ss {
			if s.Track != "" && s.Status == "working" && s.Host != "stopped" {
				out[s.Track] = true
			}
		}
		return nil
	})
	return out
}

// markWorking sets Working on the cards whose track a session is working on.
func markWorking(cards []*Card) {
	w := workingTracks()
	for _, c := range cards {
		c.Working = w[c.Name]
	}
}
