package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

func TestPinFlowAndServers(t *testing.T) {
	approved := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Client-Identifier") != "client-1" || r.Header.Get("X-Plex-Product") != Product {
			t.Errorf("missing Plex headers: %v", r.Header)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			_, _ = w.Write([]byte(`{"id":42,"code":"abcd"}`))
		case r.URL.Path == "/api/v2/pins/42":
			if approved {
				_, _ = w.Write([]byte(`{"id":42,"authToken":"user-token"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":42,"authToken":null}`))
			}
		case r.URL.Path == "/api/v2/resources":
			if r.Header.Get("X-Plex-Token") != "user-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`[
				{"name":"Wohnzimmer","provides":"server","clientIdentifier":"srv1","owned":true,"accessToken":"srv-token",
				 "connections":[{"uri":"http://192.168.1.5:32400","local":true},{"uri":"https://relay.plex.direct","relay":true}]},
				{"name":"Phone","provides":"player","clientIdentifier":"p1"}]`))
		}
	}))
	defer srv.Close()

	a := NewAccount("client-1")
	a.TVURL = srv.URL
	pin, err := a.CreatePin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pin.ID != 42 || !strings.Contains(pin.AuthURL, "code=abcd") || !strings.Contains(pin.AuthURL, "clientID=client-1") {
		t.Errorf("pin = %+v", pin)
	}
	if tok, err := a.CheckPin(context.Background(), 42); err != nil || tok != "" {
		t.Errorf("pending pin: %q %v", tok, err)
	}
	approved = true
	tok, err := a.CheckPin(context.Background(), 42)
	if err != nil || tok != "user-token" {
		t.Fatalf("approved pin: %q %v", tok, err)
	}

	servers, err := a.Servers(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].AccessToken != "srv-token" || len(servers[0].Connections) != 2 || !servers[0].Connections[0].Local {
		t.Errorf("servers = %+v", servers)
	}
	if _, err := a.Servers(context.Background(), "wrong"); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}

func TestServerAPI(t *testing.T) {
	var scanned string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "srv-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/identity":
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"srv1","version":"1.41.0"}}`))
		case "/library/sections":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"3","title":"Temp Filme","type":"movie","Location":[{"path":"/mnt/hdd/temp/filme"}]},
				{"key":"4","title":"Musik","type":"artist","Location":[{"path":"/mnt/music"}]}]}}`))
		case "/library/sections/3/refresh":
			scanned = r.URL.Query().Get("path")
		case "/library/sections/5/all":
			if r.URL.Query().Get("type") != "4" {
				t.Error("episodes not requested")
			}
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"9","guid":"plex://episode/5d9c","grandparentTitle":"Dark","parentIndex":1,"index":2,
				"viewOffset":600000,"duration":3000000,"Media":[{"Part":[{"file":"/tv/Dark/Season 01/e.mkv"}]}]}]}}`))
		case "/library/sections/3/all":
			if r.URL.Query().Get("includeGuids") != "1" {
				t.Error("GUIDs not requested")
			}
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"ratingKey":"101","guid":"plex://movie/5d77","title":"Dune: Part Two","year":2024,"viewCount":1,"lastViewedAt":1730000000,"duration":10020000,
				 "Media":[{"Part":[{"file":"/mnt/hdd/temp/filme/Dune (2024)/Dune (2024).mkv"}]}],"Guid":[{"id":"tmdb://693134"}]},
				{"ratingKey":"102","title":"Other","viewOffset":60000}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", "srv-token", "client-1")
	if id, ver, err := c.Identity(context.Background()); err != nil || id != "srv1" || ver != "1.41.0" {
		t.Errorf("identity = %q %q %v", id, ver, err)
	}
	libs, err := c.Libraries(context.Background())
	if err != nil || len(libs) != 2 || libs[0].Locations[0] != "/mnt/hdd/temp/filme" {
		t.Fatalf("libraries = %+v %v", libs, err)
	}
	if err := c.Scan(context.Background(), "3", "/mnt/hdd/temp/filme/Dune (2024)"); err != nil || scanned != "/mnt/hdd/temp/filme/Dune (2024)" {
		t.Errorf("scan path = %q %v", scanned, err)
	}
	items, err := c.Movies(context.Background(), "3")
	if err != nil || len(items) != 2 {
		t.Fatalf("movies = %+v %v", items, err)
	}
	if !items[0].Watched() || items[0].LastViewedAt.Unix() != 1730000000 || items[0].GUIDs[0] != "tmdb://693134" || len(items[0].Files) != 1 {
		t.Errorf("item = %+v", items[0])
	}
	if items[1].Watched() || items[1].ViewOffset.Seconds() != 60 {
		t.Errorf("unwatched item = %+v", items[1])
	}

	eps, err := c.Episodes(context.Background(), "5")
	if err != nil || len(eps) != 1 || eps[0].Season != 1 || eps[0].Episode != 2 || eps[0].ShowTitle != "Dark" || eps[0].Progress() != 0.2 {
		t.Errorf("episodes = %+v %v", eps, err)
	}

	c.Token = "wrong"
	if _, _, err := c.Identity(context.Background()); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}

func TestWatchlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "user-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/library/sections/watchlist/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"ratingKey":"1","title":"Dune","year":2024,"type":"movie","guid":"plex://movie/1","Guid":[{"id":"tmdb://693134"}]},
				{"ratingKey":"2","title":"Dark","year":2017,"type":"show","guid":"plex://show/2"}]}}`))
		case "/library/metadata/2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Guid":[{"id":"tvdb://123"},{"id":"tmdb://70523"}]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := NewAccount("client-1")
	a.HTTP = srv.Client()
	a.DiscoverURL = srv.URL
	a.MetadataURL = srv.URL
	items, err := a.Watchlist(context.Background(), "user-token")
	if err != nil || len(items) != 2 {
		t.Fatalf("watchlist = %+v %v", items, err)
	}
	if items[0].GUIDs[0] != "tmdb://693134" || items[0].Type != "movie" || items[0].Year != 2024 {
		t.Errorf("movie = %+v", items[0])
	}
	if len(items[1].GUIDs) != 2 || items[1].GUIDs[1] != "tmdb://70523" {
		t.Errorf("resolved guids = %+v", items[1])
	}
	if _, err := a.Watchlist(context.Background(), "wrong"); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}
