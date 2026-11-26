package main

import (
	"os"

	"github.com/emilthaudal/prism/internal/tracks"
)

func main() {
	os.Exit(tracks.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
