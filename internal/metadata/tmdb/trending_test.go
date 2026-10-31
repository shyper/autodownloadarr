package tmdb

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trending/all/week" || r.URL.Query().Get("language") != "de" || r.URL.Query().Get("api_key") != "key" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"media_type":"movie","title":"Film","release_date":"2024-01-01","poster_path":"/film.jpg"},
			{"id":2,"media_type":"tv","name":"Serie","first_air_date":"2022-01-01"},
			{"id":3,"media_type":"person","name":"Actor"},
			{"id":4,"media_type":"movie","title":"Adult","adult":true}
		]}`))
	}))
	defer srv.Close()
	c := New("key", "de")
	c.BaseURL = srv.URL
	titles, err := c.Trending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 2 || titles[0].Title != "Film" || titles[0].Year != 2024 || titles[0].PosterURL != PosterURL("/film.jpg", "w342") || titles[1].Title != "Serie" || titles[1].Year != 2022 || titles[1].MediaType != "tv" {
		t.Fatalf("unexpected trending titles: %+v", titles)
	}
}
