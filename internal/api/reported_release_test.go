package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestReportFaultyMovieAndEpisode(t *testing.T) {
	e := newEnv(t)
	admin := e.client(t)
	e.completeSetup(t, admin)
	root := t.TempDir()
	g := store.DefaultGeneral()
	g.MoviesPath = root
	g.SeriesPath = root
	if err := e.store.SetGeneral(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	mv, err := e.store.CreateMovie(t.Context(), store.Movie{TMDBID: 1, Title: "Bad movie", State: store.MovieAvailable}, 1)
	if err != nil {
		t.Fatal(err)
	}
	se, err := e.store.CreateSeries(t.Context(), store.Series{TMDBID: 2, Title: "Bad show", WindowSize: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertEpisodes(t.Context(), se.ID, []store.Episode{{Season: 1, Episode: 1, State: store.MovieAvailable}}); err != nil {
		t.Fatal(err)
	}
	eps, err := e.store.Episodes(t.Context(), se.ID)
	if err != nil {
		t.Fatal(err)
	}
	ep := eps[0]
	mv.ReleaseKey, mv.ReleaseTitle, mv.ReleaseFingerprint = "1:guid:badmovie", "badmovie", "badmovie"
	ep.ReleaseKey, ep.ReleaseTitle, ep.ReleaseFingerprint = "1:guid:badepisode", "badepisode", "badepisode"
	if err := e.store.UpdateMovie(t.Context(), mv); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateEpisode(t.Context(), ep); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, key string
		owner     store.Owner
	}{{fmt.Sprintf("/api/v1/movies/%d/report", mv.ID), mv.ReleaseKey, store.ForMovie(mv.ID)}, {fmt.Sprintf("/api/v1/episodes/%d/report", ep.ID), ep.ReleaseKey, store.ForEpisode(ep.ID)}} {
		file := filepath.Join(root, tc.key[7:]+".mp4")
		if err := os.WriteFile(file, []byte("bad"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := e.store.TrackFileOf(t.Context(), tc.owner, file, 3); err != nil {
			t.Fatal(err)
		}
		res, body := e.do(t, admin, "POST", tc.path, map[string]string{"releaseKey": "stale"})
		if res.StatusCode != 400 {
			t.Fatalf("stale report: %d %v", res.StatusCode, body)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("stale report removed file: %v", err)
		}
		res, body = e.do(t, admin, "POST", tc.path, map[string]string{"releaseKey": tc.key})
		if res.StatusCode != http.StatusAccepted || body["state"] != store.MovieSearching {
			t.Fatalf("report: %d %v", res.StatusCode, body)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("bad file retained: %v", err)
		}
		bl, err := e.store.BlocklistOf(t.Context(), tc.owner)
		if err != nil || len(bl) != 1 || bl[0].Reason != "reported_bad" {
			t.Fatalf("report blocklist: %+v %v", bl, err)
		}
		res, _ = e.do(t, admin, "POST", tc.path, map[string]string{"releaseKey": tc.key})
		if res.StatusCode != 400 {
			t.Fatalf("duplicate report status: %d", res.StatusCode)
		}
	}
}
