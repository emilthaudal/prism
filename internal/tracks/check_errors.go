package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// checkErrorLimit is how many runs in a row may fail before the failure is surfaced (EMI-439):
// a check that errors forever leaves its track stuck with nothing watching.
const checkErrorLimit = 3

// checkFailure is one check's run of failures, keyed "NAME: CHECK". Reported is set once the
// ask went out, so a check that keeps failing does not ask again until it has worked once.
type checkFailure struct {
	Count    int       `json:"count"`
	Last     string    `json:"last"`
	Since    time.Time `json:"since"`
	Reported bool      `json:"reported,omitempty"`
}

func checkErrorsPath() string { return filepath.Join(home(), "check_errors.json") }

func loadCheckErrors() map[string]checkFailure {
	m := map[string]checkFailure{}
	if b, err := os.ReadFile(checkErrorsPath()); err == nil {
		_ = json.Unmarshal(b, &m) // a damaged file only restarts the counts
	} else if !errors.Is(err, fs.ErrNotExist) {
		return m
	}
	return m
}

func saveCheckErrors(m map[string]checkFailure) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(checkErrorsPath(), append(b, '\n'))
}

// noteCheckFailure counts one failed run of the check and reports whether this run crossed
// the limit and has not been reported yet.
func noteCheckFailure(m map[string]checkFailure, key string, err error, now time.Time) (report bool) {
	f := m[key]
	if f.Count == 0 {
		f.Since = now
	}
	f.Count++
	f.Last = oneLine(err.Error())
	if f.Count >= checkErrorLimit && !f.Reported {
		f.Reported, report = true, true
	}
	m[key] = f
	return report
}

func checkFailureAsk(text string, f checkFailure) string {
	return fmt.Sprintf("Check %q has failed %d runs in a row, so nothing is watching: %s. Recommend: fix the check or the track's dir, or rewrite it as pr owner/repo#N.", text, f.Count, f.Last)
}
