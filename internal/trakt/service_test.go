package trakt

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
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

func serviceFixture(t *testing.T, handler http.HandlerFunc) (*Service, *sql.DB) {
	t.Helper()
	ctx := t.Context()
	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "trakt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	for _, id := range []int64{1, 2} {
		if _, err := sqldb.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,?,'user',0,0)`, id, fmt.Sprint("user", id), "hash"); err != nil {
			t.Fatal(err)
		}
	}
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	if err := st.SetTraktClientID(ctx, "client"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTMDBKey(ctx, "tmdb"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &movies.Services{Store: st, Log: log, TMDBBaseURL: server.URL + "/tmdb"}
	t.Cleanup(svc.Close)
	sc := scheduler.New(sqldb, log)
	bus := events.New(log)
	mv := movies.NewManager(st, svc, sc, bus, log)
	se := series.NewManager(st, svc, sc, bus, log, mv.Limiter())
	s := New(st, mv, se, sc, "https://media.example", log)
	s.APIURL = server.URL
	s.AuthURL = server.URL
	s.HTTP = server.Client()
	if err := st.SaveTraktLink(ctx, store.TraktLink{UserID: 1, ClientID: "client", AccountID: "account", Username: "trakt-user", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	return s, sqldb
}

func TestImportOwnershipQuotasDeduplicationAndRemovals(t *testing.T) {
	s, sqldb := serviceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/lists":
			fmt.Fprint(w, `[{"name":"Own list","ids":{"trakt":7}}]`)
		case "/sync/watchlist/movies/added/asc":
			fmt.Fprint(w, `[{"type":"movie","movie":{"ids":{"tmdb":42}}},{"type":"movie","movie":{"ids":{"tmdb":44}}},{"type":"movie","movie":{"ids":{"tmdb":45}}},{"type":"movie","movie":{"title":"No TMDB ID"}}]`)
		case "/sync/watchlist/shows/added/asc":
			fmt.Fprint(w, `[{"type":"show","show":{"ids":{"tmdb":99}}}]`)
		case "/users/me/lists/7/items/movie,show":
			fmt.Fprint(w, `[{"type":"movie","movie":{"ids":{"tmdb":42}}},{"type":"movie","movie":{"ids":{"tmdb":43}}},{"type":"show","show":{"ids":{"tmdb":56}}}]`)
		case "/tmdb/movie/42":
			fmt.Fprint(w, `{"id":42,"title":"Film","release_date":"2026-01-01"}`)
		case "/tmdb/movie/43":
			fmt.Fprint(w, `{"id":43,"title":"Second film","release_date":"2026-01-01"}`)
		case "/tmdb/tv/99":
			fmt.Fprint(w, `{"id":99,"name":"Show","seasons":[]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
	ctx := t.Context()
	removed, err := s.Store.CreateMovie(ctx, store.Movie{TMDBID: 44, Title: "Removed", State: store.MovieSearching}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DeleteMovie(ctx, removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CreateMovie(ctx, store.Movie{TMDBID: 45, Title: "Existing", State: store.MovieExpired}, 2); err != nil {
		t.Fatal(err)
	}
	se, err := s.Store.CreateSeries(ctx, store.Series{TMDBID: 56, Title: "Removed show", WindowSize: 2}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DeleteSeries(ctx, se.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, `UPDATE users SET quota_requests=2 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	settings := store.TraktSettings{Enabled: true, Watchlist: true, ListIDs: []int64{7}, PollMinutes: 17}
	if err := s.SaveSettings(ctx, 1, settings); err != nil {
		t.Fatal(err)
	}
	task := scheduler.Task{Payload: json.RawMessage(`1`)}
	if err := s.HandleTask(ctx, task); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("quota not enforced: %v", err)
	}
	link, err := s.Store.TraktLink(ctx, 1)
	if err != nil || link.Requested != 2 || link.Skipped != 5 || link.Failed != 1 || link.LastError != "quota_exceeded" {
		t.Fatalf("result: %+v %v", link, err)
	}
	mv, err := s.Store.MovieByTMDB(ctx, 42)
	if err != nil || mv.RequestedBy != 1 {
		t.Fatal("movie assigned to wrong user")
	}
	show, err := s.Store.SeriesByTMDB(ctx, 99)
	if err != nil || show.RequestedBy != 1 {
		t.Fatal("series assigned to wrong user")
	}
	existing, _ := s.Store.MovieByTMDB(ctx, 45)
	if existing.RequestedBy != 2 || existing.State != store.MovieExpired {
		t.Fatal("existing request restored or reassigned")
	}
	for _, id := range []int{43, 44} {
		if _, err := s.Store.MovieByTMDB(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("blocked movie %d imported", id)
		}
	}
	if _, err := s.Store.SeriesByTMDB(ctx, 56); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("removed show reimported")
	}
	if _, err := sqldb.ExecContext(ctx, `UPDATE users SET quota_requests=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// Sources becoming empty must not delete existing requests or media.
	settings.Watchlist = false
	settings.ListIDs = []int64{}
	if err := s.SaveSettings(ctx, 1, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.MovieByTMDB(ctx, 42); err != nil {
		t.Fatal("disabling sources deleted a request")
	}
}

func TestRefreshSerializedAndOwnListsValidated(t *testing.T) {
	var refreshCalls atomic.Int32
	s, _ := serviceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalls.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["refresh_token"] != "refresh" {
				t.Error("reused a single-use refresh token")
			}
			fmt.Fprintf(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":604800,"created_at":%d}`, time.Now().Unix())
		case "/users/me/lists":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Error("did not use refreshed credentials")
			}
			fmt.Fprint(w, `[{"name":"Own list","ids":{"trakt":7}}]`)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := t.Context()
	n, _ := s.Store.TraktLink(ctx, 1)
	n.ExpiresAt = 1
	if err := s.Store.SaveTraktLink(ctx, n); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := s.Lists(ctx, 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if refreshCalls.Load() != 1 {
		t.Fatal("concurrent refresh consumed the same refresh token twice")
	}
	n, _ = s.Store.TraktLink(ctx, 1)
	if n.RefreshToken != "new-refresh" {
		t.Fatal("rotated refresh token not persisted")
	}
	if err := s.SaveSettings(ctx, 1, store.TraktSettings{Enabled: true, ListIDs: []int64{999}, PollMinutes: 30}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("accepted another user's list: %v", err)
	}
}

func TestSchedulePersistsIntervalAndDoesNotResetPendingWork(t *testing.T) {
	s, sqldb := serviceFixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	ctx := t.Context()
	if err := s.Store.SetTraktSettings(ctx, 1, store.TraktSettings{Enabled: true, Watchlist: true, PollMinutes: 17}); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := sqldb.QueryRowContext(ctx, `SELECT version FROM scheduled_tasks WHERE dedupe_key=?`, taskKey(1)).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	var again int64
	if err := sqldb.QueryRowContext(ctx, `SELECT version FROM scheduled_tasks WHERE dedupe_key=?`, taskKey(1)).Scan(&again); err != nil || again != version {
		t.Fatal("poll reset a pending job")
	}
	if err := s.HandleTask(ctx, scheduler.Task{Payload: json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Store.TraktLink(ctx, 1)
	if n.NextSyncAt-n.LastSyncAt < 17*60000 || n.NextSyncAt-n.LastSyncAt > 17*60000+100 {
		t.Fatal("user interval not persisted")
	}
	if _, err := sqldb.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE dedupe_key=?`, taskKey(1)); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Store, s.Movies, s.Series, s.Scheduler, s.PublicURL, s.Log)
	restarted.APIURL = s.APIURL
	restarted.HTTP = s.HTTP
	if err := restarted.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	active, _ := s.Store.TraktTaskActive(ctx, taskKey(1))
	if active {
		t.Fatal("restart ignored the next sync deadline")
	}
	if err := restarted.Queue(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	active, _ = s.Store.TraktTaskActive(ctx, taskKey(1))
	if !active {
		t.Fatal("manual sync did not bypass the interval")
	}
}

func TestPauseCancelsQueuedWorkWithoutProviderAccess(t *testing.T) {
	s, _ := serviceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("pausing an import contacted Trakt")
		w.WriteHeader(503)
	})
	ctx := t.Context()
	if err := s.Store.SetTraktSettings(ctx, 1, store.TraktSettings{Enabled: true, Watchlist: true, PollMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(ctx, 1, store.TraktSettings{Enabled: false, Watchlist: true, ListIDs: []int64{7}, PollMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	active, err := s.Store.TraktTaskActive(ctx, taskKey(1))
	if err != nil || active {
		t.Fatal("pause left a pending import")
	}
	if err := s.HandleTask(ctx, scheduler.Task{Payload: json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
}
