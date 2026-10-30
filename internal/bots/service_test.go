package bots

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/control"
	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

func testService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	ctx := t.Context()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "bots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for id := 1; id <= 2; id++ {
		if _, err = d.ExecContext(t.Context(), `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,'unused','user',0,0)`, id, fmt.Sprintf("user%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	box, _ := secrets.New(make([]byte, 32))
	st := store.New(d, box)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(d, log)
	bus := events.New(log)
	external := &movies.Services{Store: st, Log: log}
	mv := movies.NewManager(st, external, sched, bus, log)
	se := series.NewManager(st, external, sched, bus, log, mv.Limiter())
	au, err := auth.NewService(d, auth.WithArgon2Params(auth.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st, au, &control.Service{Store: st, Services: external, Movies: mv, Series: se}, sched, "https://media.example", log)
	if err = st.SaveBotConfig(ctx, store.BotConfig{TelegramEnabled: true, TelegramMode: "polling", TelegramToken: "123:secret", DiscordEnabled: true, DiscordApplicationID: "456", DiscordToken: "discord-secret"}); err != nil {
		t.Fatal(err)
	}
	code, err := st.CreateBotLinkCode(ctx, "telegram", "42", "42", "de")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ConfirmBotLink(ctx, 1, code); err != nil {
		t.Fatal(err)
	}
	return svc, d
}
func task(id string) scheduler.Task {
	data, _ := json.Marshal(map[string]string{"provider": "telegram", "id": id})
	return scheduler.Task{Payload: data}
}
func TestPreparedBotResponseRetriesWithoutRepeatingAction(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()
	s.TelegramURL = srv.URL
	mv, err := s.Store.CreateMovie(ctx, store.Movie{TMDBID: 42, Title: "Mine", State: store.MovieAvailable}, 1)
	if err != nil {
		t.Fatal(err)
	}
	action, _ := json.Marshal(Action{Op: "keep_movie", ID: mv.ID, Bool: true})
	token, err := s.Store.CreateBotAction(ctx, "telegram", "42", action)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queue(ctx, Inbound{Provider: "telegram", ID: "1", External: "42", Chat: "42", Callback: token, Language: "de", MessageID: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.HandleTask(ctx, task("1")); err == nil {
		t.Fatal("failed delivery not retried")
	}
	mv, err = s.Store.Movie(ctx, mv.ID)
	if err != nil || !mv.Keep {
		t.Fatal("action not applied")
	}
	mv.Keep = false
	if err = s.Store.UpdateMovie(ctx, mv); err != nil {
		t.Fatal(err)
	}
	if err = s.HandleTask(ctx, task("1")); err != nil {
		t.Fatal(err)
	}
	mv, _ = s.Store.Movie(ctx, mv.ID)
	if mv.Keep {
		t.Fatal("delivery retry repeated core action")
	}
	if err = s.HandleTask(ctx, task("1")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("completed response delivered again")
	}
}
func TestBotCannotManageForeignRequests(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	foreign, err := s.Store.CreateMovie(ctx, store.Movie{TMDBID: 43, Title: "Other", State: store.MovieAvailable}, 2)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Action{Op: "keep_movie", ID: foreign.ID, Bool: true})
	token, err := s.Store.CreateBotAction(ctx, "telegram", "42", data)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.Render(ctx, Inbound{Provider: "telegram", External: "42", Chat: "42", Callback: token, Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, "eigene Anforderungen") {
		t.Fatalf("missing denial: %+v", msg)
	}
	got, _ := s.Store.Movie(ctx, foreign.ID)
	if got.Keep {
		t.Fatal("foreign request modified")
	}
}
func TestTelegramDuplicateUpdatesAndPrivateChats(t *testing.T) {
	s, d := testService(t)
	ctx := t.Context()
	message := &TelegramMessage{ID: 1, From: TelegramUser{ID: 42, Language: "de"}, Text: "/library"}
	message.Chat.ID = 42
	message.Chat.Type = "private"
	u := TelegramUpdate{ID: 1, Message: message}
	for range 2 {
		if err := s.ReceiveTelegram(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := d.QueryRowContext(t.Context(), `SELECT count(*) FROM bot_updates`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("updates=%d %v", n, err)
	}
	message.Chat.Type = "group"
	u.ID = 2
	if err := s.ReceiveTelegram(ctx, u); err != nil {
		t.Fatal(err)
	}
	_ = d.QueryRowContext(t.Context(), `SELECT count(*) FROM bot_updates`).Scan(&n)
	if n != 1 {
		t.Fatal("group Telegram message accepted")
	}
}
func TestDiscordSignatureAndStaleTimestamp(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"type":1}`)
	now := time.Now()
	stamp := strconv.FormatInt(now.Unix(), 10)
	signature := hex.EncodeToString(ed25519.Sign(priv, append([]byte(stamp), body...)))
	if !VerifyDiscord(hex.EncodeToString(pub), stamp, signature, body, now) {
		t.Fatal("valid signature rejected")
	}
	if VerifyDiscord(hex.EncodeToString(pub), stamp, signature, []byte(`{"type":2}`), now) {
		t.Fatal("tampered body accepted")
	}
	if VerifyDiscord(hex.EncodeToString(pub), stamp, signature, body, now.Add(6*time.Minute)) {
		t.Fatal("stale signature accepted")
	}
}
func TestDiscordDefersAndRejectsUnapprovedGuild(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	cfg, _ := s.Store.BotConfig(ctx)
	var interaction DiscordInteraction
	interaction.ID = "i1"
	interaction.Type = 2
	interaction.Token = "interaction-secret"
	interaction.User.ID = "42"
	interaction.Data.Name = "help"
	response, err := s.ReceiveDiscord(ctx, cfg, interaction)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(response)
	if !strings.Contains(string(data), `"type":5`) || !strings.Contains(string(data), `"flags":64`) {
		t.Fatalf("not deferred privately: %s", data)
	}
	interaction.ID = "i2"
	interaction.GuildID = "unapproved"
	response, err = s.ReceiveDiscord(ctx, cfg, interaction)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(response)
	if !strings.Contains(string(data), `"type":4`) {
		t.Fatalf("unapproved guild not denied: %s", data)
	}
}
func TestDiscordComponentsAndRateLimits(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"retry_after":60}`)
	}))
	defer srv.Close()
	s.DiscordURL = srv.URL
	for range 2 {
		if err := s.request(ctx, "GET", srv.URL+"/users/@me", "secret", nil, nil); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe rate-limit error: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("provider retry-after ignored")
	}
	data, _ := json.Marshal(discordComponents(Message{Title: "Title", Text: "Text", Buttons: []Button{{Label: "Movie", Token: "opaque", Choice: true}, {Label: "Delete", Token: "delete", Danger: true}}}))
	if !strings.Contains(string(data), `"type":3`) || !strings.Contains(string(data), `"type":17`) || !strings.Contains(string(data), `"style":4`) {
		t.Fatalf("missing modern controls: %s", data)
	}
}
func TestPendingResponseStopsAfterUnlink(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	s.TelegramURL = srv.URL
	if err := s.queue(ctx, Inbound{Provider: "telegram", ID: "revoked", External: "42", Chat: "42", Command: "help"}); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTask(ctx, task("revoked")); err == nil {
		t.Fatal("expected send failure")
	}
	link, _ := s.Store.BotLink(ctx, "telegram", "42")
	if err := s.Store.DeleteBotLink(ctx, 1, link.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTask(ctx, task("revoked")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("response sent after unlink")
	}
}

func TestBotRequestsCarryUserAndSeriesSelection(t *testing.T) {
	s, d := testService(t)
	ctx := t.Context()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			fmt.Fprint(w, `{"results":[{"id":99,"title":"Film","release_date":"2026-01-01"}]}`)
		case "/movie/99":
			fmt.Fprint(w, `{"id":99,"title":"Film","genres":[{"name":"Drama"}]}`)
		case "/tv/100":
			fmt.Fprint(w, `{"id":100,"name":"Show","genres":[{"name":"Drama"}],"seasons":[{"season_number":1},{"season_number":2},{"season_number":3}]}`)
		case "/tv/100/season/1", "/tv/100/season/3":
			fmt.Fprint(w, `{"episodes":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s.Control.Services.TMDBBaseURL = srv.URL
	if err := s.Store.SetTMDBKey(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	result, err := s.Render(ctx, Inbound{Provider: "telegram", External: "42", Chat: "42", Command: "search", Query: "Film"})
	if err != nil || len(result.Buttons) != 1 {
		t.Fatalf("search=%+v %v", result, err)
	}
	details, err := s.Render(ctx, Inbound{Provider: "telegram", External: "42", Chat: "42", Callback: result.Buttons[0].Token})
	if err != nil || len(details.Buttons) != 1 {
		t.Fatalf("details=%+v %v", details, err)
	}
	_, err = s.Render(ctx, Inbound{Provider: "telegram", External: "42", Chat: "42", Callback: details.Buttons[0].Token})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := s.Store.MovieByTMDB(ctx, 99)
	if err != nil || mv.RequestedBy != 1 {
		t.Fatalf("movie user %v %v", mv, err)
	}
	code, err := s.Store.CreateBotLinkCode(ctx, "discord", "dc-user", "dc-dm", "en")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.ConfirmBotLink(ctx, 2, code); err != nil {
		t.Fatal(err)
	}
	action, _ := json.Marshal(Action{Op: "request_series", TMDBID: 100, Options: control.Input{Options: series.RequestOptions{Seasons: []int{1, 3}, WindowSize: 5}}})
	token, err := s.Store.CreateBotAction(ctx, "discord", "dc-user", action)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Render(ctx, Inbound{Provider: "discord", External: "dc-user", Chat: "dc-dm", Callback: token})
	if err != nil {
		t.Fatal(err)
	}
	se, err := s.Store.SeriesByTMDB(ctx, 100)
	if err != nil || se.RequestedBy != 2 || se.WindowSize != 5 || len(se.Seasons) != 2 || se.Seasons[0] != 1 || se.Seasons[1] != 3 {
		t.Fatalf("series selection %+v %v", se, err)
	}
	if _, err = d.ExecContext(t.Context(), `UPDATE users SET quota_requests=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	action, _ = json.Marshal(Action{Op: "request_movie", TMDBID: 101})
	token, err = s.Store.CreateBotAction(ctx, "discord", "dc-user", action)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.Render(ctx, Inbound{Provider: "discord", External: "dc-user", Callback: token})
	if err != nil || !strings.Contains(msg.Text, "quota") {
		t.Fatalf("quota not enforced: %+v %v", msg, err)
	}
}

func TestNotificationsArePersonalDurableAndCancelable(t *testing.T) {
	s, d := testService(t)
	ctx := t.Context()
	now := time.Now()
	mv, err := s.Store.CreateMovie(ctx, store.Movie{TMDBID: 321, Title: "Ready", State: store.MovieAvailable}, 1)
	if err != nil {
		t.Fatal(err)
	}
	mv.AvailableAt = &now
	if err = s.Store.UpdateMovie(ctx, mv); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.CreateMovie(ctx, store.Movie{TMDBID: 322, Title: "Other user", State: store.MovieFailed}, 2); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.PollNotices(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var id string
	if err := d.QueryRowContext(t.Context(), `SELECT count(*),min(id) FROM bot_updates WHERE id LIKE 'notice:%'`).Scan(&count, &id); err != nil || count != 1 {
		t.Fatalf("notifications %d %s %v", count, id, err)
	}
	link, _ := s.Store.BotLink(ctx, "telegram", "42")
	if err := s.Store.DeleteBotLink(ctx, 1, link.ID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true,"result":{}}`) }))
	defer srv.Close()
	s.TelegramURL = srv.URL
	if err := s.HandleTask(ctx, task(id)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("notification delivered after unlink")
	}
}

func TestExpiredDiscordInteractionFallsBackToDM(t *testing.T) {
	s, _ := testService(t)
	ctx := t.Context()
	code, err := s.Store.CreateBotLinkCode(ctx, "discord", "42", "channel", "en")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.ConfirmBotLink(ctx, 1, code); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/users/@me/channels":
			fmt.Fprint(w, `{"id":"dm-channel"}`)
		case "/channels/dm-channel/messages":
			fmt.Fprint(w, `{"id":"message"}`)
		default:
			t.Errorf("expired interaction used: %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	s.DiscordURL = srv.URL
	if err = s.queue(ctx, Inbound{Provider: "discord", ID: "late", External: "42", Chat: "channel", Language: "en", Command: "help", InteractionToken: "expired-secret", ReceivedAt: time.Now().Add(-20 * time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(map[string]string{"provider": "discord", "id": "late"})
	if err = s.HandleTask(ctx, scheduler.Task{Payload: job}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("late response not delivered as DM")
	}
}
