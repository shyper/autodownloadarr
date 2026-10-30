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
