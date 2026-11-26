package tracks

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	answeredAsksKey = "answered_asks"
	dbFileName      = "tracks.db"
	maxShown        = 60
)

func dbPath() string { return filepath.Join(home(), dbFileName) }

// sourceCard is a card file as the file code reads it.
type sourceCard struct {
	name   string
	card   *Card
	mtime  time.Time
	closed bool
	file   string // archive file name
	hash   string // of a live card's file
}

// source is everything the file code keeps, read without writing anything.
type source struct {
	cards    []sourceCard // live cards by file name, then the archive
	checks   map[string]checkState
	events   []Event // as logged
	followed []Event // the same, each under the name its track has now
	problems []string
	// kept is the check record each (current track name, check text) imports from; keptFrom is
	// the name that record was stored under.
	kept     map[[2]string]checkState
	keptFrom map[[2]string]string
}

func readSource() source {
	var s source
	s.readLive()
	s.readClosed()
	var err error
	if s.checks, err = loadChecksFile(); err != nil {
		s.problems = append(s.problems, "checks.json: "+err.Error())
	}
	s.readEvents()
	return s
}

func (s *source) readLive() {
	files, _ := filepath.Glob(filepath.Join(home(), "cards", "*.md"))
	slices.Sort(files)
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") {
			continue
		}
		name := strings.TrimSuffix(base, ".md")
		b, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			s.problems = append(s.problems, fmt.Sprintf("card %s: %v (not imported)", name, err))
			continue
		}
		if msg := cardError(string(b)); msg != "" {
			s.problems = append(s.problems, fmt.Sprintf("card %s: %s (not imported)", name, msg))
			continue
		}
		sc := sourceCard{name: name, card: parseCard(name, string(b)), mtime: time.Now(), hash: hashText(b)}
		if st, err := os.Stat(f); err == nil {
			sc.mtime = st.ModTime()
		}
		s.cards = append(s.cards, sc)
	}
}

func (s *source) readClosed() {
	files, _ := filepath.Glob(filepath.Join(archiveDir(), "*.md"))
	slices.Sort(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			s.problems = append(s.problems, fmt.Sprintf("archive %s: %v (not imported)", filepath.Base(f), err))
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f), ".md")
		c := parseCard(base, string(b))
		sc := sourceCard{name: base, card: c, mtime: time.Now(), closed: true, file: filepath.Base(f)}
		if n := c.Meta["name"]; n != "" {
			sc.name = n
		}
		if st, err := os.Stat(f); err == nil {
			sc.mtime = st.ModTime()
		}
		s.cards = append(s.cards, sc)
	}
}

func (s *source) readEvents() {
	f, err := os.Open(logPath())
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			s.events = append(s.events, e)
		}
	}
	s.followed = slices.Clone(s.events)
	followRenames(s.followed)
}

var (
	tierWhyLine = regexp.MustCompile(`^Tier ([123]): (.+)$`)
	// metaColumns are the frontmatter keys the schema has a column for.
	metaColumns = []string{"name", "mode", "tier", "dir", "rex_session", "waiting_on", "check", "links", "updated",
		"claude_session", "claude_cwd", "answered", answeredAsksKey, "rank", "today", "blocked_by"}
	// metaTimes are the frontmatter keys that hold a time.
	metaTimes = []string{"updated", "answered", "closed"}
)

