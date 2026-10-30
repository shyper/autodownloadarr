package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

func fake(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.URL.Query().Get("api_key") == "good" || r.Header.Get("Authorization") == "Bearer eyJgood"
		if !auth {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status_code":7}`))
			return
		}
		if r.URL.Query().Get("language") != "de-DE" {
			t.Errorf("language = %q", r.URL.Query().Get("language"))
		}
		switch r.URL.Path {
		case "/search/movie":
			if r.URL.Query().Get("query") != "dune" || r.URL.Query().Get("year") != "2024" {
				t.Errorf("query = %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"results":[{"id":693134,"title":"Dune: Part Two","original_title":"Dune: Part Two","release_date":"2024-02-27","poster_path":"/p.jpg"}]}`))
		case "/movie/693134":
			_, _ = w.Write([]byte(`{"id":693134,"imdb_id":"tt15239678","title":"Dune: Part Two","original_title":"Dune: Part Two",
				"release_date":"2024-02-27","runtime":167,"genres":[{"id":878,"name":"Science Fiction"},{"id":12,"name":"Abenteuer"}],"alternative_titles":{"titles":[{"title":"Dune: Teil Zwei"},{"title":"dune: part two"}]}}`))
		case "/configuration":
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchAndDetails(t *testing.T) {
	srv := fake(t)
	c := New("good", "de-DE")
	c.BaseURL = srv.URL

	res, err := c.SearchMovies(context.Background(), "dune", 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != 693134 || res[0].Year != 2024 || res[0].PosterPath != "/p.jpg" {
		t.Fatalf("search = %+v", res)
	}

	m, err := c.Movie(context.Background(), 693134)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Genres, []string{"Science Fiction", "Abenteuer"}) {
		t.Errorf("genres = %v", m.Genres)
	}
	if m.IMDbID != "tt15239678" || m.Runtime != 167 || !slices.Equal(m.AltTitles, []string{"Dune: Teil Zwei"}) {
		t.Errorf("movie = %+v", m)
	}
	if err := c.Test(context.Background()); err != nil {
		t.Errorf("Test: %v", err)
	}
}

func TestBearerTokenAndErrors(t *testing.T) {
	srv := fake(t)
	c := New("eyJgood", "de-DE")
	c.BaseURL = srv.URL
	if err := c.Test(context.Background()); err != nil {
		t.Errorf("bearer token: %v", err)
	}

	c.Key = "bad"
	if err := c.Test(context.Background()); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("bad key: %v", err)
	}
	c.Key = ""
	if err := c.Test(context.Background()); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("no key: %v", err)
	}
	c.Key = "good"
	if _, err := c.Movie(context.Background(), 1); !errors.Is(err, httpx.ErrNotFound) {
		t.Errorf("unknown movie: %v", err)
	}
}

func TestPosterURL(t *testing.T) {
	if got := PosterURL("/p.jpg", "w342"); got != "https://image.tmdb.org/t/p/w342/p.jpg" {
		t.Errorf("got %q", got)
	}
	if PosterURL("", "w342") != "" {
		t.Error("empty path")
	}
}
