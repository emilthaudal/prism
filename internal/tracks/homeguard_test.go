package tracks

import (
	"os"
	"path/filepath"
	"testing"
)

// The tests can never reach the real state folder: TestMain moved HOME away, and home() panics
// when TRACKS_HOME points at the real folder anyway.
func TestTestsNeverReachTheRealStateFolder(t *testing.T) {
	if len(forbiddenHomes) == 0 {
		t.Fatal("TestMain set no forbidden homes")
	}
	t.Setenv("TRACKS_HOME", "")
	if h := home(); h == forbiddenHomes[0] {
		t.Fatalf("home() is the real folder %s", h)
	}
	for _, h := range []string{forbiddenHomes[0], filepath.Join(forbiddenHomes[0], "sub")} {
		t.Setenv("TRACKS_HOME", h)
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("home() returned the real folder %s", h)
				}
			}()
			home()
		}()
	}
	if os.Getenv("HOME") == filepath.Dir(filepath.Dir(filepath.Dir(forbiddenHomes[0]))) {
		t.Error("HOME is the real one")
	}
}
