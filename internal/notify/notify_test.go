package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/store"
)

func TestSendWebhook(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	svc := &Service{HTTP: srv.Client()}
	err := svc.SendTo(t.Context(), store.Notification{Provider: "webhook", URL: srv.URL},
		events.Notification{Event: "movie.available", Title: "Dune", Body: "Dune.2024"})
	if err != nil {
		t.Fatal(err)
	}
	if got["event"] != "movie.available" || got["title"] != "Dune" || got["message"] != "Dune.2024" {
		t.Errorf("payload = %+v", got)
	}
}

func TestSendDiscord(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	svc := &Service{HTTP: srv.Client()}
	if err := svc.SendTo(t.Context(), store.Notification{Provider: "discord", URL: srv.URL},
		events.Notification{Title: "Title", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "Title\nBody" {
		t.Errorf("content = %q", got["content"])
	}
}

func TestSendNtfy(t *testing.T) {
	var body, title string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, title = string(b), r.Header.Get("Title")
	}))
	defer srv.Close()

	svc := &Service{HTTP: srv.Client()}
	if err := svc.SendTo(t.Context(), store.Notification{Provider: "ntfy", URL: srv.URL},
		events.Notification{Title: "Title", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if body != "Title\nBody" || title != "Title" {
		t.Errorf("body = %q, title = %q", body, title)
	}
}

func TestSendTelegram(t *testing.T) {
	var path string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	svc := &Service{HTTP: srv.Client(), TelegramURL: srv.URL}
	if err := svc.SendTo(t.Context(), store.Notification{Provider: "telegram", Token: "tok", ChatID: "42"},
		events.Notification{Title: "Title", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bottok/sendMessage" {
		t.Errorf("path = %q", path)
	}
	if got["chat_id"] != "42" || got["text"] != "Title\nBody" {
		t.Errorf("payload = %+v", got)
	}
}

func TestSendRejectsInvalidChannel(t *testing.T) {
	svc := &Service{}
	if err := svc.SendTo(t.Context(), store.Notification{Provider: "webhook"}, events.Notification{}); err == nil {
		t.Error("a webhook without URL was accepted")
	}
}
