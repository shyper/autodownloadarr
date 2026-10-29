package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/shyper/autodownloadarr/internal/mediaserver/pathmap"
	"github.com/shyper/autodownloadarr/internal/release"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
)

// UsenetServer is a configured news server. An empty Password on save
// keeps the stored one.
type UsenetServer struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	TLS                bool   `json:"tls"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	Username           string `json:"username"`
	Password           string `json:"password,omitempty"` // write-only
	HasPassword        bool   `json:"hasPassword"`
	Connections        int    `json:"connections"`
	Priority           int    `json:"priority"`
	Enabled            bool   `json:"enabled"`
	// FullCheck asks for every article of a release before downloading
	// it, instead of a sample.
	FullCheck bool `json:"fullCheck"`
}

// NNTPConfig converts the server for the NNTP pool.
func (u UsenetServer) NNTPConfig() nntp.Config {
	return nntp.Config{
		Name:               u.Name,
		Address:            net.JoinHostPort(u.Host, strconv.Itoa(u.Port)),
		TLS:                u.TLS,
		InsecureSkipVerify: u.InsecureSkipVerify,
		Username:           u.Username,
		Password:           u.Password,
		Connections:        u.Connections,
		Priority:           u.Priority,
	}
}

// Validate checks the required fields and fills defaults.
func (u *UsenetServer) Validate() error {
	u.Host = strings.TrimSpace(u.Host)
	if u.Host == "" {
		return fmt.Errorf("%w: host is required", ErrInvalid)
	}
	if u.Port == 0 {
		u.Port = 119
		if u.TLS {
			u.Port = 563
		}
	}
	if u.Port < 1 || u.Port > 65535 {
		return fmt.Errorf("%w: invalid port %d", ErrInvalid, u.Port)
	}
	if u.Connections <= 0 {
		u.Connections = 20
	}
	if u.Name == "" {
		u.Name = u.Host
	}
	return nil
}

// UsenetServers lists all servers with decrypted passwords.
func (s *Store) UsenetServers(ctx context.Context) ([]UsenetServer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, host, port, tls, insecure_skip_verify, username, password_enc, connections, priority, enabled, full_check
		FROM usenet_servers ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsenetServer
	for rows.Next() {
		var u UsenetServer
		var enc string
		if err := rows.Scan(&u.ID, &u.Name, &u.Host, &u.Port, &u.TLS, &u.InsecureSkipVerify, &u.Username, &enc, &u.Connections, &u.Priority, &u.Enabled,
			&u.FullCheck); err != nil {
			return nil, err
		}
		u.Password = s.decrypt(enc)
		u.HasPassword = u.Password != ""
		out = append(out, u)
	}
	return out, rows.Err()
}

// SaveUsenetServer inserts (ID 0) or updates a server and returns its ID.
func (s *Store) SaveUsenetServer(ctx context.Context, u UsenetServer) (int64, error) {
	if err := u.Validate(); err != nil {
		return 0, err
	}
	enc, err := s.encrypt(u.Password)
	if err != nil {
		return 0, err
	}
	now := s.ms()
	if u.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO usenet_servers (name, host, port, tls, insecure_skip_verify, username, password_enc, connections, priority, enabled,
				full_check, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.Name, u.Host, u.Port, u.TLS, u.InsecureSkipVerify, u.Username, enc, u.Connections, u.Priority, u.Enabled, u.FullCheck, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE usenet_servers SET name = ?, host = ?, port = ?, tls = ?, insecure_skip_verify = ?, username = ?,
			password_enc = CASE WHEN ? = '' THEN password_enc ELSE ? END,
			connections = ?, priority = ?, enabled = ?, full_check = ?, updated_at = ?
		WHERE id = ?`,
		u.Name, u.Host, u.Port, u.TLS, u.InsecureSkipVerify, u.Username, enc, enc, u.Connections, u.Priority, u.Enabled, u.FullCheck,
		now, u.ID)
	return u.ID, affected(res, err)
}

