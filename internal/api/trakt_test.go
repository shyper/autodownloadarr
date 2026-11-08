package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shyper/autodownloadarr/internal/movies"
)

func TestTraktAccountLinkingIsolationSettingsAndDisconnect(t *testing.T) {
	var exchanges, revocations atomic.Int32
	var challenge atomic.Value
	challenge.Store("")
	e := newEnvWith(t, func(svc *movies.Services) {
		svc.HTTP = &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
			body := ""
			switch r.URL.Host + r.URL.Path {
			case "auth.trakt.tv/oauth/token":
				exchanges.Add(1)
				var fields map[string]string
				if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
					t.Error(err)
				}
				h := sha256.Sum256([]byte(fields["code_verifier"]))
				if fields["code"] != "authorization-code" || base64.RawURLEncoding.EncodeToString(h[:]) != challenge.Load().(string) {
					t.Error("PKCE not enforced")
				}
				body = `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":604800}`
			case "api.trakt.tv/users/settings":
				body = `{"user":{"username":"trakt-user","ids":{"uuid":"account-id","slug":"trakt-user"}}}`
			case "api.trakt.tv/users/me/lists":
				body = `[{"name":"My list","ids":{"trakt":7},"item_count":1}]`
			case "auth.trakt.tv/oauth/revoke":
				revocations.Add(1)
				body = `{}`
			default:
				t.Errorf("unexpected request: %s", r.URL)
				return nil, fmt.Errorf("unexpected URL")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
	})
	admin := e.client(t)
	e.completeSetup(t, admin)
	admin.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if res, _ := e.do(t, admin, "PUT", "/api/v1/trakt/config", map[string]string{"clientId": "client"}); res.StatusCode != 200 {
		t.Fatal("app configuration failed")
	}
	res, status := e.do(t, admin, "GET", "/api/v1/trakt", nil)
	if res.StatusCode != 200 || status["connected"] != false || status["pollMinutes"] != float64(30) {
		t.Fatalf("initial status: %v", status)
	}
	if res, _ := e.do(t, admin, "POST", "/api/v1/users", map[string]string{"username": "bob", "password": "longenough", "role": "user"}); res.StatusCode != 201 {
		t.Fatal("create user failed")
	}
	bob := e.client(t)
	if res, _ := e.do(t, bob, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "longenough"}); res.StatusCode != 200 {
		t.Fatal("login failed")
	}
	bob.CheckRedirect = admin.CheckRedirect
	if res, _ := e.do(t, bob, "PUT", "/api/v1/trakt/config", map[string]string{"clientId": "attacker"}); res.StatusCode != 403 {
		t.Fatal("ordinary user changed app configuration")
	}
	res, begin := e.do(t, admin, "POST", "/api/v1/trakt/connect", nil)
	target, err := url.Parse(fmt.Sprint(begin["url"]))
	if err != nil || res.StatusCode != 200 || target.Host != "auth.trakt.tv" || target.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("begin: %+v %v", begin, err)
	}
	challenge.Store(target.Query().Get("code_challenge"))
	callback := "/api/v1/trakt/callback?" + url.Values{"state": {target.Query().Get("state")}, "code": {"authorization-code"}}.Encode()
	res, _ = e.do(t, bob, "GET", callback, nil)
	if !strings.Contains(res.Header.Get("Location"), "trakt=failed") || exchanges.Load() != 0 {
		t.Fatal("another user completed the connection")
	}
	// A second login of the same user must not be able to complete the first session's flow.
	otherSession := e.client(t)
	otherSession.CheckRedirect = admin.CheckRedirect
	if res, _ := e.do(t, otherSession, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "longenough"}); res.StatusCode != 200 {
		t.Fatal("second login failed")
	}
	res, _ = e.do(t, otherSession, "GET", callback, nil)
	if !strings.Contains(res.Header.Get("Location"), "trakt=failed") || exchanges.Load() != 0 {
		t.Fatal("another session completed the connection")
	}
	res, _ = e.do(t, admin, "GET", callback, nil)
	if res.StatusCode != 303 || !strings.Contains(res.Header.Get("Location"), "trakt=connected") || exchanges.Load() != 1 {
		t.Fatalf("callback: %d %v", res.StatusCode, res.Header)
	}
	res, status = e.do(t, admin, "GET", "/api/v1/trakt", nil)
	raw, _ := json.Marshal(status)
	if status["connected"] != true || status["username"] != "trakt-user" || strings.Contains(string(raw), "private-") {
		t.Fatalf("unsafe status: %s", raw)
	}
	res, _ = e.do(t, admin, "GET", callback, nil)
	if !strings.Contains(res.Header.Get("Location"), "trakt=failed") || exchanges.Load() != 1 {
		t.Fatal("OAuth code replay accepted")
	}
	_, status = e.do(t, bob, "GET", "/api/v1/trakt", nil)
	if status["connected"] != false || status["username"] != "" {
		t.Fatal("connection leaked into another account")
	}
	settings := map[string]any{"enabled": true, "watchlist": true, "listIds": []int64{7}, "pollMinutes": 17}
	res, status = e.do(t, admin, "PUT", "/api/v1/trakt", settings)
	if res.StatusCode != 200 || status["pollMinutes"] != float64(17) || status["syncing"] != true {
		t.Fatalf("save: %d %v", res.StatusCode, status)
	}
	settings["listIds"] = []int64{999}
	if res, _ := e.do(t, admin, "PUT", "/api/v1/trakt", settings); res.StatusCode != 400 {
		t.Fatal("foreign list ID accepted")
	}
	settings["pollMinutes"] = 0
	if res, _ := e.do(t, admin, "PUT", "/api/v1/trakt", settings); res.StatusCode != 400 {
		t.Fatal("invalid interval accepted")
	}
	if res, _ := e.do(t, bob, "DELETE", "/api/v1/trakt", nil); res.StatusCode != 204 {
		t.Fatal("unlinked user could not disconnect safely")
	}
	_, status = e.do(t, admin, "GET", "/api/v1/trakt", nil)
	if status["connected"] != true {
		t.Fatal("one user disconnected another account")
	}
	if res, _ := e.do(t, admin, "DELETE", "/api/v1/trakt", nil); res.StatusCode != 204 || revocations.Load() != 1 {
		t.Fatal("disconnect did not revoke credentials")
	}
	_, status = e.do(t, admin, "GET", "/api/v1/trakt", nil)
	if status["linked"] != false || status["syncing"] != false {
		t.Fatal("disconnect retained the connection or queued tasks")
	}
}
