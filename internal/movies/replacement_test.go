package movies

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func TestReplacementRemovesPreviousContainer(t *testing.T) {
	for _, report := range []bool{false, true} {
		name := "manual"
		if report {
			name = "reported"
		}
		t.Run(name, func(t *testing.T) {
			world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
			world.post(t, "replacement", []rar5.File{{Name: "film.mkv", Data: []byte("replacement movie")}})
			h := newHarness(t, world)
			mv, err := h.st.CreateMovie(h.ctx, store.Movie{TMDBID: 42, Title: "Film", Year: 2024, State: store.MovieAvailable}, 0)
			must(t, err)
			old := filepath.Join(h.root, FolderName(mv), FolderName(mv)+".mp4")
			must(t, os.MkdirAll(filepath.Dir(old), 0755))
			must(t, os.WriteFile(old, []byte("bad old movie"), 0644))
			must(t, h.st.TrackFile(h.ctx, mv.ID, old, 13))
			idx, err := h.st.Indexers(h.ctx)
			must(t, err)
			broken := store.Release{IndexerID: idx[0].ID, GUID: "old", Fingerprint: "bad-old", Title: "old"}
			replacement := store.Release{IndexerID: idx[0].ID, GUID: "replacement", Fingerprint: "good-new", Title: "Film.2024.German.720p.WEB.h264-GOOD", NZBURL: idx[0].URL + "/getnzb/replacement"}
			mv.Folder, mv.ReleaseKey, mv.ReleaseTitle, mv.ReleaseFingerprint = filepath.Dir(old), broken.Identity(), broken.Title, broken.Fingerprint
			now := time.Now()
			mv.WatchedAt, mv.DeleteAt, mv.DeleteNotBefore = &now, &now, &now
			mv.Progress = 1
			must(t, h.st.UpdateMovie(h.ctx, mv))
			// The old release is deliberately absent from the refreshed search results.
			must(t, h.st.ReplaceReleases(h.ctx, mv.ID, []store.Release{replacement}))
			rels, err := h.st.Releases(h.ctx, mv.ID)
			must(t, err)
			if report {
				_, err = h.m.ReportRelease(h.ctx, mv.ID, mv.ReleaseKey)
			} else {
				_, err = h.m.DownloadRelease(h.ctx, mv.ID, rels[0].ID)
			}
			must(t, err)
			if _, err := os.Stat(old); !os.IsNotExist(err) {
				t.Fatalf("old MP4 survived replacement: %v", err)
			}
			eventually(t, "replacement available", func() bool {
				cur, _ := h.st.Movie(h.ctx, mv.ID)
				return cur.State == store.MovieAvailable && cur.ReleaseKey == replacement.Identity()
			})
			files, err := h.st.Files(h.ctx, mv.ID)
			must(t, err)
			if len(files) != 1 || filepath.Ext(files[0]) != ".mkv" {
				t.Fatalf("replacement files: %v", files)
			}
			cur, err := h.st.Movie(h.ctx, mv.ID)
			must(t, err)
			if cur.Progress != 0 || cur.WatchedAt != nil || cur.DeleteNotBefore != nil {
				t.Fatalf("old watch state survived: %+v", cur)
			}
			bl, err := h.st.BlocklistEntries(h.ctx, mv.ID)
			must(t, err)
			if report {
				if len(bl) != 1 || bl[0].Fingerprint != broken.Fingerprint || bl[0].Reason != "reported_bad" {
					t.Fatalf("reported blocklist: %+v", bl)
				}
			} else if len(bl) != 0 {
				t.Fatalf("manual selection blocklisted old release: %+v", bl)
			}
			if report {
				if _, err := h.m.ReportRelease(h.ctx, mv.ID, mv.ReleaseKey); !errors.Is(err, store.ErrInvalid) {
					t.Fatalf("stale report: %v", err)
				}
			}
		})
	}
}

func TestReplacementCleanupFailureStopsDownload(t *testing.T) {
	ctx, m, st, root := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", State: store.MovieAvailable}, 0)
	must(t, err)
	// Removing a nonempty directory as a tracked file fails reliably, even as root.
	blocked := filepath.Join(root, "blocked.mp4")
	must(t, os.MkdirAll(blocked, 0755))
	must(t, os.WriteFile(filepath.Join(blocked, "keep"), []byte("keep"), 0644))
	must(t, st.TrackFile(ctx, mv.ID, blocked, 4))
	must(t, st.ReplaceReleases(ctx, mv.ID, []store.Release{{GUID: "new", Fingerprint: "new", Title: "new"}}))
	rels, err := st.Releases(ctx, mv.ID)
	must(t, err)
	_, err = m.DownloadRelease(ctx, mv.ID, rels[0].ID)
	if !errors.Is(err, ErrReleaseCleanup) {
		t.Fatalf("cleanup failure: %v", err)
	}
	cur, err := st.Movie(ctx, mv.ID)
	must(t, err)
	if cur.State != store.MovieFailed || cur.LastError != "release_cleanup_failed" {
		t.Fatalf("cleanup state: %+v", cur)
	}
	attempts, err := st.Attempts(ctx, mv.ID)
	must(t, err)
	if len(attempts) != 0 {
		t.Fatalf("replacement started: %+v", attempts)
	}
}

