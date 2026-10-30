package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/store"
)

func TestDiscordWebhookVerifiesSignature(t *testing.T) {
	e := newEnv(t)
	client := e.client(t)
	e.completeSetup(t, client)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := store.BotConfig{DiscordEnabled: true, DiscordToken: "never-expose", DiscordApplicationID: "123", DiscordPublicKey: hex.EncodeToString(pub), TelegramMode: "polling"}
	if err := e.store.SaveBotConfig(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	body := map[string]int{"type": 1}
	data, _ := json.Marshal(body)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(stamp), data...)))
	res, out := e.do(t, client, "POST", "/bots/discord/interactions", body, "X-Signature-Timestamp", stamp, "X-Signature-Ed25519", sig)
	if res.StatusCode != 200 || out["type"] != float64(1) {
		t.Fatalf("valid ping %d %v", res.StatusCode, out)
	}
	res, _ = e.do(t, client, "POST", "/bots/discord/interactions", map[string]int{"type": 2}, "X-Signature-Timestamp", stamp, "X-Signature-Ed25519", sig)
	if res.StatusCode != 401 {
		t.Fatal("tampered webhook accepted")
	}
	res, out = e.do(t, client, "GET", "/api/v1/bots", nil)
	raw, _ := json.Marshal(out)
	if res.StatusCode != 200 || strings.Contains(string(raw), "never-expose") {
		t.Fatalf("credentials exposed %d", res.StatusCode)
	}
}
func TestTelegramWebhookSecretAndBotAccountOwnership(t *testing.T) {
	e := newEnv(t)
	admin := e.client(t)
	e.completeSetup(t, admin)
	cfg := store.BotConfig{TelegramEnabled: true, TelegramToken: "123:never-expose", TelegramMode: "webhook"}
	if err := e.store.SaveBotConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg, _ = e.store.BotConfig(t.Context())
	res, _ := e.do(t, e.client(t), "POST", "/bots/telegram/webhook", map[string]int{"update_id": 1})
	if res.StatusCode != 403 {
		t.Fatal("unverified Telegram webhook accepted")
	}
	res, _ = e.do(t, e.client(t), "POST", "/bots/telegram/webhook", map[string]int{"update_id": 1}, "X-Telegram-Bot-Api-Secret-Token", cfg.TelegramSecret)
	if res.StatusCode != 200 {
		t.Fatal("valid Telegram update rejected")
	}
	code, err := e.store.CreateBotLinkCode(t.Context(), "telegram", "42", "42", "de")
	if err != nil {
		t.Fatal(err)
	}
	res, _ = e.do(t, e.client(t), "POST", "/api/v1/bots/links", map[string]string{"code": code})
	if res.StatusCode != 401 {
		t.Fatal("anonymous account link accepted")
	}
	res, _ = e.do(t, admin, "POST", "/api/v1/bots/links", map[string]string{"code": code})
	if res.StatusCode != http.StatusNoContent {
		t.Fatal("account linking failed")
	}
	_, created := e.do(t, admin, "POST", "/api/v1/users", map[string]string{"username": "bob", "password": "longenough", "role": "user"})
	if created["id"] == nil {
		t.Fatal("create user failed")
	}
	bob := e.client(t)
	e.do(t, bob, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "longenough"})
	res, _ = e.do(t, bob, "GET", "/api/v1/bots", nil)
	if res.StatusCode != 403 {
		t.Fatal("user can read bot config")
	}
	link, err := e.store.BotLink(t.Context(), "telegram", "42")
	if err != nil {
		t.Fatal(err)
	}
	res, _ = e.do(t, bob, "DELETE", "/api/v1/bots/links/"+strconv.FormatInt(link.ID, 10), nil)
	if res.StatusCode != 404 {
		t.Fatalf("foreign link delete %d", res.StatusCode)
	}
}
