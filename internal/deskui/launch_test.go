package deskui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTracks writes a tracks that runs body when called as "tracks serve", and points TRACKS_BIN
// and TRACKS_HOME at it.
func fakeTracks(t *testing.T, body string) (home string) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	home = t.TempDir()
	bin := filepath.Join(t.TempDir(), "tracks")
	script := "#!/bin/sh\n[ \"$1\" = serve ] || exit 64\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACKS_BIN", bin)
	t.Setenv("TRACKS_HOME", home)
	t.Setenv("FAKE_URL", srv.URL)
	return home
}

const writeInfo = `printf '{"url":"%s","token":"t","pid":%s}' "$FAKE_URL" $$ > "$TRACKS_HOME/serve.json"`

func TestEnsureServeStartsAndConnects(t *testing.T) {
	home := fakeTracks(t, writeInfo+"\necho started\nexec sleep 3\n")
	h, err := connectHTTP(strings.NewReader(""), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if h.Token != "t" {
		t.Fatalf("token %q", h.Token)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "serve.log")); !strings.Contains(string(b), "started") {
		t.Fatalf("serve.log: %q", b)
	}
}

func TestEnsureServeSlowStart(t *testing.T) {
	fakeTracks(t, "sleep 1.5\n"+writeInfo+"\nexec sleep 3\n")
	start := time.Now()
	if err := EnsureServe(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Second || d > serveStartWait {
		t.Fatalf("took %s", d)
	}
}

func TestEnsureServeSkipsAReachableServe(t *testing.T) {
	home := fakeTracks(t, "echo second >> \"$TRACKS_HOME/ran\"\nexit 1\n")
	info := `{"url":"` + os.Getenv("FAKE_URL") + `","token":"t","pid":` + strconv.Itoa(os.Getpid()) + `}`
	os.WriteFile(filepath.Join(home, "serve.json"), []byte(info), 0o600)
	if err := EnsureServe(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "ran")); err == nil {
		t.Fatal("started a second serve")
	}
}

func TestEnsureServeStaleInfoStartsOne(t *testing.T) {
	home := fakeTracks(t, writeInfo+"\nexec sleep 3\n")
	os.WriteFile(filepath.Join(home, "serve.json"), []byte(`{"url":"http://127.0.0.1:1","token":"old","pid":1}`), 0o600)
	if err := EnsureServe(); err != nil {
		t.Fatal(err)
	}
	h, _ := NewHTTP()
	if h.Token != "t" {
		t.Fatalf("token %q", h.Token)
	}
}

func TestEnsureServeShowsTheRefusalAndRetries(t *testing.T) {
	home := fakeTracks(t, "echo 'tracks: the desk runs in Rex session abc' >&2\nexit 1\n")
	var out strings.Builder
	start := time.Now()
	_, err := connectHTTP(strings.NewReader("\nq\n"), &out)
	if err != errQuit {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited out the timeout instead of showing the refusal")
	}
	if n := strings.Count(out.String(), "the desk runs in Rex session abc"); n != 2 {
		t.Fatalf("message shown %d times: %q", n, out.String())
	}
	if !strings.Contains(out.String(), "Enter to retry") {
		t.Fatal(out.String())
	}
	_ = home
}

func TestEnsureServeLoserConnectsToTheWinner(t *testing.T) {
	fakeTracks(t, "echo 'tracks: prism server runs already for x' >&2\nsleep 0.5\n"+writeInfo+"\nexec sleep 3\n")
	// The loser exits at once; the winner's serve.json appears after.
	bin := os.Getenv("TRACKS_BIN")
	script := "#!/bin/sh\necho 'tracks: prism server runs already for x' >&2\nexit 1\n"
	os.WriteFile(bin, []byte(script), 0o755)
	home := os.Getenv("TRACKS_HOME")
	go func() {
		time.Sleep(700 * time.Millisecond)
		info := `{"url":"` + os.Getenv("FAKE_URL") + `","token":"w","pid":` + strconv.Itoa(os.Getpid()) + `}`
		os.WriteFile(filepath.Join(home, "serve.json"), []byte(info), 0o600)
	}()
	if err := EnsureServe(); err != nil {
		t.Fatal(err)
	}
	if h, _ := NewHTTP(); h.Token != "w" {
		t.Fatalf("token %q", h.Token)
	}
}

// serve --detach exits 0 at once; serve's own refusal comes later, in its log.
func TestEnsureServeShowsADetachedServesRefusal(t *testing.T) {
	fakeTracks(t, "[ \"$2\" = --detach ] || exit 65\n(sleep 0.3; echo 'tracks: the desk runs in Rex session abc' >> \"$TRACKS_HOME/serve.log\") &\nexit 0\n")
	start := time.Now()
	err := EnsureServe()
	if err == nil || !strings.Contains(err.Error(), "the desk runs in Rex session abc") {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited out the timeout instead of showing the refusal")
	}
}

func TestFixtureStartsNothing(t *testing.T) {
	home := fakeTracks(t, "touch \"$TRACKS_HOME/ran\"\n")
	var out, errw strings.Builder
	Main([]string{"--fixture", fixturePath(t)}, strings.NewReader(""), &out, &errw, false)
	if _, err := os.Stat(filepath.Join(home, "ran")); err == nil {
		t.Fatal("fixture mode started serve")
	}
}
