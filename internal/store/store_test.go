package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/secrets"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	box, _ := secrets.New(make([]byte, 32))
	return New(sqldb, box)
}

func TestSettings(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	g, err := s.General(ctx)
	if err != nil || g.DeleteAfterHours != 48 {
		t.Fatalf("defaults = %+v %v", g, err)
	}
	g.MoviesPath = "/movies"
	if err := s.SetGeneral(ctx, g); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.General(ctx); got.MoviesPath != "/movies" || got.DeleteAfterHours != 48 {
		t.Errorf("general = %+v", got)
	}

	if err := s.SetTMDBKey(ctx, "tmdb-key"); err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'tmdb'`).Scan(&raw)
	if raw == `"tmdb-key"` {
		t.Error("TMDB key stored in plain text")
	}
	if k, _ := s.TMDBKey(ctx); k != "tmdb-key" {
		t.Errorf("key = %q", k)
	}

	id1, _ := s.PlexClientID(ctx)
	id2, _ := s.PlexClientID(ctx)
	if id1 == "" || id1 != id2 {
		t.Errorf("client id not stable: %q %q", id1, id2)
	}
}

func TestUsenetServerKeepsPassword(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	id, err := s.SaveUsenetServer(ctx, UsenetServer{Host: "news.example.com", TLS: true, Username: "u", Password: "p", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUsenetServer(ctx, UsenetServer{ID: id, Host: "news.example.com", TLS: true, Username: "u2", Connections: 30, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.UsenetServers(ctx)
	if len(list) != 1 || list[0].Password != "p" || list[0].Username != "u2" || list[0].Port != 563 || list[0].Connections != 30 {
		t.Fatalf("servers = %+v", list)
	}
	if cfg := list[0].NNTPConfig(); cfg.Address != "news.example.com:563" || !cfg.TLS {
		t.Errorf("config = %+v", cfg)
	}
	if _, err := s.SaveUsenetServer(ctx, UsenetServer{}); err == nil {
		t.Error("server without host accepted")
	}
	if err := s.DeleteUsenetServer(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
}

func TestIndexerAndMediaServer(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	id, err := s.SaveIndexer(ctx, Indexer{Name: "Geek", URL: "https://api.example.com", APIKey: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveIndexer(ctx, Indexer{ID: id, Name: "Geek", URL: "https://api.example.com", MovieCategories: []int{2000, 2040}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.Indexers(ctx)
	if len(idx) != 1 || idx[0].APIKey != "k" || len(idx[0].MovieCategories) != 2 {
		t.Errorf("indexers = %+v", idx)
	}
	if _, err := s.SaveIndexer(ctx, Indexer{URL: "ftp://x"}); err == nil {
		t.Error("invalid URL accepted")
	}

	if _, found, _ := s.MediaServer(ctx); found {
		t.Fatal("media server before saving")
	}
	ms := MediaServer{Name: "Plex", URL: "http://plex:32400", Token: "tok", MovieLibrary: "3", Enabled: true,
		Mappings: []pathmap.Mapping{{Local: "/filme", Remote: "/mnt/hdd/filme"}, {Local: "", Remote: "ignored"}}}
	if _, err := s.SaveMediaServer(ctx, ms); err != nil {
		t.Fatal(err)
	}
	ms.Token = ""
	ms.Mappings = ms.Mappings[:1]
	ms.MovieLibrary = "4"
	if _, err := s.SaveMediaServer(ctx, ms); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.MediaServer(ctx)
	if err != nil || !found || got.Token != "tok" || got.MovieLibrary != "4" || len(got.Mappings) != 1 {
		t.Errorf("media server = %+v %v", got, err)
	}
}

func TestProfilesAndPasswords(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	p, err := s.DefaultProfile(ctx)
	if err != nil || !p.Default || p.Config.MaxResolution != 1080 {
		t.Fatalf("default profile = %+v %v", p, err)
	}
	p.Config.MaxResolution = 720
	p.Config.RequiredWords = []string{"German"}
	if err := s.SaveProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Profiles(ctx)
	if len(list) != 1 || list[0].Config.MaxResolution != 720 {
		t.Errorf("profiles = %+v", list)
	}
	p.Config.ForbiddenWords = []string{"/(/"}
	if err := s.SaveProfile(ctx, p); err == nil {
		t.Error("invalid profile saved")
	}

	_, _ = s.AddArchivePassword(ctx, "one")
	_, _ = s.AddArchivePassword(ctx, "two")
	_ = s.ArchivePasswordHit(ctx, "two")
	pws, _ := s.ArchivePasswords(ctx)
	if len(pws) != 2 || pws[0].Password != "two" || pws[0].Hits != 1 {
		t.Errorf("passwords = %+v", pws)
	}
}

func TestMovieLifecycleRecords(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	m, err := s.CreateMovie(ctx, Movie{TMDBID: 1, Title: "Dune", Year: 2024, AltTitles: []string{"Dune 2"}, State: MovieSearching}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMovie(ctx, Movie{TMDBID: 1, Title: "Dune", State: MovieSearching}, 0); err == nil {
		t.Error("duplicate TMDB ID accepted")
	}
	now := time.Now()
	m.State, m.DeleteAt = MovieWatched, &now
	if err := s.UpdateMovie(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ := s.MovieByTMDB(ctx, 1)
	if got.State != MovieWatched || got.DeleteAt == nil || got.AltTitles[0] != "Dune 2" {
		t.Errorf("movie = %+v", got)
	}
	if list, _ := s.Movies(ctx, MovieWatched); len(list) != 1 {
		t.Errorf("filtered list = %d", len(list))
	}

	_ = s.ReplaceReleases(ctx, m.ID, []Release{
		{Title: "a", Fingerprint: "fa", Score: 10, NZBURL: "u"},
		{Title: "b", Fingerprint: "fb", Score: 20, NZBURL: "u"},
		{Title: "c", Fingerprint: "fc", Score: 99, Rejected: "too_large", NZBURL: "u"},
	})
	_ = s.Blocklist(ctx, BlocklistEntry{MovieID: m.ID, Fingerprint: "fb", Title: "b", Reason: "incomplete"})
	rels, _ := s.Releases(ctx, m.ID)
	if len(rels) != 3 || rels[0].Title != "b" || !rels[0].Blocklisted || rels[2].Title != "c" {
		t.Errorf("releases = %+v", rels)
	}
	if r, err := s.Release(ctx, rels[1].ID); err != nil || r.Title != "a" {
		t.Errorf("release = %+v %v", r, err)
	}

	aid, _ := s.StartAttempt(ctx, m.ID, rels[0])
	_ = s.FinishAttempt(ctx, aid, "failed", "incomplete", "3 articles missing")
	if att, _ := s.Attempts(ctx, m.ID); len(att) != 1 || att[0].Result != "failed" || att[0].FinishedAt == nil {
		t.Errorf("attempts = %+v", att)
	}

	_ = s.TrackFile(ctx, m.ID, "/movies/Dune/Dune.mkv", 100)
	if f, _ := s.Files(ctx, m.ID); len(f) != 1 {
		t.Errorf("files = %v", f)
	}
	if err := s.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Movie(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted movie: %v", err)
	}
}

func TestSeriesAndEpisodes(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	se, err := s.CreateSeries(ctx, Series{TMDBID: 1, Title: "Dark", Seasons: []int{1, 2}, StartSeason: 1, StartEpisode: 1,
		WindowSize: 3, DeleteTrigger: TriggerProgress, TriggerValue: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !se.Wants(2) || se.Wants(3) || se.Wants(0) {
		t.Errorf("season selection = %v", se.Seasons)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.UpsertEpisodes(ctx, se.ID, []Episode{{Season: 1, Episode: 2, Title: "B"}, {Season: 1, Episode: 1, Title: "A", AirDate: "2017-12-01"}}))
	eps, _ := s.Episodes(ctx, se.ID)
	if len(eps) != 2 || eps[0].Episode != 1 || eps[0].State != EpisodePending {
		t.Fatalf("episodes = %+v", eps)
	}
	eps[0].State, eps[0].Progress = MovieAvailable, 0.5
	must(s.UpdateEpisode(ctx, eps[0]))
	// Refreshing metadata keeps the state.
	must(s.UpsertEpisodes(ctx, se.ID, []Episode{{Season: 1, Episode: 1, Title: "A2", AirDate: "2017-12-01"}}))
	e, _ := s.Episode(ctx, eps[0].ID)
	if e.State != MovieAvailable || e.Title != "A2" || e.Progress != 0.5 {
		t.Errorf("episode = %+v", e)
	}
	if !e.Aired(time.Now()) || eps[1].Aired(time.Now()) {
		t.Error("Aired")
	}
	if list, _ := s.EpisodesInState(ctx, MovieAvailable); len(list) != 1 {
		t.Errorf("in state = %d", len(list))
	}

	_ = s.ReplaceReleasesOf(ctx, ForEpisode(e.ID), []Release{{Title: "Dark.S01E01", Fingerprint: "f", NZBURL: "u"}})
	if rels, _ := s.ReleasesOf(ctx, ForEpisode(e.ID)); len(rels) != 1 || rels[0].EpisodeID != e.ID {
		t.Errorf("releases = %+v", rels)
	}
	aid, _ := s.StartAttemptOf(ctx, ForEpisode(e.ID), Release{Title: "Dark.S01E01"})
	_ = s.FinishAttempt(ctx, aid, "success", "", "")
	if att, _ := s.AttemptsOf(ctx, ForEpisode(e.ID)); len(att) != 1 {
		t.Errorf("attempts = %+v", att)
	}
	_ = s.Blocklist(ctx, BlocklistEntry{EpisodeID: e.ID, Fingerprint: "f", Title: "x", Reason: "incomplete"})
	if bl, _ := s.BlocklistOf(ctx, ForEpisode(e.ID)); len(bl) != 1 || bl[0].EpisodeID != e.ID {
		t.Errorf("blocklist = %+v", bl)
	}
	_ = s.TrackFileOf(ctx, ForEpisode(e.ID), "/tv/Dark/e.mkv", 1)
	if f, _ := s.FilesOf(ctx, ForEpisode(e.ID)); len(f) != 1 {
		t.Errorf("files = %v", f)
	}
	must(s.DeleteSeries(ctx, se.ID))
	if eps, _ := s.Episodes(ctx, se.ID); len(eps) != 0 {
		t.Error("episodes not deleted with series")
	}
}
