package tracks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// hookOut is where the hook writes its answer to Claude Code.
var hookOut io.Writer = os.Stdout

// rootScanLines is how many records rootMessage reads for a transcript's first message.
const rootScanLines = 400

// rootMessage is the id of the first message in a transcript's head, "" when it has none. A
// resumed copy of a session starts with the original's messages under their own ids, so the
// two share it while their session ids differ.
func rootMessage(path string) string {
	root := ""
	// A copy repeats the original's snapshot and queue records ahead of its first message: in
	// the 25 Nov copies it sat at line 22 and 24, so the head is read further than 40 lines.
	scanTranscriptLines(path, rootScanLines, func(_ transcriptRec, raw []byte) bool {
		var r struct {
			UUID        string `json:"uuid"`
			IsSidechain bool   `json:"isSidechain"`
		}
		if json.Unmarshal(raw, &r) == nil && r.UUID != "" && !r.IsSidechain {
			root = r.UUID
			return true
		}
		return false
	})
	return root
}

// copyHolder is the live session on an open track that the session at transcript is a resumed
// copy of. Claude Code starts a copy under a new id when a session whose process still runs is
// resumed: on 25 Nov two copies of desk sessions (43c3ddfb of as-1701's 76c64393, 96f6d18a of
// as-1668's 9dce27e9) worked beside their originals on the same branch (EMI-427).
func copyHolder(sessionID, transcript string, start time.Time) (store.Session, bool) {
	if !claudeSessionID.MatchString(sessionID) || !strings.HasSuffix(transcript, ".jsonl") {
		return store.Session{}, false
	}
	root := rootMessage(transcript)
	if root == "" {
		return store.Session{}, false
	}
	left := hookBudget - time.Since(start)
	if left <= 0 {
		return store.Session{}, false
	}
	var all []store.Session
	if withBareStore(func(st *store.Store) (err error) { all, err = st.Sessions(); return err },
		store.WithBusyTimeout(min(hookLockWait, left))) != nil {
		return store.Session{}, false
	}
	// The copy is the newer of the two: a copy with no row yet is new, and the original, older
	// than its copy, is never refused for it.
	var self store.Session
	for _, s := range all {
		if s.ID == sessionID {
			self = s
		}
	}
	dir := filepath.Dir(transcript)
	for _, s := range all {
		if s.ID == sessionID || s.TrackID == 0 || s.Host == "stopped" || !claudeSessionID.MatchString(s.ID) {
			continue
		}
		if !self.CreatedAt.IsZero() && !s.CreatedAt.Before(self.CreatedAt) {
			continue
		}
		if rootMessage(filepath.Join(dir, s.ID+".jsonl")) == root {
			return s, true
		}
	}
	return store.Session{}, false
}

// blockCopy refuses the prompt of a resumed copy whose original still runs on its track, so the
// copy starts no work beside it. It reports whether it refused.
func blockCopy(h hookInput, start time.Time) bool {
	orig, ok := copyHolder(h.SessionID, h.Transcript, start)
	if !ok {
		return false
	}
	id := shortID(orig.ID)
	how := fmt.Sprintf("Talk to it with claude attach %s; or stop it (claude stop %s) and resume it again", id, id)
	if orig.Host == "rex" {
		how = "Work in its Rex pane, or end it first"
	}
	reason := fmt.Sprintf("tracks: this session is a copy of %s, which still runs on track %s; two copies would work over each other (EMI-427). %s.",
		id, orig.Track, how)
	b, err := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	if err != nil {
		return false
	}
	_, err = fmt.Fprintln(hookOut, string(b))
	return err == nil
}
