package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/store"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramNotificationUsesBotCredentialsWithoutExposingThem(t *testing.T) {
	paths := make(chan string, 1)
	e := newEnvWith(t, func(svc *movies.Services) {
		svc.HTTP = &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
			paths <- r.URL.Path
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
		})}
	})
	client := e.client(t)
	e.completeSetup(t, client)
	if err := e.store.SaveBotConfig(t.Context(), store.BotConfig{TelegramMode: "polling", TelegramToken: "123:shared-secret"}); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"enabled": true, "provider": "telegram", "chatId": "42", "useBotToken": true}
	res, body := e.do(t, client, "PUT", "/api/v1/notifications", settings)
	if res.StatusCode != 200 || body["hasToken"] != true || body["useBotToken"] != true {
		t.Fatalf("save %d %v", res.StatusCode, body)
	}
	if _, ok := body["token"]; ok {
		t.Fatal("bot token returned to the browser")
	}
	res, body = e.do(t, client, "POST", "/api/v1/notifications/test", settings)
	if res.StatusCode != 200 {
		t.Fatalf("test notification %d %v", res.StatusCode, body)
	}
	select {
	case path := <-paths:
		if path != "/bot123:shared-secret/sendMessage" {
			t.Fatalf("wrong token used: %s", path)
		}
	default:
		t.Fatal("test notification not sent")
	}
}

func TestPushoverSecretsAreWriteOnlyAndTestUsesSavedCredentials(t *testing.T) {
	calls := 0
	e := newEnvWith(t, func(svc *movies.Services) {
		svc.HTTP = &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "https://api.pushover.net/1/messages.json" {
				t.Errorf("unexpected Pushover URL: %s", r.URL)
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("token") != "private-app" || r.Form.Get("user") != "private-user" || r.Form.Get("device") != "phone" {
				t.Error("test did not use stored credentials")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":1}`)), Request: r}, nil
		})}
	})
	client := e.client(t)
	e.completeSetup(t, client)
	settings := map[string]any{"enabled": true, "provider": "pushover", "token": "private-app", "userKey": "private-user", "device": "phone"}
	res, body := e.do(t, client, "PUT", "/api/v1/notifications", settings)
	if res.StatusCode != 200 || body["hasToken"] != true || body["hasUserKey"] != true {
		t.Fatalf("save: %d %v", res.StatusCode, body)
	}
	for _, key := range []string{"token", "userKey"} {
		if _, ok := body[key]; ok {
			t.Fatalf("secret %s returned to browser", key)
		}
	}
	delete(settings, "token")
	delete(settings, "userKey")
	res, body = e.do(t, client, "POST", "/api/v1/notifications/test", settings)
	if res.StatusCode != 200 || calls != 1 {
		t.Fatalf("test: %d %v, calls=%d", res.StatusCode, body, calls)
	}
	// An unrelated provider must never inherit stored Pushover credentials.
	res, _ = e.do(t, client, "POST", "/api/v1/notifications/test", map[string]any{"provider": "telegram", "chatId": "42"})
	if res.StatusCode == 200 || calls != 1 {
		t.Fatal("provider switch reused credentials")
	}
}
