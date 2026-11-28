package tracks

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

// noCardErr maps "no such track" and "closed" to what a missing card file gave the file code.
func noCardErr(err error) error {
	if errors.Is(err, store.ErrNoTrack) || errors.Is(err, store.ErrClosed) {
		return fs.ErrNotExist
	}
	return err
}

func cardStore(name string, u *cardUpdate, out io.Writer) error {
	cu := store.CardUpdate{TierWhy: u.tierWhy, Goal: u.goal, Asks: u.asks}
	meta := func(k string) *string {
		if v, ok := u.meta[k]; ok {
			return &v
		}
		return nil
	}
	cu.Tier, cu.Dir, cu.Links, cu.Check, cu.WaitingOn = meta("tier"), meta("dir"), meta("links"), meta("check"), meta("waiting_on")
	cu.Role, cu.Today = meta("role"), meta("today")
	if v := meta("rank"); v != nil {
		n, _ := strconv.Atoi(*v) // parseCardInput took only a whole number from 1, or empty for none
		cu.Rank = &n
	}
	if v := meta("blocked_by"); v != nil {
		names := splitNames(*v)
		cu.BlockedBy = &names
	}
	if u.where != nil {
		w := strings.Join(u.where, "\n")
		cu.Where = &w
	}
	if u.later != nil {
		l := strings.Join(u.later, "\n")
		cu.Later = &l
	}
	var res store.CardResult
	err := withStore(func(st *store.Store) (err error) {
		res, err = st.Card(name, cu)
		return err
	}, store.WithAfterRead(func() { afterCardRead() }))
	if errors.Is(err, store.ErrNoTrack) {
		return fmt.Errorf("no card for %s: run tracks bind %s first", name, name)
	}
	if err != nil {
		return err
	}
	if res.RenamedTo != "" {
		fmt.Fprintf(out, "renamed to %s\n", res.RenamedTo)
	}
	if res.Closed {
		// The track closed while its session was still stopping: its last report must not vanish with the
		// write. Keep it whole in the handled log, under the track's name, on one clean line.
		kept := false
		if u.where != nil {
			track := name
			if res.RenamedTo != "" {
				track = res.RenamedTo
			}
			if text := strings.Join(strings.Fields(cleanText(strings.Join(u.where, " "))), " "); text != "" {
				kept = withStore(func(st *store.Store) error {
					_, err := st.LogDesk(track, "noted", "", "last report from the session, after the track closed: "+text)
					return err
				}) == nil
			}
		}
		fmt.Fprintln(out, "closed")
		if kept {
			fmt.Fprintln(out, "your where: was kept in the handled log as the track's last report")
		}
		return nil
	}
	if len(res.Asks) == 0 {
		fmt.Fprintln(out, "no asks")
	}
	for i, a := range res.Asks {
		fmt.Fprintf(out, "%d. %s\n", i+1, a.Text)
	}
	return nil
}

func bindStore(name string, force bool, out io.Writer) error {
	dir, _ := os.Getwd()
	if !cardValueOK(dir) {
		dir = ""
	}
	var (
		r    store.Resolved
		text string
	)
	if !force {
		// A session whose process is gone, with no SessionEnd, must not hold the track.
		reapDue()
	}
	err := withStore(func(st *store.Store) (err error) {
		if !force {
			holders, err := st.Holders(name, os.Getenv("CLAUDE_CODE_SESSION_ID"))
			if err != nil {
				return err
			}
			if len(holders) > 0 {
				return heldError(name, holders[0])
			}
		}
		if r, err = st.Bind(name, os.Getenv("REX_SESSION"), dir); err != nil || r.Closed || r.RenamedTo != "" {
			return err
		}
		v, err := st.Get(r.Track.Name)
		if err != nil {
			return err
		}
		text = store.RenderView(v.Track, v.Asks)
		orders, err := st.Orders()
		text += ordersBlock(orders)
		return err
	})
	if err != nil {
		return err
	}
	if r.RenamedTo != "" {
		fmt.Fprintf(out, "renamed to %s\n", r.RenamedTo)
	}
	if r.Closed {
		fmt.Fprintln(out, "closed")
	}
	if r.Closed || r.RenamedTo != "" {
		return nil
	}
	_, err = io.WriteString(out, text)
	return err
}

