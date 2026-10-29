// Package notify forwards notifications to a generic webhook, Discord, ntfy
// or Telegram.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/store"
)

// DefaultTelegramURL is the Telegram bot API.
const DefaultTelegramURL = "https://api.telegram.org"

// Service sends notifications from the event bus to the configured channel.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	HTTP  *http.Client
	// TelegramURL can be overridden in tests.
	TelegramURL string
}

// New returns a notifier.
func New(st *store.Store, log *slog.Logger) *Service {
	return &Service{Store: st, Log: log, HTTP: httpx.NewClient(), TelegramURL: DefaultTelegramURL}
}

// Start forwards notification events until ctx is done.
func (s *Service) Start(ctx context.Context, bus *events.Bus) {
	ch, unsubscribe := bus.Subscribe(64, events.TypeNotification)
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				msg, ok := ev.Data.(events.Notification)
				if !ok {
					continue
				}
				sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err := s.Send(sctx, msg)
				cancel()
				if err != nil && s.Log != nil {
					s.Log.Warn("notification failed", "event", msg.Event, "err", err)
				}
			}
		}
	}()
}

// Send forwards a message to the configured channel. A disabled or
// unconfigured channel does nothing.
func (s *Service) Send(ctx context.Context, msg events.Notification) error {
	n, err := s.Store.Notification(ctx)
	if err != nil || !n.Enabled || n.Provider == "" {
		return err
	}
	return s.SendTo(ctx, n, msg)
}

// SendTo sends a message to an explicit channel; the test button uses it.
func (s *Service) SendTo(ctx context.Context, n store.Notification, msg events.Notification) error {
	if err := n.Validate(); err != nil {
		return err
	}
	text := msg.Title
	if msg.Body != "" {
		text += "\n" + msg.Body
	}
	switch n.Provider {
	case "webhook":
		body, _ := json.Marshal(map[string]any{
			"event": msg.Event, "title": msg.Title, "message": msg.Body, "time": time.Now().UTC(),
		})
		return s.post(ctx, n.URL, "application/json", body)
	case "discord":
		body, _ := json.Marshal(map[string]string{"content": text})
		return s.post(ctx, n.URL, "application/json", body)
	case "ntfy":
		return s.post(ctx, n.URL, "text/plain; charset=utf-8", []byte(text), "Title", msg.Title)
	case "telegram":
		base := s.TelegramURL
		if base == "" {
			base = DefaultTelegramURL
		}
		u := strings.TrimRight(base, "/") + "/bot" + n.Token + "/sendMessage"
		body, _ := json.Marshal(map[string]any{
			"chat_id": n.ChatID, "text": text, "disable_web_page_preview": true,
		})
		return s.post(ctx, u, "application/json", body)
	}
	return fmt.Errorf("notify: unknown provider %q", n.Provider)
}

// post sends one HTTP request. header is an optional key/value pair.
func (s *Service) post(ctx context.Context, url, contentType string, body []byte, header ...string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if len(header) == 2 {
		req.Header.Set(header[0], header[1])
	}
	client := s.HTTP
	if client == nil {
		client = httpx.NewClient()
	}
	if _, _, err := httpx.Do(client, req, 1<<20); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}
