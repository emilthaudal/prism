package tracks

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"
)

const (
	deskIdleEnv     = "TRACKS_DESK_IDLE"
	defaultDeskIdle = 30 * time.Minute
	// staleCheckAfter is how long an idle agent may hold a card check before it counts as stalled.
	staleCheckAfter = 2 * time.Hour

	stallIdle    = "idle"
	stallBlocked = "blocked"
	stallError   = "error"
	stallExited  = "exited"
)

// Stall is a track that needs the desk's attention: Reason is one of idle, blocked, error or
// exited, Detail says it in a line and Since is when Rex last reported the agent's state.
type Stall struct {
	Track  string  `json:"track"`
	Reason string  `json:"reason"`
	Detail string  `json:"detail"`
	Since  *string `json:"since"`
}

func (s Stall) line() string { return s.Track + " stalled: " + s.Detail }

// stallProbe is what the detector knows about one open track: its card and what Rex last said
// about its agent. Asks counts the asks on its card, which are already in front of Emil.
type stallProbe struct {
	Name, Mode       string
	Asks             int
	CheckStuck       string // why the card's check cannot wake it: `"…" keeps failing` or `"…" is already yes`
	Session          bool   // the card names a Rex session
	Answered         bool   // Rex has answered about it, so an empty State means no agent
	State, Kind, Msg string
	At               time.Time
}

// deskIdle is how long an agent may sit idle with no ask before it counts as stalled: the flag,
// else $TRACKS_DESK_IDLE, else half an hour.
func deskIdle(fl map[string]string) (time.Duration, error) {
	if d, err := durationFlag(fl, "--idle"); d > 0 || err != nil {
		return d, err
	}
	v := os.Getenv(deskIdleEnv)
	if v == "" {
		return defaultDeskIdle, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a duration such as 45m: %q", deskIdleEnv, v)
	}
	return d, nil
}

// detectStalls names the probes that have stalled, in the order given. A parked track is
// left alone, a waiting one may sit idle, and a track Rex has not answered about is not
// judged. A blocked agent stalls only at a permission prompt: a question is an ask for Emil.
func detectStalls(now time.Time, idleAfter time.Duration, probes []stallProbe) []Stall {
	var out []Stall
	for _, p := range probes {
		if p.Mode == "parked" || !p.Session || !p.Answered {
			continue
		}
		st := Stall{Track: p.Name}
		if !p.At.IsZero() {
			at := p.At.UTC().Format(time.RFC3339)
			st.Since = &at
		}
		switch {
		case p.CheckStuck != "" && (p.State == "idle" || p.State == "done") && p.Asks == 0 && !p.At.IsZero() && now.Sub(p.At) > staleCheckAfter:
			// The agent stopped on a check that cannot wake it (EMI-439). A waiting track is
			// meant to sit idle, but not on a check that fails or is already true.
			st.Reason, st.Detail = stallIdle, "idle "+shortDur(now.Sub(p.At))+", its check "+p.CheckStuck
		case p.State == "":
			st.Reason, st.Detail, st.Since = stallExited, "its agent has exited and the card is still open", nil
		case p.State == "blocked" && p.Kind == "permission":
			st.Reason, st.Detail = stallBlocked, "blocked on a permission prompt"
		case p.State == "error":
			st.Reason, st.Detail = stallError, "error: "+cmp.Or(oneLine(p.Msg), "no message")
		case (p.State == "idle" || p.State == "done") && p.Asks == 0 && p.Mode != "waiting" && !p.At.IsZero() && now.Sub(p.At) > idleAfter:
			st.Reason, st.Detail = stallIdle, "idle "+shortDur(now.Sub(p.At))+" with no ask"
		default:
			continue
		}
		out = append(out, st)
	}
	return out
}

// probesOf joins the open cards with what the last poll saw of their agents.
func probesOf(cards []*Card, seen map[string]*seenState) []stallProbe {
	var out []stallProbe
	fails := loadCheckErrors()
	answers, _ := storeChecks() // best effort: without it only failing checks are judged
	for _, c := range cards {
		s := seen[c.Name]
		if s == nil {
			continue
		}
		stuck := ""
		if text := c.Meta["check"]; text != "" {
			key := c.Name + ": " + text
			if fails[key].Count >= checkErrorLimit {
				stuck = fmt.Sprintf("%q keeps failing", text)
			} else if answers[key].Answer {
				stuck = fmt.Sprintf("%q is already yes", text)
			}
		}
		out = append(out, stallProbe{
			Name: c.Name, Mode: c.mode(), Asks: c.asks(), CheckStuck: stuck, Session: c.Meta["rex_session"] != "", Answered: s.answered,
			State: s.state, Kind: s.kind, Msg: s.msg, At: s.at,
		})
	}
	return out
}

