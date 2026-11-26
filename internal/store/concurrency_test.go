package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Eight writers, each with its own connection, work on one track at once. Every change must be
// there afterwards and none may fail: SQLITE_BUSY is the store's to absorb.
func TestConcurrentWritersOnOneTrack(t *testing.T) {
	const rounds = 40
	path := filepath.Join(t.TempDir(), "tracks.db")
	clock := testClock()
	setup := openAt(t, path, clock)
	if _, err := setup.Bind("alpha", "rex:1", "/w"); err != nil {
		t.Fatal(err)
	}
	// An open agent ask makes every user prompt save the track, so the version count is exact.
	if _, err := setup.Card("alpha", CardUpdate{Check: ptr("pr 1 merged"), Asks: []string{"stays open?"}}); err != nil {
		t.Fatal(err)
	}

	modes := []string{"focus", "background", "waiting"} // a parked track would skip the checks
	last := func(i int) string { return fmt.Sprint(i) }
	writers := map[string]func(s *Store, i int) error{
		"card-links": func(s *Store, i int) error {
			_, err := s.Card("alpha", CardUpdate{Links: ptr("L" + last(i))})
			return err
		},
		"card-later": func(s *Store, i int) error {
			_, err := s.Card("alpha", CardUpdate{Later: ptr("later " + last(i))})
			return err
		},
		"card-goal": func(s *Store, i int) error {
			_, err := s.Card("alpha", CardUpdate{Goal: ptr("goal " + last(i))})
			return err
		},
		"mode": func(s *Store, i int) error { return s.SetMode("alpha", modes[i%len(modes)]) },
		"hook-prompt": func(s *Store, i int) error {
			return s.Hook(HookEvent{Event: "prompt", Kind: "user", Len: i, RexSession: "rex:1"})
		},
		"hook-stop": func(s *Store, i int) error {
			return s.Hook(HookEvent{Event: "stop", RexSession: "rex:1", ClaudeSession: "claude-" + last(i)})
		},
		"check": func(s *Store, i int) error {
			_, err := s.RecordCheck("alpha", "pr 1 merged", i%2 == 0)
			return err
		},
		"open": func(s *Store, i int) error { return s.LogEvent("alpha", Event{Event: "open"}) },
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(writers)*rounds)
	for name, write := range writers {
		s, err := Open(path, WithClock(clock), WithCreate())
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.Close()
			for i := range rounds {
				if err := write(s, i); err != nil {
					errs <- fmt.Errorf("%s round %d: %w", name, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	v := get(t, setup, "alpha")
	final := rounds - 1
	eq(t, "links", v.Track.Links, "L"+last(final))
	eq(t, "later", v.Track.Later, "later "+last(final))
	eq(t, "goal", v.Track.Goal, "goal "+last(final))
	eq(t, "mode", v.Track.Mode, modes[final%len(modes)])
	eq(t, "claude_session", v.Track.ClaudeSession, "claude-"+last(final))
	eq(t, "check text", v.Track.Check, "pr 1 merged")

	counts := map[string]int{}
	evs, err := setup.Events("alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		counts[e.Event]++
	}
	for event, want := range map[string]int{"prompt": rounds, "stop": rounds, "open": rounds, "mode": rounds, "check": rounds} {
		eq(t, event+" events", counts[event], want)
	}

	// Every writer but open bumps version once per round (each check flips, each stop brings a new
	// Claude session, each prompt meets the open ask), on top of the 3 from setup. A write saved
	// from a stale read would land on an old version and leave the count short.
	if want := int64(3 + 7*rounds); v.Track.Version != want {
		t.Errorf("version = %d, want %d: some writes were lost", v.Track.Version, want)
	}

	checks, err := setup.Checks("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Answer != (final%2 == 0) {
		t.Errorf("checks = %+v, want one record with the last answer", checks)
	}
	var checkAsks int
	for _, a := range v.Asks {
		if a.Kind == "check" {
			checkAsks++
		}
	}
	eq(t, "open check asks", checkAsks, 1)
}
