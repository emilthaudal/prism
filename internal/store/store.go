// Package store keeps track state in SQLite. Every writer is one transaction, and Render turns
// a track back into the card text the files hold today.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	filePerm = 0o600
	dirPerm  = 0o700
	// DefaultBusyTimeout is how long a writer waits for another writer before it fails.
	DefaultBusyTimeout = 5 * time.Second
	// timeLayout sorts as text, which created_at < at comparisons rely on.
	timeLayout = "2006-01-02T15:04:05.000000000Z"
)

var (
	ErrNoTrack     = errors.New("no such track")
	ErrNameTaken   = errors.New("a track with this name exists")
	ErrClosed      = errors.New("track is closed")
	ErrNotClosed   = errors.New("track is open")
	ErrBadMode     = errors.New("mode must be focus, background, waiting or parked")
	ErrCardChanged = errors.New("card changed during the check, next run retries")
	ErrBadName     = errors.New("not a track name")
	ErrStaleWrite  = errors.New("track changed since it was read: a write must read it in its own transaction")
)

type Store struct {
	db        *sql.DB
	now       func() time.Time
	views     ViewWriter
	renderErr func(error)
	afterRead func()
	// afterCommit runs between a write's commit and its render, for tests that let another
	// writer in there.
	afterCommit func()
	path        string
	file        os.FileInfo // the database file as opened, to notice it was moved away
	backup      BackupFunc
	// stale is set when another process held the migration lock: the database is still on an
	// older schema, which the old tables work with and the new ones wait for.
	stale bool
}

// inPlace is whether the database file is still the one this Store opened.
func (s *Store) inPlace() bool {
	if s.file == nil {
		return true
	}
	st, err := os.Stat(s.path)
	return err == nil && os.SameFile(st, s.file)
}

type options struct {
	busy      time.Duration
	now       func() time.Time
	create    bool
	views     ViewWriter
	renderErr func(error)
	afterRead func()
	backup    BackupFunc
}

// WithCreate lets Open make the database when it is missing; without it a missing file is an
// error, so "database present" can be the switch between the store and the files.
func WithCreate() Option { return func(o *options) { o.create = true } }

// Option configures Open.
type Option func(*options)

// BackupFunc backs up before a migration from schema from. copyDB writes a copy of the
// database to a new file with SQLite itself, so the caller never opens the database's files.
type BackupFunc func(from int, copyDB func(dst string) error) error

// WithBackup runs fn before a database of an older schema is migrated, outside any
// transaction, while this process holds the migration lock. An error stops the migration.
func WithBackup(fn BackupFunc) Option { return func(o *options) { o.backup = fn } }

// WithBusyTimeout sets how long a statement waits for the database lock; the hook uses 500 ms.
func WithBusyTimeout(d time.Duration) Option { return func(o *options) { o.busy = d } }

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// Open opens the database at path. A missing file is an error (an fs.ErrNotExist) unless
// WithCreate is given, which makes it (mode 0600) with its schema. The connection runs in WAL
// mode and every write transaction starts with BEGIN IMMEDIATE.
func Open(path string, opts ...Option) (*Store, error) {
	o := options{busy: DefaultBusyTimeout, now: time.Now, afterRead: func() {}}
	for _, f := range opts {
		f(&o)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if o.create {
		if err := os.MkdirAll(filepath.Dir(abs), dirPerm); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, filePerm)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "rw")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", o.busy.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	u := url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: o.now, views: o.views, renderErr: o.renderErr, afterRead: o.afterRead, path: abs, file: st, backup: o.backup}
	// A current schema needs no write lock, so readers never wait for a writer to open.
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		db.Close()
		return nil, err
	}
	if v != schemaVersion {
		if err := s.upgrade(v); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	return s.tx(func(tx *wtx) error {
		var v int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			return err
		}
		switch {
		case v > schemaVersion:
			return fmt.Errorf("database schema is version %d, this build reads %d", v, schemaVersion)
		case v == schemaVersion:
			return nil
		}
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		for _, col := range addedColumns {
			if err := ensureColumn(tx, col); err != nil {
				return err
			}
		}
		if v < sessionsSchema {
			if err := backfillSessions(tx, s.now()); err != nil {
				return err
			}
		}
		if v < questionsSchema {
			if err := s.mirrorAllOpen(tx); err != nil {
				return err
			}
		}
		if v < deliverySchema {
			if _, err := tx.Exec(`UPDATE questions SET delivered_at = answered_at WHERE from_kind = 'agent' AND answered_at IS NOT NULL AND delivered_at IS NULL`); err != nil {
				return err
			}
		}
		_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
		return err
	})
}

