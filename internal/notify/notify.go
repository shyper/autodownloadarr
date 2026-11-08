// Package notify forwards notifications to a generic webhook, Discord, ntfy,
// Telegram or Pushover.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	// PushoverURL can be overridden in tests.
	PushoverURL string
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
// unconfigured channel, or a kind the user switched off, does nothing.
func (s *Service) Send(ctx context.Context, msg events.Notification) error {
	n, err := s.Store.Notification(ctx)
	if err != nil || !n.Enabled || n.Provider == "" || !n.Wants(msg.Event) {
		return err
	}
	return s.SendTo(ctx, n, msg)
}

// SendTo sends a message to an explicit channel; the test button uses it.
func (s *Service) SendTo(ctx context.Context, n store.Notification, msg events.Notification) error {
	if err := n.Validate(); err != nil {
		return err
	}
	text := messageIcon(msg) + " " + msg.Title
	if body := messageBody(msg); body != "" {
		text += "\n\n" + body
	}
	switch n.Provider {
	case "webhook":
		payload := map[string]any{"event": msg.Event, "title": msg.Title, "message": msg.Body, "time": time.Now().UTC()}
		if msg.ImageURL != "" {
			payload["imageUrl"] = msg.ImageURL
		}
		if msg.Subtitle != "" {
			payload["subtitle"] = msg.Subtitle
		}
		body, _ := json.Marshal(payload)
		return s.post(ctx, n.URL, "application/json", body)
	case "discord":
		embed := map[string]any{"title": clip(discordEscape(messageIcon(msg)+" "+msg.Title), 256), "description": clip(discordEscape(messageBody(msg)), 4096), "color": 0x0891b2}
		if msg.ImageURL != "" {
			embed["image"] = map[string]string{"url": msg.ImageURL}
		}
		body, _ := json.Marshal(map[string]any{"embeds": []any{embed}, "allowed_mentions": map[string]any{"parse": []string{}}})
		return s.post(ctx, n.URL, "application/json", body)
	case "ntfy":
		headers := []string{"Title", msg.Title}
		if msg.ImageURL != "" {
			headers = append(headers, "Attach", msg.ImageURL)
		}
		return s.post(ctx, n.URL, "text/plain; charset=utf-8", []byte(clipBytes(text, 4096)), headers...)
	case "pushover":
		return s.sendPushover(ctx, n, msg)
	case "telegram":
		base := s.TelegramURL
		if base == "" {
			base = DefaultTelegramURL
		}
		u := strings.TrimRight(base, "/") + "/bot" + n.Token
		if n.Token == "" {
			return fmt.Errorf("%w: bot token is required", store.ErrInvalid)
		}
		if msg.ImageURL != "" {
			body, _ := json.Marshal(map[string]any{"chat_id": n.ChatID, "photo": msg.ImageURL, "caption": telegramCaption(msg, 1024), "parse_mode": "HTML"})
			err := s.post(ctx, u+"/sendPhoto", "application/json", body)
			if err == nil {
				return nil
			}
			var status *httpx.StatusError
			if !errors.As(err, &status) || status.Code != http.StatusBadRequest {
				return err
			}
		}
		body, _ := json.Marshal(map[string]any{
			"chat_id": n.ChatID, "text": telegramCaption(msg, 4096), "parse_mode": "HTML",
			"link_preview_options": map[string]bool{"is_disabled": true},
		})
		return s.post(ctx, u+"/sendMessage", "application/json", body)
	}
	return fmt.Errorf("notify: unknown provider %q", n.Provider)
}

// icons mark the notification kinds in Telegram messages.
var icons = map[string]string{
	events.NotifyMovieAvailable:   "🎬",
	events.NotifyEpisodeAvailable: "📺",
	events.NotifyMovieFailed:      "⚠️",
	events.NotifyEpisodeFailed:    "⚠️",
	events.NotifyMovieDeleted:     "🗑️",
	events.NotifySeriesDeleted:    "🗑️",
}

// telegramText formats a message as Telegram HTML: an icon and the title in
// bold, then the body. The release name of a finished download is set as code.
func telegramText(msg events.Notification) string { return telegramCaption(msg, 4096) }

func telegramCaption(msg events.Notification, limit int) string {
	title := clip(messageIcon(msg)+" "+msg.Title, min(limit, 250))
	icon := messageIcon(msg)
	title = strings.TrimPrefix(title, icon+" ")
	text := icon + " <b>" + html.EscapeString(title) + "</b>"
	remaining := limit - len([]rune(title)) - len([]rune(icon)) - 1
	if msg.Subtitle != "" && remaining > 2 {
		subtitle := clip(msg.Subtitle, min(remaining-2, 200))
		text += "\n\n" + html.EscapeString(subtitle)
		remaining -= len([]rune(subtitle)) + 2
	}
	if msg.Body != "" && remaining > 2 {
		body := html.EscapeString(clip(msg.Body, remaining-2))
		if msg.Event == events.NotifyMovieAvailable || msg.Event == events.NotifyEpisodeAvailable {
			body = "<code>" + body + "</code>"
		}
		text += "\n\n" + body
	}
	return text
}

func discordEscape(s string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", ">", "\\>", "[", "\\[", "]", "\\]").Replace(s)
}

// post sends one HTTP request. header is an optional key/value pair.
func (s *Service) post(ctx context.Context, url, contentType string, body []byte, header ...string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	client := s.HTTP
	if client == nil {
		client = httpx.NewClient()
	}
	if _, _, err := httpx.Do(client, req, 1<<20); err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) {
			return fmt.Errorf("notify: %w", status)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Telegram URLs contain the bot token; do not return transport URLs.
		return fmt.Errorf("notify: %w", httpx.ErrUnavailable)
	}
	return nil
}
