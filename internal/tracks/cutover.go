package tracks

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	backupDirName = "backup"
	importedExt   = ".imported"
	// paneArgs is how a pane is started: tracks, tracks --float or tracks --home, by any path.
	paneArgs = `^([^ ]*/)?tracks( --(float|home))?$`
)

// dbFiles are tracks.db and the files SQLite keeps beside it, as paths in the home.
var dbFiles = []string{dbFileName, dbFileName + "-wal", dbFileName + "-shm", dbFileName + "-journal"}

// panesRunning lists the pids of tracks panes running on this home; tests replace it.
var panesRunning = findPanes

// migrateHolding runs once migrate holds every lock, before it reads the files; tests start
// competing writers there.
var migrateHolding = func() {}

// findPanes asks pgrep for pane processes and keeps those whose TRACKS_HOME (or HOME) points
// at this home. A pane whose environment cannot be read counts, since it may be this home's.
func findPanes() ([]string, error) {
	b, err := exec.Command("pgrep", "-f", paneArgs).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pgrep: %w", err)
	}
	var pids []string
	for _, pid := range strings.Fields(string(b)) {
		if h := paneHome(pid); h == "" || samePath(h, home()) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// paneHome is the tracks home of process pid, read from its environment; "" when unknown.
func paneHome(pid string) string {
	b, err := exec.Command("ps", "-wwE", "-o", "command=", "-p", pid).Output()
	if err != nil {
		return ""
	}
	var trackHome, userHome string
	for _, f := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(f, "TRACKS_HOME="); ok {
			trackHome = v
		} else if v, ok := strings.CutPrefix(f, "HOME="); ok {
			userHome = v
		}
	}
	switch {
	case trackHome != "":
		return trackHome
	case userHome != "":
		return filepath.Join(userHome, ".local", "state", "tracks")
	}
	return ""
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return clean(a) == clean(b)
}

// migrate is tracks migrate: it imports the files into tracks.db and makes it the source of
// truth, with a verified backup first. It holds checks.lock, the cards lock and the events lock
// from before it reads the files until every view is rendered, so no writer on the files lands
// between the import and the switch.
func migrate(out io.Writer) error {
	if dbExists() {
		fmt.Fprintln(out, "already migrated")
		// Under the cards lock, which export holds from writing checks.json and attention.jsonl
		// until it moves tracks.db aside, so the finish never sets aside files export just wrote.
		return withCardsLock(func() error {
			if !dbExists() {
				return nil
			}
			return finishMigrate(out)
		})
	}
	pids, err := panesRunning()
	if err != nil {
		return fmt.Errorf("could not look for a running pane: %w", err)
	}
	if len(pids) > 0 {
		return fmt.Errorf("a tracks pane is running (pid %s): quit the pane, then run again", strings.Join(pids, ", "))
	}
	// The order tracks check takes them in: checks.lock, then the cards lock, then the events lock.
	unlock, err := lockChecks()
	if err != nil {
		return fmt.Errorf("%w: run again when it is done", err)
	}
	defer unlock()
	return withCardsLock(func() error {
		return withEventsLock(func() error { return migrateLocked(out) })
	})
}

func migrateLocked(out io.Writer) error {
	if dbExists() {
		fmt.Fprintln(out, "already migrated")
		return nil
	}
	migrateHolding()
	next := dbPath() + ".new"
	if err := removeDB(next); err != nil {
		return err
	}
	src := readSource()
	notes, diffs, err := buildDB(next, src)
	if err != nil {
		return errors.Join(err, removeDB(next))
	}
	live := 0
	for _, sc := range src.cards {
		if !sc.closed {
			live++
		}
	}
	for _, n := range notes {
		fmt.Fprintln(out, "note:", n)
	}
	if len(diffs) > 0 {
		for _, d := range diffs {
			fmt.Fprintln(out, "differs:", d)
		}
		fmt.Fprintf(out, "%d differences: nothing changed\n", len(diffs))
		return errors.Join(exitCode(1), removeDB(next))
	}
	backup, files, err := backupHome("pre-store", time.Now())
	if err != nil {
		return errors.Join(fmt.Errorf("backup: %w", err), removeDB(next))
	}
	for _, suffix := range []string{"-wal", "-shm", ""} {
		if err := os.Rename(next+suffix, dbPath()+suffix); err != nil && !(suffix != "" && errors.Is(err, fs.ErrNotExist)) {
			return err
		}
	}
	fmt.Fprintf(out, "imported %d live and %d archived cards, %d check records, %d events into %s\n",
		live, len(src.cards)-live, len(src.checks), len(src.events), dbFileName)
	fmt.Fprintf(out, "backup: %s (%d files)\n", backup, files)
	for _, p := range []string{checksPath(), logPath()} {
		if err := os.Rename(p, p+importedExt); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s holds the tracks now, but %s was not set aside: %w", dbFileName, filepath.Base(p), err)
		}
	}
	fmt.Fprintf(out, "renamed %s and %s to *%s\n", filepath.Base(checksPath()), filepath.Base(logPath()), importedExt)
	if err := renderEveryView(); err != nil {
		return fmt.Errorf("%s holds the tracks now, but the views were not all rendered (run tracks render): %w", dbFileName, err)
	}
	fmt.Fprintln(out, "rendered every card view from", dbFileName)
	return nil
}

