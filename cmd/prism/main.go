// Command prism is tracks under its Prism name: prism server, prism tui (EMI-393).
package main

import (
	"os"

	"github.com/emilthaudal/prism/internal/tracks"
)

func main() {
	tracks.LoadEnvFile()
	os.Exit(tracks.RunPrism(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
