package tracks

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile reads $TRACKS_HOME/env (default ~/.local/state/tracks/env) into the process's
// environment, so the settings in it (TRACKS_ISSUE_URLS, TRACKS_GH_ACCOUNTS, TRACKS_LEAN_*,
// TRACKS_DIR_PLUGINS, …) reach every tracks and prism process alike: the launchd jobs, a server
// Prism.app starts, and a shell. Each line is KEY=VALUE; blank lines and lines starting with #
// are skipped, and quotes around VALUE are dropped. A variable already set in the environment
// wins over the file. A missing file is no error.
func LoadEnvFile() {
	h := os.Getenv("TRACKS_HOME")
	if h == "" {
		u, err := os.UserHomeDir()
		if err != nil {
			return
		}
		h = filepath.Join(u, ".local", "state", "tracks")
	}
	loadEnvFile(filepath.Join(h, "env"))
}

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