// finishMigrate completes a migrate that died after the switch: it sets aside a checks.json or
// attention.jsonl still in place (unless an *.imported copy already holds that name), and
// renders every view when a live card's file lacks the generated line. Nothing reads those
// files once tracks.db exists, so this is tidying, not recovery of data.
func finishMigrate(out io.Writer) error {
	for _, p := range []string{checksPath(), logPath()} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if _, err := os.Stat(p + importedExt); err == nil {
			fmt.Fprintf(out, "note: %s and %s both exist; left both alone\n", filepath.Base(p), filepath.Base(p)+importedExt)
			continue
		}
		if err := os.Rename(p, p+importedExt); err != nil {
			return err
		}
		fmt.Fprintf(out, "renamed %s to %s\n", filepath.Base(p), filepath.Base(p)+importedExt)
	}
	paths, err := filepath.Glob(filepath.Join(home(), "cards", "*.md"))
	if err != nil {
		return err
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && !bytes.Contains(b, []byte("\ngenerated: ")) {
			if err := renderEveryView(); err != nil {
				return fmt.Errorf("the views were not all rendered (run tracks render): %w", err)
			}
			fmt.Fprintln(out, "rendered every card view from", dbFileName)
			break
		}
	}
	return nil
}

// renderEveryView writes every live card from the database, including a card the import read
// and nothing has written since, so each view carries the generated line and its own hash.
func renderEveryView() error {
	force := func(v store.ViewChange) (string, string, error) {
		v.Repair = false
		return writeView(v)
	}
	st, err := openDB(store.WithViews(force), store.WithRenderErrors(noteRenderFailure))
	if err != nil {
		return err
	}
	defer st.Close()
	return renderAll(st)
}

// removeDB deletes a database file and the files SQLite keeps beside it.
func removeDB(path string) error {
	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// backupHome archives every file under the home except the backup folder to
// backup/PREFIX-TIME.tar.gz, then reads the archive back and checks that it lists each of
// those files with its size. It returns the archive's path and how many files it holds.
func backupHome(prefix string, now time.Time) (string, int, error) {
	dir := filepath.Join(home(), backupDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, prefix+"-"+now.UTC().Format("20060102T150405Z")+".tar.gz")
	// The archive is written and verified under a .partial name, and only a verified one takes
	// the real name, so a crash mid-write never leaves a truncated archive that looks good.
	// A leftover .partial is a crashed run's and is ours to remove.
	partial := path + ".partial"
	if err := os.Remove(partial); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", 0, err
	}
	if err := writeBackup(partial); err != nil {
		return "", 0, err
	}
	n, err := verifyBackup(partial)
	if err != nil {
		os.Remove(partial)
		return "", 0, err
	}
	// Link, not rename: it fails rather than replace an archive already under the name.
	if err := os.Link(partial, path); err != nil {
		os.Remove(partial)
		return "", 0, err
	}
	os.Remove(partial)
	return path, n, nil
}

// verifyBackup checks that the archive lists every file the home holds, with its size, except
// the records a hook or render may rewrite meanwhile.
func verifyBackup(path string) (int, error) {
	want, err := homeFiles()
	if err != nil {
		return 0, err
	}
	for _, f := range []string{hookOKPath(), hookErrPath(), renderErrPath()} {
		delete(want, filepath.Base(f))
	}
	got, err := listBackup(path)
	if err != nil {
		return 0, fmt.Errorf("%s does not read back: %w", path, err)
	}
	for name, size := range want {
		if s, ok := got[name]; !ok {
			return 0, fmt.Errorf("%s does not list %s", path, name)
		} else if s != size {
			return 0, fmt.Errorf("%s lists %s with %d bytes, the file has %d", path, name, s, size)
		}
	}
	return len(want), nil
}

// walkHome calls fn for everything under the home except the home itself, the backup folder,
// the database migrate is building and tracks.db's own files: this process may hold SQLite
// locks on them, which closing any descriptor of them would drop. A migration's backup copies
// the database with SQLite instead.
func walkHome(fn func(p, rel string, info fs.FileInfo) error) error {
	building := dbFileName + ".new"
	return filepath.WalkDir(home(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home(), p)
		if err != nil || rel == "." {
			return err
		}
		if rel == backupDirName && d.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasPrefix(rel, building) || slices.Contains(dbFiles, rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(p, filepath.ToSlash(rel), info)
	})
}

// homeFiles is every regular file the backup must hold, with its size.
func homeFiles() (map[string]int64, error) {
	files := map[string]int64{}
	err := walkHome(func(_, rel string, info fs.FileInfo) error {
		if info.Mode().IsRegular() {
			files[rel] = info.Size()
		}
		return nil
	})
	return files, err
}

// writeBackup writes the archive, a new file: directories, regular files and symlinks. An
// archive it could not finish is removed.
func writeBackup(path string) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(path)
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = walkHome(func(p, rel string, info fs.FileInfo) error {
		var err error
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		case !info.Mode().IsRegular() && !info.IsDir():
			return nil // sockets and pipes hold no state
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// listBackup reads an archive to its end and returns its regular files with their sizes.
func listBackup(path string) (map[string]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	got := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return got, gz.Close()
		}
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(io.Discard, tr)
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			got[hdr.Name] = n
		}
	}
}
