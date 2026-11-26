package tracks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const readySchema = 1

// What a ready track waits on: Emil (it has asks), a check tracks runs, something outside
// tracks (waiting_on with no check), or nothing.
const (
	waitsEmil    = "emil"
	waitsCheck   = "check"
	waitsOutside = "waiting"
	waitsNothing = "nothing"
)

type readyDoc struct {
	Schema      int          `json:"schema"`
	GeneratedAt string       `json:"generated_at"`
	Tracks      []readyTrack `json:"tracks"`
}

type readyTrack struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Rank  *int   `json:"rank"`
	Today string `json:"today"`
	// BlockedBy is the tracks it waited on, all closed now.
	BlockedBy []string `json:"blocked_by"`
	WaitsOn   string   `json:"waits_on"`
	// Waiting says what: the asks, the check, or the waiting_on line.
	Waiting string `json:"waiting"`
}

// readyTracks is what can move: open tracks that are not parked, not a lead's threads, and not
// waiting on a track still open. Focus comes first, then background and waiting, each by rank
// with unranked last, then by name.
func readyTracks(all []lsTrack) []readyTrack {
	open := map[string]bool{}
	for _, t := range all {
		open[strings.ToLower(t.Name)] = true
	}
	var in []lsTrack
	for _, t := range all {
		if t.Error != nil || t.Mode == "parked" || t.Parent != nil ||
			slices.ContainsFunc(t.BlockedBy, func(b string) bool { return open[strings.ToLower(b)] }) {
			continue
		}
		in = append(in, t)
	}
	slices.SortStableFunc(in, func(a, b lsTrack) int {
		if d := modeOrder(a.Mode) - modeOrder(b.Mode); d != 0 {
			return d
		}
		if d := rankKey(a.Rank) - rankKey(b.Rank); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	out := make([]readyTrack, 0, len(in))
	for _, t := range in {
		r := readyTrack{Name: t.Name, Mode: t.Mode, Rank: t.Rank, Today: t.Today, BlockedBy: t.BlockedBy}
		r.WaitsOn, r.Waiting = waitsOn(t)
		out = append(out, r)
	}
	return out
}

func modeOrder(mode string) int {
	return slices.IndexFunc(groups, func(g struct{ mode, label string }) bool { return g.mode == mode })
}

func rankKey(r *int) int {
	if r == nil {
		return int(^uint(0) >> 1)
	}
	return *r
}

func waitsOn(t lsTrack) (kind, what string) {
	switch {
	case len(t.Asks) == 1:
		return waitsEmil, "1 ask: " + t.Asks[0].Text
	case len(t.Asks) > 1:
		return waitsEmil, strconv.Itoa(len(t.Asks)) + " asks"
	case strings.TrimSpace(t.Check) != "":
		return waitsCheck, t.Check
	case t.Mode == "waiting" && strings.TrimSpace(t.WaitingOn) != "":
		return waitsOutside, t.WaitingOn
	}
	return waitsNothing, ""
}

func readyCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("ready", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "print one JSON document")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return errors.New("usage: tracks ready [--json]")
	}
	ready := readyTracks(loadAllCards())
	if *asJSON {
		b, err := json.MarshalIndent(readyDoc{readySchema, time.Now().UTC().Format(time.RFC3339), ready}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	if len(ready) == 0 {
		_, err := fmt.Fprintln(out, "nothing ready")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODE\tRANK\tWAITS ON\tTODAY")
	for _, r := range ready {
		rank := "-"
		if r.Rank != nil {
			rank = strconv.Itoa(*r.Rank)
		}
		waits := r.WaitsOn
		if r.Waiting != "" {
			waits += ": " + r.Waiting
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Mode, rank, waits, r.Today)
	}
	return tw.Flush()
}
