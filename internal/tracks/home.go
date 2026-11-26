package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// homeLabel is the legacy Rex session the tracks pane lives in. It is only the home when the
// desk cannot be started: otherwise the desk's session (its agent with the tracks pane beside it)
// is the one place to work from, which cmd+shift+h switches to and back from.
const homeLabel = "tracks"

// homeCommand keeps the pane running in its block: quitting it (q, ctrl+c, a crash) starts it
// again, which also picks up a freshly installed binary.
const homeCommand = `while :; do "$HOME/.local/bin/tracks" --home; sleep 0.5; done`

func homeFromPath() string { return filepath.Join(home(), "home-from") }

type rexSession struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}

// homeTarget is the session to select: from the home session, the one Emil came from (when
// it still exists); from anywhere else, the home session. back is whether it goes back.
func homeTarget(from, homeID, last string, live []rexSession) (target string, back bool) {
	if from != "" && from == homeID {
		for _, s := range live {
			if s.ID == last && last != homeID {
				return last, true
			}
		}
		return "", true
	}
	return homeID, false
}

// homeCmd is `tracks home --from SESSION`: Rex's cmd+shift+h runs it with the session the
// key was pressed in. It creates the home session (or its pane) when missing.
func homeCmd(args []string, out io.Writer) error {
	from := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from" && i+1 < len(args):
			from = args[i+1]
			i++
		default:
			return errors.New("usage: tracks home [--from SESSION]")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	b, err := rexRun(ctx, "session", "ls", "--json")
	if err != nil {
		return fmt.Errorf("could not list Rex sessions: %w", err)
	}
	var l struct {
		Sessions []rexSession `json:"sessions"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	homeID, label, err := deskHomeSession(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tracks home: the desk is not available (%v); using the %s session\n", err, homeLabel)
		label = homeLabel
		homeID = ""
		for _, s := range l.Sessions {
			if s.Label == homeLabel {
				homeID = s.ID
				break
			}
		}
		if homeID == "" {
			if homeID, err = createHome(ctx); err != nil {
				return err
			}
		} else if err := ensureHomePane(ctx, homeID); err != nil {
			return err
		}
	} else if !slices.ContainsFunc(l.Sessions, func(s rexSession) bool { return s.ID == homeID }) {
		l.Sessions = append(l.Sessions, rexSession{ID: homeID, Label: label}) // started just now
	}
	last, _ := os.ReadFile(homeFromPath())
	target, back := homeTarget(from, homeID, strings.TrimSpace(string(last)), l.Sessions)
	if target == "" {
		fmt.Fprintln(out, "no session to go back to")
		return nil
	}
	if !back && from != "" {
		_ = os.WriteFile(homeFromPath(), []byte(from+"\n"), 0o644) // only loses the way back
	}
	if _, err := rexRun(ctx, "do", "session.select", "session_id="+target); err != nil {
		return fmt.Errorf("could not switch Rex to the session: %w", err)
	}
	if back {
		fmt.Fprintln(out, "back")
	} else {
		fmt.Fprintln(out, label)
	}
	return nil
}

// deskHomeSession is the session cmd+shift+h goes to: the desk's session, started when its agent is
// not running. It keeps the session first in the sidebar and the tracks pane beside the agent.
func deskHomeSession(ctx context.Context) (id, label string, err error) {
	sess, err := deskSessionNow()
	if err != nil {
		return "", "", err
	}
	block := ""
	if sess != "" {
		r := fetchSession(sess, "", "")
		if r.failed {
			return "", "", errors.New("Rex did not answer")
		}
		if r.found && !r.state.Exited {
			id, block = r.id, r.block
		}
	}
	if id == "" {
		if id, block, err = startDesk(io.Discard); err != nil {
			return "", "", err
		}
	} else if err := ensureDeskPane(ctx, id, block); err != nil {
		fmt.Fprintf(os.Stderr, "tracks home: the tracks pane did not open beside the desk: %v\n", err)
	}
	moveToTop(ctx, id)
	return id, deskLabel, nil
}

// ensureDeskPane opens the tracks pane beside the desk's agent when the session has none.
func ensureDeskPane(ctx context.Context, id, agent string) error {
	b, err := rexRun(ctx, "block", "ls", "-s", id, "--json")
	if err != nil {
		return err
	}
	var l struct {
		Blocks []struct {
			ID        string `json:"block_id"`
			Label     string `json:"label"`
			Placement string `json:"placement"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	for _, bl := range l.Blocks {
		if bl.Label == "tracks" && bl.Placement == "placed" {
			return nil
		}
	}
	if agent == "" {
		for _, bl := range l.Blocks {
			if bl.Label != "tracks" && bl.Placement == "placed" {
				agent = bl.ID
				break
			}
		}
	}
	return splitDeskPane(id, agent)
}

// moveToTop puts the session first in the sidebar, where the desk always is. Not fatal if it fails.
func moveToTop(ctx context.Context, id string) {
	first := firstSession(ctx)
	if first == "" || first == id {
		return
	}
	req, _ := json.Marshal(map[string]string{"before_session_id": first})
	rexRunIn(ctx, string(req), "api", "call", "-s", id, "session.move", "-")
}

func homeLayout() map[string]any {
	return map[string]any{"block": map[string]any{
		"flavor":  "com.superlogical.terminal.shell",
		"label":   homeLabel,
		"options": map[string]any{"command": []string{"/bin/sh", "-c", homeCommand}},
	}}
}

func createHome(ctx context.Context) (string, error) {
	req, _ := json.Marshal(map[string]any{
		"label":           homeLabel,
		"initial_windows": []any{map[string]any{"window_label": homeLabel, "layout": homeLayout()}},
	})
	b, err := rexRunIn(ctx, string(req), "api", "call", "session.create", "-")
	if err != nil {
		return "", fmt.Errorf("could not create the %s session: %w", homeLabel, err)
	}
	var r struct {
		ID string `json:"session_id"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.ID == "" {
		return "", fmt.Errorf("could not read the new %s session: %s", homeLabel, strings.TrimSpace(string(b)))
	}
	// First in the sidebar, so it reads as the place to start from. Not fatal if it fails.
	if first := firstSession(ctx); first != "" && first != r.ID {
		req, _ := json.Marshal(map[string]string{"before_session_id": first})
		rexRunIn(ctx, string(req), "api", "call", "-s", r.ID, "session.move", "-")
	}
	return r.ID, nil
}

func firstSession(ctx context.Context) string {
	b, err := rexRun(ctx, "session", "ls", "--json")
	if err != nil {
		return ""
	}
	var l struct {
		Sessions []rexSession `json:"sessions"`
	}
	if json.Unmarshal(b, &l) != nil || len(l.Sessions) == 0 {
		return ""
	}
	return l.Sessions[0].ID
}

// ensureHomePane opens a window with the pane when the home session has no placed block left.
func ensureHomePane(ctx context.Context, id string) error {
	b, err := rexRun(ctx, "api", "call", "-s", id, "session.list_blocks")
	if err != nil {
		return fmt.Errorf("could not list the %s session's blocks: %w", homeLabel, err)
	}
	var l struct {
		Blocks []struct {
			Placement string `json:"placement"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	for _, bl := range l.Blocks {
		if bl.Placement == "placed" {
			return nil
		}
	}
	req, _ := json.Marshal(map[string]any{"window_label": homeLabel, "layout": homeLayout()})
	if _, err := rexRunIn(ctx, string(req), "api", "call", "-s", id, "session.new_window", "-"); err != nil {
		return fmt.Errorf("could not reopen the tracks pane: %w", err)
	}
	return nil
}
