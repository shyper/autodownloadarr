// Package store persists configuration and movies. Secrets (passwords, API
// keys, tokens) are encrypted on write and decrypted on read; they never
// leave this package in JSON (see the json:"-" tags).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/secrets"
)

// Errors returned by Store.
var (
	// ErrNotFound is returned when a record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid wraps validation errors of user input.
	ErrInvalid = errors.New("invalid input")
)

// Store gives access to all tables except users and sessions (see auth).
type Store struct {
	db  *sql.DB
	box *secrets.Box
	now func() time.Time
}

// New returns a store.
func New(db *sql.DB, box *secrets.Box) *Store {
	return &Store{db: db, box: box, now: time.Now}
}

func (s *Store) ms() int64 { return s.now().UnixMilli() }

func (s *Store) encrypt(v string) (string, error) { return s.box.Encrypt(v) }

func (s *Store) decrypt(v string) string {
	plain, err := s.box.Decrypt(v)
	if err != nil {
		return "" // unreadable after a key change; the user has to re-enter it
	}
	return plain
}

// Setting keys.
const (
	keyGeneral       = "general"
	keyTMDB          = "tmdb"
	keyOnboarding    = "onboarding"
	keyPlexClient    = "plex.client_id"
	keyNotifications = "notifications"
)

// General holds the application settings.
type General struct {
	// MoviesPath and SeriesPath are the folders (inside the container) for
	// temporary movies and series.
	MoviesPath string `json:"moviesPath"`
	SeriesPath string `json:"seriesPath"`
	// Defaults for new series: episodes kept ready, and when a watched
	// episode is deleted (DeleteTrigger "progress": TriggerValue percent of
	// the next episode watched; "minutes": minutes after the next one
	// started; "watched": when marked watched).
	SeriesWindow       int    `json:"seriesWindow"`
	SeriesTrigger      string `json:"seriesTrigger"`
	SeriesTriggerValue int    `json:"seriesTriggerValue"`
	// DeleteAfterHours is how long a watched movie is kept.
	DeleteAfterHours int `json:"deleteAfterHours"`
	// StartedExpiryDays deletes started movies after first playback (0: never).
	StartedExpiryDays int `json:"startedExpiryDays"`
	// UnwatchedExpiryDays deletes movies nobody watched and the files of
	// series nobody watches any more (0: never).
	UnwatchedExpiryDays int `json:"unwatchedExpiryDays"`
	// MaxSources is how many releases are tried before giving up.
	MaxSources int `json:"maxSources"`
	// ParallelDownloads is the total number of movies and episodes downloaded at once.
	ParallelDownloads int `json:"parallelDownloads"`
	// SaveNZBs archives fetched NZB documents, including failed download attempts.
	SaveNZBs bool `json:"saveNZBs"`
	// WatchPollMinutes is how often the media server is asked for the
	// watch state.
	WatchPollMinutes int `json:"watchPollMinutes"`
	// Watchlist imports the Plex account's watchlist (movies and series).
	Watchlist bool `json:"watchlist"`
	// WatchlistPollMinutes is how often the watchlist is checked.
	WatchlistPollMinutes int `json:"watchlistPollMinutes"`
	// Language for metadata, e.g. "de-DE".
	Language string `json:"language"`
	// MaxDownloadMBps limits the download speed in MiB/s (0: unlimited).
	MaxDownloadMBps float64 `json:"maxDownloadMBps"`
	// MaxLibraryGB caps the size of the temporary library (0: unlimited).
	// Downloads wait while the cap is reached.
	MaxLibraryGB float64 `json:"maxLibraryGB"`
	// MinFreeGB keeps at least this much space free on the download folder's
	// file system (0: off).
	MinFreeGB float64 `json:"minFreeGB"`
}

// DefaultGeneral returns the defaults.
func DefaultGeneral() General {
	return General{
		DeleteAfterHours:     48,
		UnwatchedExpiryDays:  14,
		StartedExpiryDays:    2,
		MaxSources:           5,
		SeriesWindow:         3,
		SeriesTrigger:        "progress",
		SeriesTriggerValue:   10,
		ParallelDownloads:    1,
		WatchPollMinutes:     2,
		WatchlistPollMinutes: 30,
		Language:             "de-DE",
	}
}

