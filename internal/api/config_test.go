package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func TestGeneralSettings(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	_, body := e.do(t, c, "GET", "/api/v1/settings/general", nil)
	if body["deleteAfterHours"] != float64(48) {
		t.Errorf("defaults = %v", body)
	}
	g := store.DefaultGeneral()
	g.MoviesPath = "relative/path"
	resp, body := e.do(t, c, "PUT", "/api/v1/settings/general", g)
	if resp.StatusCode != http.StatusBadRequest || errorCode(body) != "path_not_writable" {
		t.Errorf("relative path: %d %v", resp.StatusCode, body)
	}
	g.MoviesPath = t.TempDir() + "/movies"
	if resp, body := e.do(t, c, "PUT", "/api/v1/settings/general", g); resp.StatusCode != http.StatusOK {
		t.Errorf("valid path: %d %v", resp.StatusCode, body)
	}
}

func TestUsenetServers(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)
	news := nntptest.New(t)
	news.RequireAuth("user", "secret")
	host, port, _ := net.SplitHostPort(news.Addr())
	portNum, _ := strconv.Atoi(port)

	srv := map[string]any{"name": "Main", "host": host, "port": portNum, "tls": false, "username": "user", "password": "secret", "enabled": true}
	if resp, body := e.do(t, c, "POST", "/api/v1/usenet-servers/test", srv); resp.StatusCode != http.StatusOK {
		t.Fatalf("test: %d %v", resp.StatusCode, body)
	}
	srv["password"] = "wrong"
	if resp, body := e.do(t, c, "POST", "/api/v1/usenet-servers/test", srv); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(body) != "credentials_rejected" {
		t.Errorf("wrong password: %d %v", resp.StatusCode, body)
	}
	srv["password"] = "secret"
	_, body := e.do(t, c, "POST", "/api/v1/usenet-servers", srv)
	id := int64(body["id"].(float64))

	// The list never contains the password; saving without one keeps it.
	srv["password"] = ""
	srv["connections"] = 30
	e.do(t, c, "PUT", fmt.Sprintf("/api/v1/usenet-servers/%d", id), srv)
	resp, err := c.Get(e.srv.URL + "/api/v1/usenet-servers")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	list, _ := e.store.UsenetServers(t.Context())
	if len(list) != 1 || list[0].Password != "secret" || list[0].Connections != 30 {
		t.Errorf("stored = %+v", list)
	}
	srv["id"] = id
	if resp, body := e.do(t, c, "POST", "/api/v1/usenet-servers/test", srv); resp.StatusCode != http.StatusOK {
		t.Errorf("test with stored password: %d %v", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, c, "DELETE", fmt.Sprintf("/api/v1/usenet-servers/%d", id), nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", resp.StatusCode)
	}
}

func TestUsenetListHidesPassword(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)
	_, _ = e.store.SaveUsenetServer(t.Context(), store.UsenetServer{Host: "news.example.com", Password: "secret", Enabled: true})
	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.srv.URL+"/api/v1/usenet-servers", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	_ = decodeBody(resp, &list)
	if len(list) != 1 || list[0]["password"] != nil || list[0]["hasPassword"] != true {
		t.Errorf("list = %v", list)
	}
}

