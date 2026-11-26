package tracks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// editedPrefix starts the name a hand-edited card view is kept under.
const editedPrefix = ".edited-"

// beforeStoreCheck runs before every check for the database; tests import their fixtures there.
var beforeStoreCheck = func() {}

// storeOn is the switch: with $TRACKS_HOME/tracks.db present it is the source of truth and the
// card files are views of it; without it the file code runs.
func storeOn() bool {
	beforeStoreCheck()
	return dbExists()
}

func dbExists() bool {
	_, err := os.Stat(dbPath())
	return err == nil
}

// openDB opens tracks.db, backing up the home first when the database needs migrating.
func openDB(opts ...store.Option) (*store.Store, error) {
	return store.Open(dbPath(), append([]store.Option{store.WithBackup(migrationBackup)}, opts...)...)
}

// migrationBackup copies the database with SQLite to backup/pre-migrate-vN-TIME.db and checks
// the copy, then archives the rest of the home beside it as tracks migrate does.
func migrationBackup(from int, copyDB func(dst string) error) error {
	now := time.Now()
	prefix := fmt.Sprintf("pre-migrate-v%d", from)
	dir := filepath.Join(home(), backupDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dbCopy := filepath.Join(dir, prefix+"-"+now.UTC().Format("20060102T150405Z")+".db")
	if err := copyDB(dbCopy); err != nil {
		return err
	}
	if err := store.CheckCopy(dbCopy); err != nil {
		os.Remove(dbCopy)
		return err
	}
	_, _, err := backupHome(prefix, now)
	return err
}

// openStore opens tracks.db with views rendered after every write.
func openStore(opts ...store.Option) (*store.Store, error) {
	return openDB(append([]store.Option{store.WithViews(writeView), store.WithRenderErrors(noteRenderFailure)}, opts...)...)
}

func renderErrPath() string { return filepath.Join(home(), "render.err") }

// noteRenderFailure keeps a render that failed after its write committed in render.err, which
// the pane footer shows until a full render succeeds.
func noteRenderFailure(err error) {
	rec := hookRecord{At: time.Now().UTC(), Version: buildID(), Error: err.Error()}
	if b, merr := json.Marshal(rec); merr == nil {
		_ = writeAtomic(renderErrPath(), append(b, '\n'))
	}
}

// renderFailure is render.err's record, when there is one.
func renderFailure() (hookRecord, bool) { return readHookRecord(renderErrPath()) }

// renderAll rewrites every view and, when that worked, clears a render failure noted before it began.
func renderAll(st *store.Store) error {
	start := time.Now().UTC()
	if err := st.RenderAll(); err != nil {
		return err
	}
	if r, ok := renderFailure(); ok && r.At.Before(start) {
		_ = os.Remove(renderErrPath())
	}
	return nil
}

// withStore runs fn on an open store and closes it.
func withStore(fn func(st *store.Store) error, opts ...store.Option) error {
	st, err := openStore(opts...)
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

// withBareStore is withStore without rendering views: the view of what fn changes stays as
// it was until the next write or tracks render.
func withBareStore(fn func(st *store.Store) error, opts ...store.Option) error {
	st, err := openDB(opts...)
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

func hashText(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// keepEdited moves a view whose hash is not the stored one (its last render, or the card file
// it was imported from) to cards/.edited-NAME-TIME.md, so a hand edit is never overwritten. A
// track with no stored hash cannot vouch for any file, so its file is kept too.
func keepEdited(path, name, hash string) {
	if b, err := os.ReadFile(path); err == nil && hashText(b) != hash {
		keepAside(path, name)
	}
}

func keepAside(path, name string) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	_ = os.Rename(path, filepath.Join(filepath.Dir(path), editedPrefix+name+"-"+stamp+".md"))
}

// removeView deletes the live view of name, keeping it first if it is not the track's own.
func removeView(name, hash string) {
	path := cardPath(name)
	if _, err := os.Stat(path); err != nil {
		return
	}
	keepEdited(path, name, hash)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
}

// writeView renders a track the database changed: its live card, or for a track closed by this
// write its archive file, and no live view under a name it gave up.
func writeView(v store.ViewChange) (hash, archive string, err error) {
	t := v.Track
	var perm os.FileMode // a renamed card keeps its file's mode, as the file code's rename does
	for _, n := range v.Gone {
		if st, err := os.Stat(cardPath(n)); err == nil && perm == 0 {
			perm = st.Mode().Perm()
		}
		removeView(n, t.RenderHash)
	}
	if t.Closed() {
		mode := os.FileMode(0o644)
		if st, err := os.Stat(cardPath(t.Name)); err == nil {
			mode = st.Mode().Perm()
		}
		removeView(t.Name, t.RenderHash)
		if !v.Closing {
			return t.RenderHash, "", nil
		}
		dest, err := linkArchive(archiveDir(), t.Name, t.ClosedAt, []byte(store.RenderView(t, v.Asks)), mode)
		if err != nil {
			return "", "", err
		}
		return "", filepath.Base(dest), nil
	}
	text := []byte(store.RenderView(t, v.Asks))
	hash = hashText(text)
	path := cardPath(t.Name)
	if b, err := os.ReadFile(path); err == nil {
		// A file that already shows this render is no hand edit, even when its hash was never
		// stored because the process stopped between writing it and committing the hash.
		switch got := hashText(b); {
		case got == hash && v.Repair:
			return hash, "", nil
		case got == t.RenderHash && v.Repair && t.Version == 1:
			// The card file an import read, with no write since, holds what the database holds.
			return t.RenderHash, "", nil
		case got != hash && got != t.RenderHash:
			keepAside(path, t.Name)
		}
	}
	if err = writeAtomic(path, text); err != nil {
		return "", "", err
	}
	if perm != 0 {
		_ = os.Chmod(path, perm)
	}
	return hash, "", nil
}

// editedViews lists the hand-edited card views kept aside, newest last.
func editedViews() []string {
	files, _ := filepath.Glob(filepath.Join(home(), "cards", editedPrefix+"*.md"))
	slices.Sort(files)
	return files
}

// contentAt is when a track's content last changed, what a card file's mtime says on the
// files: ls --json reports it as updated. Mode keys and the hook's bookkeeping move only
// changed_at, as they kept a file's mtime. An import sets created_at and changed_at to the
// card file's mtime and updated_at to the card's updated: stamp, never later than the mtime.
func contentAt(t store.Track) time.Time {
	if t.UpdatedAt.After(t.CreatedAt) {
		return t.UpdatedAt
	}
	return t.CreatedAt
}

// cardOf is the card a reader sees for a track: its rendered text parsed as a card file is,
// with the answered asks from the asks rows, and contentAt as its mtime.
func cardOf(v store.View) *Card {
	c := parseCard(v.Track.Name, store.Render(v.Track, v.Asks))
	c.Mtime = contentAt(v.Track)
	var answered []int
	var at time.Time
	for i, a := range v.Asks {
		c.askIDs = append(c.askIDs, a.ID)
		if !a.AnsweredAt.IsZero() {
			answered = append(answered, i+1)
			if a.AnsweredAt.After(at) {
				at = a.AnsweredAt
			}
		}
	}
	if len(answered) > 0 {
		c.Meta["answered_asks"] = askedValue(answered, at)
	}
	return c
}

func byFileName(vs []store.View) {
	slices.SortFunc(vs, func(a, b store.View) int { return strings.Compare(a.Track.Name+".md", b.Track.Name+".md") })
}

func liveViews() ([]store.View, error) {
	var vs []store.View
	err := withStore(func(st *store.Store) (err error) {
		vs, err = st.LiveTracks()
		return err
	})
	byFileName(vs)
	return vs, err
}

func storeCards() []*Card {
	vs, _ := liveViews()
	var out []*Card
	for _, v := range vs {
		out = append(out, cardOf(v))
	}
	markWorking(out)
	return out
}

// storeCard is the live track called exactly name (any case), like the file cards/NAME.md.
func storeCard(name string) (*Card, error) {
	var v store.View
	err := withStore(func(st *store.Store) (err error) {
		v, err = st.Get(name)
		return err
	})
	if err != nil || v.Closed || v.RenamedTo != "" {
		return nil, fs.ErrNotExist
	}
	return cardOf(v), nil
}

func storeEvents() []Event {
	var evs []store.Event
	_ = withStore(func(st *store.Store) (err error) {
		evs, err = st.AllEvents()
		return err
	})
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		out = append(out, Event{At: e.At, Event: e.Event, RexSession: e.RexSession, RexBlock: e.RexBlock,
			Track: e.Current, Cwd: e.Cwd, Len: e.Len, Kind: e.Kind})
	}
	return out
}

func storeChecks() (map[string]checkState, error) {
	var cs []store.NamedCheck
	err := withStore(func(st *store.Store) (err error) {
		cs, err = st.AllChecks()
		return err
	})
	m := map[string]checkState{}
	for _, c := range cs {
		m[c.Track+": "+c.Text] = checkStateOf(c.Check)
	}
	return m, err
}

func checkStateOf(c store.Check) checkState {
	cs := checkState{Answer: c.Answer, RanAt: c.RanAt}
	if !c.AskedAt.IsZero() {
		cs.Asked = c.AskedAt.UTC().Format(time.RFC3339Nano)
	}
	return cs
}

func storeRetired() retired {
	r := retired{}
	_ = withStore(func(st *store.Store) error {
		m, err := st.Names()
		for k, v := range m {
			r[k] = v
		}
		return err
	})
	return r
}

// storeArchived is the archive file of the closed track called name, "" when there is none.
func storeArchived(name string) string {
	var v store.View
	if err := withStore(func(st *store.Store) (err error) {
		v, err = st.Get(name)
		return err
	}); err != nil || !v.Closed || v.RenamedTo != "" {
		return ""
	}
	return archiveFile(v.Track)
}

func storeArchive() []closedCard {
	var ts []store.Track
	_ = withStore(func(st *store.Store) (err error) {
		ts, err = st.ClosedTracks()
		return err
	})
	var out []closedCard
	for _, t := range ts {
		c := parseCard(t.Name, store.Render(t, nil))
		out = append(out, closedCard{t.Name, t.ClosedAt, c.firstLine("Outcome"), filepath.Join(archiveDir(), archiveFile(t))})
	}
	slices.SortStableFunc(out, func(a, b closedCard) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return strings.Compare(b.File, a.File)
	})
	return out
}

// storeLs is ls --json from the database: updated is contentAt and an ask's since its own
// created_at.
func storeLs() []lsTrack {
	vs, err := liveViews()
	out := []lsTrack{}
	if err != nil {
		return append(out, brokenTrack(dbFileName, err.Error()))
	}
	working := workingTracks()
	for _, v := range vs {
		c := cardOf(v)
		c.Working = working[c.Name]
		t := lsTrackOf(c, contentAt(v.Track).UTC())
		for i := range t.Asks {
			if i < len(v.Asks) {
				t.Asks[i].Since = timeStr(v.Asks[i].CreatedAt.UTC())
			}
		}
		out = append(out, t)
	}
	return out
}

// storeEvent logs e as appendEvent would: at its own time, under the track it names.
func storeEvent(e Event) error {
	return withStore(func(st *store.Store) error {
		return st.Log(e.Track, store.Event{At: e.At, Event: e.Event, RexSession: e.RexSession, RexBlock: e.RexBlock, Cwd: e.Cwd, Len: e.Len, Kind: e.Kind})
	})
}

// newTrack writes a new track's card, failing with errExists when the name is held.
func newTrack(name, mode, tier, dir, goal string) error {
	if !storeOn() {
		err := withFileLock(func() error { return createCard(name, []byte(cardText(name, mode, tier, dir, goal))) })
		if !errors.Is(err, errSwitched) {
			return err
		}
	}
	err := withStore(func(st *store.Store) error {
		_, err := st.NewTrack(store.NewTrackSpec{Name: name, Mode: mode, Tier: tier, Dir: dir, Goal: goal})
		return err
	})
	if errors.Is(err, store.ErrNameTaken) {
		return errExists
	}
	return err
}

// storeNameTaken is whether any track holds or held name.
func storeNameTaken(name string) bool {
	err := withStore(func(st *store.Store) error {
		_, err := st.Get(name)
		return err
	})
	return !errors.Is(err, store.ErrNoTrack)
}

var (
	watchMu   sync.Mutex
	watcher   *store.Watcher
	watchPath string
)

// storeStamp changes whenever another connection commits to tracks.db.
func storeStamp() string {
	watchMu.Lock()
	defer watchMu.Unlock()
	if watcher != nil && watchPath != dbPath() {
		watcher.Close()
		watcher = nil
	}
	if watcher == nil {
		w, err := store.Watch(dbPath())
		if err != nil {
			return "db:" + err.Error()
		}
		watcher, watchPath = w, dbPath()
	}
	v, err := watcher.Version()
	if err != nil {
		watcher.Close()
		watcher = nil
		return "db:" + err.Error()
	}
	return fmt.Sprintf("db:%d;", v)
}
