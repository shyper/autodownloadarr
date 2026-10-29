package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

func TestServerAPI(t *testing.T) {
	var scanned string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"Id":"srv1","Version":"10.9.0"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[
				{"Name":"Filme","ItemId":"lib1","CollectionType":"movies","Locations":["/data/filme"]},
				{"Name":"Serien","ItemId":"lib2","CollectionType":"tvshows","Locations":["/data/serien"]}]`))
		case "/Items/lib1/Refresh":
			scanned = "full"
		case "/Users":
			_, _ = w.Write([]byte(`[{"Id":"u2","Policy":{"IsAdministrator":false}},{"Id":"u1","Policy":{"IsAdministrator":true}}]`))
		case "/Library/Media/Updated":
			var body struct {
				Updates []struct {
					Path       string `json:"Path"`
					UpdateType string `json:"UpdateType"`
				} `json:"Updates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode scan: %v", err)
			}
			if len(body.Updates) == 1 {
				scanned = body.Updates[0].Path
			}
		case "/Items":
			if r.URL.Query().Get("UserId") != "u1" {
				t.Errorf("UserId = %q", r.URL.Query().Get("UserId"))
			}
			switch r.URL.Query().Get("IncludeItemTypes") {
			case "Movie":
				_, _ = w.Write([]byte(`{"Items":[
					{"Id":"m1","Name":"Dune: Part Two","ProductionYear":2024,"RunTimeTicks":100200000000,
					 "ProviderIds":{"Tmdb":"693134","Imdb":"tt15239678"},"MediaSources":[{"Path":"/data/filme/Dune (2024)/Dune.mkv"}],
					 "UserData":{"PlayCount":1,"Played":true,"LastPlayedDate":"2024-11-01T18:00:00.0000000Z","PlaybackPositionTicks":0}},
					{"Id":"m2","Name":"Other","RunTimeTicks":6000000000,"UserData":{"PlaybackPositionTicks":600000000}}]}`))
			case "Episode":
				_, _ = w.Write([]byte(`{"Items":[
					{"Id":"e1","Name":"Episode 2","SeriesName":"Dark","ParentIndexNumber":1,"IndexNumber":2,"RunTimeTicks":3000000000,
					 "MediaSources":[{"Path":"/data/serien/Dark/Season 01/e.mkv"}],
					 "UserData":{"Played":false,"PlaybackPositionTicks":600000000}}]}`))
			default:
				t.Errorf("unexpected item type %q", r.URL.Query().Get("IncludeItemTypes"))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", "api-key")
	if id, ver, err := c.Identity(context.Background()); err != nil || id != "srv1" || ver != "10.9.0" {
		t.Errorf("identity = %q %q %v", id, ver, err)
	}
	libs, err := c.Libraries(context.Background())
	if err != nil || len(libs) != 2 || libs[0].Key != "lib1" || libs[0].Type != "movie" || libs[1].Type != "show" ||
		libs[0].Locations[0] != "/data/filme" {
		t.Fatalf("libraries = %+v %v", libs, err)
	}
	if err := c.Scan(context.Background(), "lib1", "/data/filme/Dune (2024)"); err != nil || scanned != "/data/filme/Dune (2024)" {
		t.Errorf("scan path = %q %v", scanned, err)
	}
	if err := c.Scan(context.Background(), "lib1", ""); err != nil || scanned != "full" {
		t.Errorf("full scan = %q %v", scanned, err)
	}

	movies, err := c.Movies(context.Background(), "lib1")
	if err != nil || len(movies) != 2 {
		t.Fatalf("movies = %+v %v", movies, err)
	}
	if !movies[0].Watched() || movies[0].Key != "m1" || movies[0].Year != 2024 || movies[0].GUIDs[0] != "tmdb://693134" ||
		movies[0].LastViewedAt.IsZero() || len(movies[0].Files) != 1 || movies[0].Duration.Seconds() != 10020 {
		t.Errorf("movie = %+v", movies[0])
	}
	if movies[1].Watched() || movies[1].ViewOffset.Seconds() != 60 {
		t.Errorf("unwatched movie = %+v", movies[1])
	}

	eps, err := c.Episodes(context.Background(), "lib2")
	if err != nil || len(eps) != 1 || eps[0].Season != 1 || eps[0].Episode != 2 || eps[0].ShowTitle != "Dark" ||
		eps[0].Progress() != 0.2 {
		t.Errorf("episodes = %+v %v", eps, err)
	}

	c.Token = "wrong"
	if _, _, err := c.Identity(context.Background()); !errors.Is(err, httpx.ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}
