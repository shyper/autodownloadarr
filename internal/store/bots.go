package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type BotConfig struct {
	TelegramEnabled      bool     `json:"telegramEnabled"`
	TelegramToken        string   `json:"-"`
	TelegramTokenEnc     string   `json:"telegramTokenEnc,omitempty"`
	TelegramMode         string   `json:"telegramMode"`
	TelegramSecret       string   `json:"-"`
	TelegramSecretEnc    string   `json:"telegramSecretEnc,omitempty"`
	DiscordEnabled       bool     `json:"discordEnabled"`
	DiscordToken         string   `json:"-"`
	DiscordTokenEnc      string   `json:"discordTokenEnc,omitempty"`
	DiscordApplicationID string   `json:"discordApplicationId"`
	DiscordPublicKey     string   `json:"discordPublicKey"`
	DiscordGuilds        []string `json:"discordGuilds"`
}

func (s *Store) BotConfig(ctx context.Context) (BotConfig, error) {
	c := BotConfig{TelegramMode: "polling", DiscordGuilds: []string{}}
	_, err := s.getSetting(ctx, "bots", &c)
	if c.DiscordGuilds == nil {
		c.DiscordGuilds = []string{}
	}
	c.TelegramToken = s.decrypt(c.TelegramTokenEnc)
	c.DiscordToken = s.decrypt(c.DiscordTokenEnc)
	c.TelegramSecret = s.decrypt(c.TelegramSecretEnc)
	return c, err
}
func (s *Store) SaveBotConfig(ctx context.Context, c BotConfig) error {
	old, err := s.BotConfig(ctx)
	if err != nil {
		return err
	}
	if strings.SplitN(old.TelegramToken, ":", 2)[0] != strings.SplitN(c.TelegramToken, ":", 2)[0] {
		if err := s.SetTelegramOffset(ctx, 0); err != nil {
			return err
		}
	}
	if c.TelegramTokenEnc, err = s.encrypt(c.TelegramToken); err != nil {
		return err
	}
	if c.DiscordTokenEnc, err = s.encrypt(c.DiscordToken); err != nil {
		return err
	}
	if c.TelegramSecret == "" {
		c.TelegramSecret = botRandom()
	}
	if c.TelegramSecretEnc, err = s.encrypt(c.TelegramSecret); err != nil {
		return err
	}
	return s.setSetting(ctx, "bots", c)
}
func botRandom() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func botHash(v string) []byte { sum := sha256.Sum256([]byte(v)); return sum[:] }

type BotLink struct {
	ID            int64  `json:"id"`
	Provider      string `json:"provider"`
	ExternalID    string `json:"externalId"`
	ChatID        string `json:"-"`
	UserID        int64  `json:"userId"`
	Language      string `json:"language"`
	Notifications bool   `json:"notifications"`
	CreatedAt     int64  `json:"createdAt"`
}

