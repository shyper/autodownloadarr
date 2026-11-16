package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[{"Id":"u1","Name":"Anna","Policy":{"IsAdministrator":true}},
			{"Id":"u2","Name":"Ben","Policy":{"IsDisabled":true}}]`))
	}))
	defer srv.Close()
	users, err := NewClient(srv.URL, "k").Users(context.Background())
	if err != nil || len(users) != 2 {
		t.Fatalf("users = %+v, %v", users, err)
	}
	if users[0] != (User{ID: "u1", Name: "Anna", Admin: true}) || users[1] != (User{ID: "u2", Name: "Ben", Disabled: true}) {
		t.Fatalf("users = %+v", users)
	}
}