// importData is what the store takes: tracks, asks, checks and events in the order the files
// hold them, and the problems that stopped something from being imported.
func (s *source) importData() store.ImportData {
	var d store.ImportData
	s.keepChecks()
	retired := map[string][]store.RetiredName{}
	for i, e := range s.events {
		if e.Event == renameEvent && e.Kind != "" && !strings.EqualFold(e.Kind, s.followed[i].Track) {
			now := s.followed[i].Track
			retired[now] = append(retired[now], store.RetiredName{Name: e.Kind, At: e.At})
		}
	}
	for _, sc := range s.cards {
		d.Tracks = append(d.Tracks, s.importTrack(sc, retired[sc.name]))
	}
	for slot, st := range s.kept {
		asked, _ := time.Parse(time.RFC3339Nano, st.Asked)
		d.Checks = append(d.Checks, store.ImportCheck{Track: slot[0], Text: slot[1], Answer: st.Answer, AskedAt: asked, RanAt: st.RanAt})
	}
	slices.SortFunc(d.Checks, func(a, b store.ImportCheck) int {
		return strings.Compare(a.Track+"\x00"+a.Text, b.Track+"\x00"+b.Text)
	})
	for i, e := range s.events {
		d.Events = append(d.Events, store.ImportEvent{
			Event: store.Event{At: e.At, Event: e.Event, TrackName: e.Track, RexSession: e.RexSession, RexBlock: e.RexBlock,
				Cwd: e.Cwd, Len: e.Len, Kind: e.Kind},
			Owner: s.followed[i].Track,
		})
	}
	return d
}

func (s *source) importTrack(sc sourceCard, retired []store.RetiredName) store.ImportTrack {
	c := sc.card
	t := store.Track{
		Name: sc.name, Mode: c.Meta["mode"], Tier: c.Meta["tier"], Dir: c.Meta["dir"], RexSession: c.Meta["rex_session"],
		ClaudeSession: c.Meta["claude_session"], ClaudeCwd: c.Meta["claude_cwd"], Check: c.Meta["check"],
		WaitingOn: c.Meta["waiting_on"], Links: c.Meta["links"],
		Goal: c.Sections["Goal"], Standing: c.Sections["Standing orders"], Later: c.Sections["Later"],
		AnsweredAt: metaTime(c, "answered"), UpdatedAt: importedUpdated(sc), ChangedAt: sc.mtime,
		RenderHash: sc.hash, Today: c.Meta["today"],
	}
	if r, err := strconv.Atoi(c.Meta["rank"]); err == nil && r > 0 {
		t.Rank = r
	}
	// The file's mtime, its last content change on the files, keeps ls --json's updated as it was.
	t.CreatedAt = sc.mtime
	whereKey := "Where it is"
	if sc.closed {
		whereKey = "Outcome"
		if t.ClosedAt = parseClosed(c.Meta["closed"]); t.ClosedAt.IsZero() {
			t.ClosedAt = sc.mtime
		}
	}
	t.Where = c.Sections[whereKey]
	if m := tierWhyLine.FindStringSubmatch(lastLine(t.Where)); m != nil && m[1] == t.Tier {
		t.TierWhy = m[2]
		t.Where = strings.TrimSuffix(strings.TrimSuffix(t.Where, lastLine(t.Where)), "\n")
	}
	extra := store.Extra{Meta: map[string]string{}, Sections: map[string]string{}, File: sc.file}
	for k, v := range c.Meta {
		if !slices.Contains(metaColumns, k) && !(sc.closed && k == "closed") {
			extra.Meta[k] = v
		}
	}
	for k, v := range c.Sections {
		if !slices.Contains([]string{"Goal", whereKey, "Needs me", "Standing orders", "Later"}, k) {
			extra.Sections[k] = v
		}
	}
	t.Extra = extra

	it := store.ImportTrack{Track: t, Retired: retired, BlockedBy: splitNames(c.Meta["blocked_by"])}
	answeredNs, answeredAt := cardAnsweredAsks(c), time.Time{}
	if stamp, _, ok := strings.Cut(strings.TrimSpace(c.Meta[answeredAsksKey]), " "); ok {
		answeredAt, _ = time.Parse(time.RFC3339, stamp)
	}
	for _, a := range c.askList() {
		ia := store.ImportAsk{Kind: "agent", Text: a.text, CreatedAt: sc.mtime}
		if slices.Contains(answeredNs, a.n) {
			ia.AnsweredAt = answeredAt
		}
		linked := true
		if isCheckAsk(a.text) {
			// A check ask is as old as the record that added it, and seen by the old code's own test.
			ia.Kind, linked = "check", false
			if m := checkAskQuote.FindStringSubmatch(a.text); m != nil {
				chk, err := strconv.Unquote(m[1])
				rec, ok := s.kept[[2]string{sc.name, chk}]
				// The ask always links to its record, so a flip closes it. Without a readable asked
				// the old code never counts it seen, so it gets no seen_at.
				if err == nil && ok && rec.Answer == (m[2] == "yes") {
					ia.CheckText = chk
					if asked, perr := time.Parse(time.RFC3339Nano, rec.Asked); perr == nil {
						linked, ia.CreatedAt = true, asked
					}
				}
			}
		}
		if i := slices.IndexFunc(s.followed, func(e Event) bool {
			return linked && e.Track == sc.name && e.At.After(ia.CreatedAt) && seenSince([]Event{e}, sc.name, ia.CreatedAt)
		}); i >= 0 {
			ia.SeenAt = s.followed[i].At
		}
		it.Asks = append(it.Asks, ia)
	}
	return it
}