// General returns the settings, filled with defaults.
func (s *Store) General(ctx context.Context) (General, error) {
	g := DefaultGeneral()
	_, err := s.getSetting(ctx, keyGeneral, &g)
	return g, err
}

// SetGeneral saves the settings.
func (s *Store) SetGeneral(ctx context.Context, g General) error {
	return s.setSetting(ctx, keyGeneral, g)
}

// TMDBKey returns the decrypted TMDB API key.
func (s *Store) TMDBKey(ctx context.Context) (string, error) {
	var enc string
	if _, err := s.getSetting(ctx, keyTMDB, &enc); err != nil {
		return "", err
	}
	return s.decrypt(enc), nil
}

// SetTMDBKey stores the TMDB API key.
func (s *Store) SetTMDBKey(ctx context.Context, key string) error {
	enc, err := s.encrypt(key)
	if err != nil {
		return err
	}
	return s.setSetting(ctx, keyTMDB, enc)
}

// Notification is the configured notification channel.
type Notification struct {
	UseBotToken bool   `json:"useBotToken"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"` // webhook, discord, ntfy, telegram or pushover
	URL         string `json:"url"`
	ChatID      string `json:"chatId"`
	// Token is the Telegram bot token or Pushover application token. It is write-only: handlers clear it
	// before sending the configuration to the UI.
	Token      string `json:"token,omitempty"`
	HasToken   bool   `json:"hasToken"`
	UserKey    string `json:"userKey,omitempty"`
	HasUserKey bool   `json:"hasUserKey"`
	Device     string `json:"device"`
	// DisabledEvents are the notification kinds (events.NotificationEvents)
	// that are not sent. Kinds not listed are sent, so new kinds start on.
	DisabledEvents []string `json:"disabledEvents"`
}

// Wants reports whether a notification of this kind is sent.
func (n Notification) Wants(event string) bool {
	return !slices.Contains(n.DisabledEvents, event)
}

