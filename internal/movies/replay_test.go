package movies

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestMovieRepeatViewingLifecycle(t *testing.T) {
	ctx, m, st, _ := deleteTestManager(t)
	monday := time.Date(2026, 11, 2, 20, 0, 0, 0, time.UTC)
	wednesday := monday.Add(48*time.Hour - 15*time.Minute)
	g, err := st.General(ctx)
	must(t, err)
	g.DeleteAfterHours, g.StartedExpiryDays = 48, 2
	must(t, st.SetGeneral(ctx, g))
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 99, Title: "Replay", State: store.MovieAvailable}, 0)
	must(t, err)
	mv.Base = store.WatchBase{Set: true}
	item := mediaserver.Item{Key: "film", Duration: 2 * time.Hour, ViewCount: 1}
	poll := func(now time.Time) {
		t.Helper()
		m.now = func() time.Time { return now }
		observeMovie(&mv, item, time.Time{}, now)
		must(t, st.UpdateMovie(ctx, mv))
		must(t, m.scheduleDeletion(ctx, mv))
		mv, err = st.Movie(ctx, mv.ID)
		must(t, err)
	}
	deadline := func(want time.Time) {
		t.Helper()
		if mv.DeleteAt == nil || !mv.DeleteAt.Equal(want) {
			t.Fatalf("deadline = %v, want %v", mv.DeleteAt, want)
		}
	}
	poll(monday)
	deadline(monday.Add(48 * time.Hour))
	poll(monday.Add(time.Hour)) // unchanged watch history must not extend the deadline
	deadline(monday.Add(48 * time.Hour))
	item.ViewOffset = time.Minute
	poll(wednesday)
	if mv.State != store.MovieAvailable || mv.StartedAt == nil || !mv.StartedAt.Equal(wednesday) || mv.WatchedAt != nil {
		t.Fatalf("replay not started: %+v", mv)
	}
	deadline(wednesday.Add(48 * time.Hour))
	// A queued deletion from the previous cycle must not delete the replay.
	payload, err := json.Marshal(deletePayload{MovieID: mv.ID, Reason: "watched"})
	must(t, err)
	must(t, m.handleDelete(ctx, scheduler.Task{Payload: payload}))
	mv, err = st.Movie(ctx, mv.ID)
	must(t, err)
	if mv.State != store.MovieAvailable {
		t.Fatal("old deletion removed the replay")
	}
	item.ViewOffset = 30 * time.Minute
	poll(wednesday.Add(30 * time.Minute))
	deadline(wednesday.Add(48 * time.Hour)) // continuing does not restart the started timer
	item.ViewCount, item.ViewOffset = 2, 0
	finished := wednesday.Add(2 * time.Hour)
	poll(finished)
	deadline(finished.Add(48 * time.Hour))
	poll(finished.Add(time.Hour))
	deadline(finished.Add(48 * time.Hour))
	// A complete repeat viewing between polls also starts a fresh watched timer.
	item.ViewCount = 3
	poll(finished.Add(3 * time.Hour))
	deadline(finished.Add(51 * time.Hour))
}

func TestMovieReplayPreservesLaterDeadline(t *testing.T) {
	ctx, m, st, _ := deleteTestManager(t)
	now := time.Date(2026, 11, 4, 19, 45, 0, 0, time.UTC)
	later := now.Add(5 * 24 * time.Hour)
	g, err := st.General(ctx)
	must(t, err)
	g.StartedExpiryDays, g.DeleteAfterHours = 2, 24
	must(t, st.SetGeneral(ctx, g))
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 100, Title: "Keep deadline", State: store.MovieWatched}, 0)
	must(t, err)
	mv.Base, mv.DeleteAt = store.WatchBase{Set: true}, &later
	item := mediaserver.Item{Key: "film", ViewCount: 1, ViewOffset: time.Minute, Duration: 2 * time.Hour}
	observeMovie(&mv, item, time.Time{}, now)
	must(t, st.UpdateMovie(ctx, mv))
	must(t, m.scheduleDeletion(ctx, mv))
	mv, err = st.Movie(ctx, mv.ID) // the floor must survive database round trips
	must(t, err)
	item.ViewCount, item.ViewOffset = 2, 0
	observeMovie(&mv, item, time.Time{}, now.Add(2*time.Hour))
	must(t, st.UpdateMovie(ctx, mv))
	must(t, m.scheduleDeletion(ctx, mv))
	mv, err = st.Movie(ctx, mv.ID)
	must(t, err)
	if mv.DeleteAt == nil || !mv.DeleteAt.Equal(later) {
		t.Fatalf("later deadline shortened: %v", mv.DeleteAt)
	}
	// Disabling the applicable policy still cancels deletion.
	mv.State = store.MovieAvailable
	g.StartedExpiryDays = 0
	must(t, st.SetGeneral(ctx, g))
	must(t, st.UpdateMovie(ctx, mv))
	must(t, m.scheduleDeletion(ctx, mv))
	mv, err = st.Movie(ctx, mv.ID)
	must(t, err)
	if mv.DeleteAt != nil {
		t.Fatal("disabled policy retained a deadline")
	}
}

func TestMovieCreditsDoNotStartReplay(t *testing.T) {
	now := time.Now()
	mv := store.Movie{State: store.MovieAvailable, Base: store.WatchBase{Set: true}}
	item := mediaserver.Item{ViewCount: 1, ViewOffset: 110 * time.Minute, Duration: 2 * time.Hour}
	observeMovie(&mv, item, time.Time{}, now)
	item.ViewOffset = 115 * time.Minute
	observeMovie(&mv, item, time.Time{}, now.Add(5*time.Minute))
	item.ViewOffset = 0
	observeMovie(&mv, item, time.Time{}, now.Add(10*time.Minute))
	if mv.State != store.MovieWatched || mv.WatchedAt == nil || !mv.WatchedAt.Equal(now) {
		t.Fatalf("credits changed viewing cycle: %+v", mv)
	}
	item.ViewOffset = time.Minute
	observeMovie(&mv, item, time.Time{}, now.Add(time.Hour))
	if mv.State != store.MovieAvailable {
		t.Fatal("replay after credits not detected")
	}
}