// keepChecks picks the record of checks.json each (track, check) imports from. Records are
// keyed "NAME: CHECK" under whatever name the track had; the one under its current name wins,
// and every record that is dropped, or that no card claims, is a problem.
func (s *source) keepChecks() {
	cards := map[string]string{}
	for _, sc := range s.cards {
		cards[strings.ToLower(sc.name)] = sc.name
	}
	s.kept, s.keptFrom = map[[2]string]checkState{}, map[[2]string]string{}
	for _, k := range slices.Sorted(maps.Keys(s.checks)) {
		name, text, ok := strings.Cut(k, ": ")
		if !ok {
			continue
		}
		final, ok := cards[strings.ToLower(name)]
		if !ok {
			// followed rename events carry the final name, so a chain a→b→c resolves to c.
			final, ok = cards[strings.ToLower(renamedTo(s.followed, name))]
		}
		if !ok {
			s.problems = append(s.problems, fmt.Sprintf("check %q of %s: no track has a card of that name, not imported", text, name))
			continue
		}
		slot := [2]string{final, text}
		prev, taken := s.keptFrom[slot]
		switch {
		case !taken:
		case name == final:
			s.problems = append(s.problems, fmt.Sprintf("check %q of %s: stored under %s and %s, kept the record under %s", text, final, prev, name, name))
		default:
			s.problems = append(s.problems, fmt.Sprintf("check %q of %s: stored under %s and %s, kept the record under %s", text, final, prev, name, prev))
			continue
		}
		s.kept[slot], s.keptFrom[slot] = s.checks[k], name
	}
}

// importedUpdated is the card's updated: stamp, but never later than the file's mtime: the
// content cannot have changed after the file last did, and a stamp past the mtime would make
// contentAt report the stamp once a mode key or the hook moves changed_at past it.
func importedUpdated(sc sourceCard) time.Time {
	u := metaTime(sc.card, "updated")
	if u.After(sc.mtime) {
		return sc.mtime
	}
	return u
}

func lastLine(s string) string { return s[strings.LastIndex(s, "\n")+1:] }

// normalised is a card's meta and sections with what the file code treats as nothing taken
// out: empty values, and time stamps written in another layout of the same instant.
func normalised(c *Card, name string) (meta, secs map[string]string) {
	meta, secs = map[string]string{}, map[string]string{}
	for k, v := range c.Meta {
		if slices.Contains(metaTimes, k) {
			if t := metaTime(c, k); !t.IsZero() {
				v = t.UTC().Format(time.RFC3339)
			}
		}
		if v != "" {
			meta[k] = v
		}
	}
	if meta["name"] == "" {
		meta["name"] = name
	}
	for k, v := range c.Sections {
		if v != "" {
			secs[k] = v
		}
	}
	return meta, secs
}

