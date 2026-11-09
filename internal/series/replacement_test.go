package series

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestEpisodeReplacementCleansOnlyItsFiles(t *testing.T) {
	for _, report := range []bool{false, true} {
		name := "manual"
		if report {
			name = "reported"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m, st, root := setup(ctx, t, newWorld(t), nil)
			// Keep the queue from starting so assertions inspect the preparation itself.
			if !m.svc.AcquireDownload(1) {
				t.Fatal("slot unavailable")
			}
			se, err := st.CreateSeries(ctx, store.Series{TMDBID: 7, Title: "Show", WindowSize: 2, DeleteTrigger: store.TriggerProgress, TriggerValue: 10}, 0)
			must(t, err)
			must(t, st.UpsertEpisodes(ctx, se.ID, []store.Episode{{Season: 1, Episode: 1, State: store.MovieAvailable}, {Season: 1, Episode: 2, State: store.MovieAvailable}}))
			eps, err := st.Episodes(ctx, se.ID)
			must(t, err)
			for i := range eps {
				path := filepath.Join(root, SeriesFolder(se), SeasonFolder(1), EpisodeFileName(se, eps[i])+".mp4")
				must(t, os.MkdirAll(filepath.Dir(path), 0755))
				must(t, os.WriteFile(path, []byte("old"), 0644))
				must(t, st.TrackFileOf(ctx, store.ForEpisode(eps[i].ID), path, 3))
				eps[i].File = path
				must(t, st.UpdateEpisode(ctx, eps[i]))
			}
			e := eps[0]
			e.ReleaseKey, e.ReleaseTitle, e.ReleaseFingerprint = "1:guid:old", "old", "broken-episode"
			now := time.Now()
			e.DeleteAt = &now
			e.Progress = 1
			must(t, st.UpdateEpisode(ctx, e))
			must(t, st.ReplaceReleasesOf(ctx, store.ForEpisode(e.ID), []store.Release{{GUID: "new", Fingerprint: "good", Title: "new"}}))
			rels, err := st.ReleasesOf(ctx, store.ForEpisode(e.ID))
			must(t, err)
			if report {
				_, err = m.ReportRelease(ctx, e.ID, e.ReleaseKey)
			} else {
				_, err = m.DownloadRelease(ctx, e.ID, rels[0].ID)
			}
			must(t, err)
			if _, err := os.Stat(e.File); !os.IsNotExist(err) {
				t.Fatalf("old episode survived: %v", err)
			}
			if _, err := os.Stat(eps[1].File); err != nil {
				t.Fatalf("neighbor episode removed: %v", err)
			}
			cur, err := st.Episode(ctx, e.ID)
			must(t, err)
			if cur.State != store.MovieQueued || cur.File != "" || cur.Progress != 0 || cur.DeleteAt != nil {
				t.Fatalf("replacement state: %+v", cur)
			}
			bl, err := st.BlocklistOf(ctx, store.ForEpisode(e.ID))
			must(t, err)
			if report && (len(bl) != 1 || bl[0].Fingerprint != "broken-episode") {
				t.Fatalf("episode blocklist: %+v", bl)
			}
		})
	}
}

func TestEpisodeReplacementCleanupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m, st, root := setup(ctx, t, newWorld(t), nil)
	se, err := st.CreateSeries(ctx, store.Series{TMDBID: 7, Title: "Show", WindowSize: 1}, 0)
	must(t, err)
	must(t, st.UpsertEpisodes(ctx, se.ID, []store.Episode{{Season: 1, Episode: 1, State: store.MovieAvailable}}))
	eps, err := st.Episodes(ctx, se.ID)
	must(t, err)
	e := eps[0]
	path := filepath.Join(root, "blocked.mp4")
	must(t, os.MkdirAll(path, 0755))
	must(t, os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0644))
	must(t, st.TrackFileOf(ctx, store.ForEpisode(e.ID), path, 4))
	must(t, st.ReplaceReleasesOf(ctx, store.ForEpisode(e.ID), []store.Release{{GUID: "new", Fingerprint: "new", Title: "new"}}))
	rels, err := st.ReleasesOf(ctx, store.ForEpisode(e.ID))
	must(t, err)
	_, err = m.DownloadRelease(ctx, e.ID, rels[0].ID)
	if !errors.Is(err, movies.ErrReleaseCleanup) {
		t.Fatalf("cleanup failure: %v", err)
	}
	cur, err := st.Episode(ctx, e.ID)
	must(t, err)
	if cur.State != store.MovieFailed || cur.LastError != "release_cleanup_failed" {
		t.Fatalf("cleanup state: %+v", cur)
	}
	if len(m.Downloads()) != 0 {
		t.Fatal("replacement started after cleanup failed")
	}
}

func TestEpisodeReplacementWaitsForLateFile(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m, st, root := setup(ctx, t, newWorld(t), nil)
	if !m.svc.AcquireDownload(1) {
		t.Fatal("slot unavailable")
	}
	se, err := st.CreateSeries(ctx, store.Series{TMDBID: 7, Title: "Show", WindowSize: 1}, 0)
	must(t, err)
	must(t, st.UpsertEpisodes(ctx, se.ID, []store.Episode{{Season: 1, Episode: 1, State: store.MovieDownloading}}))
	eps, err := st.Episodes(ctx, se.ID)
	must(t, err)
	e := eps[0]
	must(t, st.ReplaceReleasesOf(ctx, store.ForEpisode(e.ID), []store.Release{{GUID: "new", Fingerprint: "new", Title: "new"}}))
	rels, err := st.ReleasesOf(ctx, store.ForEpisode(e.ID))
	must(t, err)
	cancelled := make(chan struct{})
	done := make(chan struct{})
	m.mu.Lock()
	m.active[e.ID] = &activeDownload{episode: e, cancel: func(error) { close(cancelled) }, done: done}
	m.mu.Unlock()
	result := make(chan error, 1)
	go func() { _, err := m.DownloadRelease(ctx, e.ID, rels[0].ID); result <- err }()
	<-cancelled
	select {
	case err := <-result:
		t.Fatalf("replacement did not wait: %v", err)
	default:
	}
	path := filepath.Join(root, "late.mp4")
	must(t, os.WriteFile(path, []byte("late"), 0644))
	must(t, st.TrackFileOf(ctx, store.ForEpisode(e.ID), path, 4))
	m.mu.Lock()
	delete(m.active, e.ID)
	m.mu.Unlock()
	close(done)
	select {
	case err := <-result:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("replacement stayed blocked")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("late episode file survived: %v", err)
	}
}
