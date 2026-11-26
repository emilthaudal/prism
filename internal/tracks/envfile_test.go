package tracks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheEnvFileFillsWhatTheEnvironmentLeavesUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	body := "# settings\n\nTRACKS_TEST_A=one\nexport TRACKS_TEST_B=\"two, three\"\nTRACKS_TEST_C=from-file\nnot a setting\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACKS_TEST_A", "")
	os.Unsetenv("TRACKS_TEST_A")
	t.Setenv("TRACKS_TEST_B", "")
	os.Unsetenv("TRACKS_TEST_B")
	t.Setenv("TRACKS_TEST_C", "from-env")
	loadEnvFile(path)
	if got := os.Getenv("TRACKS_TEST_A"); got != "one" {
		t.Errorf("A = %q", got)
	}
	if got := os.Getenv("TRACKS_TEST_B"); got != "two, three" {
		t.Errorf("B = %q", got)
	}
	if got := os.Getenv("TRACKS_TEST_C"); got != "from-env" {
		t.Errorf("the file overrode the environment: C = %q", got)
	}
	loadEnvFile(filepath.Join(t.TempDir(), "missing"))
}