func shown(s string) string {
	if r := []rune(s); len(r) > maxShown {
		s = string(r[:maxShown]) + "..."
	}
	return strconv.Quote(s)
}

func diffMaps(what, owner string, a, b map[string]string) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []string
	for _, k := range slices.Sorted(mapKeys(keys)) {
		if a[k] != b[k] {
			out = append(out, fmt.Sprintf("%s: %s %s: file has %s, database renders %s", owner, what, k, shown(a[k]), shown(b[k])))
		}
	}
	return out
}

func mapKeys(m map[string]bool) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// cardDiffs compares a card with the card the database renders for it. A closed card that
// had no closed: line gets one from the file's mtime, which is not a difference.
func cardDiffs(sc sourceCard, v store.View) []string {
	got := parseCard(sc.name, store.Render(v.Track, v.Asks))
	owner := "card " + sc.name
	if sc.closed {
		owner = "archived card " + sc.file
	}
	sm, ss := normalised(sc.card, sc.name)
	gm, gs := normalised(got, sc.name)
	if sc.closed && sm["closed"] == "" {
		delete(gm, "closed")
	}
	// answered_asks moves to the asks' answered_at, which the card does not render.
	delete(sm, answeredAsksKey)
	if u := importedUpdated(sc); !u.IsZero() {
		sm["updated"] = u.UTC().Format(time.RFC3339)
	}
	return append(diffMaps("meta", owner, sm, gm), diffMaps("section", owner, ss, gs)...)
}

// seenDiffs compares, for each check ask on the card, the old code's answer to "has Emil seen
// it" (checkAskAnswered, from the files) with whether the ask's seen_at is set. An ask whose
// record still sits under an old name is skipped: the old code looks it up by the current name
// only, so it says unseen until the next tracks check moves the record, while the import
// already follows the rename, which is where the old code lands after that run.
func seenDiffs(sc sourceCard, v store.View, keptFrom map[[2]string]string) []string {
	var out []string
	seen := checkAskAnswered(sc.name)
	for _, a := range sc.card.askList() {
		if !isCheckAsk(a.text) {
			continue
		}
		if m := checkAskQuote.FindStringSubmatch(a.text); m != nil {
			if chk, err := strconv.Unquote(m[1]); err == nil {
				if from, ok := keptFrom[[2]string{sc.name, chk}]; ok && !strings.EqualFold(from, sc.name) {
					continue
				}
			}
		}
		i := slices.IndexFunc(v.Asks, func(x store.Ask) bool { return x.Text == a.text })
		if i < 0 {
			continue // the card comparison reports a missing ask
		}
		if old, got := seen(a.text), !v.Asks[i].SeenAt.IsZero(); old != got {
			out = append(out, fmt.Sprintf("card %s: ask %d: the file code says seen is %t, the database has seen_at %s",
				sc.name, a.n, old, map[bool]string{true: "set", false: "empty"}[got]))
		}
	}
	return out
}

// lsFromStore is what `tracks ls --json` lists when the database is the source: the live
// tracks in the order of their file names, each from its rendered card.
func lsFromStore(views []store.View) []lsTrack {
	var live []store.View
	for _, v := range views {
		if !v.Track.Closed() {
			live = append(live, v)
		}
	}
	slices.SortFunc(live, func(a, b store.View) int { return strings.Compare(a.Track.Name+".md", b.Track.Name+".md") })
	out := []lsTrack{}
	for _, v := range live {
		out = append(out, lsTrackOf(parseCard(v.Track.Name, store.Render(v.Track, v.Asks)), contentAt(v.Track).UTC()))
	}
	return out
}

