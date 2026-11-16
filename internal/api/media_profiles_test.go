package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestMediaProfileEndpoints(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Plex-Token")
		switch r.URL.Path {
		case "/api/v2/home/users":
			_, _ = w.Write([]byte(`{"users":[{"id":1,"uuid":"own","title":"Owner","admin":true},
				{"id":2,"uuid":"kid","title":"Kid","protected":true}]}`))
		case "/api/v2/home/users/kid/switch":
			if token != "account" || r.URL.Query().Get("pin") != "1234" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"authToken":"kid-account"}`))
		case "/api/v2/resources":
			_, _ = w.Write([]byte(`[{"name":"Home","provides":"server","clientIdentifier":"machine","accessToken":"kid-server"}]`))
		case "/pms/identity":
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine","version":"1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	e := newEnvWith(t, func(s *movies.Services) { s.PlexTVURL = fake.URL })
	c := e.client(t)
	e.completeSetup(t, c)
	ctx := context.Background()

	if resp, body := e.do(t, c, "POST", "/api/v1/mediaserver/profiles/refresh", nil); resp.StatusCode != http.StatusConflict || errorCode(body) != "not_configured" {
		t.Fatalf("refresh without server: %d %v", resp.StatusCode, body)
	}
	if _, err := e.store.SaveMediaServer(ctx, store.MediaServer{Kind: "plex", Name: "Plex", URL: fake.URL + "/pms", Token: "server",
		AccountToken: "account", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if resp, body := e.do(t, c, "POST", "/api/v1/mediaserver/profiles/refresh", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: %d %v", resp.StatusCode, body)
	}
	profiles, err := e.store.MediaProfiles(ctx)
	if err != nil || len(profiles) != 2 || profiles[1].Name != "Kid" {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	owner, kid := profiles[0], profiles[1]
	resp, body := e.do(t, c, "POST", "/api/v1/users", map[string]any{"username": "bob", "password": "longenough", "role": "user"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %d %v", resp.StatusCode, body)
	}
	bob := int64(body["id"].(float64))

	path := "/api/v1/mediaserver/profiles/"
	if resp, body := e.do(t, c, "PUT", path+strconv.FormatInt(kid.ID, 10), map[string]any{"userId": bob, "watchlist": true}); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(body) != "pin_required" {
		t.Fatalf("assign without PIN: %d %v", resp.StatusCode, body)
	}
	if resp, body := e.do(t, c, "PUT", path+strconv.FormatInt(kid.ID, 10), map[string]any{"userId": bob, "watchlist": true, "pin": "0000"}); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(body) != "credentials_rejected" {
		t.Fatalf("assign with wrong PIN: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, c, "PUT", path+strconv.FormatInt(kid.ID, 10), map[string]any{"userId": bob, "watchlist": true, "pin": "1234"})
	if resp.StatusCode != http.StatusOK || body["userName"] != "bob" || body["watchlist"] != true || body["hasToken"] != true {
		t.Fatalf("assign: %d %v", resp.StatusCode, body)
	}
	if _, leaked := body["serverToken"]; leaked {
		t.Fatal("token in response")
	}
	got, err := e.store.MediaProfile(ctx, kid.ID)
	if err != nil || got.ServerToken != "kid-server" || got.AccountToken != "kid-account" {
		t.Fatalf("stored tokens: %+v, %v", got, err)
	}
	if resp, body := e.do(t, c, "PUT", path+strconv.FormatInt(owner.ID, 10), map[string]any{"userId": bob}); resp.StatusCode != http.StatusConflict || errorCode(body) != "profile_taken" {
		t.Fatalf("second profile: %d %v", resp.StatusCode, body)
	}
	if resp, body := e.do(t, c, "PUT", path+strconv.FormatInt(owner.ID, 10), map[string]any{"userId": 999}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown user: %d %v", resp.StatusCode, body)
	}
	if resp, body := e.do(t, c, "GET", "/api/v1/mediaserver/profile", nil); resp.StatusCode != http.StatusOK || body["profile"] != nil {
		t.Fatalf("admin's own profile: %d %v", resp.StatusCode, body)
	}

	// bob sees his profile but cannot change assignments.
	bc := e.client(t)
	if resp, body := e.do(t, bc, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "longenough"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, bc, "GET", "/api/v1/mediaserver/profile", nil)
	if p, _ := body["profile"].(map[string]any); resp.StatusCode != http.StatusOK || p["name"] != "Kid" {
		t.Fatalf("bob's profile: %d %v", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, bc, "PUT", path+strconv.FormatInt(kid.ID, 10), map[string]any{"userId": 0}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin change: %d", resp.StatusCode)
	}
}
