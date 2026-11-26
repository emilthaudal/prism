package tracks

import "io"

// RunPrism is the prism binary: `prism` opens the Prism TUI, `prism server` runs the backend,
// and every other command is the tracks command of the same name.
func RunPrism(args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		args = []string{"tui"}
	}
	return Run(args, in, out, errw)
}
