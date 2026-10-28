package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

const testToken = "ABCD-EFGH-JKLM"

type env struct {
	srv   *httptest.Server
	bus   *events.Bus
	store *store.Store
	svc   *movies.Services
}

func newEnv(t *testing.T) *env {
	return newEnvWith(t, func(*movies.Services) {})
}

// newEnvWith lets a test point the external services at fakes.
func newEnvWith(t *testing.T, configure func(*movies.Services)) *env {
	t.Helper()
	sqldb, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	svc, err := auth.NewService(sqldb, auth.WithArgon2Params(auth.Argon2Params{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.New(log)
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)
	services := &movies.Services{Store: st, Log: log}
	configure(services)
	t.Cleanup(services.Close)
	sched := scheduler.New(sqldb, log)
	manager := movies.NewManager(st, services, sched, bus, log)
	seriesManager := series.NewManager(st, services, sched, bus, log, manager.Limiter())
	done := make(chan struct{})
	h := New(Options{
		Store:      st,
		Services:   services,
		Movies:     manager,
		Series:     seriesManager,
		Log:        log,
		DB:         sqldb,
		Auth:       svc,
		Limiter:    auth.NewLimiter(3, time.Minute),
		Bus:        bus,
		SetupToken: testToken,
		Version:    "test",
		Done:       done,
		Static: fstest.MapFS{
			"index.html":       {Data: []byte("<!doctype html><title>AutoDownloadarr</title>")},
			"assets/app-1a.js": {Data: []byte("console.log(1)")},
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return &env{srv: srv, bus: bus, store: st, svc: services}
}

// client returns an HTTP client with its own cookie jar.
func (e *env) client(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

// result is the part of a response the tests look at; the body is
// already read and closed.
type result struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r result) Cookies() []*http.Cookie { return r.cookies }

func (e *env) do(t *testing.T, c *http.Client, method, path string, body any, headers ...string) (result, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return result{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, out
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func (e *env) completeSetup(t *testing.T, c *http.Client) {
	t.Helper()
	resp, body := e.do(t, c, "POST", "/api/v1/setup", map[string]string{
		"token": testToken, "username": "admin", "password": "longenough",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: status %d, body %v", resp.StatusCode, body)
	}
}

func TestSetupAndLoginFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)

	_, status := e.do(t, c, "GET", "/api/v1/system/status", nil)
	if status["setupRequired"] != true || status["version"] != "test" {
		t.Fatalf("status = %v", status)
	}

	resp, body := e.do(t, c, "POST", "/api/v1/setup", map[string]string{
		"token": "WRONG-TOKEN-0000", "username": "admin", "password": "longenough",
	})
	if resp.StatusCode != http.StatusForbidden || errorCode(body) != "invalid_setup_token" {
		t.Errorf("wrong token: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, c, "POST", "/api/v1/setup", map[string]string{
		"token": testToken, "username": "admin", "password": "short",
	})
	if resp.StatusCode != http.StatusBadRequest || errorCode(body) != "weak_password" {
		t.Errorf("weak password: %d %v", resp.StatusCode, body)
	}

	e.completeSetup(t, c) // also logs in
	resp, body = e.do(t, c, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me after setup: %d %v", resp.StatusCode, body)
	}
	if u, _ := body["user"].(map[string]any); u["username"] != "admin" || u["role"] != "admin" {
		t.Errorf("me = %v", body)
	}

	resp, body = e.do(t, c, "POST", "/api/v1/setup", map[string]string{
		"token": testToken, "username": "other", "password": "longenough",
	})
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "setup_completed" {
		t.Errorf("second setup: %d %v", resp.StatusCode, body)
	}

	resp, _ = e.do(t, c, "POST", "/api/v1/auth/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("logout: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, c, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("me after logout: %d", resp.StatusCode)
	}

	resp, body = e.do(t, c, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "nope-nope"})
	if resp.StatusCode != http.StatusUnauthorized || errorCode(body) != "invalid_credentials" {
		t.Errorf("bad login: %d %v", resp.StatusCode, body)
	}
	resp, _ = e.do(t, c, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "longenough"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	cookie := resp.Cookies()[0]
	if cookie.Name != sessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("unexpected cookie %+v", cookie)
	}
	if resp, _ := e.do(t, c, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("me after login: %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	for range 3 {
		e.do(t, c, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong-password"})
	}
	resp, body := e.do(t, c, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "longenough"})
	if resp.StatusCode != http.StatusTooManyRequests || errorCode(body) != "too_many_attempts" {
		t.Errorf("status %d %v, want 429", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("Retry-After missing")
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	resp, body := e.do(t, c, "POST", "/api/v1/auth/password", map[string]string{"currentPassword": "wrong-one", "newPassword": "newpassword"})
	if resp.StatusCode != http.StatusForbidden || errorCode(body) != "invalid_current_password" {
		t.Errorf("wrong current: %d %v", resp.StatusCode, body)
	}
	resp, _ = e.do(t, c, "POST", "/api/v1/auth/password", map[string]string{"currentPassword": "longenough", "newPassword": "newpassword"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, c, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Error("current session ended by password change")
	}
}

func TestProtectedEndpointsRequireLogin(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/v1/auth/me"},
		{"POST", "/api/v1/auth/logout"},
		{"POST", "/api/v1/auth/password"},
		{"GET", "/api/v1/events"},
	} {
		resp, body := e.do(t, c, p.method, p.path, nil)
		if resp.StatusCode != http.StatusUnauthorized || errorCode(body) != "unauthorized" {
			t.Errorf("%s %s: %d %v", p.method, p.path, resp.StatusCode, body)
		}
	}
}

func TestCrossOriginRequestsAreRejected(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	resp, body := e.do(t, c, "POST", "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "longenough"},
		"Sec-Fetch-Site", "cross-site")
	if resp.StatusCode != http.StatusForbidden || errorCode(body) != "cross_origin_rejected" {
		t.Errorf("status %d %v, want 403", resp.StatusCode, body)
	}
}

func TestInvalidBodyAndUnknownRoute(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	resp, body := e.do(t, c, "POST", "/api/v1/auth/login", map[string]string{"user": "x"})
	if resp.StatusCode != http.StatusBadRequest || errorCode(body) != "invalid_request" {
		t.Errorf("unknown field: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, c, "GET", "/api/v1/nope", nil)
	if resp.StatusCode != http.StatusNotFound || errorCode(body) != "not_found" {
		t.Errorf("unknown route: %d %v", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("API response is cacheable")
	}
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	resp, body := e.do(t, e.client(t), "GET", "/api/v1/health", nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Errorf("health: %d %v", resp.StatusCode, body)
	}
}

func TestStaticFiles(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	get := func(p string) (result, string) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+p, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return result{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
	}

	for _, p := range []string{"/", "/movies/42", "/index.html"} {
		resp, body := get(p)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<title>AutoDownloadarr</title>") {
			t.Errorf("%s: %d %q", p, resp.StatusCode, body)
		}
	}
	resp, body := get("/assets/app-1a.js")
	if body != "console.log(1)" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %q, cache %q", body, resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("security headers missing")
	}
}

func TestEventStream(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	req, _ := http.NewRequestWithContext(context.Background(), "GET", e.srv.URL+"/api/v1/events", nil)
	resp, err := c.Do(req) //nolint:bodyclose // closed below; the linter loses track because a goroutine reads the body
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for event stream")
			return ""
		}
	}

	if l := next(); l != ": connected" {
		t.Fatalf("first line %q", l)
	}
	next() // blank line
	e.bus.Publish("test.hello", map[string]int{"n": 1})
	if l := next(); l != "event: test.hello" {
		t.Errorf("got %q", l)
	}
	if l := next(); !strings.HasPrefix(l, `data: {"type":"test.hello"`) || !strings.Contains(l, `"data":{"n":1}`) {
		t.Errorf("got %q", l)
	}
}

func decodeBody(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}