// DeleteUsenetServer removes a server.
func (s *Store) DeleteUsenetServer(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM usenet_servers WHERE id = ?`, id))
}

// Indexer is a configured Newznab indexer. An empty APIKey on save keeps
// the stored one.
type Indexer struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	APIKey          string `json:"apiKey,omitempty"` // write-only
	HasAPIKey       bool   `json:"hasApiKey"`
	MovieCategories []int  `json:"movieCategories"`
	TVCategories    []int  `json:"tvCategories"`
	Priority        int    `json:"priority"`
	Enabled         bool   `json:"enabled"`
}

// Validate checks the required fields and fills defaults.
func (i *Indexer) Validate() error {
	i.URL = strings.TrimSpace(i.URL)
	if !strings.HasPrefix(i.URL, "http://") && !strings.HasPrefix(i.URL, "https://") {
		return fmt.Errorf("%w: URL must start with http:// or https://", ErrInvalid)
	}
	if len(i.MovieCategories) == 0 {
		i.MovieCategories = []int{2000}
	}
	if len(i.TVCategories) == 0 {
		i.TVCategories = []int{5000}
	}
	if i.Name == "" {
		i.Name = i.URL
	}
	return nil
}

// Indexers lists all indexers with decrypted API keys.
func (s *Store) Indexers(ctx context.Context) ([]Indexer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, api_key_enc, movie_categories, tv_categories, priority, enabled FROM indexers ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Indexer
	for rows.Next() {
		var i Indexer
		var enc, cats, tvCats string
		if err := rows.Scan(&i.ID, &i.Name, &i.URL, &enc, &cats, &tvCats, &i.Priority, &i.Enabled); err != nil {
			return nil, err
		}
		i.APIKey = s.decrypt(enc)
		i.HasAPIKey = i.APIKey != ""
		_ = json.Unmarshal([]byte(cats), &i.MovieCategories)
		_ = json.Unmarshal([]byte(tvCats), &i.TVCategories)
		out = append(out, i)
	}
	return out, rows.Err()
}

// SaveIndexer inserts (ID 0) or updates an indexer and returns its ID.
func (s *Store) SaveIndexer(ctx context.Context, i Indexer) (int64, error) {
	if err := i.Validate(); err != nil {
		return 0, err
	}
	enc, err := s.encrypt(i.APIKey)
	if err != nil {
		return 0, err
	}
	cats, _ := json.Marshal(i.MovieCategories)
	tvCats, _ := json.Marshal(i.TVCategories)
	now := s.ms()
	if i.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO indexers (name, url, api_key_enc, movie_categories, tv_categories, priority, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			i.Name, i.URL, enc, string(cats), string(tvCats), i.Priority, i.Enabled, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE indexers SET name = ?, url = ?, api_key_enc = CASE WHEN ? = '' THEN api_key_enc ELSE ? END,
			movie_categories = ?, tv_categories = ?, priority = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		i.Name, i.URL, enc, enc, string(cats), string(tvCats), i.Priority, i.Enabled, now, i.ID)
	return i.ID, affected(res, err)
}

// DeleteIndexer removes an indexer.
func (s *Store) DeleteIndexer(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM indexers WHERE id = ?`, id))
}

// MediaServer is the configured Plex server. An empty Token on save keeps
// the stored one.
type MediaServer struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Token        string            `json:"token,omitempty"` // write-only
	HasToken     bool              `json:"hasToken"`
	MovieLibrary string            `json:"movieLibrary"`
	TVLibrary    string            `json:"tvLibrary"`
	Mappings     []pathmap.Mapping `json:"mappings"`
	Enabled      bool              `json:"enabled"`
}

// MediaServer returns the Plex server, if one is configured.
func (s *Store) MediaServer(ctx context.Context) (MediaServer, bool, error) {
	var m MediaServer
	var enc string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, url, token_enc, movie_library, tv_library, enabled FROM media_servers ORDER BY id LIMIT 1`,
	).Scan(&m.ID, &m.Name, &m.URL, &enc, &m.MovieLibrary, &m.TVLibrary, &m.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaServer{}, false, nil
	}
	if err != nil {
		return MediaServer{}, false, err
	}
	m.Token = s.decrypt(enc)
	m.HasToken = m.Token != ""
	rows, err := s.db.QueryContext(ctx, `SELECT local_path, remote_path FROM path_mappings WHERE media_server_id = ? ORDER BY id`, m.ID)
	if err != nil {
		return MediaServer{}, false, err
	}
	defer rows.Close()
	m.Mappings = []pathmap.Mapping{}
	for rows.Next() {
		var mp pathmap.Mapping
		if err := rows.Scan(&mp.Local, &mp.Remote); err != nil {
			return MediaServer{}, false, err
		}
		m.Mappings = append(m.Mappings, mp)
	}
	return m, true, rows.Err()
}

// SaveMediaServer stores the Plex server (there is only one) with its path
// mappings.
func (s *Store) SaveMediaServer(ctx context.Context, m MediaServer) (int64, error) {
	if !strings.HasPrefix(m.URL, "http://") && !strings.HasPrefix(m.URL, "https://") {
		return 0, fmt.Errorf("%w: URL must start with http:// or https://", ErrInvalid)
	}
	existing, found, err := s.MediaServer(ctx)
	if err != nil {
		return 0, err
	}
	token := m.Token
	if token == "" {
		token = existing.Token
	}
	enc, err := s.encrypt(token)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.ms()
	id := existing.ID
	if !found {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO media_servers (kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at)
			VALUES ('plex', ?, ?, ?, ?, ?, ?, ?, ?)`, m.Name, m.URL, enc, m.MovieLibrary, m.TVLibrary, m.Enabled, now, now)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE media_servers SET name = ?, url = ?, token_enc = ?, movie_library = ?, tv_library = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		m.Name, m.URL, enc, m.MovieLibrary, m.TVLibrary, m.Enabled, now, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM path_mappings WHERE media_server_id = ?`, id); err != nil {
		return 0, err
	}
	for _, mp := range m.Mappings {
		if strings.TrimSpace(mp.Local) == "" || strings.TrimSpace(mp.Remote) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO path_mappings (media_server_id, local_path, remote_path) VALUES (?, ?, ?)`,
			id, strings.TrimSpace(mp.Local), strings.TrimSpace(mp.Remote)); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// DeleteMediaServer removes the Plex configuration.