// openStalls reads every open track once, asks Rex about each agent and returns the stalls.
func openStalls(now time.Time, idleAfter time.Duration) ([]Stall, error) {
	cards, err := openCards()
	if err != nil {
		return nil, err
	}
	seen := map[string]*seenState{}
	if _, err := pollCards(cards, seen, map[string]string{}, true); err != nil {
		return nil, err
	}
	return detectStalls(now, idleAfter, probesOf(cards, seen)), nil
}

// openCards are the cards of every open track.
func openCards() ([]*Card, error) {
	vs, err := liveViews()
	if err != nil {
		return nil, err
	}
	out := make([]*Card, 0, len(vs))
	for _, v := range vs {
		out = append(out, cardOf(v))
	}
	markWorking(out)
	return out, nil
}

// stallEvents is the lines for the stalls that are new since the last poll, and makes known
// the stalls of this one. A track that stops stalling and stalls again is new again. With
// report false (the first poll) nothing is printed: a stall that was there when wait began is
// in tracks desk --json.
func stallEvents(stalls []Stall, known map[string]string, report bool) []string {
	var lines []string
	next := map[string]string{}
	for _, s := range stalls {
		next[s.Track] = s.Reason
		if report && known[s.Track] != s.Reason {
			lines = append(lines, s.line())
		}
	}
	clear(known)
	for k, v := range next {
		known[k] = v
	}
	return lines
}

// waitAllCmd is tracks wait --all: wait over every open track. It prints a line per change as
// wait LEAD does and a line per new stall. Without --follow it returns after the first poll that
// prints; with it, it keeps watching from the same starting point, so nothing that changes
// between two polls is missed, and a failed poll is reported as a line rather than ending it.
func waitAllCmd(args []string, out, errw io.Writer) error {
	follow := slices.Contains(args, "--follow")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--follow" })
	pos, fl, err := leadFlags(args, "--timeout", "--idle")
	if err != nil || len(pos) != 0 {
		return errors.New(waitUsage)
	}
	timeout, err := durationFlag(fl, "--timeout")
	if err != nil {
		return err
	}
	idle, err := deskIdle(fl)
	if err != nil {
		return err
	}
	if !storeOn() {
		return errors.New("tracks wait " + needsStore)
	}
	start := time.Now()
	seen := map[string]*seenState{}
	blocks := map[string]string{}
	known := map[string]string{}
	first := true
	lastErr := ""
	failed := func(err error) {
		if msg := "tracks wait: " + oneLine(err.Error()) + ", still watching"; msg != lastErr {
			fmt.Fprintln(out, msg)
			lastErr = msg
		}
	}
	for {
		cards, cerr := openCards()
		switch {
		case cerr != nil && follow:
			failed(cerr)
		case cerr != nil: // an unreadable store says nothing about the tracks
		default:
			lines, err := pollCards(cards, seen, blocks, first)
			switch {
			case err != nil && !follow:
				return err
			case err != nil:
				failed(err)
			default:
				lastErr = ""
				stalls := detectStalls(time.Now(), idle, probesOf(cards, seen))
				lines = append(lines, stallEvents(stalls, known, !first)...)
				if ans, err := answeredLines(); err == nil {
					lines = append(lines, ans...)
				}
				first = false
				for _, l := range lines {
					fmt.Fprintln(out, l)
				}
				if len(lines) > 0 && !follow {
					return nil
				}
			}
		}
		if timeout > 0 && time.Since(start) >= timeout {
			if follow {
				fmt.Fprintf(errw, "tracks: stopped watching after %s\n", timeout)
			} else {
				fmt.Fprintf(errw, "tracks: no change in any track within %s\n", timeout)
			}
			return exitTimedOut
		}
		time.Sleep(waitPoll)
	}
}
