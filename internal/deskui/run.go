package deskui

import (
	"errors"
	"flag"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

// Main runs prism tui with its flags; in and out must be a terminal.
func Main(args []string, in io.Reader, out, errw io.Writer, tty bool) int {
	fs := flag.NewFlagSet("prism tui", flag.ContinueOnError)
	fs.SetOutput(errw)
	fixture := fs.String("fixture", "", "run from a snapshot file, with actions applied in memory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errw, "usage: prism tui [--fixture FILE]")
		return 2
	}
	if !tty {
		fmt.Fprintln(errw, "prism tui: needs a terminal")
		return 1
	}
	var b Backend
	var err error
	if *fixture != "" {
		b, err = LoadFixture(*fixture)
	} else {
		b, err = connectHTTP(in, errw)
	}
	if errors.Is(err, errQuit) {
		return 1
	}
	if err != nil {
		fmt.Fprintln(errw, "prism tui:", err)
		return 1
	}
	m := New(b)
	if _, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		fmt.Fprintln(errw, "prism tui:", err)
		return 1
	}
	return 0
}