func TestIndexerTest(t *testing.T) {
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "k" {
			_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
			return
		}
		_, _ = w.Write([]byte(`<caps><searching><movie-search available="yes" supportedParams="q,imdbid"/></searching>
			<categories><category id="2000" name="Movies"><subcat id="2040" name="HD"/></category></categories></caps>`))
	}))
	defer idx.Close()
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	resp, body := e.do(t, c, "POST", "/api/v1/indexers/test", map[string]any{"url": idx.URL, "apiKey": "k"})
	if resp.StatusCode != http.StatusOK || body["movieSearch"] != true {
		t.Errorf("test: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, c, "POST", "/api/v1/indexers/test", map[string]any{"url": idx.URL, "apiKey": "bad"})
	if resp.StatusCode != http.StatusUnprocessableEntity || errorCode(body) != "credentials_rejected" {
		t.Errorf("bad key: %d %v", resp.StatusCode, body)
	}
}

func TestPlexSignIn(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("POST /tv/api/v2/pins", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":7,"code":"c0de"}`))
	})
	mux.HandleFunc("/tv/api/v2/pins/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":7,"authToken":"account-token"}`))
	})
	mux.HandleFunc("/tv/api/v2/resources", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"name":"Home","provides":"server","clientIdentifier":"srv","owned":true,"accessToken":"server-token",
			"connections":[{"uri":"%s/pms","local":true}]}]`, base)
	})
	mux.HandleFunc("/pms/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "server-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"srv","version":"1"}}`))
	})
	mux.HandleFunc("/pms/library/sections", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"2","title":"Temp Filme","type":"movie","Location":[{"path":"/mnt/hdd/filme"}]}]}}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()
	base = fake.URL

	e := newEnvWith(t, func(s *movies.Services) { s.PlexTVURL = fake.URL + "/tv" })
	c := e.client(t)
	e.completeSetup(t, c)

	_, pin := e.do(t, c, "POST", "/api/v1/plex/pin", nil)
	if pin["id"] != float64(7) || pin["authUrl"] == "" {
		t.Fatalf("pin = %v", pin)
	}
	_, status := e.do(t, c, "GET", "/api/v1/plex/pin/7", nil)
	if status["authorized"] != true {
		t.Fatalf("status = %v", status)
	}
	resp, body := e.do(t, c, "POST", "/api/v1/plex/connect", map[string]any{"pinId": 7, "serverId": "srv", "uri": fake.URL + "/pms"})
	if resp.StatusCode != http.StatusOK || body["configured"] != true {
		t.Fatalf("connect: %d %v", resp.StatusCode, body)
	}
	ms, _, _ := e.store.MediaServer(t.Context())
	if ms.Token != "server-token" || ms.Name != "Home" {
		t.Errorf("stored = %+v", ms)
	}

	g := store.DefaultGeneral()
	g.MoviesPath = "/filme"
	_ = e.store.SetGeneral(t.Context(), g)
	_, libs := e.do(t, c, "GET", "/api/v1/plex/libraries", nil)
	sugg, _ := libs["suggestions"].([]any)
	mv, _ := libs["movies"].(map[string]any)
	if len(sugg) != 1 || mv["inLibrary"] != false {
		t.Errorf("libraries = %v", libs)
	}
}

func TestMovieRequestFlow(t *testing.T) {
	tmdbFake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			_, _ = w.Write([]byte(`{"results":[{"id":5,"title":"Film","release_date":"2024-01-01","poster_path":"/p.jpg"}]}`))
		case "/movie/5":
			_, _ = w.Write([]byte(`{"id":5,"imdb_id":"tt5","title":"Film","release_date":"2024-01-01","runtime":90}`))
		}
	}))
	defer tmdbFake.Close()
	e := newEnvWith(t, func(s *movies.Services) { s.TMDBBaseURL = tmdbFake.URL })
	c := e.client(t)
	e.completeSetup(t, c)

	if resp, body := e.do(t, c, "GET", "/api/v1/movies/search?q=film", nil); resp.StatusCode != http.StatusConflict || errorCode(body) != "not_configured" {
		t.Errorf("without TMDB key: %d %v", resp.StatusCode, body)
	}
	_ = e.store.SetTMDBKey(t.Context(), "key")

	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.srv.URL+"/api/v1/movies/search?q=film", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var results []map[string]any
	_ = decodeBody(resp, &results)
	if len(results) != 1 || results[0]["posterUrl"] != "https://image.tmdb.org/t/p/w342/p.jpg" {
		t.Fatalf("results = %v", results)
	}

	resp, body := e.do(t, c, "POST", "/api/v1/movies", map[string]any{"tmdbId": 5})
	if resp.StatusCode != http.StatusCreated || body["state"] != store.MovieSearching || body["imdbId"] != "tt5" {
		t.Fatalf("request: %d %v", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, c, "POST", "/api/v1/movies", map[string]any{"tmdbId": 5}); resp.StatusCode != http.StatusOK {
		t.Errorf("second request: %d", resp.StatusCode)
	}
	id := int64(body["id"].(float64))
	_, detail := e.do(t, c, "GET", fmt.Sprintf("/api/v1/movies/%d", id), nil)
	if detail["releases"] == nil || detail["attempts"] == nil {
		t.Errorf("detail = %v", detail)
	}
	if resp, _ := e.do(t, c, "DELETE", fmt.Sprintf("/api/v1/movies/%d?forget=1", id), nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", resp.StatusCode)
	}
}
