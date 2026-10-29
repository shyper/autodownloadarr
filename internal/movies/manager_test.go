package movies

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/fetch"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/testutil/rar5"
	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/yenc"
)

// fakeWorld holds fake TMDB, indexer, Usenet and Plex servers.
type fakeWorld struct {
	usenet  *nntptest.Server
	nzbs    map[string][]byte // release id → NZB document
	items   string            // indexer search result items
	watched atomic.Bool

	mu      sync.Mutex
	scanned []string
}

// post uploads files as a RAR5 multi-volume release and returns its NZB.
func (w *fakeWorld) post(t *testing.T, id string, files []rar5.File) {
	t.Helper()
	volumes, err := rar5.Write(t.TempDir(), id, files, 20_000)
	if err != nil {
		t.Fatal(err)
	}
	n := &nzb.NZB{Meta: map[string][]string{}}
	for vi, v := range volumes {
		data, _ := os.ReadFile(v)
		name := filepath.Base(v)
		total := (len(data) + 7999) / 8000
		f := nzb.File{Subject: fmt.Sprintf("%q yEnc (1/%d)", name, total)}
		for i := range total {
			begin, end := i*8000, min((i+1)*8000, len(data))
			mid := fmt.Sprintf("%s-%d-%d@test", id, vi, i)
			w.usenet.AddPart(mid, yenc.Part{Name: name, FileSize: int64(len(data)), Number: i + 1, Total: total, Begin: int64(begin), Data: data[begin:end]})
			f.Segments = append(f.Segments, nzb.Segment{Number: i + 1, Bytes: int64(end - begin), MessageID: mid})
		}
		n.Files = append(n.Files, f)
	}
	var buf bytes.Buffer
	if err := n.WriteXML(&buf); err != nil {
		t.Fatal(err)
	}
	w.nzbs[id] = buf.Bytes()
}

func (w *fakeWorld) handler(t *testing.T, base func() string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/tmdb/movie/42", func(rw http.ResponseWriter, _ *http.Request) {
		_, _ = rw.Write([]byte(`{"id":42,"imdb_id":"tt0000042","title":"Film","original_title":"Film","release_date":"2024-05-01","runtime":100}`))
	})
	mux.HandleFunc("/indexer/api", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			_, _ = rw.Write([]byte(`<caps><searching><movie-search available="yes" supportedParams="q,imdbid"/></searching></caps>`))
			return
		}
		if r.URL.Query().Get("imdbid") != "0000042" {
			t.Errorf("unexpected search %s", r.URL.RawQuery)
		}
		_, _ = fmt.Fprintf(rw, `<rss><channel>%s</channel></rss>`, strings.ReplaceAll(w.items, "BASE", base()))
	})
	mux.HandleFunc("/indexer/getnzb/", func(rw http.ResponseWriter, r *http.Request) {
		_, _ = rw.Write(w.nzbs[strings.TrimPrefix(r.URL.Path, "/indexer/getnzb/")])
	})
	mux.HandleFunc("/plex/library/sections/1/refresh", func(_ http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.scanned = append(w.scanned, r.URL.Query().Get("path"))
		w.mu.Unlock()
	})
	mux.HandleFunc("/plex/library/sections/1/all", func(rw http.ResponseWriter, _ *http.Request) {
		views := 0
		if w.watched.Load() {
			views = 1
		}
		_, _ = fmt.Fprintf(rw, `{"MediaContainer":{"Metadata":[{"ratingKey":"7","guid":"plex://movie/5d77","title":"Film","viewCount":%d,"lastViewedAt":%d,
			"Media":[{"Part":[{"file":"/remote/movies/Film (2024) {tmdb-42}/Film (2024) {tmdb-42}.mkv"}]}]}]}}`,
			views, time.Now().Add(-time.Hour).Unix())
	})
	return mux
}

