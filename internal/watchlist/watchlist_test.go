package watchlist

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
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestPollRequestsWatchlist(t *testing.T) {
	var removed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discover/actions/removeFromWatchlist":
			if r.Method != http.MethodPut || r.URL.Query().Get("ratingKey") != "1" || r.Header.Get("X-Plex-Token") != "account-token" {
				t.Error("invalid watchlist removal request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			removed.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/tmdb/movie/42":
			_, _ = w.Write([]byte(`{"id":42,"title":"Film","release_date":"2024-05-01","runtime":100}`))
		case "/tmdb/movie/43":
			_, _ = w.Write([]byte(`{"id":43,"title":"Fallback","release_date":"2023-01-01","runtime":90}`))
		case "/tmdb/tv/99":
			_, _ = w.Write([]byte(`{"id":99,"name":"Show","first_air_date":"2020-01-01","seasons":[]}`))
		case "/discover/library/sections/watchlist/all":
			if r.Header.Get("X-Plex-Token") != "account-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"ratingKey":"1","title":"Film","year":2024,"type":"movie","guid":"plex://movie/1","Guid":[{"id":"tmdb://42"}]},
				{"ratingKey":"2","title":"Show","year":2020,"type":"show","guid":"plex://show/2","Guid":[{"id":"tmdb://99"}]},
				{"ratingKey":"3","title":"Fallback","year":2023,"type":"movie","guid":"plex://movie/3"}]}}`))
		case "/metadata/library/metadata/3":
			if r.Header.Get("X-Plex-Token") != "account-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Guid":[{"id":"tmdb://43"}]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	if err := st.SetTMDBKey(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	g := store.DefaultGeneral()
	g.Watchlist = true
	if err := st.SetGeneral(ctx, g); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveMediaServer(ctx, store.MediaServer{
		Kind: "plex", Name: "Plex", URL: "http://plex:32400", Token: "server-token",
		AccountToken: "account-token", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &movies.Services{Store: st, Log: log, TMDBBaseURL: srv.URL + "/tmdb",
		PlexDiscoverURL: srv.URL + "/discover", PlexMetadataURL: srv.URL + "/metadata"}
	defer svc.Close()
	sched := scheduler.New(sqldb, log)
	bus := events.New(log)
	mv := movies.NewManager(st, svc, sched, bus, log)
	se := series.NewManager(st, svc, sched, bus, log, mv.Limiter())
	wm := NewManager(st, svc, mv, se, log)

	if err := wm.PollNow(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MovieByTMDB(ctx, 42); err != nil {
		t.Errorf("movie 42 not requested: %v", err)
	}
	if _, err := st.MovieByTMDB(ctx, 43); err != nil {
		t.Errorf("movie 43 (resolved from metadata) not requested: %v", err)
	}
	if _, err := st.SeriesByTMDB(ctx, 99); err != nil {
		t.Errorf("series 99 not requested: %v", err)
	}

	// A second poll must not create duplicates.
	if err := wm.PollNow(ctx); err != nil {
		t.Fatal(err)
	}
	if removed.Load() != 0 {
		t.Fatal("unwatched movie removed")
	}
	movie, err := st.MovieByTMDB(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	movie.State, movie.WatchedAt = store.MovieWatched, &now
	if err := st.UpdateMovie(ctx, movie); err != nil {
		t.Fatal(err)
	}
	if err := wm.PollNow(ctx); err != nil {
		t.Fatal(err)
	}
	if removed.Load() != 1 {
		t.Fatal("watched movie not removed")
	}
	list, err := st.Movies(ctx)
	if err != nil || len(list) != 2 {
		t.Errorf("movies after second poll = %d, %v", len(list), err)
	}
}

func TestPollRespectsSettings(t *testing.T) {
	ctx := context.Background()
	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &movies.Services{Store: st, Log: log}
	defer svc.Close()
	sched := scheduler.New(sqldb, log)
	bus := events.New(log)
	mv := movies.NewManager(st, svc, sched, bus, log)
	se := series.NewManager(st, svc, sched, bus, log, mv.Limiter())
	wm := NewManager(st, svc, mv, se, log)

	// Disabled by default: no server request and no error.
	if err := wm.PollNow(ctx); err != nil {
		t.Errorf("poll while disabled: %v", err)
	}
}

func TestTMDBID(t *testing.T) {
	if got := tmdbID([]string{"imdb://tt1", "tmdb://693134", "tvdb://1"}); got != 693134 {
		t.Errorf("tmdbID = %d", got)
	}
	if got := tmdbID([]string{"imdb://tt1", "tmdb://nope"}); got != 0 {
		t.Errorf("invalid tmdbID = %d", got)
	}
}
