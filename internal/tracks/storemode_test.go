package tracks

import (
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

// storeModeEnv runs the whole suite against a tracks.db imported from each test's file fixture:
// TRACKS_TEST_STORE=1 go test ./internal/tracks. Real behaviour keys only on the file existing.
const storeModeEnv = "TRACKS_TEST_STORE"

func TestMain(m *testing.M) {
	if os.Getenv(fakePaneEnv) == "1" {
		// A stand-in pane process: it waits for its stdin to close.
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	if storeMode() {
		beforeStoreCheck = importFixture
	}
	watchReaps = false
	// The tests' issue keys link the way a setup with Linear and Jira would.
	os.Setenv("TRACKS_ISSUE_URLS", "EMI=https://linear.app/example/issue/,*=https://example.atlassian.net/browse/")
	os.Exit(runAwayFromRealHome(m))
}

// runAwayFromRealHome runs the tests with HOME and TRACKS_HOME in a throwaway folder, so a test
// that skips setup, clears TRACKS_HOME or outlives its own t.Setenv lands there, and makes
// home() panic on the real state folders.
func runAwayFromRealHome(m *testing.M) int {
	if u, err := os.UserHomeDir(); err == nil {
		forbiddenHomes = append(forbiddenHomes, filepath.Join(u, ".local", "state", "tracks"))
	}
	if h := os.Getenv("TRACKS_HOME"); h != "" {
		forbiddenHomes = append(forbiddenHomes, filepath.Clean(h))
	}
	tmp, err := os.MkdirTemp("", "tracks-test-home-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	// Go's caches stay where they are: a test that builds must not refill them from scratch.
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if os.Getenv(k) == "" {
			if v, err := exec.Command("go", "env", k).Output(); err == nil {
				os.Setenv(k, strings.TrimSpace(string(v)))
			}
		}
	}
	os.Setenv("HOME", tmp)
	// Not $HOME/.local/state/tracks: that path names the live server's launchd job (serveLabel).
	os.Setenv("TRACKS_HOME", filepath.Join(tmp, "state"))
	return m.Run()
}

func storeMode() bool { return os.Getenv(storeModeEnv) == "1" }

var (
	fixtureMu sync.Mutex
	// fileHomes are the homes of tests that cover the file code itself and stay on files.
	noImportHomes = map[string]bool{}
)

// importFixture builds tracks.db from the files in a test's home the first time a command
// looks for it, as tracks migrate would. Only a home under the temp dir is ever touched.
func importFixture() {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	h := home()
	if noImportHomes[h] || dbExists() || !strings.HasPrefix(h, os.TempDir()) {
		return
	}
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		return
	}
	st, err := store.Open(dbPath(), store.WithCreate())
	if err != nil {
		panic(err)
	}
	defer st.Close()
	src := readSource()
	if _, err := st.Import(src.importData()); err != nil {
		panic(err)
	}
}

// noImport keeps the store run from importing for this test: it covers file code the store
// replaces (locks, hard links, file races, card text surgery) and stays on files in both runs,
// or it makes tracks.db itself so it runs the same in both.
func noImport(t *testing.T) {
	t.Helper()
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	h := home()
	noImportHomes[h] = true
	t.Cleanup(func() {
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		delete(noImportHomes, h)
	})
}

// ranOnTheStore fails a probe that the store run did not run against tracks.db.
func ranOnTheStore(t *testing.T) {
	t.Helper()
	if storeMode() && !dbExists() {
		t.Fatal("the store run left this test on the files")
	}
}

// sameCard is whether two card texts hold the same values, the way migrate --check compares a
// card with its render: time stamps by instant, empty values as absent, the generated line and
// the frontmatter comments and layout not counted. With tracks.db the files are renders, so a
// test that compares exact card text compares this instead.
func sameCard(got, want string) bool {
	gm, gs := normalised(parseCard("", got), "")
	wm, ws := normalised(parseCard("", want), "")
	delete(gm, "generated")
	return maps.Equal(gm, wm) && maps.Equal(gs, ws)
}

// foldBack hands the state back to the files before a test writes a fixture file mid-test: the
// database is exported and removed, so the next command imports the files again.
func foldBack(t *testing.T) {
	t.Helper()
	if !storeMode() || !dbExists() {
		return
	}
	if err := exportCmd(nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	moved, _ := filepath.Glob(dbPath() + ".rolled-back-*")
	for _, f := range moved {
		os.Remove(f)
	}
}
