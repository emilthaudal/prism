package tracks

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const screenUsage = "usage: tracks screen TRACK"

// screenCmd prints what a track's agent shows on its screen, so the desk can read a stalled track
// without running rex: the desk's settings deny every rex command, since rex can type into any
// pane, press keys at a permission prompt, and start commands outside the desk's process tree,
// where tracks' own desk checks cannot see them.
func screenCmd(args []string, out io.Writer) error {
	if len(args) != 1 || !validName.MatchString(args[0]) {
		return errors.New(screenUsage)
	}
	c, err := loadCard(args[0])
	if err != nil {
		return fmt.Errorf("no card for %s", args[0])
	}
	sess := c.Meta["rex_session"]
	if sess == "" {
		return fmt.Errorf("%s has no Rex session on its card", args[0])
	}
	a := []string{"capture", "-s", sess, "--trim"}
	block := agentBlock(readEvents(), sess)
	if block != "" {
		a = append(a, "-b", block)
	}
	ctx, cancel := context.WithTimeout(context.Background(), rexTimeout)
	defer cancel()
	b, err := rexRun(ctx, a...)
	if err != nil {
		return fmt.Errorf("could not read %s's screen: %v", args[0], err)
	}
	if block == "" {
		// No hook event names the agent's block yet, so rex read the session's focused pane.
		fmt.Fprintf(out, "(no agent pane known for %s yet: this is the session's focused pane)\n", args[0])
	}
	_, err = out.Write(b)
	return err
}