// lsDiffs compares two ls --json track lists, ignoring each ask's since.
func lsDiffs(files, db []lsTrack) []string {
	byName := func(ts []lsTrack) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, t := range ts {
			asks := slices.Clone(t.Asks)
			for i := range asks {
				asks[i].Since = nil
			}
			t.Asks = asks
			b, _ := json.Marshal(t)
			var doc map[string]any
			_ = json.Unmarshal(b, &doc)
			m[t.Name] = doc
		}
		return m
	}
	f, d := byName(files), byName(db)
	names := map[string]bool{}
	for n := range f {
		names[n] = true
	}
	for n := range d {
		names[n] = true
	}
	var out []string
	for _, n := range slices.Sorted(mapKeys(names)) {
		fd, fok := f[n]
		dd, dok := d[n]
		switch {
		case !dok:
			out = append(out, fmt.Sprintf("ls --json: %s is listed from the files, not from the database", n))
		case !fok:
			out = append(out, fmt.Sprintf("ls --json: %s is listed from the database, not from the files", n))
		default:
			for k := range fd {
				if !reflect.DeepEqual(fd[k], dd[k]) {
					fb, _ := json.Marshal(fd[k])
					db, _ := json.Marshal(dd[k])
					out = append(out, fmt.Sprintf("ls --json: %s: %s: files %s, database %s", n, k, shown(string(fb)), shown(string(db))))
				}
			}
		}
	}
	return out
}

// buildDB imports the files into a new database at path and verifies it against them.
func buildDB(path string, src source) (notes, diffs []string, err error) {
	st, err := store.Open(path, store.WithCreate())
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	if notes, err = st.Import(src.importData()); err != nil {
		return nil, nil, err
	}
	views, err := st.AllTracks()
	if err != nil {
		return nil, nil, err
	}
	diffs = slices.Clone(src.problems)
	if len(views) != len(src.cards) {
		diffs = append(diffs, fmt.Sprintf("the database holds %d tracks for %d cards", len(views), len(src.cards)))
	} else {
		for i, sc := range src.cards {
			diffs = append(diffs, cardDiffs(sc, views[i])...)
			diffs = append(diffs, seenDiffs(sc, views[i], src.keptFrom)...)
		}
	}
	diffs = append(diffs, lsDiffs(loadAllCardsFile(), lsFromStore(views))...)
	return notes, diffs, nil
}

func migrateCmd(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	check := fl.Bool("check", false, "build the database in a temp file, compare it with the files and touch nothing else")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return errors.New("usage: tracks migrate [--check]")
	}
	if !*check {
		return migrate(out)
	}
	if dbExists() {
		return fmt.Errorf("%s is already in %s: the card files are its views, so there is nothing to check", dbFileName, home())
	}
	dir, err := os.MkdirTemp("", "tracks-migrate-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	src := readSource()
	notes, diffs, err := buildDB(filepath.Join(dir, dbFileName), src)
	if err != nil {
		return err
	}
	live := 0
	for _, sc := range src.cards {
		if !sc.closed {
			live++
		}
	}
	fmt.Fprintf(out, "checked %d live and %d archived cards, %d check records, %d events\n",
		live, len(src.cards)-live, len(src.checks), len(src.events))
	for _, n := range notes {
		fmt.Fprintln(out, "note:", n)
	}
	for _, d := range diffs {
		fmt.Fprintln(out, "differs:", d)
	}
	if len(diffs) > 0 {
		fmt.Fprintf(out, "%d differences\n", len(diffs))
		return exitCode(1)
	}
	fmt.Fprintln(out, "no differences")
	return nil
}

// archiveFile is the file a closed track's card goes to: the one it was imported from, else
// DATE-NAME.md, with -2 and up when another track's card is there.
func archiveFile(t store.Track) string {
	if t.Extra.File != "" {
		return t.Extra.File
	}
	date := t.ClosedAt.Local().Format(closeDateLayout)
	for i := 1; i <= maxArchiveTries; i++ {
		f := fmt.Sprintf("%s-%s.md", date, t.Name)
		if i > 1 {
			f = fmt.Sprintf("%s-%s-%d.md", date, t.Name, i)
		}
		b, err := os.ReadFile(filepath.Join(archiveDir(), f))
		if err != nil || parseCard(t.Name, string(b)).Meta["name"] == t.Name {
			return f
		}
	}
	return fmt.Sprintf("%s-%s.md", date, t.Name)
}