func TestReplacementWaitsForWriterAndRemovesLateFile(t *testing.T) {
	ctx, m, st, root := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", State: store.MovieDownloading}, 0)
	must(t, err)
	must(t, st.ReplaceReleases(ctx, mv.ID, []store.Release{{GUID: "new", Fingerprint: "new", Title: "new"}}))
	rels, err := st.Releases(ctx, mv.ID)
	must(t, err)
	cancelled := make(chan struct{})
	done := make(chan struct{})
	m.active[mv.ID] = &activeDownload{cancel: func(error) { close(cancelled) }, done: done}
	result := make(chan error, 1)
	go func() { _, err := m.DownloadRelease(ctx, mv.ID, rels[0].ID); result <- err }()
	<-cancelled
	select {
	case err := <-result:
		t.Fatalf("replacement did not wait: %v", err)
	default:
	}
	file := filepath.Join(root, "late.mp4")
	must(t, os.WriteFile(file, []byte("late"), 0644))
	must(t, st.TrackFile(ctx, mv.ID, file, 4))
	// Mimic the writer's deregistration; reserve the slot to inspect the queue.
	if !m.svc.AcquireDownload(1) {
		t.Fatal("slot unavailable")
	}
	m.mu.Lock()
	delete(m.active, mv.ID)
	m.mu.Unlock()
	close(done)
	select {
	case err := <-result:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("replacement stayed blocked")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("late old file survived: %v", err)
	}
}

func TestInvalidVideoTriesNextRelease(t *testing.T) {
	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	video := filepath.Join(t.TempDir(), "good.mkv")
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=1", "-t", "1", "-c:v", "ffv1", "-y", video).CombinedOutput(); err != nil {
		t.Fatalf("video fixture: %v %s", err, out)
	}
	good, err := os.ReadFile(video)
	must(t, err)
	world.post(t, "bad", []rar5.File{{Name: "bad.mkv", Data: []byte("not a video despite its extension")}})
	world.post(t, "good", []rar5.File{{Name: "good.mkv", Data: good}})
	world.items = item("bad", "Film.2024.German.720p.BluRay.x264-BAD") + item("good", "Film.2024.German.720p.WEB.h264-GOOD")
	h := newHarness(t, world)
	h.m.fetcher.Probe = fetch.ValidateVideo
	mv := h.requestAndWait(t)
	if mv.State != store.MovieAvailable || !strings.Contains(mv.ReleaseTitle, "GOOD") {
		t.Fatalf("fallback: %+v", mv)
	}
	bl, err := h.st.BlocklistEntries(h.ctx, mv.ID)
	must(t, err)
	if len(bl) != 1 || bl[0].Reason != fetch.KindInvalidVideo {
		t.Fatalf("invalid video blocklist: %+v", bl)
	}
	files, err := h.st.Files(h.ctx, mv.ID)
	must(t, err)
	if len(files) != 1 {
		t.Fatalf("fallback files: %v", files)
	}
	must(t, fetch.ValidateVideo(h.ctx, files[0]))
}

// A fresh search or queue recovery must not bypass failed replacement cleanup.
func TestQueuedDownloadCannotBypassCleanup(t *testing.T) {
	ctx, m, st, root := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", State: store.MovieQueued}, 0)
	must(t, err)
	path := filepath.Join(root, "blocked.mp4")
	must(t, os.MkdirAll(path, 0755))
	must(t, os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0644))
	must(t, st.TrackFile(ctx, mv.ID, path, 4))
	must(t, st.ReplaceReleases(ctx, mv.ID, []store.Release{{GUID: "new", Fingerprint: "new", Title: "new"}}))
	m.startQueued()
	eventually(t, "cleanup failure", func() bool {
		cur, _ := st.Movie(ctx, mv.ID)
		return cur.State == store.MovieFailed && strings.HasPrefix(cur.LastError, "release_cleanup_failed")
	})
	attempts, err := st.Attempts(ctx, mv.ID)
	must(t, err)
	if len(attempts) != 0 {
		t.Fatalf("download bypassed cleanup: %+v", attempts)
	}
}
