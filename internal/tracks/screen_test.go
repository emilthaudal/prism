package tracks

import (
	"strings"
	"testing"
)

// The desk reads a track's screen through tracks, since its settings deny rex itself.
func TestScreenPrintsTheTracksScreen(t *testing.T) {
	_, bin := actsHome(t, "2: product code", versionAsk)
	asDesk(t)
	out, code := do("screen", "solo")
	if code != 0 || !strings.Contains(out, "screen of capture -s "+ansSess+" --trim") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	nothingTyped(t, bin)
	if out, code := do("screen", "nobody"); code == 0 || !strings.Contains(out, "no card for nobody") {
		t.Errorf("a track with no card: %d %q", code, out)
	}
	if out, code := do("screen"); code == 0 || !strings.Contains(out, screenUsage) {
		t.Errorf("no track: %d %q", code, out)
	}
}