// addedColumns are columns a table of an older schema lacks: CREATE TABLE IF NOT EXISTS leaves
// such a table as it is.
var addedColumns = []struct{ table, name, def string }{
	{"desk_asks", "answer_kind", "TEXT NOT NULL DEFAULT ''"},
	{"desk_asks", "answer", "TEXT NOT NULL DEFAULT ''"},
	{"desk_asks", "answered_at", "TEXT"},
	{"desk_asks", "picked_up_at", "TEXT"},
	{"desk_log", "undone_at", "TEXT"},
	{"desk_asks", "raised_by", "TEXT NOT NULL DEFAULT ''"},
	{"tracks", "rank", "INTEGER"},
	{"tracks", "today", "TEXT NOT NULL DEFAULT ''"},
	{"questions", "recommendation", "TEXT NOT NULL DEFAULT ''"},
	{"questions", "ask_id", "INTEGER"},
	{"desk", "claude_session", "TEXT NOT NULL DEFAULT ''"},
	{"questions", "attachments", "TEXT NOT NULL DEFAULT '[]'"},
	{"sessions", "activity", "TEXT NOT NULL DEFAULT ''"},
	{"milestones", "pr", "TEXT"},
	{"milestones", "pr_state", "TEXT"},
}

func ensureColumn(tx *wtx, col struct{ table, name, def string }) error {
	rows, err := tx.Query(fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, col.table))
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		has = has || n == col.name
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil || has {
		return err
	}
	_, err = tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, col.table, col.name, col.def))
	return err
}

// wtx is a write transaction that remembers which tracks it changed, so their views are
// rendered once it has committed.
type wtx struct {
	*sql.Tx
	touched  []int64
	oldNames map[int64]string
	full     bool // RenderAll: every track's gone names are cleaned up too
}

func (w *wtx) touch(id int64) {
	if !slices.Contains(w.touched, id) {
		w.touched = append(w.touched, id)
	}
}

// tx runs fn in one write transaction and commits when it returns nil. The views of the tracks
// fn changed are rendered after the commit; a render that fails leaves the write committed and
// goes to the WithRenderErrors handler.
func (s *Store) tx(fn func(tx *wtx) error) error {
	w, err := s.commit(fn)
	if err != nil {
		return err
	}
	if s.afterCommit != nil {
		s.afterCommit()
	}
	if err := s.render(w); err != nil && s.renderErr != nil {
		s.renderErr(err)
	}
	return nil
}

// commit runs fn in one write transaction and commits it, refusing a database moved away.
func (s *Store) commit(fn func(tx *wtx) error) (*wtx, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	w := &wtx{Tx: tx, oldNames: map[int64]string{}}
	if !s.inPlace() {
		tx.Rollback()
		return nil, ErrMoved
	}
	if err := fn(w); err != nil {
		tx.Rollback()
		return nil, err
	}
	return w, tx.Commit()
}

// connQ gives a plain connection the same methods as a transaction.
type connQ struct{ c *sql.Conn }

func (q connQ) QueryRow(query string, args ...any) *sql.Row {
	return q.c.QueryRowContext(context.Background(), query, args...)
}

func (q connQ) Query(query string, args ...any) (*sql.Rows, error) {
	return q.c.QueryContext(context.Background(), query, args...)
}

func (q connQ) Exec(query string, args ...any) (sql.Result, error) {
	return q.c.ExecContext(context.Background(), query, args...)
}

