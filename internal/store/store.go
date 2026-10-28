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
	"time"

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
	keyGeneral    = "general"
	keyTMDB       = "tmdb"
	keyOnboarding = "onboarding"
	keyPlexClient = "plex.client_id"
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
	// UnwatchedExpiryDays deletes movies nobody watched (0: never).
	UnwatchedExpiryDays int `json:"unwatchedExpiryDays"`
	// MaxSources is how many releases are tried before giving up.
	MaxSources int `json:"maxSources"`
	// ParallelDownloads is the number of movies downloaded at once.
	ParallelDownloads int `json:"parallelDownloads"`
	// WatchPollMinutes is how often the media server is asked for the
	// watch state.
	WatchPollMinutes int `json:"watchPollMinutes"`
	// Language for metadata, e.g. "de-DE".
	Language string `json:"language"`
	// MaxDownloadMBps limits the download speed in MiB/s (0: unlimited).
	MaxDownloadMBps float64 `json:"maxDownloadMBps"`
}

// DefaultGeneral returns the defaults.
func DefaultGeneral() General {
	return General{
		DeleteAfterHours:    48,
		UnwatchedExpiryDays: 14,
		MaxSources:          5,
		SeriesWindow:        3,
		SeriesTrigger:       "progress",
		SeriesTriggerValue:  10,
		ParallelDownloads:   1,
		WatchPollMinutes:    2,
		Language:            "de-DE",
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
