package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/shyper/autodownloadarr/internal/bots"
)

func (s *server) botReady(w http.ResponseWriter) bool {
	if s.Bots == nil {
		writeError(w, 503, "not_configured", "bots not configured")
		return false
	}
	return true
}

type botConfigView struct {
	TelegramEnabled        bool     `json:"telegramEnabled"`
	TelegramMode           string   `json:"telegramMode"`
	HasTelegramToken       bool     `json:"hasTelegramToken"`
	DiscordEnabled         bool     `json:"discordEnabled"`
	HasDiscordToken        bool     `json:"hasDiscordToken"`
	DiscordApplicationID   string   `json:"discordApplicationId"`
	DiscordPublicKey       string   `json:"discordPublicKey"`
	DiscordGuilds          []string `json:"discordGuilds"`
	TelegramWebhookURL     string   `json:"telegramWebhookUrl"`
	DiscordInteractionsURL string   `json:"discordInteractionsUrl"`
}

func (s *server) handleGetBots(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	v := botConfigView{TelegramEnabled: c.TelegramEnabled, TelegramMode: c.TelegramMode, HasTelegramToken: c.TelegramToken != "", DiscordEnabled: c.DiscordEnabled, HasDiscordToken: c.DiscordToken != "", DiscordApplicationID: c.DiscordApplicationID, DiscordPublicKey: c.DiscordPublicKey, DiscordGuilds: c.DiscordGuilds}
	if s.PublicURL != "" {
		v.TelegramWebhookURL = s.PublicURL + "/bots/telegram/webhook"
		v.DiscordInteractionsURL = s.PublicURL + "/bots/discord/interactions"
	}
	writeJSON(w, 200, v)
}
func (s *server) handleSaveBots(w http.ResponseWriter, r *http.Request) {
	if !s.botReady(w) {
		return
	}
	var in struct {
		botConfigView
		TelegramToken *string `json:"telegramToken"`
		DiscordToken  *string `json:"discordToken"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	c.TelegramEnabled = in.TelegramEnabled
	c.TelegramMode = in.TelegramMode
	c.DiscordEnabled = in.DiscordEnabled
	c.DiscordApplicationID = in.DiscordApplicationID
	c.DiscordPublicKey = in.DiscordPublicKey
	c.DiscordGuilds = in.DiscordGuilds
	if in.TelegramToken != nil && *in.TelegramToken != "" {
		c.TelegramToken = *in.TelegramToken
	}
	if in.DiscordToken != nil && *in.DiscordToken != "" {
		c.DiscordToken = *in.DiscordToken
	}
	if bots.ValidateConfig(c) != nil {
		writeError(w, 400, "invalid_input", "invalid bot configuration")
		return
	}
	if (c.DiscordEnabled || c.TelegramEnabled) && s.PublicURL == "" {
		writeError(w, 400, "invalid_input", "configure the public URL for account linking")
		return
	}
	if err = s.Store.SaveBotConfig(r.Context(), c); err != nil {
		s.internalError(w, "save bots", err)
		return
	}
	s.handleGetBots(w, r)
}
func (s *server) handleTestBots(w http.ResponseWriter, r *http.Request) {
	if !s.botReady(w) {
		return
	}
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	if err = s.Bots.Test(r.Context(), c); err != nil {
		writeError(w, 422, "bot_connection_failed", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleSyncBots(w http.ResponseWriter, r *http.Request) {
	if !s.botReady(w) {
		return
	}
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	if err = s.Bots.Sync(r.Context(), c); err != nil {
		writeError(w, 422, "bot_connection_failed", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleBotLinks(w http.ResponseWriter, r *http.Request) {
	u, _, _ := currentUser(r.Context())
	links, err := s.Store.BotLinks(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, "read bot links", err)
		return
	}
	writeJSON(w, 200, links)
}
func (s *server) handleBotLinkCode(w http.ResponseWriter, r *http.Request) {
	link, err := s.Store.BotLinkCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		s.writeStoreError(w, "read link code", err)
		return
	}
	writeJSON(w, 200, link)
}
func (s *server) handleConfirmBotLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, _, _ := currentUser(r.Context())
	if err := s.Store.ConfirmBotLink(r.Context(), u.ID, in.Code); err != nil {
		s.writeStoreError(w, "link bot", err)
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleUpdateBotLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID            int64  `json:"id"`
		Language      string `json:"language"`
		Notifications bool   `json:"notifications"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, _, _ := currentUser(r.Context())
	if err := s.Store.UpdateBotLink(r.Context(), u.ID, in.ID, in.Language, in.Notifications); err != nil {
		s.writeStoreError(w, "update bot link", err)
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleDeleteBotLink(w http.ResponseWriter, r *http.Request) {
	id, _ := idParam(r, "id")
	u, _, _ := currentUser(r.Context())
	if err := s.Store.DeleteBotLink(r.Context(), u.ID, id); err != nil {
		s.writeStoreError(w, "unlink bot", err)
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.botReady(w) {
		return
	}
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	secret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if !c.TelegramEnabled || c.TelegramMode != "webhook" || secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(c.TelegramSecret)) != 1 {
		writeError(w, 403, "forbidden", "invalid webhook secret")
		return
	}
	var update bots.TelegramUpdate
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if json.NewDecoder(r.Body).Decode(&update) != nil {
		writeError(w, 400, "invalid_request", "invalid update")
		return
	}
	if err := s.Bots.ReceiveTelegram(r.Context(), update); err != nil {
		s.internalError(w, "queue Telegram update", err)
		return
	}
	w.WriteHeader(200)
}
func (s *server) handleDiscordInteraction(w http.ResponseWriter, r *http.Request) {
	if !s.botReady(w) {
		return
	}
	c, err := s.Store.BotConfig(r.Context())
	if err != nil {
		s.internalError(w, "read bots", err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid_request", "invalid interaction")
		return
	}
	if !c.DiscordEnabled || !bots.VerifyDiscord(c.DiscordPublicKey, r.Header.Get("X-Signature-Timestamp"), r.Header.Get("X-Signature-Ed25519"), body, time.Now()) {
		writeError(w, 401, "unauthorized", "invalid interaction signature")
		return
	}
	var interaction bots.DiscordInteraction
	if json.Unmarshal(body, &interaction) != nil {
		writeError(w, 400, "invalid_request", "invalid interaction")
		return
	}
	response, err := s.Bots.ReceiveDiscord(r.Context(), c, interaction)
	if err != nil {
		s.internalError(w, "queue Discord interaction", err)
		return
	}
	writeJSON(w, 200, response)
}