// read runs fn against one consistent snapshot without taking the write lock; WAL readers never
// wait for a writer.
func (s *Store) read(fn func(q querier) error) error {
	ctx := context.Background()
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN DEFERRED`); err != nil {
		return err
	}
	defer c.ExecContext(ctx, `ROLLBACK`)
	return fn(connQ{c})
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmtTime(t)
}

// parseTime reads a stored time; a value in any other format is an error, never a zero time.
func parseTime(s sql.NullString) (time.Time, error) {
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(timeLayout, s.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored time %q is not in the store's format: %w", s.String, err)
	}
	return t, nil
}

// parseTimes parses each value into its destination, stopping at the first bad one.
func parseTimes(pairs ...any) error {
	for i := 0; i < len(pairs); i += 2 {
		t, err := parseTime(pairs[i].(sql.NullString))
		if err != nil {
			return err
		}
		*pairs[i+1].(*time.Time) = t
	}
	return nil
}

// Extra is what a card holds that the schema has no column for: frontmatter keys and sections
// kept from an import. Render gives it back.
type Extra struct {
	Meta     map[string]string `json:"meta,omitempty"`
	Sections map[string]string `json:"sections,omitempty"`
	// File is the archive file name an imported closed card had; export writes it back there.
	File string `json:"file,omitempty"`
}

func (e Extra) encode() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Track is one row of tracks. A zero time is a NULL (or, for UpdatedAt, a track nobody wrote yet).
type Track struct {
	ID                             int64
	Name, Mode                     string
	Tier, TierWhy                  string
	Dir                            string
	RexSession                     string
	ClaudeSession, ClaudeCwd       string
	Check, WaitingOn, Links        string
	Goal, Where, Standing, Later   string
	Extra                          Extra
	AnsweredAt, UpdatedAt          time.Time
	ChangedAt, CreatedAt, ClosedAt time.Time
	Version                        int64
	// RenderHash is the hash of the card file as last rendered, for spotting a hand edit.
	RenderHash string
	// Rank, Today and BlockedBy are Emil's and the desk's, like Mode. Rank 0 is no rank;
	// BlockedBy is read from blockers as the blocking tracks' current names, and save leaves it.
	Rank      int
	Today     string
	BlockedBy []string
}

func (t Track) Closed() bool { return !t.ClosedAt.IsZero() }

// Ask is one row of asks. N is its number among the open asks of its track, set by the readers.
type Ask struct {
	ID, TrackID                   int64
	N                             int
	Kind, Text                    string
	Position                      int
	CheckID                       int64
	CreatedAt, SeenAt, AnsweredAt time.Time
	ClosedAt                      time.Time
	ClosedBy                      string
}

func (a Ask) Open() bool { return a.ClosedAt.IsZero() }

// Event is one row of events.
type Event struct {
	ID         int64
	At         time.Time
	Event      string
	TrackID    int64
	TrackName  string
	Current    string // the track's name now; set by readers
	RexSession string
	RexBlock   string
	Cwd        string
	Len        int
	Kind       string
}

// Check is one row of checks.
type Check struct {
	ID, TrackID    int64
	Text           string
	Answer         bool
	AskedAt, RanAt time.Time
}

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

const trackCols = `id, name, mode, tier, tier_why, dir, rex_session, claude_session, claude_cwd, check_text,
  waiting_on, links, goal, where_text, standing, later, extra, answered_at, updated_at, changed_at,
  created_at, closed_at, version, render_hash, rank, today,
  (SELECT group_concat(b.name, ', ' ORDER BY k.position) FROM blockers k JOIN tracks b ON b.id = k.blocker_id
    WHERE k.track_id = tracks.id)`

type scanner interface{ Scan(dest ...any) error }

func scanTrack(r scanner) (Track, error) {
	var (
		t                                 Track
		extra                             string
		answered, updated, changed, creat sql.NullString
		closed                            sql.NullString
		rank                              sql.NullInt64
		blocked                           sql.NullString
	)
	err := r.Scan(&t.ID, &t.Name, &t.Mode, &t.Tier, &t.TierWhy, &t.Dir, &t.RexSession, &t.ClaudeSession,
		&t.ClaudeCwd, &t.Check, &t.WaitingOn, &t.Links, &t.Goal, &t.Where, &t.Standing, &t.Later, &extra,
		&answered, &updated, &changed, &creat, &closed, &t.Version, &t.RenderHash, &rank, &t.Today, &blocked)
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(extra), &t.Extra)
	t.Rank = int(rank.Int64)
	if blocked.String != "" {
		t.BlockedBy = strings.Split(blocked.String, blockerSep)
	}
	err = parseTimes(answered, &t.AnsweredAt, updated, &t.UpdatedAt, changed, &t.ChangedAt, creat, &t.CreatedAt, closed, &t.ClosedAt)
	return t, err
}

const askCols = `id, track_id, kind, text, position, check_id, created_at, seen_at, answered_at, closed_at, closed_by`

func scanAsk(r scanner) (Ask, error) {
	var (
		a                               Ask
		check                           sql.NullInt64
		created, seen, answered, closed sql.NullString
		closedBy                        sql.NullString
	)
	if err := r.Scan(&a.ID, &a.TrackID, &a.Kind, &a.Text, &a.Position, &check, &created, &seen, &answered, &closed, &closedBy); err != nil {
		return a, err
	}
	a.CheckID = check.Int64
	a.ClosedBy = closedBy.String
	err := parseTimes(created, &a.CreatedAt, seen, &a.SeenAt, answered, &a.AnsweredAt, closed, &a.ClosedAt)
	return a, err
}

func openAsks(q querier, trackID int64) ([]Ask, error) {
	rows, err := q.Query(`SELECT `+askCols+` FROM asks WHERE track_id = ? AND closed_at IS NULL ORDER BY position, id`, trackID)
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

func trackByID(q querier, id int64) (Track, error) {
	return scanTrack(q.QueryRow(`SELECT `+trackCols+` FROM tracks WHERE id = ?`, id))
}

// Resolved is the track a name points to. RenamedTo is the track's current name when it is not
// the one asked for, and Closed says the track was closed.
type Resolved struct {
	Track     Track
	RenamedTo string
	Closed    bool
}

// resolve follows name through names, which holds every name a track has had.
func resolve(q querier, name string) (Resolved, error) {
	var id int64
	err := q.QueryRow(`SELECT track_id FROM names WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Resolved{}, ErrNoTrack
	}
	if err != nil {
		return Resolved{}, err
	}
	t, err := trackByID(q, id)
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{Track: t, Closed: t.Closed()}
	if !strings.EqualFold(t.Name, name) {
		r.RenamedTo = t.Name
	}
	return r, nil
}

