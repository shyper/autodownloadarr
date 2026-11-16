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

// Each assigned profile's watchlist is imported for its user; a failing
// profile does not stop the others.
func TestPollImportsProfileWatchlists(t *testing.T) {
	var bobRemoved, ownerRemoved atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Plex-Token")
		switch r.URL.Path {
		case "/tmdb/movie/42":
			_, _ = w.Write([]byte(`{"id":42,"title":"Owner Film","release_date":"2024-05-01","runtime":100}`))
		case "/tmdb/movie/43":
			_, _ = w.Write([]byte(`{"id":43,"title":"Bob Film","release_date":"2023-01-01","runtime":90}`))
		case "/discover/actions/removeFromWatchlist":
			switch key := r.URL.Query().Get("ratingKey"); {
			case token == "bob-account" && key == "b1":
				bobRemoved.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			case token == "owner-account" && key == "o1":
				ownerRemoved.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			t.Errorf("unexpected removal by %q of %q", token, r.URL.Query().Get("ratingKey"))
			w.WriteHeader(http.StatusBadRequest)
		case "/discover/library/sections/watchlist/all":
			switch token {
			case "owner-account":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"o1","title":"Owner Film","type":"movie","Guid":[{"id":"tmdb://42"}]}]}}`))
			case "bob-account":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"ratingKey":"b1","title":"Bob Film","type":"movie","Guid":[{"id":"tmdb://43"}]},
					{"ratingKey":"b2","title":"Owner Film","type":"movie","Guid":[{"id":"tmdb://42"}]}]}}`))
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
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
	for id, name := range map[int64]string{1: "alice", 2: "bob", 3: "carol"} {
		if _, err := sqldb.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,'x','user',0,0)`, id, name); err != nil {
			t.Fatal(err)
		}
	}
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
	if _, err := st.SaveMediaServer(ctx, store.MediaServer{Kind: "plex", Name: "Plex", URL: "http://plex:32400", Token: "server-token",
		AccountToken: "owner-account", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncMediaProfiles(ctx, []store.MediaProfileInfo{
		{ExternalID: "o", Name: "Owner", Owner: true}, {ExternalID: "b", Name: "Bob"}, {ExternalID: "c", Name: "Carol"},
	}); err != nil {
		t.Fatal(err)
	}
	profiles, err := st.MediaProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, p := range profiles {
		ids[p.ExternalID] = p.ID
	}
	for ext, user := range map[string]int64{"o": 1, "b": 2, "c": 3} {
		if err := st.AssignMediaProfile(ctx, ids[ext], user, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMediaProfileTokens(ctx, ids["b"], "bob-server", "bob-account"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaProfileTokens(ctx, ids["c"], "carol-server", "carol-account"); err != nil {
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

	if err := wm.PollNow(ctx); err == nil {
		t.Fatal("carol's failing watchlist was not reported")
	}
	owner, err := st.MovieByTMDB(ctx, 42)
	if err != nil || owner.RequestedBy != 1 {
		t.Fatalf("owner film: requested by %d, %v", owner.RequestedBy, err)
	}
	bobs, err := st.MovieByTMDB(ctx, 43)
	if err != nil || bobs.RequestedBy != 2 {
		t.Fatalf("bob film: requested by %d, %v", bobs.RequestedBy, err)
	}
	if p, _ := st.MediaProfile(ctx, ids["c"]); p.LastError == "" {
		t.Fatal("carol's error not recorded")
	}

	// Watched films leave only the watchlist of the account that watched
	// them: bob's own film goes, the owner's film stays on bob's list.
	now := time.Now()
	for _, m := range []store.Movie{owner, bobs} {
		m.State, m.WatchedAt = store.MovieWatched, &now
		if err := st.UpdateMovie(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AssignMediaProfile(ctx, ids["c"], 3, false); err != nil {
		t.Fatal(err)
	}
	if err := wm.PollNow(ctx); err != nil {
		t.Fatal(err)
	}
	if bobRemoved.Load() != 1 || ownerRemoved.Load() != 1 {
		t.Fatalf("removals: bob %d, owner %d", bobRemoved.Load(), ownerRemoved.Load())
	}
}
