package bots

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/shyper/autodownloadarr/internal/store"
)

func (s *Service) Send(ctx context.Context, c store.BotConfig, in Inbound, msg Message) error {
	if in.Provider == "telegram" {
		return s.sendTelegram(ctx, c, in, msg)
	}
	components := discordComponents(msg)
	body := map[string]any{"flags": 32768, "components": components, "allowed_mentions": map[string]any{"parse": []string{}}}
	if in.InteractionToken != "" {
		return s.request(ctx, http.MethodPatch, s.DiscordURL+"/webhooks/"+c.DiscordApplicationID+"/"+in.InteractionToken+"/messages/@original", "", body, nil)
	}
	var channel struct {
		ID string `json:"id"`
	}
	if err := s.request(ctx, http.MethodPost, s.DiscordURL+"/users/@me/channels", c.DiscordToken, map[string]string{"recipient_id": in.External}, &channel); err != nil {
		return err
	}
	if channel.ID == "" {
		return errors.New("Discord did not return a DM channel")
	}
	return s.request(ctx, http.MethodPost, s.DiscordURL+"/channels/"+channel.ID+"/messages", c.DiscordToken, body, nil)
}
func discordComponents(msg Message) []any {
	content := "## " + escapeDiscord(clip(msg.Title, 150)) + "\n" + escapeDiscord(clip(msg.Text, 1600))
	var parts []any
	display := map[string]any{"type": 10, "content": content}
	if msg.Poster != "" {
		parts = append(parts, map[string]any{"type": 9, "components": []any{display}, "accessory": map[string]any{"type": 11, "media": map[string]string{"url": msg.Poster}}})
	} else {
		parts = append(parts, display)
	}

	choices := []any{}
	buttons := []Button{}
	for _, b := range msg.Buttons {
		if b.Choice && b.Token != "" {
			choices = append(choices, map[string]string{"label": clip(b.Label, 100), "value": b.Token})
		} else {
			buttons = append(buttons, b)
		}
	}
	if len(choices) > 0 {
		parts = append(parts, map[string]any{"type": 1, "components": []any{map[string]any{"type": 3, "custom_id": "select", "options": choices, "min_values": 1, "max_values": 1}}})
	}
	msg.Buttons = buttons
	for start := 0; start < len(msg.Buttons) && start < 25; start += 5 {
		row := []any{}
		for _, b := range msg.Buttons[start:min(start+5, len(msg.Buttons))] {
			button := map[string]any{"type": 2, "label": clip(b.Label, 80), "style": 2}
			if b.URL != "" {
				button["style"] = 5
				button["url"] = b.URL
			} else {
				button["custom_id"] = b.Token
				if b.Danger {
					button["style"] = 4
				}
			}
			row = append(row, button)
		}
		parts = append(parts, map[string]any{"type": 1, "components": row})
	}
	return []any{map[string]any{"type": 17, "accent_color": 0x0891b2, "components": parts}}
}
func escapeDiscord(s string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", ">", "\\>", "[", "\\[", "]", "\\]").Replace(s)
}
func (s *Service) sendTelegram(ctx context.Context, c store.BotConfig, in Inbound, msg Message) error {
	rows := []any{}
	for _, b := range msg.Buttons {
		button := map[string]any{"text": b.Label}
		if b.URL != "" {
			button["url"] = b.URL
		} else {
			button["callback_data"] = b.Token
		}
		rows = append(rows, []any{button})
	}
	markup := map[string]any{"inline_keyboard": rows}
	body := map[string]any{"chat_id": in.Chat, "reply_markup": markup}
	content := msg.Title + "\n\n" + msg.Text
	if in.MessageID > 0 {
		body["message_id"] = in.MessageID
		if in.Photo {
			if msg.Poster != "" {
				body["media"] = map[string]any{"type": "photo", "media": msg.Poster, "caption": clip(content, 1024)}
				return s.telegram(ctx, c, "editMessageMedia", body, nil)
			}
			body["caption"] = clip(content, 1024)
			return s.telegram(ctx, c, "editMessageCaption", body, nil)
		}
		body["text"] = clip(content, 4096)
		body["link_preview_options"] = map[string]bool{"is_disabled": true}
		return s.telegram(ctx, c, "editMessageText", body, nil)
	}
	if msg.Poster != "" {
		body["photo"] = msg.Poster
		body["caption"] = clip(content, 1024)
		return s.telegram(ctx, c, "sendPhoto", body, nil)
	}
	body["text"] = clip(content, 4096)
	body["link_preview_options"] = map[string]bool{"is_disabled": true}
	return s.telegram(ctx, c, "sendMessage", body, nil)
}
func (s *Service) Test(ctx context.Context, c store.BotConfig) error {
	if !c.TelegramEnabled && !c.DiscordEnabled {
		return errors.New("no bots enabled")
	}
	if c.TelegramEnabled {
		if err := s.telegram(ctx, c, "getMe", nil, &json.RawMessage{}); err != nil {
			return err
		}
	}
	if c.DiscordEnabled {
		return s.request(ctx, http.MethodGet, s.DiscordURL+"/users/@me", c.DiscordToken, nil, &json.RawMessage{})
	}
	return nil
}
func (s *Service) Sync(ctx context.Context, c store.BotConfig) error {
	if !c.TelegramEnabled && !c.DiscordEnabled {
		return errors.New("no bots enabled")
	}
	if c.TelegramEnabled {
		commands := []map[string]string{{"command": "search", "description": "Search movies / Filme suchen"}, {"command": "series", "description": "Search series / Serien suchen"}, {"command": "library", "description": "My library / Meine Bibliothek"}, {"command": "downloads", "description": "Downloads and queue / Warteschlange"}, {"command": "connect", "description": "Connect account / Konto verbinden"}, {"command": "language", "description": "Language: de / en"}, {"command": "help", "description": "Help / Hilfe"}}
		if err := s.telegram(ctx, c, "setMyCommands", map[string]any{"commands": commands}, nil); err != nil {
			return err
		}
		if c.TelegramMode == "webhook" {
			if s.PublicURL == "" {
				return errors.New("public URL required for Telegram webhook")
			}
			if err := s.telegram(ctx, c, "setWebhook", map[string]any{"url": s.PublicURL + "/bots/telegram/webhook", "secret_token": c.TelegramSecret, "allowed_updates": []string{"message", "callback_query"}}, nil); err != nil {
				return err
			}
		} else {
			if err := s.telegram(ctx, c, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil); err != nil {
				return err
			}
		}
	}
	if c.DiscordEnabled {
		commands := []any{}
		for _, cmd := range []struct{ name, description, de string }{{"connect", "Connect your account", "Konto verbinden"}, {"search", "Search movies", "Filme suchen"}, {"series", "Search TV series", "Serien suchen"}, {"library", "Your requests and shared library", "Eigene Anforderungen und gemeinsame Bibliothek"}, {"downloads", "Downloads and queue", "Downloads und Warteschlange"}, {"language", "Choose language", "Sprache wählen"}, {"help", "Show help", "Hilfe anzeigen"}} {
			item := map[string]any{"name": cmd.name, "description": cmd.description, "description_localizations": map[string]string{"de": cmd.de}, "contexts": []int{0, 1, 2}, "integration_types": []int{0, 1}}
			if cmd.name == "search" || cmd.name == "series" {
				item["options"] = []any{map[string]any{"type": 3, "name": "query", "description": "Title / Titel", "required": true, "autocomplete": true}}
			}
			if cmd.name == "language" {
				item["options"] = []any{map[string]any{"type": 3, "name": "language", "description": "Language / Sprache", "required": true, "choices": []any{map[string]string{"name": "Deutsch", "value": "de"}, map[string]string{"name": "English", "value": "en"}}}}
			}
			commands = append(commands, item)
		}
		return s.request(ctx, http.MethodPut, s.DiscordURL+"/applications/"+c.DiscordApplicationID+"/commands", c.DiscordToken, commands, nil)
	}
	return nil
}
func ValidateConfig(c store.BotConfig) error {
	if c.TelegramMode != "polling" && c.TelegramMode != "webhook" {
		return store.ErrInvalid
	}
	if c.TelegramEnabled && (c.TelegramToken == "" || strings.ContainsAny(c.TelegramToken, "/\r\n?# ")) {
		return store.ErrInvalid
	}
	if c.DiscordEnabled {
		if c.DiscordToken == "" || strings.ContainsAny(c.DiscordToken, "\r\n ") {
			return store.ErrInvalid
		}
		if _, err := strconv.ParseUint(c.DiscordApplicationID, 10, 64); err != nil {
			return store.ErrInvalid
		}
		if key, err := hex.DecodeString(c.DiscordPublicKey); err != nil || len(key) != 32 {
			return store.ErrInvalid
		}
	}
	for _, g := range c.DiscordGuilds {
		if _, err := strconv.ParseUint(g, 10, 64); err != nil {
			return store.ErrInvalid
		}
	}
	return nil
}
