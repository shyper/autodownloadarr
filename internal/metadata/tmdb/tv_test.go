package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/tv":
			_, _ = w.Write([]byte(`{"results":[{"id":1399,"name":"Game of Thrones","first_air_date":"2011-04-17"}]}`))
		case "/tv/1399":
			_, _ = w.Write([]byte(`{"id":1399,"name":"Game of Thrones","original_name":"Game of Thrones","first_air_date":"2011-04-17",
				"genres":[{"id":18,"name":"Drama"}],"status":"Ended","episode_run_time":[60],"seasons":[{"season_number":0,"name":"Specials","episode_count":3},
				{"season_number":1,"name":"Staffel 1","episode_count":10,"air_date":"2011-04-17"}],
				"external_ids":{"tvdb_id":121361,"imdb_id":"tt0944947"},"alternative_titles":{"results":[{"title":"GoT"}]}}`))
		case "/tv/1399/season/1":
			_, _ = w.Write([]byte(`{"episodes":[{"season_number":1,"episode_number":1,"name":"Der Winter naht","air_date":"2011-04-17","runtime":62}]}`))
		}
	}))
	defer srv.Close()
	c := New("k", "de-DE")
	c.BaseURL = srv.URL

	found, err := c.SearchShows(context.Background(), "thrones")
	if err != nil || len(found) != 1 || found[0].Year != 2011 {
		t.Fatalf("search = %+v %v", found, err)
	}
	s, err := c.Show(context.Background(), 1399)
	if err != nil || s.TVDBID != 121361 || s.IMDbID != "tt0944947" || len(s.Seasons) != 2 || s.Runtime != 60 || s.AltTitles[0] != "GoT" {
		t.Fatalf("show = %+v %v", s, err)
	}
	if len(s.Genres) != 1 || s.Genres[0] != "Drama" {
		t.Fatalf("genres = %v", s.Genres)
	}
	eps, err := c.Season(context.Background(), 1399, 1)
	if err != nil || len(eps) != 1 || eps[0].Title != "Der Winter naht" || eps[0].AirDate != "2011-04-17" {
		t.Errorf("season = %+v %v", eps, err)
	}
}