// heldError refuses a bind to a track that the live session s holds: two sessions on one track
// work over each other's branch and card (EMI-427).
func heldError(name string, s store.Session) error {
	whose := "Emil's"
	if s.Owner == "desk" {
		whose = "the desk's"
	}
	state := s.Status
	if s.Activity != "" {
		state += ", " + s.Activity
	}
	how := fmt.Sprintf("Take that session over with claude attach %s, or stop it (claude stop %s) and bind again", shortID(s.ID), shortID(s.ID))
	if s.Host == "rex" {
		how = "Work in that session's Rex pane, or end it and bind again"
	}
	return fmt.Errorf("refused: track %s is held by %s session %s (%s), which still runs; a second session would work over it. "+
		"%s; tracks bind --force %s shares the track anyway", name, whose, shortID(s.ID), state, how, name)
}

func newCardStore(name, mode, dir string) error {
	return withStore(func(st *store.Store) error {
		v, err := st.Get(name)
		switch {
		case err == nil && v.RenamedTo != "":
			return fmt.Errorf("%s was renamed to %s: use /track %s", name, v.RenamedTo, v.RenamedTo)
		case err == nil && v.Closed:
			return fmt.Errorf("track %s was closed (%s): it is not recreated", name, archiveFile(v.Track))
		case err == nil:
			return fmt.Errorf("card %s already exists", cardPath(name))
		case !errors.Is(err, store.ErrNoTrack):
			return err
		}
		_, err = st.NewTrack(store.NewTrackSpec{Name: name, Mode: mode, Dir: dir, Goal: defaultGoal})
		if errors.Is(err, store.ErrNameTaken) {
			return fmt.Errorf("card %s already exists", cardPath(name))
		}
		return err
	})
}

// closeStore closes the live track called name; it returns its archive file and Rex session.
func closeStore(name string, now time.Time) (dest, sess string, err error) {
	var v store.View
	err = withStore(func(st *store.Store) error {
		if v, err = st.Get(name); err != nil || v.Closed || v.RenamedTo != "" {
			return fmt.Errorf("no card for %q", name)
		}
		if err := st.CloseTrack(name); err != nil {
			return err
		}
		v, err = st.Get(name)
		return err
	}, store.WithClock(func() time.Time { return now }))
	if err != nil {
		return "", "", err
	}
	return filepath.Join(archiveDir(), archiveFile(v.Track)), v.Track.RexSession, nil
}

