package tracks

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateStoreGolden = flag.Bool("update-store-golden", false, "rewrite internal/store/testdata from cardText")

// storeGoldens are the cards internal/store must render byte for byte. The files are what
// cardText writes; the store's own test renders the same tracks and compares with them.
var storeGoldens = []struct {
	file                  string
	name, mode, tier, dir string
	goal                  string
}{
	{"fresh.md", "fresh-track", "focus", "", "/Users/emil/Developer/tracks", defaultGoal},
	{"tiered.md", "tiered.track_2", "background", "2", "/Users/emil/Developer/acme/pantry-api", "Ship the thing."},
	{"nodir.md", "nodir", "parked", "3", "", defaultGoal},
}

func TestCardTextMatchesStoreGoldens(t *testing.T) {
	for _, g := range storeGoldens {
		path := filepath.Join("..", "store", "testdata", g.file)
		want := cardText(g.name, g.mode, g.tier, g.dir, g.goal)
		if *updateStoreGolden {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./internal/tracks -run StoreGoldens -update-store-golden)", g.file, err)
		}
		if string(got) != want {
			t.Errorf("%s differs from cardText:\n--- file\n%s\n--- cardText\n%s", g.file, got, want)
		}
	}
}
