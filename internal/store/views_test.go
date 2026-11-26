package store

import (
	"errors"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A view is written only once its write has committed: another connection already reads what
// the view shows, so a process killed mid-write never leaves a view of uncommitted data.
func TestViewsAreWrittenAfterTheCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	other := openAt(t, path, testClock())
	var seen []string
	s, err := Open(path, WithClock(testClock()), WithViews(func(v ViewChange) (string, string, error) {
		got, err := other.Get(v.Track.Name)
		if err != nil {
			return "", "", err
		}
		seen = append(seen, v.Track.Where+" / committed: "+got.Track.Where)
		return "h", "", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustTrack(t, s, "a")
	if _, err := s.Card("a", CardUpdate{Where: ptr("Rendered.")}); err != nil {
		t.Fatal(err)
	}
	want := []string{notStarted + " / committed: " + notStarted, "Rendered. / committed: Rendered."}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Errorf("views saw:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

// A render that fails leaves the write committed and reaches the render error handler.
func TestARenderThatFailsIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	openAt(t, path, testClock())
	var reported []error
	s, err := Open(path, WithClock(testClock()),
		WithViews(func(ViewChange) (string, string, error) { return "", "", errors.New("disk full") }),
		WithRenderErrors(func(err error) { reported = append(reported, err) }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.NewTrack(NewTrackSpec{Name: "a"}); err != nil {
		t.Fatalf("the write failed with its render: %v", err)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "disk full") {
		t.Errorf("reported: %v", reported)
	}
	if err := s.RenderAll(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("RenderAll: %v", err)
	}
	get(t, s, "a")
}

// Writers on one track from many connections: no view goes back to an older commit, and the
// last view written is the last commit's.
func TestViewsLandInCommitOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracks.db")
	mustTrack(t, openAt(t, path, testClock()), "a")
	var (
		mu        sync.Mutex
		file      string
		version   int64
		backwards int
	)
	views := WithViews(func(v ViewChange) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		if v.Track.Version < version {
			backwards++
		}
		version = v.Track.Version
		file = RenderView(v.Track, v.Asks)
		return "h", "", nil
	})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path, views)
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			// Another writer can commit before this one renders.
			s.afterCommit = func() { time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond) }
			for i := range 10 {
				where := strings.Repeat("x", w*10+i)
				if _, err := s.Card("a", CardUpdate{Where: &where}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if backwards > 0 {
		t.Errorf("%d views went back to an older commit", backwards)
	}
	s := openAt(t, path, testClock())
	v := get(t, s, "a")
	if want := RenderView(v.Track, v.Asks); file != want {
		t.Errorf("last view written:\n%s\ncommitted:\n%s", file, want)
	}
}
