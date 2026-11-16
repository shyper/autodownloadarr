package movies

import (
	"fmt"
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
	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/store"
)

// A request counts as watched when the requester's own profile watched it,
// not when the server owner did.
func TestPollWatchedUsesRequesterProfile(t *testing.T) {
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
	m := NewManager(st, &Services{Store: st, Log: log}, scheduler.New(sqldb, log), events.New(log), log)
	var ownerViews, profileViews atomic.Int32
	var profileFails atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections/1/all" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		views := ownerViews.Load()
		switch r.Header.Get("X-Plex-Token") {
		case "owner":
		case "bob-server":
			if profileFails.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			views = profileViews.Load()
		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"7","title":"Film","viewCount":%d,"lastViewedAt":%d,"duration":6000000,
			"Media":[{"Part":[{"file":"/remote/Film (2024)/Film.mkv"}]}]}]}}`, views, time.Now().Add(-time.Hour).Unix())
	}))
	defer srv.Close()

	_, err = st.SaveMediaServer(ctx, store.MediaServer{Kind: "plex", Name: "Plex", URL: srv.URL, Token: "owner", MovieLibrary: "1",
		Enabled: true, Mappings: []pathmap.Mapping{{Local: root, Remote: "/remote"}}})
	must(t, err)
	_, err = sqldb.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(2,'bob','x','user',0,0)`)
	must(t, err)
	must(t, st.SyncMediaProfiles(ctx, []store.MediaProfileInfo{{ExternalID: "owner", Name: "Owner", Owner: true}, {ExternalID: "bob", Name: "Bob"}}))
	profiles, err := st.MediaProfiles(ctx)
	must(t, err)
	bob := profiles[1]
	must(t, st.AssignMediaProfile(ctx, bob.ID, 2, false))
	must(t, st.SetMediaProfileTokens(ctx, bob.ID, "bob-server", "bob-account"))

	mv, err := st.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Film", Year: 2024, State: store.MovieAvailable}, 2)
	must(t, err)
	now := time.Now()
	mv.Folder, mv.AvailableAt = filepath.Join(root, "Film (2024)"), &now
	must(t, st.UpdateMovie(ctx, mv))
	poll := func() store.Movie {
		t.Helper()
		must(t, m.PollWatched(ctx))
		cur, err := st.Movie(ctx, mv.ID)
		must(t, err)
		return cur
	}

	cur := poll()
	if !cur.Base.Set || cur.Base.Account != bob.AccountKey() || cur.State != store.MovieAvailable {
		t.Fatalf("first poll: %+v %s", cur.Base, cur.State)
	}
	ownerViews.Store(1)
	if cur = poll(); cur.State != store.MovieAvailable {
		t.Fatal("the owner's viewing counted for bob's request")
	}
	// A profile that cannot be read leaves the request alone and records
	// the error.
	profileFails.Store(true)
	profileViews.Store(1)
	if cur = poll(); cur.State != store.MovieAvailable {
		t.Fatal("state changed while the profile was unreadable")
	}
	if p, _ := st.MediaProfile(ctx, bob.ID); p.LastError == "" {
		t.Fatal("profile error not recorded")
	}
	profileFails.Store(false)
	if cur = poll(); cur.State != store.MovieWatched {
		t.Fatalf("bob's viewing not detected: %s", cur.State)
	}
	if p, _ := st.MediaProfile(ctx, bob.ID); p.LastError != "" {
		t.Fatalf("profile error not cleared: %q", p.LastError)
	}
	// Removing the assignment switches back to the owner; the movie stays
	// watched.
	must(t, st.AssignMediaProfile(ctx, bob.ID, 0, false))
	if cur = poll(); cur.State != store.MovieWatched || cur.Base.Account != "" {
		t.Fatalf("after unassigning: %s %+v", cur.State, cur.Base)
	}
}

func TestSwitchAccount(t *testing.T) {
	it := mediaserver.Item{ViewCount: 3, ViewOffset: time.Minute}
	base := store.WatchBase{Set: true, Views: 1, Account: ""}
	SwitchAccount(&base, "profile:1", false, it)
	if base != (store.WatchBase{Account: "profile:1"}) {
		t.Fatalf("unwatched switch = %+v", base)
	}
	base = store.WatchBase{Set: true, Views: 5, Account: "profile:1"}
	SwitchAccount(&base, "", true, it)
	if base != (store.WatchBase{Set: true, Views: 2, Offset: time.Minute.Milliseconds()}) {
		t.Fatalf("watched switch = %+v", base)
	}
	if w, _ := Observe(&base, it, time.Time{}); !w || base.Account != "" {
		t.Fatalf("watched item lost its state: %+v", base)
	}
	same := store.WatchBase{Set: true, Views: 9, Account: "x"}
	SwitchAccount(&same, "x", false, it)
	if same.Views != 9 {
		t.Fatal("same account changed the baseline")
	}
	base = store.WatchBase{Account: "profile:4"}
	if Observe(&base, it, time.Time{}); base.Account != "profile:4" {
		t.Fatal("Observe dropped the account")
	}
}