func (s *Store) BotLinks(ctx context.Context, userID int64) ([]BotLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,provider,external_id,chat_id,user_id,language,notifications,created_at FROM bot_links WHERE user_id=? ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BotLink{}
	for rows.Next() {
		var l BotLink
		if err := rows.Scan(&l.ID, &l.Provider, &l.ExternalID, &l.ChatID, &l.UserID, &l.Language, &l.Notifications, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) BotLink(ctx context.Context, provider, external string) (BotLink, error) {
	var l BotLink
	err := s.db.QueryRowContext(ctx, `SELECT id,provider,external_id,chat_id,user_id,language,notifications,created_at FROM bot_links WHERE provider=? AND external_id=?`, provider, external).Scan(&l.ID, &l.Provider, &l.ExternalID, &l.ChatID, &l.UserID, &l.Language, &l.Notifications, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return l, err
}
func (s *Store) UpdateBotLink(ctx context.Context, userID, id int64, language string, notifications bool) error {
	if language != "de" && language != "en" {
		return ErrInvalid
	}
	return affected(s.db.ExecContext(ctx, `UPDATE bot_links SET language=?,notifications=? WHERE id=? AND user_id=?`, language, notifications, id, userID))
}
func (s *Store) DeleteBotLink(ctx context.Context, userID, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM bot_links WHERE id=? AND user_id=?`, id, userID))
}
func (s *Store) CreateBotLinkCode(ctx context.Context, provider, external, chat, language string) (string, error) {
	if provider != "telegram" && provider != "discord" {
		return "", ErrInvalid
	}
	code := botRandom()
	_, err := s.db.ExecContext(ctx, `INSERT INTO bot_link_codes(hash,provider,external_id,chat_id,language,expires_at) VALUES(?,?,?,?,?,?)`, botHash(code), provider, external, chat, language, s.now().Add(10*time.Minute).UnixMilli())
	return code, err
}
func (s *Store) BotLinkCode(ctx context.Context, code string) (BotLink, error) {
	var l BotLink
	err := s.db.QueryRowContext(ctx, `SELECT provider,external_id,chat_id,language FROM bot_link_codes WHERE hash=? AND expires_at>?`, botHash(code), s.ms()).Scan(&l.Provider, &l.ExternalID, &l.ChatID, &l.Language)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return l, err
}
func (s *Store) ConfirmBotLink(ctx context.Context, userID int64, code string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var l BotLink
	err = tx.QueryRowContext(ctx, `DELETE FROM bot_link_codes WHERE hash=? AND expires_at>? RETURNING provider,external_id,chat_id,language`, botHash(code), s.ms()).Scan(&l.Provider, &l.ExternalID, &l.ChatID, &l.Language)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM bot_links WHERE provider=? AND external_id=?`, l.Provider, l.ExternalID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != 0 && owner != userID {
		return ErrInvalid
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM bot_links WHERE provider=? AND user_id=?`, l.Provider, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO bot_links(provider,external_id,chat_id,user_id,language,created_at) VALUES(?,?,?,?,?,?)`, l.Provider, l.ExternalID, l.ChatID, userID, l.Language, s.ms()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CreateBotAction(ctx context.Context, provider, external string, payload []byte) (string, error) {
	token := botRandom()
	_, err := s.db.ExecContext(ctx, `INSERT INTO bot_actions(hash,provider,external_id,payload,expires_at) VALUES(?,?,?,?,?)`, botHash(token), provider, external, string(payload), s.now().Add(15*time.Minute).UnixMilli())
	return token, err
}
func (s *Store) TakeBotAction(ctx context.Context, provider, external, token string) ([]byte, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `UPDATE bot_actions SET used=1 WHERE hash=? AND provider=? AND external_id=? AND expires_at>? AND used=0 RETURNING payload`, botHash(token), provider, external, s.ms()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return []byte(data), err
}

// QueueBotUpdate durably deduplicates an inbound update and schedules it in one transaction.
// Transport credentials, including Discord interaction tokens, are encrypted at rest.
func (s *Store) QueueBotUpdate(ctx context.Context, provider, id string, payload []byte) error {
	encrypted, err := s.encrypt(string(payload))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO bot_updates(provider,id,payload_enc,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, provider, id, encrypted, s.ms())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	job, _ := json.Marshal(map[string]string{"provider": provider, "id": id})
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_tasks(type,dedupe_key,payload,run_at,max_attempts,created_at,updated_at) VALUES('bot.update',?,?,?,5,?,?)`, "bot:"+provider+":"+id, string(job), s.ms(), s.ms(), s.ms())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) BotUpdate(ctx context.Context, provider, id string) ([]byte, []byte, bool, error) {
	var payload, response string
	var done bool
	err := s.db.QueryRowContext(ctx, `SELECT payload_enc,response_enc,done FROM bot_updates WHERE provider=? AND id=?`, provider, id).Scan(&payload, &response, &done)
	return []byte(s.decrypt(payload)), []byte(s.decrypt(response)), done, err
}
func (s *Store) SaveBotResponse(ctx context.Context, provider, id string, response []byte) error {
	data, err := s.encrypt(string(response))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE bot_updates SET response_enc=? WHERE provider=? AND id=?`, data, provider, id)
	return err
}
func (s *Store) FinishBotUpdate(ctx context.Context, provider, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE bot_updates SET done=1,payload_enc='',response_enc='' WHERE provider=? AND id=?`, provider, id)
	return err
}
func (s *Store) TelegramOffset(ctx context.Context) (int64, error) {
	var offset int64
	_, err := s.getSetting(ctx, "bots.telegram.offset", &offset)
	return offset, err
}
func (s *Store) SetTelegramOffset(ctx context.Context, n int64) error {
	return s.setSetting(ctx, "bots.telegram.offset", n)
}

type BotNotice struct {
	Provider, ExternalID, ChatID, Language, Kind, Title, State, Generation string
	ID                                                                     int64
	LinkID                                                                 int64
}

func (s *Store) BotNotices(ctx context.Context) ([]BotNotice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.provider,l.external_id,l.chat_id,l.language,'movie',m.title,m.state,CAST(COALESCE(m.available_at,0) AS TEXT),m.id,l.id
 FROM movies m JOIN bot_links l ON l.user_id=m.requested_by WHERE l.notifications=1 AND m.state IN ('available','failed') AND m.updated_at>=l.created_at
 UNION ALL
 SELECT l.provider,l.external_id,l.chat_id,l.language,'series',se.title || ' · S' || printf('%02d',e.season) || 'E' || printf('%02d',e.episode),e.state,CAST(COALESCE(e.available_at,0) AS TEXT) || ':' || CAST(e.id AS TEXT),se.id,l.id
 FROM episodes e JOIN series se ON se.id=e.series_id JOIN bot_links l ON l.user_id=se.requested_by WHERE l.notifications=1 AND e.state IN ('available','failed') AND e.updated_at>=l.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BotNotice{}
	for rows.Next() {
		var n BotNotice
		if err := rows.Scan(&n.Provider, &n.ExternalID, &n.ChatID, &n.Language, &n.Kind, &n.Title, &n.State, &n.Generation, &n.ID, &n.LinkID); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) PurgeBotState(ctx context.Context) error {
	// Keep deduplication markers longer than Telegram's 24-hour update retention.
	for _, stmt := range []string{`DELETE FROM bot_link_codes WHERE expires_at<?`, `DELETE FROM bot_actions WHERE expires_at<?`, `DELETE FROM bot_updates WHERE done=1 AND id NOT LIKE 'notice:%' AND created_at<?`} {
		cutoff := s.ms()
		if strings.Contains(stmt, "bot_updates") {
			cutoff = s.now().Add(-45 * 24 * time.Hour).UnixMilli()
		}
		if _, err := s.db.ExecContext(ctx, stmt, cutoff); err != nil {
			return err
		}
	}
	return nil
}