// notificationConfig is how the channel is stored: the token is encrypted
// next to the other fields.
type notificationConfig struct {
	UseBotToken bool   `json:"useBotToken"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	URL         string `json:"url"`
	ChatID      string `json:"chatId"`
	TokenEnc    string `json:"tokenEnc,omitempty"`
	UserKeyEnc  string `json:"userKeyEnc,omitempty"`
	Device      string `json:"device,omitempty"`
	// DisabledEvents is an opt-out list, so channels stored before it existed
	// keep every kind.
	DisabledEvents []string `json:"disabledEvents,omitempty"`
}

// Validate checks the fields of a notification channel. An empty token is
// allowed here; SetNotification keeps the stored one in that case.
func (n *Notification) Validate() error {
	n.Provider = strings.TrimSpace(n.Provider)
	n.URL = strings.TrimSpace(n.URL)
	n.ChatID = strings.TrimSpace(n.ChatID)
	n.Token = strings.TrimSpace(n.Token)
	n.UserKey = strings.TrimSpace(n.UserKey)
	n.Device = strings.TrimSpace(n.Device)
	disabled := []string{}
	for _, e := range n.DisabledEvents {
		if !slices.Contains(events.NotificationEvents, e) {
			return fmt.Errorf("%w: unknown notification event %q", ErrInvalid, e)
		}
		if !slices.Contains(disabled, e) {
			disabled = append(disabled, e)
		}
	}
	n.DisabledEvents = disabled
	switch n.Provider {
	case "webhook", "discord", "ntfy":
		if !strings.HasPrefix(n.URL, "http://") && !strings.HasPrefix(n.URL, "https://") {
			return fmt.Errorf("%w: URL must start with http:// or https://", ErrInvalid)
		}
	case "pushover":
		if len(n.Device) > 25 || strings.IndexFunc(n.Device, func(r rune) bool {
			return !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-", r)
		}) >= 0 {
			return fmt.Errorf("%w: invalid Pushover device name", ErrInvalid)
		}
	case "telegram":
		if n.ChatID == "" {
			return fmt.Errorf("%w: chat ID is required", ErrInvalid)
		}
	case "":
		return fmt.Errorf("%w: provider is required", ErrInvalid)
	default:
		return fmt.Errorf("%w: unknown provider %q", ErrInvalid, n.Provider)
	}
	return nil
}

// Notification returns the notification channel. Nothing is configured yet
// when no provider is set.
func (s *Store) Notification(ctx context.Context) (Notification, error) {
	var c notificationConfig
	if _, err := s.getSetting(ctx, keyNotifications, &c); err != nil {
		return Notification{}, err
	}
	n := Notification{Enabled: c.Enabled, Provider: c.Provider, URL: c.URL, ChatID: c.ChatID, UseBotToken: c.UseBotToken, DisabledEvents: c.DisabledEvents, UserKey: s.decrypt(c.UserKeyEnc), Device: c.Device}
	if n.DisabledEvents == nil {
		n.DisabledEvents = []string{}
	}
	n.Token = s.decrypt(c.TokenEnc)
	if n.Provider == "telegram" && n.UseBotToken {
		bots, err := s.BotConfig(ctx)
		if err != nil {
			return Notification{}, err
		}
		n.Token = bots.TelegramToken
	}
	n.HasToken = n.Token != ""
	n.HasUserKey = n.UserKey != ""
	return n, nil
}

// SetNotification stores the channel. An empty Token keeps the stored one.
func (s *Store) SetNotification(ctx context.Context, n Notification) error {
	cur, err := s.Notification(ctx)
	if err != nil {
		return err
	}
	if err := n.Validate(); err != nil {
		return err
	}
	token := n.Token
	if token == "" && n.Provider == cur.Provider && !cur.UseBotToken {
		token = cur.Token
	}
	userKey := n.UserKey
	if userKey == "" && n.Provider == cur.Provider {
		userKey = cur.UserKey
	}
	if n.Provider == "pushover" && (token == "" || userKey == "") {
		return fmt.Errorf("%w: Pushover application token and user/group key are required", ErrInvalid)
	}
	if n.Provider != "pushover" {
		userKey = ""
		n.Device = ""
	}
	if n.Provider != "telegram" {
		n.UseBotToken = false
	}
	if n.UseBotToken {
		bots, err := s.BotConfig(ctx)
		if err != nil {
			return err
		}
		if bots.TelegramToken == "" {
			return fmt.Errorf("%w: configure the Telegram bot token first", ErrInvalid)
		}
		token = ""
	}
	if n.Provider == "telegram" && !n.UseBotToken && token == "" {
		return fmt.Errorf("%w: bot token is required", ErrInvalid)
	}
	c := notificationConfig{Enabled: n.Enabled, Provider: n.Provider, URL: n.URL, ChatID: n.ChatID, UseBotToken: n.UseBotToken, DisabledEvents: n.DisabledEvents, Device: n.Device}
	if token != "" {
		if c.TokenEnc, err = s.encrypt(token); err != nil {
			return err
		}
	}
	if userKey != "" {
		if c.UserKeyEnc, err = s.encrypt(userKey); err != nil {
			return err
		}
	}
	return s.setSetting(ctx, keyNotifications, c)
}

// Onboarding is the progress of the setup wizard.
type Onboarding struct {
	Done     []string `json:"done"`     // completed or skipped steps
	Finished bool     `json:"finished"` // wizard closed
}

// Onboarding returns the wizard progress.
func (s *Store) Onboarding(ctx context.Context) (Onboarding, error) {
	o := Onboarding{Done: []string{}}
	_, err := s.getSetting(ctx, keyOnboarding, &o)
	return o, err
}

// SetOnboarding saves the wizard progress.
func (s *Store) SetOnboarding(ctx context.Context, o Onboarding) error {
	return s.setSetting(ctx, keyOnboarding, o)
}

// PlexClientID returns the stable client identifier of this installation,
// creating it on first use.
func (s *Store) PlexClientID(ctx context.Context) (string, error) {
	var id string
	found, err := s.getSetting(ctx, keyPlexClient, &id)
	if err != nil || found {
		return id, err
	}
	id = fmt.Sprintf("autodownloadarr-%d", s.now().UnixNano())
	return id, s.setSetting(ctx, keyPlexClient, id)
}

func (s *Store) getSetting(ctx context.Context, key string, dst any) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dst)
}

func (s *Store) setSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(raw), s.ms())
	return err
}

// nullTime converts a nullable millisecond column.
func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

func timeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}