// save writes every column of t back, bumps version and changed_at, and moves updated_at too
// when the write is a change of content. It refuses a t read before another write landed
// (ErrStaleWrite), so a row read outside the transaction can never overwrite newer fields.
func (s *Store) save(q *wtx, t *Track, content bool) error {
	now := s.now()
	t.ChangedAt = now
	if content {
		t.UpdatedAt = now
	}
	read := t.Version
	t.Version++
	res, err := q.Exec(`UPDATE tracks SET name=?, mode=?, tier=?, tier_why=?, dir=?, rex_session=?, claude_session=?,
	  claude_cwd=?, check_text=?, waiting_on=?, links=?, goal=?, where_text=?, standing=?, later=?, extra=?,
	  answered_at=?, updated_at=?, changed_at=?, closed_at=?, version=?, rank=?, today=? WHERE id=? AND version=?`,
		t.Name, t.Mode, t.Tier, t.TierWhy, t.Dir, t.RexSession, t.ClaudeSession, t.ClaudeCwd, t.Check,
		t.WaitingOn, t.Links, t.Goal, t.Where, t.Standing, t.Later, t.Extra.encode(), nullTime(t.AnsweredAt),
		fmtTime(t.UpdatedAt), fmtTime(t.ChangedAt), nullTime(t.ClosedAt), t.Version, nullRank(t.Rank), t.Today, t.ID, read)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		t.Version = read
		return ErrStaleWrite
	}
	q.touch(t.ID)
	return nil
}

// seenEvent is whether Emil opened the track, typed into it or applied one of its asks.
func seenEvent(e Event) bool {
	return e.Event == "open" || e.Event == "answer" || e.Event == "prompt" && e.Kind != "system"
}

// logEvent inserts e, and for a seen event marks the track's open asks created before it as seen.
func logEvent(q querier, e Event) error {
	var track any
	if e.TrackID != 0 {
		track = e.TrackID
	}
	if _, err := q.Exec(`INSERT INTO events (at, event, track_id, track_name, rex_session, rex_block, cwd, len, kind)
	  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, fmtTime(e.At), e.Event, track, e.TrackName, e.RexSession, e.RexBlock, e.Cwd, e.Len, e.Kind); err != nil {
		return err
	}
	if e.TrackID != 0 && seenEvent(e) {
		_, err := q.Exec(`UPDATE asks SET seen_at = ? WHERE track_id = ? AND seen_at IS NULL AND closed_at IS NULL AND created_at < ?`,
			fmtTime(e.At), e.TrackID, fmtTime(e.At))
		return err
	}
	return nil
}

// Log records e as given, like a line appended to the attention log: at e.At (now when zero),
// for the track name points to, or with no track when name is "" or names none.
func (s *Store) Log(name string, e Event) error {
	return s.tx(func(tx *wtx) error {
		if e.At.IsZero() {
			e.At = s.now()
		}
		e.TrackID, e.TrackName = 0, name
		if name != "" {
			r, err := resolve(tx, name)
			switch {
			case err == nil:
				e.TrackID, e.TrackName = r.Track.ID, r.Track.Name
			case !errors.Is(err, ErrNoTrack):
				return err
			}
		}
		return logEvent(tx, e)
	})
}

// LogEvent records an event for the track name points to, such as the pane opening it.
func (s *Store) LogEvent(name string, e Event) error {
	return s.tx(func(tx *wtx) error {
		r, err := resolve(tx, name)
		if err != nil {
			return err
		}
		e.At, e.TrackID, e.TrackName = s.now(), r.Track.ID, r.Track.Name
		return logEvent(tx, e)
	})
}
