package movies

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestSharedDownloadSlots(t *testing.T) {
	svc := &Services{}
	// Concurrent movie and episode queues must share one limit.
	var wg sync.WaitGroup
	results := make(chan bool, 20)
	for range 20 {
		wg.Go(func() { results <- svc.AcquireDownload(1) })
	}
	wg.Wait()
	close(results)
	n := 0
	for ok := range results {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("reserved %d slots, want 1", n)
	}
	woke := make(chan struct{}, 2)
	svc.RegisterDownloadQueue(func() { woke <- struct{}{} })
	svc.RegisterDownloadQueue(func() { woke <- struct{}{} })
	svc.ReleaseDownload()
	for range 2 {
		select {
		case <-woke:
		case <-time.After(time.Second):
			t.Fatal("queue not woken")
		}
	}
	if !svc.AcquireDownload(1) {
		t.Fatal("released slot cannot be reused")
	}
}

func TestMovieDeletionPolicies(t *testing.T) {
	available := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	started := available.Add(3 * 24 * time.Hour)
	watched := started.Add(time.Hour)
	g := store.DefaultGeneral()
	g.UnwatchedExpiryDays, g.StartedExpiryDays, g.DeleteAfterHours = 7, 2, 6
	mv := store.Movie{State: store.MovieAvailable, AvailableAt: &available}
	check := func(at time.Time, reason string) {
		t.Helper()
		got, why := movieDeletion(mv, g)
		if !got.Equal(at) || why != reason {
			t.Fatalf("deadline %v (%s), want %v (%s)", got, why, at, reason)
		}
	}
	check(available.Add(7*24*time.Hour), "expired")
	mv.StartedAt = &started
	check(started.Add(2*24*time.Hour), "started")
	// A zero started expiry must not fall back to the unwatched deadline.
	g.StartedExpiryDays = 0
	check(time.Time{}, "")
	mv.State, mv.WatchedAt = store.MovieWatched, &watched
	check(watched.Add(6*time.Hour), "watched")
	mv.Keep = true
	check(time.Time{}, "")
}

func TestMissingFilesRequireConfirmedRemoval(t *testing.T) {
	ctx, _, st, root := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 123, Title: "Missing", State: store.MovieAvailable}, 0)
	must(t, err)
	owner := store.ForMovie(mv.ID)
	check := func(want bool) {
		t.Helper()
		got, err := FilesMissing(ctx, st, owner, root)
		must(t, err)
		if got != want {
			t.Fatalf("missing = %v, want %v", got, want)
		}
	}
	check(false) // no tracked files, e.g. not scanned yet
	file := filepath.Join(root, "movie.mkv")
	must(t, os.WriteFile(file, []byte("video"), 0o600))
	must(t, st.TrackFile(ctx, mv.ID, file, 5))
	check(false)
	must(t, os.Remove(file))
	check(true)
	unavailable := filepath.Join(root, "unmounted")
	got, err := FilesMissing(ctx, st, owner, unavailable)
	must(t, err)
	if got {
		t.Fatal("inaccessible root treated as deletion")
	}
}

func TestStartedDeadlinePersistsAndCanBeDisabled(t *testing.T) {
	ctx, m, st, _ := deleteTestManager(t)
	now := m.now()
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 456, Title: "Started", State: store.MovieAvailable}, 0)
	must(t, err)
	mv.AvailableAt, mv.StartedAt = &now, &now
	must(t, st.UpdateMovie(ctx, mv))
	must(t, m.scheduleDeletion(ctx, mv))
	mv, err = st.Movie(ctx, mv.ID)
	must(t, err)
	if mv.StartedAt == nil || mv.DeleteAt == nil || mv.DeleteAt.Sub(*mv.StartedAt) != 48*time.Hour {
		t.Fatalf("wrong persisted playback/deadline: %+v", mv)
	}
	g, err := st.General(ctx)
	must(t, err)
	g.StartedExpiryDays = 0
	must(t, st.SetGeneral(ctx, g))
	must(t, m.scheduleDeletion(ctx, mv))
	mv, err = st.Movie(ctx, mv.ID)
	must(t, err)
	if mv.DeleteAt != nil {
		t.Fatal("disabled policy left a deletion deadline")
	}
}
