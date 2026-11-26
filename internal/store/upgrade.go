package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
)

// migrateLockName is the lock file beside the database that one migration at a time holds. It
// is never the database: closing any descriptor of a file drops this process's SQLite locks on it.
const migrateLockName = "migrate.lock"

// ErrStale is returned by what needs the current schema while another process migrates.
var ErrStale = errors.New("tracks.db is being migrated by another process: try again")

// upgrade migrates a database on schema v under the migration lock, after the backup. A new
// database waits for the lock; an existing one that another process is migrating is left as it
// is for this run, marked stale.
func (s *Store) upgrade(v int) error {
	if v > schemaVersion {
		return fmt.Errorf("database schema is version %d, this build reads %d", v, schemaVersion)
	}
	unlock, ok, err := lockMigration(filepath.Join(filepath.Dir(s.path), migrateLockName), v == 0)
	if err != nil {
		return err
	}
	if !ok {
		s.stale = true
		return nil
	}
	defer unlock()
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v == schemaVersion {
		return err
	}
	if v > 0 && s.backup != nil {
		if err := s.backup(v, s.copyDB); err != nil {
			return fmt.Errorf("backup before migrating to schema %d: %w", schemaVersion, err)
		}
	}
	return s.migrate()
}

func lockMigration(path string, wait bool) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}

// copyDB writes a consistent copy of the database to dst, a file that must not exist.
func (s *Store) copyDB(dst string) error {
	_, err := s.db.Exec(`VACUUM INTO ?`, dst)
	return err
}

// CheckCopy opens a database copy read-only and checks that SQLite finds it intact.
func CheckCopy(path string) error {
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("%s fails integrity_check: %s", path, res)
	}
	return nil
}