func item(id, title string) string {
	return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><link>BASE/indexer/getnzb/%s</link>
		<attr name="size" value="200000"/><attr name="grabs" value="5"/></item>`, title, id, id)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMovieLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	world := &fakeWorld{usenet: nntptest.New(t), nzbs: map[string][]byte{}}
	movie := bytes.Repeat([]byte("frame-data "), 8000)
	// The better ranked release (BluRay) is damaged: one article is gone.
	world.post(t, "bad", []rar5.File{{Name: "film.bluray.mkv", Data: movie}, {Name: "film.sample.mkv", Data: []byte("sample")}})
	world.usenet.AddArticle("bad-1-2@test", []byte("corrupt\r\n"))
	world.post(t, "good", []rar5.File{{Name: "film.web.mkv", Data: movie}, {Name: "Sample/film-sample.mkv", Data: []byte("sample")}})
	world.items = item("bad", "Film.2024.German.720p.BluRay.x264-BAD") + item("good", "Film.2024.German.720p.WEB.h264-GOOD")

	var srvURL string
	srv := httptest.NewServer(world.handler(t, func() string { return srvURL }))
	defer srv.Close()
	srvURL = srv.URL

	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)

	root := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath, g.DeleteAfterHours = root, 0
	must(t, st.SetGeneral(ctx, g))
	must(t, st.SetTMDBKey(ctx, "key"))
	host, port, _ := net.SplitHostPort(world.usenet.Addr())
	portNum, _ := strconv.Atoi(port)
	_, err = st.SaveUsenetServer(ctx, store.UsenetServer{Host: host, Port: portNum, Connections: 4, Enabled: true, FullCheck: true})
	must(t, err)
	_, err = st.SaveIndexer(ctx, store.Indexer{Name: "fake", URL: srv.URL + "/indexer", APIKey: "k", Enabled: true})
	must(t, err)
	_, err = st.SaveMediaServer(ctx, store.MediaServer{Name: "plex", URL: srv.URL + "/plex", Token: "t", MovieLibrary: "1", Enabled: true,
		Mappings: []pathmap.Mapping{{Local: root, Remote: "/remote/movies"}}})
	must(t, err)
	profile, _ := st.DefaultProfile(ctx)
	profile.Config.MinMBPerMinute, profile.Config.MaxMBPerMinute = 0, 0
	must(t, st.SaveProfile(ctx, profile))

	sched := scheduler.New(sqldb, log, scheduler.WithPollInterval(20*time.Millisecond))
	svc := &Services{Store: st, Log: log, TMDBBaseURL: srv.URL + "/tmdb"}
	defer svc.Close()
	bus := events.New(log)
	m := NewManager(st, svc, sched, bus, log)
	must(t, m.Start(ctx))
	done := make(chan struct{})
	go func() { _ = sched.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	mv, err := m.Request(ctx, 42, 0, 0)
	must(t, err)
	if _, err := m.Request(ctx, 42, 0, 0); !errors.Is(err, ErrAlreadyRequested) {
		t.Errorf("second request: %v", err)
	}

	eventually(t, "movie to become available", func() bool {
		cur, _ := st.Movie(ctx, mv.ID)
		if cur.State == store.MovieFailed {
			t.Fatalf("movie failed: %s", cur.LastError)
		}
		return cur.State == store.MovieAvailable
	})

	folder := filepath.Join(root, "Film (2024) {tmdb-42}")
	file := filepath.Join(folder, "Film (2024) {tmdb-42}.mkv")
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, movie) {
		t.Fatalf("movie file: %v", err)
	}
	entries, _ := os.ReadDir(folder)
	if len(entries) != 1 {
		t.Errorf("folder should only hold the movie, has %d entries", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, fetch.WorkDirName, "incomplete", strconv.FormatInt(mv.ID, 10))); !os.IsNotExist(err) {
		t.Error("work dir not cleaned up")
	}
	if _, err := os.Stat(filepath.Join(root, fetch.WorkDirName, ".plexignore")); err != nil {
		t.Error(".plexignore missing")
	}

	attempts, _ := st.Attempts(ctx, mv.ID)
	if len(attempts) != 2 || attempts[1].Result != "failed" || attempts[1].ErrorKind != kindIncomplete || attempts[0].Result != "success" {
		t.Errorf("attempts = %+v", attempts)
	}
	bl, _ := st.BlocklistEntries(ctx, mv.ID)
	if len(bl) != 1 || bl[0].Title != "Film.2024.German.720p.BluRay.x264-BAD" {
		t.Errorf("blocklist = %+v", bl)
	}
	// Plex is told twice: once the start of the file is extracted (it can
	// be played while downloading) and when it is complete.
	eventually(t, "early and final plex scan", func() bool {
		world.mu.Lock()
		defer world.mu.Unlock()
		n := 0
		for _, p := range world.scanned {
			if p == "/remote/movies/Film (2024) {tmdb-42}" {
				n++
			}
		}
		return n >= 2
	})

	// Not watched yet: nothing happens.
	must(t, m.PollWatched(ctx))
	if cur, _ := st.Movie(ctx, mv.ID); cur.State != store.MovieAvailable || cur.MediaKey != "7" {
		t.Errorf("after poll: %+v", cur)
	}

	// Watched an hour ago with a delay of 0 h: deleted right away.
	world.watched.Store(true)
	must(t, m.PollWatched(ctx))
	eventually(t, "movie to be deleted", func() bool {
		cur, _ := st.Movie(ctx, mv.ID)
		return cur.State == store.MovieDeleted
	})
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("movie file still exists")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Error("movie folder still exists")
	}
}

func TestDownloadsReportPlayable(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.New(log)
	m := &Manager{active: map[int64]*activeDownload{}, bus: bus}
	m.active[7] = &activeDownload{
		release:  store.Release{Title: "Film.2024"},
		progress: download.Progress{DoneBytes: 42},
	}
	ch, unsubscribe := bus.Subscribe(8, EventDownloadProgress)
	defer unsubscribe()

	m.markPlayable(7)

	list := m.Downloads()
	if len(list) != 1 || !list[0].Playable || list[0].ReleaseTitle != "Film.2024" {
		t.Fatalf("downloads = %+v", list)
	}
	select {
	case ev := <-ch:
		got, ok := ev.Data.(Download)
		if !ok || !got.Playable || got.MovieID != 7 || got.Progress.DoneBytes != 42 {
			t.Errorf("event = %+v", ev.Data)
		}
	default:
		t.Fatal("no playable event published")
	}

	// Later progress updates keep the flag.
	m.progress(7, download.Progress{DoneBytes: 100})
	list = m.Downloads()
	if !list[0].Playable || list[0].Progress.DoneBytes != 100 {
		t.Errorf("after progress: %+v", list[0])
	}
}

func TestKeepPreventsDeletion(t *testing.T) {
	cur := store.Movie{ID: 1, State: store.MovieWatched, Keep: true}
	ctx := context.Background()
	sqldb, _ := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	defer sqldb.Close()
	sched := scheduler.New(sqldb, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m := &Manager{sched: sched}
	must(t, m.scheduleDeletion(ctx, cur))
	var n int
	_ = sqldb.QueryRow(`SELECT COUNT(*) FROM scheduled_tasks`).Scan(&n)
	if n != 0 {
		t.Error("deletion scheduled for a kept movie")
	}
}

func TestHelpers(t *testing.T) {
	if !within("/movies", "/movies/a/b.mkv") || within("/movies", "/movies") || within("/movies", "/movies2/x") || within("/movies", "/movies/../etc") {
		t.Error("within")
	}
	if got := FolderName(store.Movie{Title: "Mission: Impossible", Year: 1996, TMDBID: 954}); got != "Mission Impossible (1996) {tmdb-954}" {
		t.Errorf("FolderName = %q", got)
	}
}

// deleteTestManager builds a manager with a store and a scheduler, without
// providers. It is enough to exercise deletion.
func deleteTestManager(t *testing.T) (context.Context, *Manager, *store.Store, string) {
	t.Helper()
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	must(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	root := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath = root
	must(t, st.SetGeneral(ctx, g))
	m := &Manager{
		store: st, sched: scheduler.New(sqldb, log), bus: events.New(log), log: log.With("component", "movies"),
		now: time.Now, ctx: ctx,
		active: map[int64]*activeDownload{}, forced: map[int64]int64{}, deleting: map[int64]bool{},
	}
	return ctx, m, st, root
}

// Deleting a movie must cancel its running download and wait until the
// goroutine has stopped, so that nothing writes into the folder afterwards.
func TestDeleteCancelsAndWaitsForDownloads(t *testing.T) {
	ctx, m, st, _ := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", Year: 2024, State: store.MovieDownloading}, 0)
	must(t, err)

	var cancelled atomic.Bool
	downloadDone := make(chan struct{})
	m.mu.Lock()
	m.active[mv.ID] = &activeDownload{
		cancel: func(error) { cancelled.Store(true) },
		done:   downloadDone,
	}
	m.mu.Unlock()

	deleted := make(chan error, 1)
	go func() { deleted <- m.Delete(ctx, mv.ID, true) }()

	// Delete must not return while the download is still running.
	select {
	case err := <-deleted:
		t.Fatalf("Delete returned before the download stopped: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if !cancelled.Load() {
		t.Error("running download was not cancelled")
	}

	close(downloadDone)
	select {
	case err := <-deleted:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Delete did not return after the download stopped")
	}
	if _, err := st.Movie(ctx, mv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("movie still exists: %v", err)
	}
	m.mu.Lock()
	stillDeleting := m.deleting[mv.ID]
	m.mu.Unlock()
	if stillDeleting {
		t.Error("movie is still marked as deleting")
	}
}

// A file that a cancelled download still tracks while it finishes must be
// removed by Delete instead of being left behind.
func TestDeleteRemovesFileTrackedWhileCancelling(t *testing.T) {
	ctx, m, st, root := deleteTestManager(t)
	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", Year: 2024, State: store.MovieDownloading}, 0)
	must(t, err)

	file := filepath.Join(root, "Film (2024) {tmdb-42}", "Film (2024) {tmdb-42}.mkv")
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	must(t, os.WriteFile(file, []byte("video"), 0o644))

	dctx, cancel := context.WithCancelCause(ctx)
	downloadDone := make(chan struct{})
	m.mu.Lock()
	m.active[mv.ID] = &activeDownload{cancel: cancel, done: downloadDone}
	m.mu.Unlock()

	// The download notices the cancellation, writes and tracks its file, and
	// only then finishes.
	go func() {
		<-dctx.Done()
		_ = st.TrackFile(ctx, mv.ID, file, 5)
		close(downloadDone)
	}()

	must(t, m.Delete(ctx, mv.ID, true))
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file tracked by the cancelled download was left behind")
	}
}

// A movie that is being deleted must not start its queued download.
func TestDeleteBlocksQueuedDownloads(t *testing.T) {
	ctx, m, st, _ := deleteTestManager(t)
	// No movies path: if the guard were missing, the download would fail
	// early instead of touching providers.
	g := store.DefaultGeneral()
	must(t, st.SetGeneral(ctx, g))

	indexerID, err := st.SaveIndexer(ctx, store.Indexer{Name: "test", URL: "http://example.invalid", Enabled: true})
	must(t, err)

	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", Year: 2024, State: store.MovieQueued}, 0)
	must(t, err)
	must(t, st.ReplaceReleases(ctx, mv.ID, []store.Release{{
		MovieID: mv.ID, IndexerID: indexerID, GUID: "film", Title: "Film.2024.German.720p.WEB.h264-GRP",
		Size: 200000, NZBURL: "http://example.invalid/getnzb/film", Fingerprint: "fp",
	}}))

	m.mu.Lock()
	m.deleting[mv.ID] = true
	m.mu.Unlock()
	m.startQueued()

	m.mu.Lock()
	_, running := m.active[mv.ID]
	m.mu.Unlock()
	if running {
		t.Error("a download started for a movie that is being deleted")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
