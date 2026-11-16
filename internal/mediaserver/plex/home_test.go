package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHomeUsersAndSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/home/users":
			if r.Header.Get("X-Plex-Token") != "owner" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"users":[
				{"id":1,"uuid":"aaa","title":"Owner","admin":true},
				{"id":2,"uuid":"bbb","title":"Kid","protected":true,"restricted":true,"thumb":"https://plex.tv/users/bbb/avatar"}]}`))
		case "/api/v2/home/users/bbb/switch":
			if r.Method != http.MethodPost || r.Header.Get("X-Plex-Token") != "owner" || r.URL.Query().Get("pin") != "1234" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":2,"uuid":"bbb","authToken":"kid-account"}`))
		case "/api/v2/resources":
			if r.Header.Get("X-Plex-Token") != "kid-account" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`[{"name":"Other","provides":"server","clientIdentifier":"other","accessToken":"x"},
				{"name":"Home","provides":"server","clientIdentifier":"machine","accessToken":"kid-server"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	a := NewAccount("client")
	a.TVURL = srv.URL

	users, err := a.HomeUsers(ctx, "owner")
	if err != nil || len(users) != 2 {
		t.Fatalf("home users = %+v, %v", users, err)
	}
	if !users[0].Admin || users[1].ExternalID() != "bbb" || !users[1].Protected || users[1].Title != "Kid" {
		t.Fatalf("home users = %+v", users)
	}
	if (HomeUser{ID: 7}).ExternalID() != "7" {
		t.Fatal("external id fallback")
	}
	if _, err := a.SwitchHomeUser(ctx, "owner", "bbb", "0000"); err == nil {
		t.Fatal("switch with a wrong PIN succeeded")
	}
	token, err := a.SwitchHomeUser(ctx, "owner", "bbb", "1234")
	if err != nil || token != "kid-account" {
		t.Fatalf("switch = %q, %v", token, err)
	}
	server, err := a.ServerToken(ctx, token, "machine")
	if err != nil || server != "kid-server" {
		t.Fatalf("server token = %q, %v", server, err)
	}
	if _, err := a.ServerToken(ctx, token, "missing"); err == nil {
		t.Fatal("token for an unknown server")
	}
}
