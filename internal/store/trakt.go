package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// TraktSettings controls only imports. No Trakt history or lists are modified.
type TraktSettings struct {
	Enabled     bool    `json:"enabled"`
	Watchlist   bool    `json:"watchlist"`
	ListIDs     []int64 `json:"listIds"`
	PollMinutes int     `json:"pollMinutes"`
}

func (s *TraktSettings) Validate() error {
	if s.PollMinutes < 1 || s.PollMinutes > 10080 || len(s.ListIDs) > 100 {
		return fmt.Errorf("%w: invalid Trakt interval or list selection", ErrInvalid)
	}
	ids := []int64{}
	for _, id := range s.ListIDs {
		if id <= 0 {
			return fmt.Errorf("%w: invalid Trakt list ID", ErrInvalid)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	s.ListIDs = ids
	return nil
}

// TraktLink's credentials never leave the backend in JSON.
type TraktLink struct {
	TraktSettings
	UserID       int64  `json:"-"`
	ClientID     string `json:"-"`
	AccountID    string `json:"-"`
	Username     string `json:"username"`
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ExpiresAt    int64  `json:"-"`
	LastSyncAt   int64  `json:"lastSyncAt"`
	NextSyncAt   int64  `json:"nextSyncAt"`
	LastError    string `json:"lastError"`
	Requested    int    `json:"requested"`
	Skipped      int    `json:"skipped"`
	Failed       int    `json:"failed"`
}

func (s *Store) TraktClientID(ctx context.Context) (string, error) {
	var id string
	_, err := s.getSetting(ctx, "trakt.client_id", &id)
	return id, err
}
func (s *Store) SetTraktClientID(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if len(id) > 200 || strings.ContainsAny(id, " \r\n\t/?#") {
		return ErrInvalid
	}
	return s.setSetting(ctx, "trakt.client_id", id)
}

func (s *Store) TraktLink(ctx context.Context, userID int64) (TraktLink, error) {
	n := TraktLink{UserID: userID}
	var ids, access, refresh string
	err := s.db.QueryRowContext(ctx, `SELECT client_id,account_id,username,access_enc,refresh_enc,expires_at,enabled,watchlist,list_ids,poll_minutes,last_sync_at,next_sync_at,last_error,requested,skipped,failed FROM trakt_links WHERE user_id=?`, userID).Scan(&n.ClientID, &n.AccountID, &n.Username, &access, &refresh, &n.ExpiresAt, &n.Enabled, &n.Watchlist, &ids, &n.PollMinutes, &n.LastSyncAt, &n.NextSyncAt, &n.LastError, &n.Requested, &n.Skipped, &n.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	if err = json.Unmarshal([]byte(ids), &n.ListIDs); err != nil {
		return n, err
	}
	n.AccessToken, n.RefreshToken = s.decrypt(access), s.decrypt(refresh)
	return n, nil
}

// SaveTraktLink atomically replaces rotated tokens. Same-account reconnection keeps import settings.
func (s *Store) SaveTraktLink(ctx context.Context, n TraktLink) error {
	access, err := s.encrypt(n.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := s.encrypt(n.RefreshToken)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO trakt_links(user_id,client_id,account_id,username,access_enc,refresh_enc,expires_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET client_id=excluded.client_id,username=excluded.username,access_enc=excluded.access_enc,refresh_enc=excluded.refresh_enc,expires_at=excluded.expires_at,
 list_ids=CASE WHEN trakt_links.account_id=excluded.account_id THEN trakt_links.list_ids ELSE '[]' END,
 watchlist=CASE WHEN trakt_links.account_id=excluded.account_id THEN trakt_links.watchlist ELSE 0 END,
 account_id=excluded.account_id,last_error=''`, n.UserID, n.ClientID, n.AccountID, n.Username, access, refresh, n.ExpiresAt)
	return err
}

func (s *Store) SetTraktSettings(ctx context.Context, userID int64, n TraktSettings) error {
	if err := n.Validate(); err != nil {
		return err
	}
	ids, _ := json.Marshal(n.ListIDs)
	return affected(s.db.ExecContext(ctx, `UPDATE trakt_links SET enabled=?,watchlist=?,list_ids=?,poll_minutes=?,next_sync_at=? WHERE user_id=?`, n.Enabled, n.Watchlist, string(ids), n.PollMinutes, s.ms(), userID))
}
func (s *Store) DeleteTraktLink(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM trakt_oauth_states WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM trakt_links WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) TraktUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM trakt_links WHERE enabled=1 AND (watchlist=1 OR list_ids!='[]') ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) RecordTraktSync(ctx context.Context, userID int64, code string, requested, skipped, failed int) error {
	return affected(s.db.ExecContext(ctx, `UPDATE trakt_links SET last_sync_at=?,next_sync_at=?+poll_minutes*60000,last_error=?,requested=?,skipped=?,failed=? WHERE user_id=?`, s.ms(), s.ms(), code, requested, skipped, failed, userID))
}

func stateHash(state string) string {
	h := sha256.Sum256([]byte(state))
	return hex.EncodeToString(h[:])
}
func (s *Store) CreateTraktState(ctx context.Context, userID, sessionID int64, clientID, state, verifier string) error {
	enc, err := s.encrypt(verifier)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM trakt_oauth_states WHERE expires_at<=? OR user_id=?`, s.ms(), userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO trakt_oauth_states VALUES(?,?,?,?,?,?)`, stateHash(state), userID, sessionID, clientID, enc, s.now().Add(10*time.Minute).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeTraktState is session-bound, expiring and single-use, including across restarts.
func (s *Store) ConsumeTraktState(ctx context.Context, userID, sessionID int64, clientID, state string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `DELETE FROM trakt_oauth_states WHERE state_hash=? AND user_id=? AND session_id=? AND client_id=? AND expires_at>? RETURNING verifier_enc`, stateHash(state), userID, sessionID, clientID, s.ms()).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	verifier := s.decrypt(enc)
	if verifier == "" {
		return "", ErrInvalid
	}
	return verifier, nil
}
func (s *Store) AutomaticImportExcluded(ctx context.Context, kind string, tmdbID int) (bool, error) {
	if kind == "movie" {
		return s.MovieExcludedFromWatchlist(ctx, tmdbID)
	}
	var excluded bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automatic_series_exclusions WHERE tmdb_id=?)`, tmdbID).Scan(&excluded)
	return excluded, err
}

// TraktTaskActive prevents the periodic poll from resetting in-flight tasks and retries.
func (s *Store) TraktTaskActive(ctx context.Context, key string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_tasks WHERE dedupe_key=? AND status IN ('pending','running'))`, key).Scan(&active)
	return active, err
}