func writeDated(path, text string, at time.Time) error {
	if err := writeAtomic(path, []byte(text)); err != nil {
		return err
	}
	return os.Chtimes(path, at, at)
}

// exportHolding runs while export holds the write lock, before it writes anything; tests start
// a competing writer there.
var exportHolding = func() {}

// exportCmd writes the database back to the files the previous release reads, then moves the
// database aside. Card mtimes are set to the track's contentAt, which ls reports as updated.
func exportCmd(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: tracks export")
	}
	st, err := openDB()
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no %s in %s: nothing to export", dbFileName, home())
	}
	if err != nil {
		return err
	}
	defer st.Close()
	aside := dbPath() + ".rolled-back-" + time.Now().UTC().Format("20060102T150405Z")
	var snap store.Snapshot
	// One transaction from the reads through the move: a writer that waits for it finds the
	// database gone and writes the files instead, so nothing commits in between and is lost.
	err = st.Export(func(s store.Snapshot) error {
		snap = s
		exportHolding()
		return withCardsLock(func() error {
			if err := exportFiles(s); err != nil {
				return err
			}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if err := os.Rename(dbPath()+suffix, aside+suffix); err != nil && !(suffix != "" && errors.Is(err, fs.ErrNotExist)) {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	if err := st.Close(); err != nil {
		return err
	}
	// Opening and closing the moved database folds its -wal back in and removes -wal and -shm.
	if moved, err := store.Open(aside); err == nil {
		moved.Close()
	}
	fmt.Fprintf(out, "exported %d tracks, %d check records and %d events to %s\nmoved %s to %s\n",
		len(snap.Tracks), len(snap.Checks), len(snap.Events), home(), dbFileName, filepath.Base(aside))
	return nil
}

// exportFiles writes a snapshot as the files the file code reads: every live card, an archive
// file for a closed track that has none, checks.json and attention.jsonl. A card file left for
// a name that is closed or renamed away is removed, unless it was edited by hand.
func exportFiles(s store.Snapshot) error {
	hashes := map[int64]string{}
	for _, v := range s.Tracks {
		hashes[v.Track.ID] = v.Track.RenderHash
	}
	for _, n := range slices.Sorted(maps.Keys(s.Gone)) {
		removeView(n, hashes[s.Gone[n]])
	}
	for _, v := range s.Tracks {
		path := cardPath(v.Track.Name)
		if v.Track.Closed() {
			path = filepath.Join(archiveDir(), archiveFile(v.Track))
			if _, err := os.Stat(path); err == nil {
				continue
			}
		} else {
			keepEdited(path, v.Track.Name, v.Track.RenderHash)
		}
		if err := writeDated(path, store.Render(v.Track, v.Asks), contentAt(v.Track)); err != nil {
			return err
		}
	}
	stored := map[string]checkState{}
	for _, c := range s.Checks {
		stored[c.Track+": "+c.Text] = checkStateOf(c.Check)
	}
	b, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(checksPath(), append(b, '\n')); err != nil {
		return err
	}
	var log strings.Builder
	for _, e := range s.Events {
		b, err := json.Marshal(Event{At: e.At, Event: e.Event, RexSession: e.RexSession, RexBlock: e.RexBlock,
			Track: e.TrackName, Cwd: e.Cwd, Len: e.Len, Kind: e.Kind})
		if err != nil {
			return err
		}
		log.Write(b)
		log.WriteByte('\n')
	}
	return writeAtomic(logPath(), []byte(log.String()))
}
