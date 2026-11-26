package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrMoved is a write that found the database file gone from its path: tracks export moved it
// aside while this writer waited, so the write belongs to the files now.
var ErrMoved = errors.New("tracks.db was moved away (rolled back to files)")

// ViewChange is a track whose view a render writes, as the database holds it now.
type ViewChange struct {
	// Track.RenderHash is the hash stored for the last render of its view.
	Track Track
	Asks  []Ask
	// Gone are names whose live view must not exist: the name before a rename, or, for a full
	// render, every name the track gave up.
	Gone []string
	// Closing is set for a closed track whose archive file was never written: it is written once.
	Closing bool
	// Repair is set by RenderAll: nothing changed, so a view that is already right stays as it is.
	Repair bool
}

// ViewWriter writes the files a track is shown as. It runs after the write committed, in a
// write transaction of its own, so views land in commit order and show only committed data. It
// returns the hash of the live view it wrote, and the archive file name when it wrote one. An
// error keeps the stored hash: the database stays right and the next render tries again.
type ViewWriter func(v ViewChange) (hash, archiveFile string, err error)

// WithViews makes every write render the views of the tracks it changed.
func WithViews(w ViewWriter) Option { return func(o *options) { o.views = w } }

// WithRenderErrors hands f every render that failed after its write committed, so the failure
// leaves a trace although the write itself succeeded.
func WithRenderErrors(f func(error)) Option { return func(o *options) { o.renderErr = f } }

// WithAfterRead runs f inside Card's transaction once it has read the track, for tests that
// start a competing writer there.
func WithAfterRead(f func()) Option { return func(o *options) { o.afterRead = f } }

// render writes the views of the tracks w changed, read again after w committed. Holding the
// write lock while it writes keeps a slower render from putting back an older view, and the
// hashes it stores are those of the files it wrote. A database moved away was rendered by export.
func (s *Store) render(w *wtx) error {
	if s.views == nil || len(w.touched) == 0 {
		return nil
	}
	var errs []error
	_, err := s.commit(func(r *wtx) error {
		errs = s.writeViews(r, w)
		return nil
	})
	if errors.Is(err, ErrMoved) {
		return nil
	}
	return errors.Join(append(errs, err)...)
}

// writeViews renders each track w touched from what r reads now. A track that fails is
// reported and skipped, so the hashes of the views that were written still commit.
func (s *Store) writeViews(r, w *wtx) []error {
	var errs []error
	for _, id := range w.touched {
		if err := s.writeView(r, w, id); err != nil {
			errs = append(errs, fmt.Errorf("view of track %d: %w", id, err))
		}
	}
	return errs
}

func (s *Store) writeView(r, w *wtx, id int64) error {
	t, err := trackByID(r, id)
	if err != nil {
		return err
	}
	v := ViewChange{Track: t, Closing: t.Closed() && t.Extra.File == "", Repair: w.full}
	if v.Closing {
		// The archive keeps the asks the track had when it closed, as the file code's does.
		v.Asks, err = asksClosedWith(r, t)
	} else {
		v.Asks, err = openAsks(r, id)
	}
	if err != nil {
		return err
	}
	if old, ok := w.oldNames[id]; ok {
		v.Gone = append(v.Gone, old)
	}
	if w.full {
		if v.Gone, err = trackNames(r, t); err != nil {
			return err
		}
	}
	hash, file, err := s.views(v)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name, err)
	}
	if file != "" && t.Extra.File == "" {
		t.Extra.File = file
		if _, err := r.Exec(`UPDATE tracks SET extra = ? WHERE id = ?`, t.Extra.encode(), id); err != nil {
			return err
		}
	}
	if hash != t.RenderHash {
		if _, err := r.Exec(`UPDATE tracks SET render_hash = ? WHERE id = ?`, hash, id); err != nil {
			return err
		}
	}
	return nil
}

// asksClosedWith is the asks closing t closed, in their order.
func asksClosedWith(q querier, t Track) ([]Ask, error) {
	rows, err := q.Query(`SELECT `+askCols+` FROM asks WHERE track_id = ? AND closed_by = ? AND closed_at = ? ORDER BY position, id`,
		t.ID, closedByClosed, fmtTime(t.ClosedAt))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		a.N = len(out) + 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// trackNames is every name of t that has no live view: all of them once it is closed, else
// the ones it was renamed away from.
func trackNames(q querier, t Track) ([]string, error) {
	rows, err := q.Query(`SELECT name FROM names WHERE track_id = ? ORDER BY name`, t.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if t.Closed() || !strings.EqualFold(n, t.Name) {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// RenderAll repairs the view of every track: a live card that is missing, stale or edited by
// hand is written again, and no live view stays for a name that is closed or renamed away.
// Archive files are left as they are.
func (s *Store) RenderAll() error {
	w := &wtx{full: true}
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT id FROM tracks ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			w.touch(id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	return s.render(w)
}

// Snapshot is the whole database, read in one transaction.
type Snapshot struct {
	Tracks []View
	Checks []NamedCheck
	Events []Event
	// Gone maps every name with no live view (closed, or renamed away) to its track's id.
	Gone map[string]int64
}

// Export reads everything in one write transaction and hands it to fn while the lock is held,
// so fn can move the database file away before any other writer commits: such a writer then
// fails with ErrMoved instead of landing in the moved file.
func (s *Store) Export(fn func(Snapshot) error) error {
	return s.tx(func(tx *wtx) error {
		var snap Snapshot
		var err error
		if snap.Tracks, err = allTracks(tx); err != nil {
			return err
		}
		if snap.Checks, err = allChecks(tx); err != nil {
			return err
		}
		if snap.Events, err = allEvents(tx); err != nil {
			return err
		}
		snap.Gone = map[string]int64{}
		for _, v := range snap.Tracks {
			names, err := trackNames(tx, v.Track)
			if err != nil {
				return err
			}
			for _, n := range names {
				snap.Gone[n] = v.Track.ID
			}
		}
		return fn(snap)
	})
}

// Watcher reads PRAGMA data_version on one connection of its own, which changes whenever
// another connection commits.
type Watcher struct {
	db *sql.DB
	c  *sql.Conn
}

// Watch opens path read-only for a Watcher. A missing file is an error.
func Watch(path string) (*Watcher, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", DefaultBusyTimeout.Milliseconds()))
	u := url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	c, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Watcher{db: db, c: c}, nil
}

// Version is the connection's data_version.
func (w *Watcher) Version() (int64, error) {
	var v int64
	err := w.c.QueryRowContext(context.Background(), `PRAGMA data_version`).Scan(&v)
	return v, err
}

func (w *Watcher) Close() error {
	w.c.Close()
	return w.db.Close()
}

// AsksClosedWith is the asks closing the track called name closed with it, in their order: the
// ones nobody answered. A track that is not closed has none.
func (s *Store) AsksClosedWith(name string) ([]Ask, error) {
	var out []Ask
	err := s.read(func(q querier) error {
		r, err := resolve(q, name)
		if err != nil {
			return err
		}
		if !r.Closed {
			return nil
		}
		out, err = asksClosedWith(q, r.Track)
		return err
	})
	return out, err
}
