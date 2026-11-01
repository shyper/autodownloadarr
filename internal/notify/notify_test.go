package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/secrets"
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
		events.Notification{Event: events.NotifyMovieAvailable, Title: "Movie ready: Tom & <Jerry>", Body: "Tom.and.Jerry.2160p"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bottok/sendMessage" {
		t.Errorf("path = %q", path)
	}
	want := "🎬 <b>Movie ready: Tom &amp; &lt;Jerry&gt;</b>\n\n<code>Tom.and.Jerry.2160p</code>"
	if got["chat_id"] != "42" || got["parse_mode"] != "HTML" || got["text"] != want {
		t.Errorf("payload = %+v", got)
	}
}

func TestTelegramText(t *testing.T) {
	for _, tc := range []struct {
		msg  events.Notification
		want string
	}{
		{events.Notification{Event: events.NotifyEpisodeFailed, Title: "Show S01E02 failed", Body: "no_release: <none>"},
			"⚠️ <b>Show S01E02 failed</b>\n\nno_release: &lt;none&gt;"},
		{events.Notification{Event: events.NotifySeriesDeleted, Title: "Series deleted: Show"}, "🗑️ <b>Series deleted: Show</b>"},
		{events.Notification{Event: "test", Title: "AutoDownloadarr", Body: "Test"}, "🔔 <b>AutoDownloadarr</b>\n\nTest"},
	} {
		if got := telegramText(tc.msg); got != tc.want {
			t.Errorf("telegramText(%s) = %q, want %q", tc.msg.Event, got, tc.want)
		}
	}
}

func TestSendSkipsDisabledEvents(t *testing.T) {
	sqldb, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(sqldb, box)

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body["event"].(string))
	}))
	defer srv.Close()
	if err := st.SetNotification(t.Context(), store.Notification{Enabled: true, Provider: "webhook", URL: srv.URL,
		DisabledEvents: []string{events.NotifyMovieDeleted}}); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Store: st, HTTP: srv.Client()}
	for _, e := range []string{events.NotifyMovieDeleted, events.NotifyMovieAvailable} {
		if err := svc.Send(t.Context(), events.Notification{Event: e, Title: "Dune"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 1 || got[0] != events.NotifyMovieAvailable {
		t.Errorf("sent = %v", got)
	}
}

func TestSendRejectsInvalidChannel(t *testing.T) {
	svc := &Service{}
	if err := svc.SendTo(t.Context(), store.Notification{Provider: "webhook"}, events.Notification{}); err == nil {
		t.Error("a webhook without URL was accepted")
	}
}
