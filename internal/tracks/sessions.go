package tracks

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	sessionsSchema = 1
	// agentsTimeout bounds claude agents --json, which says which sessions are alive.
	agentsTimeout = 2 * time.Second
	answerColMax  = 60
)

type sessionsDoc struct {
	Schema      int            `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Liveness    bool           `json:"liveness"`
	Sessions    []sessionEntry `json:"sessions"`
}

type sessionEntry struct {
	ID         string  `json:"id"`
	Track      *string `json:"track"`
	Parent     *string `json:"parent"`
	Owner      string  `json:"owner"`
	Host       string  `json:"host"`
	Name       string  `json:"name"`
	Cwd        string  `json:"cwd"`
	Model      string  `json:"model"`
	Status     string  `json:"status"`
	WaitingFor string  `json:"waiting_for"`
	LastAnswer string  `json:"last_answer"`
	// AgentsWorking counts the session's subagents that still run, after its turn too.
	AgentsWorking int     `json:"agents_working"`
	SeenAt        *string `json:"seen_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	EndedAt       *string `json:"ended_at"`
}

// liveSessions is claude agents --json by session id: every interactive and background
// session that runs now. A stopped background session stays listed with pid 0 so that it can
// be resumed; it runs nothing, so it is left out, or one that died without SessionEnd would
// hold its track (EMI-430).
func liveSessions() (map[string]claudeAgent, error) {
	list, err := claudeAgents()
	if err != nil {
		return nil, err
	}
	live := map[string]claudeAgent{}
	for _, a := range list {
		if a.PID != 0 {
			live[a.SessionID] = a
		}
	}
	return live, nil
}

// readSessions lists the sessions, first writing host stopped on those whose row says they
// run but claude agents does not list, and marking those whose turn ended without a Stop hook
// (see stalledSessions). When claude agents fails, rows stay as the hooks left them and
// liveness is false.
func readSessions(st *store.Store) (list []store.Session, liveness bool, err error) {
	before := time.Now()
	live, lerr := liveSessions()
	if list, err = st.Sessions(); err != nil || lerr != nil {
		return list, false, err
	}
	var gone []string
	for _, s := range list {
		if _, ok := live[s.ID]; s.Host != "stopped" && !ok {
			gone = append(gone, s.ID)
		}
	}
	stalls := stalledSessions(list, live, before)
	if len(gone) == 0 && len(stalls) == 0 {
		return list, true, nil
	}
	if err := st.MarkStopped(gone, before); err != nil {
		return nil, false, err
	}
	if err := st.MarkStalled(stalls, before); err != nil {
		return nil, false, err
	}
	list, err = st.Sessions()
	return list, true, err
}

func sessionsCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "print one JSON document")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return errors.New("usage: tracks sessions [--json]")
	}
	if !storeOn() {
		return fmt.Errorf("tracks sessions needs %s: run tracks migrate", dbFileName)
	}
	var (
		list     []store.Session
		liveness bool
		agents   map[string]int
	)
	err := withBareStore(func(st *store.Store) (err error) {
		if list, liveness, err = readSessions(st); err == nil {
			agents, err = workingAgents(st)
		}
		return err
	})
	if err != nil {
		return err
	}
	if *asJSON {
		doc := sessionsDoc{sessionsSchema, time.Now().UTC().Format(time.RFC3339), liveness, []sessionEntry{}}
		for _, s := range list {
			e := sessionJSON(s)
			e.AgentsWorking = agents[s.ID]
			doc.Sessions = append(doc.Sessions, e)
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tTRACK\tOWNER\tHOST\tSTATUS\tAGE\tWAITING FOR / LAST ANSWER")
	now := time.Now()
	for _, s := range list {
		track := s.Track
		if track == "" {
			track = "-"
		}
		note := s.WaitingFor
		if note == "" {
			note = firstLine(s.LastAnswer)
		}
		status := s.Status
		if n := agents[s.ID]; n > 0 {
			status += ", " + waitingOnAgents(n)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", shortID(s.ID), track, s.Owner, s.Host, status,
			shortDur(now.Sub(s.UpdatedAt)), clipRunes(note, answerColMax))
	}
	if !liveness {
		fmt.Fprintln(tw, "(claude agents did not answer: hosts are as the hooks last wrote them)")
	}
	return tw.Flush()
}

func sessionJSON(s store.Session) sessionEntry {
	str := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	at := func(t time.Time) *string {
		if t.IsZero() {
			return nil
		}
		v := t.UTC().Format(time.RFC3339)
		return &v
	}
	return sessionEntry{
		ID: s.ID, Track: str(s.Track), Parent: str(s.ParentID), Owner: s.Owner, Host: s.Host, Name: s.Name,
		Cwd: s.Cwd, Model: s.Model, Status: s.Status, WaitingFor: s.WaitingFor, LastAnswer: s.LastAnswer,
		SeenAt: at(s.SeenAt), CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339), EndedAt: at(s.EndedAt),
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