func (s *Store) DeleteMediaServer(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM media_servers`)
	return err
}

// Profile is a stored quality profile.
type Profile struct {
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Config  release.Profile `json:"config"`
	Default bool            `json:"default"`
}

// Profiles lists the quality profiles, creating the default one first.
func (s *Store) Profiles(ctx context.Context) ([]Profile, error) {
	if _, err := s.DefaultProfile(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, config, is_default FROM quality_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		var p Profile
		var cfg string
		if err := rows.Scan(&p.ID, &p.Name, &cfg, &p.Default); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cfg), &p.Config); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Profile returns one profile.
func (s *Store) Profile(ctx context.Context, id int64) (Profile, error) {
	var p Profile
	var cfg string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, config, is_default FROM quality_profiles WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &cfg, &p.Default)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	return p, json.Unmarshal([]byte(cfg), &p.Config)
}

// DefaultProfile returns the default profile, creating it if needed.
func (s *Store) DefaultProfile(ctx context.Context) (Profile, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM quality_profiles WHERE is_default = 1 ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		cfg, _ := json.Marshal(release.DefaultProfile())
		now := s.ms()
		res, err := s.db.ExecContext(ctx, `INSERT INTO quality_profiles (name, config, is_default, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
			"Standard", string(cfg), now, now)
		if err != nil {
			return Profile{}, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return Profile{}, err
		}
	} else if err != nil {
		return Profile{}, err
	}
	return s.Profile(ctx, id)
}

// SaveProfile updates a profile's name and configuration.
func (s *Store) SaveProfile(ctx context.Context, p Profile) error {
	if err := p.Config.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	cfg, err := json.Marshal(p.Config)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE quality_profiles SET name = ?, config = ?, updated_at = ? WHERE id = ?`,
		p.Name, string(cfg), s.ms(), p.ID))
}

// ArchivePassword is an entry of the password list.
type ArchivePassword struct {
	ID       int64  `json:"id"`
	Password string `json:"password"`
	Hits     int    `json:"hits"`
}

// ArchivePasswords lists the passwords, most successful first.
func (s *Store) ArchivePasswords(ctx context.Context) ([]ArchivePassword, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, password_enc, hits FROM archive_passwords ORDER BY hits DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchivePassword
	for rows.Next() {
		var p ArchivePassword
		var enc string
		if err := rows.Scan(&p.ID, &enc, &p.Hits); err != nil {
			return nil, err
		}
		if p.Password = s.decrypt(enc); p.Password != "" {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// AddArchivePassword adds a password to the list.
func (s *Store) AddArchivePassword(ctx context.Context, password string) (int64, error) {
	if password = strings.TrimSpace(password); password == "" {
		return 0, fmt.Errorf("%w: password is empty", ErrInvalid)
	}
	enc, err := s.encrypt(password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO archive_passwords (password_enc, created_at) VALUES (?, ?)`, enc, s.ms())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteArchivePassword removes a password.
func (s *Store) DeleteArchivePassword(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM archive_passwords WHERE id = ?`, id))
}

// ArchivePasswordHit counts a successful use of a listed password.
func (s *Store) ArchivePasswordHit(ctx context.Context, password string) error {
	list, err := s.ArchivePasswords(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.Password == password {
			_, err := s.db.ExecContext(ctx, `UPDATE archive_passwords SET hits = hits + 1, last_hit_at = ? WHERE id = ?`, s.ms(), p.ID)
			return err
		}
	}
	return nil
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
