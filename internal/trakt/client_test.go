package trakt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shyper/autodownloadarr/internal/httpx"
)

func TestClientPaginationAndReadOnlySources(t *testing.T) {
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("trakt-api-key") != "client" || r.Header.Get("trakt-api-version") != "2" {
			t.Error("missing Trakt request authentication")
		}
		if r.URL.Query().Get("limit") != "100" {
			t.Error("missing explicit pagination")
		}
		switch r.URL.Path {
		case "/users/me/lists":
			w.Header().Set("X-Pagination-Page-Count", "2")
			w.Header().Set("X-Pagination-Limit", "1")
			fmt.Fprintf(w, `[{"name":"List","item_count":1,"ids":{"trakt":%s}}]`, r.URL.Query().Get("page"))
		case "/sync/watchlist/movies/added/asc", "/users/me/lists/1/items/movie,show":
			fmt.Fprint(w, `[{"type":"movie","movie":{"title":"Film","ids":{"tmdb":42}}}]`)
		case "/sync/watchlist/shows/added/asc":
			fmt.Fprint(w, `[{"type":"show","show":{"title":"Show","ids":{"tmdb":99}}}]`)
		default:
			t.Errorf("unexpected URL: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := Client{APIURL: srv.URL, ClientID: "client", HTTP: srv.Client()}
	lists, err := c.Lists(t.Context(), "access")
	if err != nil || len(lists) != 2 || lists[1].IDs.Trakt != 2 {
		t.Fatalf("lists: %+v %v", lists, err)
	}
	items, err := c.Watchlist(t.Context(), "access")
	if err != nil || len(items) != 2 || items[0].Movie.IDs.TMDB != 42 || items[1].Show.IDs.TMDB != 99 {
		t.Fatalf("items: %+v %v", items, err)
	}
	if _, err := c.ListItems(t.Context(), "access", 1); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 {
		t.Fatalf("requests: %v", calls)
	}
}
func TestOAuthPKCEAndRefreshUseAuthHost(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/oauth/token" {
			t.Error("wrong token endpoint")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["client_secret"]; ok {
			t.Error("PKCE sent a client secret")
		}
		if calls == 1 && (body["code_verifier"] != "verifier" || body["grant_type"] != "authorization_code") {
			t.Errorf("exchange: %+v", body)
		}
		if calls == 2 && (body["refresh_token"] != "refresh" || body["grant_type"] != "refresh_token") {
			t.Errorf("refresh: %+v", body)
		}
		fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":604800,"created_at":1794100000}`)
	}))
	defer srv.Close()
	c := Client{APIURL: "https://must-not-call.invalid", AuthURL: srv.URL, ClientID: "client", HTTP: srv.Client()}
	token, err := c.Exchange(t.Context(), "https://media.example/callback", "code", "verifier", "")
	if err != nil || token.Access != "new-access" {
		t.Fatalf("exchange: %v", err)
	}
	if _, err := c.Exchange(t.Context(), "https://media.example/callback", "", "", "refresh"); err != nil {
		t.Fatal(err)
	}
}
func TestClientRejectsErrorsWithoutLeakingCredentials(t *testing.T) {
	for _, status := range []int{200, 400, 401, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"private-access-token"}`)
			}))
			defer srv.Close()
			c := Client{AuthURL: srv.URL, HTTP: srv.Client(), ClientID: "client"}
			_, err := c.Exchange(t.Context(), "https://media.example/callback", "code", "verifier", "")
			if err == nil || strings.Contains(err.Error(), "private-access-token") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if status == 429 && !errors.Is(err, httpx.ErrRateLimited) {
				t.Fatalf("rate limit not preserved: %v", err)
			}
			if status == 400 && !errors.Is(err, httpx.ErrUnauthorized) {
				t.Fatalf("invalid grant not recognized: %v", err)
			}
		})
	}
}
