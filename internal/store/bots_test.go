package store

import (
	"bytes"
	"encoding/json"
	"testing"
)

func botUser(t *testing.T, s *Store, id int) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,'unused','user',0,0)`, id, string(rune('a'+id))); err != nil {
		t.Fatal(err)
	}
}
func TestBotLinkCodesAndOwnership(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	botUser(t, s, 1)
	botUser(t, s, 2)
	code, err := s.CreateBotLinkCode(ctx, "telegram", "42", "42", "de")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmBotLink(ctx, 1, code); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmBotLink(ctx, 1, code); err == nil {
		t.Fatal("link code reused")
	}
	link, err := s.BotLink(ctx, "telegram", "42")
	if err != nil || link.UserID != 1 {
		t.Fatalf("link=%+v %v", link, err)
	}
	code, err = s.CreateBotLinkCode(ctx, "telegram", "42", "42", "de")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmBotLink(ctx, 2, code); err == nil {
		t.Fatal("another account stole an existing link")
	}
	if err = s.DeleteBotLink(ctx, 2, link.ID); err == nil {
		t.Fatal("foreign account unlinked bot")
	}
	if err = s.UpdateBotLink(ctx, 2, link.ID, "en", false); err == nil {
		t.Fatal("foreign account updated bot")
	}
	if err = s.DeleteBotLink(ctx, 1, link.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmBotLink(ctx, 2, code); err != nil {
		t.Fatal(err)
	}
	expired, err := s.CreateBotLinkCode(ctx, "discord", "99", "99", "en")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE bot_link_codes SET expires_at=0`); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmBotLink(ctx, 1, expired); err == nil {
		t.Fatal("expired code accepted")
	}
}
func TestBotUpdatesAreDurableEncryptedAndDeduplicated(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	secret := []byte(`{"InteractionToken":"sensitive-secret"}`)
	for range 2 {
		if err := s.QueueBotUpdate(ctx, "discord", "123", secret); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scheduled_tasks WHERE type='bot.update'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tasks=%d %v", count, err)
	}
	var raw string
	if err := s.db.QueryRowContext(t.Context(), `SELECT payload_enc FROM bot_updates`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(raw), []byte("sensitive-secret")) {
		t.Fatal("transport token stored in plaintext")
	}
	payload, _, done, err := s.BotUpdate(ctx, "discord", "123")
	if err != nil || done || !bytes.Equal(payload, secret) {
		t.Fatalf("update=%s %v %v", payload, done, err)
	}
	response, _ := json.Marshal(map[string]string{"title": "Prepared response"})
	if err = s.SaveBotResponse(ctx, "discord", "123", response); err != nil {
		t.Fatal(err)
	}
	_, cached, _, err := s.BotUpdate(ctx, "discord", "123")
	if err != nil || !bytes.Equal(cached, response) {
		t.Fatal("response not recoverable")
	}
	if err = s.FinishBotUpdate(ctx, "discord", "123"); err != nil {
		t.Fatal(err)
	}
	_, _, done, err = s.BotUpdate(ctx, "discord", "123")
	if err != nil || !done {
		t.Fatal("completion not persisted")
	}
	token, err := s.CreateBotAction(ctx, "telegram", "42", []byte(`{"Op":"delete_movie","ID":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TakeBotAction(ctx, "telegram", "99", token); err == nil {
		t.Fatal("foreign user consumed button")
	}
	if _, err = s.TakeBotAction(ctx, "discord", "42", token); err == nil {
		t.Fatal("wrong provider consumed button")
	}
	if _, err = s.TakeBotAction(ctx, "telegram", "42", token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TakeBotAction(ctx, "telegram", "42", token); err == nil {
		t.Fatal("button reused")
	}
}
func TestBotConfigKeepsSecretsEncrypted(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	c := BotConfig{TelegramToken: "123:secret", TelegramMode: "polling", DiscordToken: "discord-secret"}
	if err := s.SaveBotConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRowContext(t.Context(), `SELECT value FROM settings WHERE key='bots'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(raw), []byte("123:secret")) || bytes.Contains([]byte(raw), []byte("discord-secret")) {
		t.Fatal("plaintext bot credentials")
	}
	got, err := s.BotConfig(ctx)
	if err != nil || got.TelegramToken != c.TelegramToken || got.DiscordToken != c.DiscordToken || got.TelegramSecret == "" {
		t.Fatalf("config not recovered %v", err)
	}
}
