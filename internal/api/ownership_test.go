package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestSharedLibraryOwnership(t *testing.T) {
	e := newEnv(t)
	admin := e.client(t)
	e.completeSetup(t, admin)
	_, created := e.do(t, admin, "POST", "/api/v1/users", map[string]string{"username": "bob", "password": "longenough", "role": "user"})
	owner := int64(created["id"].(float64))
	mv, err := e.store.CreateMovie(t.Context(), store.Movie{TMDBID: 123, Title: "Owned", State: store.MovieDeleted}, 1)
	if err != nil {
		t.Fatal(err)
	}
	se, err := e.store.CreateSeries(t.Context(), store.Series{TMDBID: 234, Title: "Show", WindowSize: 3, DeleteTrigger: "watched"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertEpisodes(t.Context(), se.ID, []store.Episode{{Season: 1, Episode: 1}}); err != nil {
		t.Fatal(err)
	}
	eps, err := e.store.Episodes(t.Context(), se.ID)
	if err != nil {
		t.Fatal(err)
	}
	bob := e.client(t)
	e.do(t, bob, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "longenough"})
	for _, path := range []string{"/api/v1/movies", fmt.Sprintf("/api/v1/movies/%d", mv.ID), "/api/v1/series"} {
		res, body := e.do(t, bob, "GET", path, nil)
		if res.StatusCode != 200 {
			t.Fatalf("shared read %s: %d %v", path, res.StatusCode, body)
		}
	}
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{"POST", fmt.Sprintf("/api/v1/movies/%d/keep", mv.ID), map[string]bool{"keep": true}},
		{"DELETE", fmt.Sprintf("/api/v1/movies/%d?forget=1", mv.ID), nil},
		{"POST", "/api/v1/movies", map[string]int{"tmdbId": 123}},
		{"DELETE", fmt.Sprintf("/api/v1/series/%d", se.ID), nil},
		{"PUT", fmt.Sprintf("/api/v1/series/%d", se.ID), map[string]bool{"paused": true}},
		{"POST", fmt.Sprintf("/api/v1/episodes/%d/search", eps[0].ID), nil},
		{"POST", fmt.Sprintf("/api/v1/episodes/%d/skip", eps[0].ID), nil},
	} {
		res, body := e.do(t, bob, op.method, op.path, op.body)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("foreign mutation %s: %d %v", op.path, res.StatusCode, body)
		}
	}
	own, err := e.store.CreateMovie(t.Context(), store.Movie{TMDBID: 456, Title: "Bob", State: store.MovieAvailable}, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*http.Client{bob, admin} {
		res, body := e.do(t, client, "POST", fmt.Sprintf("/api/v1/movies/%d/keep", own.ID), map[string]bool{"keep": true})
		if res.StatusCode != 200 {
			t.Fatalf("owner/admin denied: %d %v", res.StatusCode, body)
		}
	}
}
