package movies

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/store"
)

// A failing Plex scan is shown as a warning and retried until it works.
func TestScanRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	defer func(d time.Duration) { scanRetryDelay = d }(scanRetryDelay)
	scanRetryDelay = 10 * time.Millisecond

	var calls atomic.Int32
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections/1/refresh" || r.URL.Query().Get("path") != "/remote/movies/Film" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer plexSrv.Close()

	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	must(t, err)
	defer sqldb.Close()
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	_, err = st.SaveMediaServer(ctx, store.MediaServer{Name: "plex", URL: plexSrv.URL, Token: "t", MovieLibrary: "1", Enabled: true,
		Mappings: []pathmap.Mapping{{Local: "/movies", Remote: "/remote/movies"}}})
	must(t, err)

	sched := scheduler.New(sqldb, log, scheduler.WithPollInterval(10*time.Millisecond),
		scheduler.WithBackoff(func(int) time.Duration { return 10 * time.Millisecond }))
	svc := &Services{Store: st, Log: log}
	svc.HandleScanRetries(sched)
	svc.HandleScanRetries(sched) // both managers call it
	done := make(chan struct{})
	go func() { _ = sched.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	svc.Scan(ctx, false, "/movies/Film")
	if w := svc.Warnings(); len(w) != 1 || w[0].Code != WarnScanMovies {
		t.Fatalf("warnings after failed scan = %+v", w)
	}
	eventually(t, "successful retry", func() bool { return calls.Load() == 3 && len(svc.Warnings()) == 0 })
}
