// Package bots provides interactive, account-linked Telegram and Discord clients.
package bots

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/control"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/store"
)

type Service struct {
	rateMu                  sync.Mutex
	rateUntil               map[string]time.Time
	Store                   *store.Store
	Auth                    *auth.Service
	Control                 *control.Service
	PublicURL               string
	Log                     *slog.Logger
	HTTP                    *http.Client
	TelegramURL, DiscordURL string
}
type Inbound struct {
	ReceivedAt                                                                         int64
	UserID                                                                             int64
	Provider, ID, External, Chat, Language, Command, Query, Callback, InteractionToken string
	MessageID                                                                          int64
	Photo                                                                              bool
	Notice                                                                             *store.BotNotice
}
type Button struct {
	Choice bool   `json:"choice,omitempty"`
	Label  string `json:"label"`
	Token  string `json:"token,omitempty"`
	URL    string `json:"url,omitempty"`
	Danger bool   `json:"danger,omitempty"`
}
type Message struct {
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Poster  string   `json:"poster,omitempty"`
	Buttons []Button `json:"buttons,omitempty"`
}
type Action struct {
	Op      string
	ID      int64
	TMDBID  int
	Query   string
	Kind    string
	Page    int
	Value   int
	Bool    bool
	Options control.Input
}

func New(st *store.Store, au *auth.Service, ctl *control.Service, sched *scheduler.Scheduler, publicURL string, log *slog.Logger) *Service {
	s := &Service{Store: st, Auth: au, Control: ctl, PublicURL: strings.TrimRight(publicURL, "/"), Log: log, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("bot API redirect rejected") }}, TelegramURL: "https://api.telegram.org", DiscordURL: "https://discord.com/api/v10"}
	sched.Handle("bot.update", s.HandleTask)
	sched.Every("telegram-updates", time.Second, s.PollTelegram)
	sched.Every("bot-notifications", time.Minute, s.PollNotices)
	sched.Every("purge-bot-state", time.Hour, st.PurgeBotState)
	return s
}
func text(lang, de, en string) string {
	if lang == "de" {
		return de
	}
	return en
}
func language(raw string) string {
	if strings.HasPrefix(strings.ToLower(raw), "de") {
		return "de"
	}
	return "en"
}
func (s *Service) queue(ctx context.Context, in Inbound) error {
	if in.ReceivedAt == 0 {
		in.ReceivedAt = time.Now().UnixMilli()
	}
	if link, err := s.Store.BotLink(ctx, in.Provider, in.External); err == nil {
		in.UserID = link.UserID
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.Store.QueueBotUpdate(ctx, in.Provider, in.ID, data)
}
func (s *Service) HandleTask(ctx context.Context, task scheduler.Task) error {
	var job struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(task.Payload, &job); err != nil {
		return scheduler.Permanent(err)
	}
	payload, cached, done, err := s.Store.BotUpdate(ctx, job.Provider, job.ID)
	if err != nil || done {
		return err
	}
	var in Inbound
	if json.Unmarshal(payload, &in) != nil {
		return scheduler.Permanent(errors.New("invalid bot update"))
	}
	if in.UserID > 0 {
		link, e := s.Store.BotLink(ctx, in.Provider, in.External)
		if e != nil || link.UserID != in.UserID {
			return s.Store.FinishBotUpdate(ctx, job.Provider, job.ID)
		}
	}
	cfg, err := s.Store.BotConfig(ctx)
	if err != nil {
		return err
	}
	if (in.Provider == "telegram" && !cfg.TelegramEnabled) || (in.Provider == "discord" && !cfg.DiscordEnabled) {
		return s.Store.FinishBotUpdate(ctx, job.Provider, job.ID)
	}
	var msg Message
	if len(cached) > 0 {
		if err := json.Unmarshal(cached, &msg); err != nil {
			return err
		}
	} else {
		if in.Notice != nil {
			link, e := s.Store.BotLink(ctx, in.Provider, in.External)
			if e != nil || link.ID != in.Notice.LinkID || !link.Notifications {
				return s.Store.FinishBotUpdate(ctx, job.Provider, job.ID)
			}
			state := text(link.Language, "Bereit zum Schauen", "Ready to watch")
			if in.Notice.State == "failed" {
				state = text(link.Language, "Download fehlgeschlagen", "Download failed")
			}
			msg = Message{Title: state, Text: in.Notice.Title, Buttons: []Button{{Label: text(link.Language, "Details öffnen", "Open details"), URL: s.PublicURL + "/" + map[string]string{"movie": "movies", "series": "series"}[in.Notice.Kind] + "/" + strconv.FormatInt(in.Notice.ID, 10)}}}
		} else {
			msg, err = s.Render(ctx, in)
			if err != nil {
				return err
			}
		}
		cached, err = json.Marshal(msg)
		if err != nil {
			return err
		}
		if err = s.Store.SaveBotResponse(ctx, job.Provider, job.ID, cached); err != nil {
			return err
		}
	}
	// A revoked account link must stop queued personal notifications too.
	if in.Notice != nil {
		link, e := s.Store.BotLink(ctx, in.Provider, in.External)
		if e != nil || link.ID != in.Notice.LinkID || !link.Notifications {
			return s.Store.FinishBotUpdate(ctx, job.Provider, job.ID)
		}
	}
	delivery := in
	if in.Provider == "discord" && in.InteractionToken != "" && in.ReceivedAt > 0 && time.Since(time.UnixMilli(in.ReceivedAt)) >= 14*time.Minute {
		delivery.InteractionToken = ""
		buttons := []Button{}
		stale := false
		for _, b := range msg.Buttons {
			if b.URL != "" {
				buttons = append(buttons, b)
			} else {
				stale = true
			}
		}
		msg.Buttons = buttons
		if stale {
			msg.Text += "\n\n" + text(in.Language, "Öffne /library oder starte die Suche erneut für aktuelle Aktionen.", "Open /library or search again for current actions.")
		}
	}
	if err = s.Send(ctx, cfg, delivery, msg); err != nil {
		return err
	}
	return s.Store.FinishBotUpdate(ctx, job.Provider, job.ID)
}
func (s *Service) PollNotices(ctx context.Context) error {
	cfg, err := s.Store.BotConfig(ctx)
	if err != nil {
		return err
	}
	items, err := s.Store.BotNotices(ctx)
	if err != nil {
		return err
	}
	for _, n := range items {
		if (n.Provider == "telegram" && !cfg.TelegramEnabled) || (n.Provider == "discord" && !cfg.DiscordEnabled) {
			continue
		}
		id := fmt.Sprintf("notice:%d:%s:%d:%s:%s", n.LinkID, n.Kind, n.ID, n.State, n.Generation)
		if err := s.queue(ctx, Inbound{Provider: n.Provider, ID: id, External: n.ExternalID, Chat: n.ChatID, Language: n.Language, Notice: &n}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) request(ctx context.Context, method, endpoint, token string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid bot endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bot "+token)
	}
	provider := "discord"
	if strings.HasPrefix(endpoint, s.TelegramURL+"/bot") {
		provider = "telegram"
	}
	s.rateMu.Lock()
	until := s.rateUntil[provider]
	s.rateMu.Unlock()
	if time.Now().Before(until) {
		return scheduler.RetryAfter(errors.New("bot API rate limited"), time.Until(until))
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return errors.New("bot API request failed")
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return errors.New("bot API response failed")
	}
	// Provider URLs contain credentials; only stable sanitized errors leave this layer.
	if resp.StatusCode == http.StatusTooManyRequests {
		var limits struct {
			Retry      float64 `json:"retry_after"`
			Parameters struct {
				Retry float64 `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(data, &limits)
		seconds, _ := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
		seconds = max(seconds, limits.Retry, limits.Parameters.Retry, 1)
		delay := time.Duration(min(seconds, 86400) * float64(time.Second))
		s.rateMu.Lock()
		if s.rateUntil == nil {
			s.rateUntil = map[string]time.Time{}
		}
		s.rateUntil[provider] = time.Now().Add(delay)
		s.rateMu.Unlock()
		return scheduler.RetryAfter(errors.New("bot API rate limited"), delay)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bot API status %d", resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err = json.Unmarshal(data, out); err != nil {
			return errors.New("invalid bot API response")
		}
	}
	return nil
}
func (s *Service) telegram(ctx context.Context, c store.BotConfig, method string, body any, out any) error {
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	err := s.request(ctx, http.MethodPost, s.TelegramURL+"/bot"+c.TelegramToken+"/"+method, "", body, &envelope)
	if err != nil {
		return err
	}
	if !envelope.OK {
		if strings.Contains(envelope.Description, "message is not modified") {
			return nil
		}
		return errors.New("telegram API rejected request")
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (s *Service) PollTelegram(ctx context.Context) error {
	c, err := s.Store.BotConfig(ctx)
	if err != nil || !c.TelegramEnabled || c.TelegramMode != "polling" {
		return err
	}
	s.rateMu.Lock()
	until := s.rateUntil["telegram"]
	s.rateMu.Unlock()
	if time.Now().Before(until) {
		return nil
	}
	offset, err := s.Store.TelegramOffset(ctx)
	if err != nil {
		return err
	}
	var updates []TelegramUpdate
	if err = s.telegram(ctx, c, "getUpdates", map[string]any{"offset": offset, "timeout": 20, "allowed_updates": []string{"message", "callback_query"}}, &updates); err != nil {
		s.rateMu.Lock()
		if s.rateUntil == nil {
			s.rateUntil = map[string]time.Time{}
		}
		s.rateUntil["telegram"] = maxTime(s.rateUntil["telegram"], time.Now().Add(15*time.Second))
		s.rateMu.Unlock()
		return err
	}
	for _, update := range updates {
		if err = s.ReceiveTelegram(ctx, update); err != nil {
			return err
		}
		if update.ID >= offset {
			offset = update.ID + 1
		}
	}
	return s.Store.SetTelegramOffset(ctx, offset)
}

type TelegramUpdate struct {
	ID       int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
	Callback *struct {
		ID      string           `json:"id"`
		From    TelegramUser     `json:"from"`
		Data    string           `json:"data"`
		Message *TelegramMessage `json:"message"`
	} `json:"callback_query"`
}
type TelegramUser struct {
	ID       int64  `json:"id"`
	Language string `json:"language_code"`
	Bot      bool   `json:"is_bot"`
}
type TelegramMessage struct {
	ID    int64             `json:"message_id"`
	From  TelegramUser      `json:"from"`
	Text  string            `json:"text"`
	Photo []json.RawMessage `json:"photo"`
	Chat  struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
}

func (s *Service) ReceiveTelegram(ctx context.Context, u TelegramUpdate) error {
	m := u.Message
	var user TelegramUser
	callback := ""
	if u.Callback != nil {
		m = u.Callback.Message
		user = u.Callback.From
		callback = u.Callback.Data
	} else if m != nil {
		user = m.From
	}
	if m == nil || user.Bot || m.Chat.Type != "private" || user.ID != m.Chat.ID {
		return nil
	}
	c, err := s.Store.BotConfig(ctx)
	if err != nil {
		return err
	}
	in := Inbound{Provider: "telegram", ID: strings.SplitN(c.TelegramToken, ":", 2)[0] + ":" + strconv.FormatInt(u.ID, 10), External: strconv.FormatInt(user.ID, 10), Chat: strconv.FormatInt(m.Chat.ID, 10), Language: language(user.Language), MessageID: m.ID, Photo: len(m.Photo) > 0, Callback: callback}
	if callback == "" {
		parts := strings.SplitN(strings.TrimSpace(m.Text), " ", 2)
		in.Command = strings.ToLower(strings.TrimPrefix(strings.SplitN(parts[0], "@", 2)[0], "/"))
		if len(parts) > 1 {
			in.Query = strings.TrimSpace(parts[1])
		}
		if !strings.HasPrefix(m.Text, "/") {
			in.Command = "search"
			in.Query = strings.TrimSpace(m.Text)
		}
		in.MessageID = 0
	}
	if u.Callback != nil {
		c, err := s.Store.BotConfig(ctx)
		if err != nil {
			return err
		}
		ackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_ = s.telegram(ackCtx, c, "answerCallbackQuery", map[string]string{"callback_query_id": u.Callback.ID}, nil)
		cancel()
	}
	return s.queue(ctx, in)
}

// VerifyDiscord checks the exact signed body and rejects stale/replayed timestamps.
func VerifyDiscord(key, timestamp, signature string, body []byte, now time.Time) bool {
	pub, err := hex.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(seconds, 0)) > 5*time.Minute || time.Unix(seconds, 0).Sub(now) > time.Minute {
		return false
	}
	return ed25519.Verify(pub, append([]byte(timestamp), body...), sig)
}

type DiscordInteraction struct {
	ID        string `json:"id"`
	Type      int    `json:"type"`
	Token     string `json:"token"`
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Locale    string `json:"locale"`
	User      struct {
		ID string `json:"id"`
	} `json:"user"`
	Member struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"member"`
	Data struct {
		Name     string   `json:"name"`
		CustomID string   `json:"custom_id"`
		Values   []string `json:"values"`
		Options  []struct {
			Name    string `json:"name"`
			Value   string `json:"value"`
			Focused bool   `json:"focused"`
		} `json:"options"`
	} `json:"data"`
}

func (s *Service) ReceiveDiscord(ctx context.Context, c store.BotConfig, d DiscordInteraction) (any, error) {
	if d.Type == 1 {
		return map[string]int{"type": 1}, nil
	}
	external := d.User.ID
	if external == "" {
		external = d.Member.User.ID
	}
	if external == "" || d.ID == "" || d.Token == "" {
		return nil, store.ErrInvalid
	}
	if d.GuildID != "" {
		allowed := false
		for _, g := range c.DiscordGuilds {
			if g == d.GuildID {
				allowed = true
			}
		}
		if !allowed {
			return map[string]any{"type": 4, "data": map[string]any{"content": text(language(d.Locale), "Dieser Server ist nicht freigegeben.", "This server is not enabled."), "flags": 64}}, nil
		}
	}
	in := Inbound{Provider: "discord", ID: d.ID, External: external, Chat: d.ChannelID, Language: language(d.Locale), Command: d.Data.Name, Callback: d.Data.CustomID, InteractionToken: d.Token}
	if d.Data.CustomID == "select" && len(d.Data.Values) == 1 {
		in.Callback = d.Data.Values[0]
	}
	for _, o := range d.Data.Options {
		if o.Name == "query" {
			in.Query = o.Value
		}
		if o.Name == "kind" && o.Value == "series" {
			in.Command = "series"
		}
		if o.Name == "language" {
			in.Query = o.Value
		}
	}
	if d.Type == 4 {
		choices := []map[string]string{}
		link, e := s.Store.BotLink(ctx, "discord", external)
		if e == nil && len(in.Query) >= 2 {
			user, e := s.Auth.User(ctx, link.UserID)
			if e == nil {
				short, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
				choices = s.autocomplete(short, user, in)
				cancel()
			}
		}
		return map[string]any{"type": 8, "data": map[string]any{"choices": choices}}, nil
	}
	if d.Type != 2 && d.Type != 3 {
		return nil, store.ErrInvalid
	}
	if err := s.queue(ctx, in); err != nil {
		return nil, err
	}
	return map[string]any{"type": 5, "data": map[string]any{"flags": 64}}, nil
}
func clip(value string, n int) string {
	r := []rune(value)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return value
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
