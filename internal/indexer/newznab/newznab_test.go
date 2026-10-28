package newznab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
    <tv-search available="yes" supportedParams="q,season,ep,tvdbid"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"><subcat id="2040" name="HD"/><subcat id="2045" name="UHD"/></category>
    <category id="5000" name="TV"/>
  </categories>
</caps>`

const searchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
  <item>
    <title>Dune.Part.Two.2024.German.DL.720p.WEB.h264-GRP</title>
    <guid isPermaLink="true">https://idx/details/abc</guid>
    <link>https://idx/getnzb/abc?apikey=k</link>
    <pubDate>Tue, 05 Mar 2024 10:00:00 +0000</pubDate>
    <enclosure url="https://idx/getnzb/abc?apikey=k" length="1" type="application/x-nzb"/>
    <newznab:attr name="category" value="2000"/>
    <newznab:attr name="category" value="2040"/>
    <newznab:attr name="size" value="3221225472"/>
    <newznab:attr name="grabs" value="42"/>
    <newznab:attr name="password" value="1"/>
  </item>
  <item><title></title><link>https://idx/x</link></item>
</channel>
</rss>`

func fakeIndexer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/getnzb/abc" {
			w.Header().Set("Content-Disposition", `attachment; filename="Dune{{secret}}.nzb"`)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><nzb></nzb>`))
			return
		}
		if q.Get("apikey") != "key" {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`))
			return
		}
		switch q.Get("t") {
		case "caps":
			_, _ = w.Write([]byte(capsXML))
		case "movie", "search", "tvsearch":
			_, _ = w.Write([]byte(searchXML))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func TestCapsAndSearch(t *testing.T) {
	srv, queries := fakeIndexer(t)
	c := New("test", srv.URL+"/", "key")

	caps, err := c.Caps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !caps.MovieSearch || len(caps.MovieParams) != 2 || len(caps.Categories) != 2 || len(caps.Categories[0].Subs) != 2 {
		t.Fatalf("caps = %+v", caps)
	}

	items, err := c.SearchMovie(context.Background(), caps, MovieQuery{IMDbID: "tt15239678", TMDBID: 693134, Title: "Dune", Year: 2024, Categories: []int{2000}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Size != 3221225472 || it.Grabs != 42 || !it.Passworded || it.PubDate.Year() != 2024 || len(it.Categories) != 2 {
		t.Errorf("item = %+v", it)
	}
	last := (*queries)[len(*queries)-1]
	if want := "/api?apikey=key&cat=2000&extended=1&imdbid=15239678&limit=100&t=movie"; last != want {
		t.Errorf("query = %s, want %s", last, want)
	}

	// Without ID support the title search is used.
	if _, err := c.SearchMovie(context.Background(), Caps{}, MovieQuery{Title: "Dune Part Two", Year: 2024}); err != nil {
		t.Fatal(err)
	}
	if last := (*queries)[len(*queries)-1]; last != "/api?apikey=key&extended=1&limit=100&q=Dune+Part+Two+2024&t=search" {
		t.Errorf("text query = %s", last)
	}
}

func TestErrors(t *testing.T) {
	srv, _ := fakeIndexer(t)
	c := New("test", srv.URL+"/api", "wrong")
	_, err := c.Caps(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 100 || !errors.Is(err, httpx.ErrUnauthorized) || !IsConfigError(err) {
		t.Errorf("bad key: %v", err)
	}

	c.URL = "ftp://nope"
	if _, err := c.Caps(context.Background()); err == nil {
		t.Error("invalid URL accepted")
	}
}

func TestFetchNZB(t *testing.T) {
	srv, _ := fakeIndexer(t)
	c := New("test", srv.URL, "key")
	data, name, err := c.FetchNZB(context.Background(), srv.URL+"/getnzb/abc")
	if err != nil {
		t.Fatal(err)
	}
	if name != "Dune{{secret}}.nzb" || len(data) == 0 {
		t.Errorf("name = %q, %d bytes", name, len(data))
	}
}

func TestAPIErrorClassification(t *testing.T) {
	if !errors.Is(&APIError{Code: 500}, httpx.ErrRateLimited) {
		t.Error("request limit not classified as rate limit")
	}
}

func TestSearchEpisode(t *testing.T) {
	srv, queries := fakeIndexer(t)
	c := New("test", srv.URL, "key")
	caps, err := c.Caps(context.Background())
	if err != nil || !caps.TVSearch || len(caps.TVParams) != 4 {
		t.Fatalf("caps = %+v %v", caps, err)
	}
	if _, err := c.SearchEpisode(context.Background(), caps, EpisodeQuery{TVDBID: 121361, Season: 1, Episode: 2, Categories: []int{5000}}); err != nil {
		t.Fatal(err)
	}
	if last := (*queries)[len(*queries)-1]; last != "/api?apikey=key&cat=5000&ep=2&extended=1&limit=100&season=1&t=tvsearch&tvdbid=121361" {
		t.Errorf("id query = %s", last)
	}
	if _, err := c.SearchEpisode(context.Background(), Caps{}, EpisodeQuery{Title: "Dark", Season: 2}); err != nil {
		t.Fatal(err)
	}
	if last := (*queries)[len(*queries)-1]; last != "/api?apikey=key&extended=1&limit=100&q=Dark+S02&t=search" {
		t.Errorf("text query = %s", last)
	}
}