// renameStore is the database half of renameTrack; it returns the renamed track's card.
func renameStore(old, name string) (*Card, error) {
	var v store.View
	err := withStore(func(st *store.Store) error {
		cur, err := st.Get(old)
		switch {
		case errors.Is(err, store.ErrNoTrack) || err == nil && cur.RenamedTo != "":
			return fmt.Errorf("no card for %q", old)
		case err != nil:
			return err
		case cur.Closed:
			return fmt.Errorf("track %s is closed (%s): a closed track is not renamed", old, archiveFile(cur.Track))
		case cur.Track.Name != old:
			return fmt.Errorf("no card named exactly %q (names are case-sensitive)", old)
		}
		other, err := st.Get(name)
		switch {
		case err == nil && other.RenamedTo != "":
			return fmt.Errorf("%s was the name of a track renamed to %s: pick another", name, other.RenamedTo)
		case err == nil && other.Closed:
			return fmt.Errorf("%s is the name of a closed track (%s): pick another", name, archiveFile(other.Track))
		case err == nil:
			return fmt.Errorf("a track named %s exists", name)
		case !errors.Is(err, store.ErrNoTrack):
			return err
		}
		if err := st.Rename(old, name); err != nil {
			if errors.Is(err, store.ErrNameTaken) {
				return fmt.Errorf("a track named %s exists", name)
			}
			return err
		}
		v, err = st.Get(name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return cardOf(v), nil
}

// setMetasStore is setMetas on the database. answered_asks, the pane's "TIME N,N" record of
// the asks one answer accepted, goes to the asks' answered_at.
func setMetasStore(name string, kv [][2]string) error {
	m := map[string]string{}
	for _, p := range kv {
		m[p[0]] = p[1]
	}
	return noCardErr(withStore(func(st *store.Store) error {
		if v, ok := m[answeredAsksKey]; ok {
			delete(m, answeredAsksKey)
			stamp, list, _ := strings.Cut(strings.TrimSpace(v), " ")
			at, _ := time.Parse(time.RFC3339, stamp)
			var ns []int
			for _, f := range strings.Split(list, ",") {
				if n, err := strconv.Atoi(f); err == nil {
					ns = append(ns, n)
				}
			}
			if err := st.SetAnswered(name, ns, at); err != nil {
				return err
			}
		}
		if len(m) == 0 {
			return nil
		}
		return st.SetMeta(name, m)
	}))
}

func setModeStore(name, mode string) error {
	return noCardErr(withStore(func(st *store.Store) error { return st.SetMode(name, mode) }))
}

// applyCheckAskStore closes the check ask by its id, after making sure the track still shows
// it as the pane did: same number, same text.
func applyCheckAskStore(name string, a ask) (status string, ok bool) {
	applied := fmt.Sprintf("Applied: ask %d cleared", a.n)
	changed := fmt.Sprintf("could not apply ask %d: it changed on the card", a.n)
	err := withStore(func(st *store.Store) error {
		v, err := st.Get(name)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(v.Asks, func(x store.Ask) bool {
			return x.N == a.n && x.Text == a.text && (a.id == 0 || x.ID == a.id)
		})
		if i < 0 {
			return nil
		}
		ok, err = st.ApplyCheckAsk(v.Asks[i].ID)
		return err
	})
	switch {
	case err != nil:
		return "could not apply ask: " + err.Error(), false
	case !ok:
		return changed, false
	}
	if mm := recommendMode.FindStringSubmatch(a.text); mm != nil {
		applied = "Applied: mode " + mm[1]
	}
	return applied, true
}

// answerSentStore records an answer the pane sent: the asks it accepted, by the ids the pane
// showed when Emil chose them, and the answer event.
func answerSentStore(name string, ids []int64, e Event) error {
	return withStore(func(st *store.Store) error {
		return st.AnswerSent(name, ids, store.Event{RexSession: e.RexSession, RexBlock: e.RexBlock, Cwd: e.Cwd, Len: e.Len, Kind: e.Kind})
	})
}

const (
	// hookBudget is how long the hook may spend on the database: Claude Code kills a hook after
	// about 2 seconds.
	hookBudget = 1800 * time.Millisecond
	// hookLogReserve is the part of hookBudget kept for logging the event alone.
	hookLogReserve = 400 * time.Millisecond
)

// hookStore is the hook as one transaction. When another writer holds the database past
// hookLockWait, the whole hook is tried again without rendering the view, which keeps the
// write short, until hookLogReserve is left of hookBudget; then the event alone is logged, so
// it lands even if the change to the card does not. A dropped stop would leave an answered
// stamp behind. It reports moved when tracks export took the database away meanwhile, so the
// caller writes the files instead.
func hookStore(e Event, sessionID, transcript, hookCwd string) (moved bool, err error) {
	start := time.Now()
	h := store.HookEvent{Event: e.Event, RexSession: e.RexSession, RexBlock: e.RexBlock, Cwd: e.Cwd, Kind: e.Kind, Len: e.Len}
	if e.Event != "session_start" && claudeSessionID.MatchString(sessionID) {
		cwd := ""
		if strings.HasSuffix(transcript, ".jsonl") {
			cwd = transcriptCwd(transcript)
		}
		if cwd == "" {
			cwd = hookCwd
		}
		if !cardValueOK(cwd) {
			cwd = ""
		}
		h.ClaudeSession, h.ClaudeCwd = sessionID, cwd
	}
	err = withStore(func(st *store.Store) error { return st.Hook(h) }, store.WithBusyTimeout(hookLockWait))
	if store.IsBusy(err) {
		err = withBareStore(func(st *store.Store) error { return st.Hook(h) },
			store.WithBusyTimeout(hookBudget-hookLogReserve-time.Since(start)))
	}
	if store.IsBusy(err) && h.Event != "session_start" {
		logged := withBareStore(func(st *store.Store) error { return st.HookLog(h) }, store.WithBusyTimeout(hookBudget-time.Since(start)))
		if logged == nil {
			return false, fmt.Errorf("event logged, card not updated: %w", err)
		}
		err = errors.Join(err, logged)
	}
	if errors.Is(err, store.ErrMoved) || errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// checkNote is what a desk session hears when its check changes: "[check] pr 188 approved".
func checkNote(text string, ans bool) string {
	if ans {
		return "[check] " + text
	}
	return "[check] " + text + ": no longer true"
}

// wakeCheck gives note to the desk's session id on track name as tracks send does: delivered
// now when the session is idle, held until it stops while it works, and held for the desk's
// watch when delivery fails. It returns what happened, for tracks check's output.
var wakeCheck = func(name, id, note string, errw io.Writer) string {
	s, ok, err := deskTarget(id)
	if err == nil && !ok {
		err = fmt.Errorf("session %s not found", shortID(id))
	}
	if err != nil {
		fmt.Fprintf(errw, "%s: waking its session: %v\n", name, err)
		return "could not wake its session"
	}
	held, failed, err := noteAnswer(name, s, "system", note)
	switch {
	case err != nil:
		fmt.Fprintf(errw, "%s: waking its session: %v\n", name, err)
		return "could not wake its session"
	case failed != nil:
		fmt.Fprintf(errw, "%s: waking its session: %v\n", name, failed)
		return "woke nobody yet: the note is held for the desk's watch"
	case held:
		return "the note waits until its session stops"
	}
	return "woke its session"
}

// runChecksStore is tracks check on the database: answers are fetched outside any
// transaction, then each is recorded in one. A check ask cannot be lost to an agent's rewrite
// here, so nothing is re-added. Every view is rendered at the end.
func runChecksStore(dry bool, eval evaluator, out, errw io.Writer) error {
	if !dry {
		unlock, err := lockChecks()
		if err != nil {
			return err
		}
		defer unlock()
	}
	vs, err := liveViews()
	if err != nil {
		return err
	}
	prev, err := storeChecks()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	fails := loadCheckErrors()
	defer func() {
		if !dry {
			// Only checks still on a card keep a count.
			live := map[string]bool{}
			for _, v := range vs {
				if c := cardOf(v); c.Meta["check"] != "" {
					live[c.Name+": "+c.Meta["check"]] = true
				}
			}
			for k := range fails {
				if !live[k] {
					delete(fails, k)
				}
			}
			if err := saveCheckErrors(fails); err != nil {
				fmt.Fprintf(errw, "check errors: %v\n", err)
			}
		}
	}()
	for _, v := range vs {
		c := cardOf(v)
		text := c.Meta["check"]
		if text == "" || c.mode() == "parked" {
			continue
		}
		chk, ok := parseCheck(text)
		if !ok {
			fmt.Fprintf(errw, "%s: unknown check %q, not run\n", c.Name, text)
			continue
		}
		got, err := eval(c.Meta["dir"], chk)
		key := c.Name + ": " + text
		if err != nil {
			fmt.Fprintf(errw, "%s: check %q: %v\n", c.Name, text, err)
			if !dry && noteCheckFailure(fails, key, err, time.Now().UTC()) {
				// The ask puts a check that can never run in front of whoever owns the track.
				if aerr := st.AddAsk(c.Name, checkFailureAsk(text, fails[key])); aerr != nil {
					fmt.Fprintf(errw, "%s: asking about the failing check: %v\n", c.Name, aerr)
				}
			}
			continue
		}
		delete(fails, key)
		if got == pending {
			fmt.Fprintf(out, "%s: %q is pending, not recorded\n", c.Name, text)
			continue
		}
		ans := got == yes
		if dry {
			if ans == prev[c.Name+": "+text].Answer {
				fmt.Fprintf(out, "%s: %q is %s, unchanged\n", c.Name, text, got)
			} else {
				fmt.Fprintf(out, "%s: %q is %s, would add an ask\n", c.Name, text, got)
			}
			continue
		}
		res, err := st.RecordCheck(c.Name, text, ans)
		switch {
		case err != nil: // store.ErrCardChanged says the next run retries
			fmt.Fprintf(errw, "%s: %v\n", c.Name, err)
		case res.Wake != "":
			fmt.Fprintf(out, "%s: %q is %s, %s\n", c.Name, text, got, wakeCheck(c.Name, res.Wake, checkNote(text, ans), errw))
		case res.Fired:
			fmt.Fprintf(out, "%s: %q is %s, added an ask\n", c.Name, text, got)
		default:
			fmt.Fprintf(out, "%s: %q is %s, unchanged\n", c.Name, text, got)
		}
	}
	refreshMilestones(st, dry, eval, out, errw)
	if dry {
		return nil
	}
	return renderAll(st)
}

// renderCmd is tracks render: it writes every card view from tracks.db again, after a render
// that failed or a hand edit.
func renderCmd(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: tracks render")
	}
	if !storeOn() {
		return fmt.Errorf("no %s in %s: the card files are the tracks, nothing to render", dbFileName, home())
	}
	if err := withStore(renderAll); err != nil {
		return err
	}
	fmt.Fprintln(out, "rendered every card from", dbFileName)
	return nil
}
