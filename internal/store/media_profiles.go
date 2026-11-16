package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrConflict is returned when a change collides with existing data, e.g.
// assigning a second media profile to a user.
var ErrConflict = errors.New("conflict")

// MediaProfile is a profile of the media server: a Plex Home member or a
// Jellyfin/Emby user. Assigned to an AutoDownloadarr user, its watchlist is
// imported for that user and the user's requests use its watch state.
type MediaProfile struct {
	ID         int64  `json:"id"`
	ExternalID string `json:"externalId"`
	Name       string `json:"name"`
	Thumb      string `json:"thumb"`
	// Owner marks the server owner (Plex) or the administrator whose watch
	// state is used by default (Jellyfin/Emby).
	Owner     bool   `json:"owner"`
	Protected bool   `json:"protected"` // Plex profile with a PIN
	UserID    int64  `json:"userId"`    // 0 when not assigned
	UserName  string `json:"userName"`
	Watchlist bool   `json:"watchlist"`
	LastError string `json:"lastError"`
	HasToken  bool   `json:"hasToken"`
	// ServerToken is the profile's access token for the Plex server and
	// AccountToken its plex.tv token (watchlist). Both never leave the backend.
	ServerToken  string `json:"-"`
	AccountToken string `json:"-"`
}

// AccountKey identifies the profile's watch state in WatchBase.Account.
func (p MediaProfile) AccountKey() string { return ProfileAccountKey(p.ID) }

// ProfileAccountKey is the WatchBase.Account value of a profile.
func ProfileAccountKey(id int64) string { return "profile:" + strconv.FormatInt(id, 10) }

// ProfileIDFromAccountKey returns the profile ID of an account key, or 0
// for the default account.
func ProfileIDFromAccountKey(key string) int64 {
	id, err := strconv.ParseInt(strings.TrimPrefix(key, "profile:"), 10, 64)
	if err != nil || !strings.HasPrefix(key, "profile:") {
		return 0
	}
	return id
}

// MediaProfileInfo is what a media server reports about one profile.
type MediaProfileInfo struct {
	ExternalID string
	Name       string
	Thumb      string
	Owner      bool
	Protected  bool
}

const mediaProfileColumns = `p.id, p.external_id, p.name, p.thumb, p.owner, p.protected, COALESCE(p.user_id, 0),
	COALESCE(u.username, ''), p.watchlist, p.last_error, p.server_token_enc, p.account_token_enc`

const mediaProfileFrom = ` FROM media_profiles p LEFT JOIN users u ON u.id = p.user_id
	WHERE p.media_server_id = (SELECT id FROM media_servers ORDER BY id LIMIT 1)`

func (s *Store) scanMediaProfile(row interface{ Scan(...any) error }) (MediaProfile, error) {
	var p MediaProfile
	var server, account string
	err := row.Scan(&p.ID, &p.ExternalID, &p.Name, &p.Thumb, &p.Owner, &p.Protected, &p.UserID, &p.UserName,
		&p.Watchlist, &p.LastError, &server, &account)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaProfile{}, ErrNotFound
	}
	if err != nil {
		return MediaProfile{}, err
	}
	p.ServerToken, p.AccountToken = s.decrypt(server), s.decrypt(account)
	p.HasToken = p.ServerToken != "" || p.AccountToken != ""
	return p, nil
}

// MediaProfiles lists the profiles of the configured media server, the owner
// first.
func (s *Store) MediaProfiles(ctx context.Context) ([]MediaProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mediaProfileColumns+mediaProfileFrom+` ORDER BY p.owner DESC, p.name COLLATE NOCASE, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaProfile{}
	for rows.Next() {
		p, err := s.scanMediaProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MediaProfile returns one profile of the configured media server.
func (s *Store) MediaProfile(ctx context.Context, id int64) (MediaProfile, error) {
	return s.scanMediaProfile(s.db.QueryRowContext(ctx, `SELECT `+mediaProfileColumns+mediaProfileFrom+` AND p.id = ?`, id))
}

// MediaProfileForUser returns the profile assigned to a user.
func (s *Store) MediaProfileForUser(ctx context.Context, userID int64) (MediaProfile, error) {
	if userID <= 0 {
		return MediaProfile{}, ErrNotFound
	}
	return s.scanMediaProfile(s.db.QueryRowContext(ctx, `SELECT `+mediaProfileColumns+mediaProfileFrom+` AND p.user_id = ?`, userID))
}

// SyncMediaProfiles stores the profiles the media server reported. Known
// profiles keep their assignment and tokens; profiles that disappeared are
// removed.
func (s *Store) SyncMediaProfiles(ctx context.Context, infos []MediaProfileInfo) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var serverID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM media_servers ORDER BY id LIMIT 1`).Scan(&serverID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no media server configured", ErrInvalid)
		}
		return err
	}
	now := s.ms()
	keep := make([]any, 0, len(infos)+1)
	keep = append(keep, serverID)
	for _, in := range infos {
		if strings.TrimSpace(in.ExternalID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_profiles (media_server_id, external_id, name, thumb, owner, protected, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (media_server_id, external_id) DO UPDATE SET
				name = excluded.name, thumb = excluded.thumb, owner = excluded.owner, protected = excluded.protected,
				updated_at = excluded.updated_at`,
			serverID, in.ExternalID, in.Name, in.Thumb, in.Owner, in.Protected, now, now); err != nil {
			return err
		}
		keep = append(keep, in.ExternalID)
	}
	q := `DELETE FROM media_profiles WHERE media_server_id = ?`
	if len(keep) > 1 {
		q += ` AND external_id NOT IN (?` + strings.Repeat(`, ?`, len(keep)-2) + `)`
	}
	if _, err := tx.ExecContext(ctx, q, keep...); err != nil {
		return err
	}
	return tx.Commit()
}

// AssignMediaProfile assigns a profile to a user (0 removes the assignment)
// and switches its watchlist import. A user can have only one profile.
func (s *Store) AssignMediaProfile(ctx context.Context, id, userID int64, watchlist bool) error {
	var user any
	if userID > 0 {
		user = userID
		var other int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM media_profiles WHERE user_id = ? AND id != ?`, userID, id).Scan(&other)
		if err == nil {
			return fmt.Errorf("%w: the user already has a media profile", ErrConflict)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return affected(s.db.ExecContext(ctx, `UPDATE media_profiles SET user_id = ?, watchlist = ?, updated_at = ? WHERE id = ?`,
		user, watchlist, s.ms(), id))
}

// SetMediaProfileTokens stores the Plex tokens of a profile and clears its
// error.
func (s *Store) SetMediaProfileTokens(ctx context.Context, id int64, serverToken, accountToken string) error {
	server, err := s.encrypt(serverToken)
	if err != nil {
		return err
	}
	account, err := s.encrypt(accountToken)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE media_profiles SET server_token_enc = ?, account_token_enc = ?, last_error = '', updated_at = ? WHERE id = ?`,
		server, account, s.ms(), id))
}

// SetMediaProfileError records the last error of a profile ("" clears it).
func (s *Store) SetMediaProfileError(ctx context.Context, id int64, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := s.db.ExecContext(ctx, `UPDATE media_profiles SET last_error = ? WHERE id = ? AND last_error != ?`, msg, id, msg)
	return err
}
