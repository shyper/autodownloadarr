// Package auth manages user accounts, password hashing and login sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Role controls what a user may do.
type Role string

// Roles.
const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Password length limits. The upper bound keeps hashing cost predictable.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 1024
)

// Errors returned by Service.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrSetupCompleted     = errors.New("initial setup has already been completed")
	ErrInvalidUsername    = errors.New("username must be 1-64 characters: letters, digits, '.', '_' or '-'")
	ErrWeakPassword       = fmt.Errorf("password must be %d to %d characters long", MinPasswordLength, MaxPasswordLength)
	ErrInvalidSession     = errors.New("invalid or expired session")
	ErrUserNotFound       = errors.New("user not found")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// sessionTouchInterval limits how often activity extends a session, so that
// not every request writes to the database.
const sessionTouchInterval = time.Hour

// User is an account as exposed to the rest of the application.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
	// QuotaRequests and QuotaGB limit what the user may request (0: no
	// limit). The API enforces them when a new request is added.
	QuotaRequests int        `json:"quotaRequests"`
	QuotaGB       int        `json:"quotaGb"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
}

// Session is a login session. The token itself is never stored.
type Session struct {
	ID        int64
	UserID    int64
	ExpiresAt time.Time
}

// Service implements account and session operations.
type Service struct {
	db         *sql.DB
	now        func() time.Time
	params     Argon2Params
	sessionTTL time.Duration
	dummyHash  string
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the time source (for tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithArgon2Params overrides the hashing cost (tests use cheap parameters).
func WithArgon2Params(p Argon2Params) Option { return func(s *Service) { s.params = p } }

// WithSessionTTL sets how long an idle session stays valid.
func WithSessionTTL(d time.Duration) Option { return func(s *Service) { s.sessionTTL = d } }

// NewService creates a Service.
func NewService(db *sql.DB, opts ...Option) (*Service, error) {
	s := &Service{db: db, now: time.Now, params: DefaultArgon2Params, sessionTTL: 30 * 24 * time.Hour}
	for _, opt := range opts {
		opt(s)
	}
	// Verified against when a username does not exist, so that response time
	// does not reveal which usernames are taken.
	h, err := HashPassword("timing-equaliser", s.params)
	if err != nil {
		return nil, err
	}
	s.dummyHash = h
	return s, nil
}

// SetupRequired reports whether no account exists yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// CreateInitialAdmin creates the first account. It fails with
// ErrSetupCompleted once any account exists.
func (s *Service) CreateInitialAdmin(ctx context.Context, username, password string) (User, error) {
	if !usernamePattern.MatchString(username) {
		return User{}, ErrInvalidUsername
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password, s.params)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupCompleted
	}
	now := s.now()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, hash, RoleAdmin, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Role: RoleAdmin, CreatedAt: time.UnixMilli(now.UnixMilli())}, nil
}

// Authenticate checks a username and password and records the login.
func (s *Service) Authenticate(ctx context.Context, username, password string) (User, error) {
	if len(password) > MaxPasswordLength {
		return User{}, ErrInvalidCredentials
	}
	var (
		u         User
		hash      string
		createdAt int64
		lastLogin sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, quota_requests, quota_gb, password_hash, created_at, last_login_at FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.Role, &u.QuotaRequests, &u.QuotaGB, &hash, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = VerifyPassword(password, s.dummyHash)
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return User{}, fmt.Errorf("verify password of %s: %w", u.Username, err)
	}
	if !ok {
		return User{}, ErrInvalidCredentials
	}

	now := time.UnixMilli(s.now().UnixMilli())
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, now.UnixMilli(), u.ID); err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(createdAt)
	u.LastLoginAt = &now
	return u, nil
}

// CreateSession starts a session and returns its secret token.
func (s *Service) CreateSession(ctx context.Context, userID int64, userAgent, ip string) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	sess := Session{UserID: userID, ExpiresAt: time.UnixMilli(now.Add(s.sessionTTL).UnixMilli())}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), userID, now.UnixMilli(), sess.ExpiresAt.UnixMilli(), now.UnixMilli(), userAgent, ip)
	if err != nil {
		return "", Session{}, err
	}
	if sess.ID, err = res.LastInsertId(); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// ValidateSession resolves a token to its user. Activity extends the session.
func (s *Service) ValidateSession(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrInvalidSession
	}
	var (
		u                              User
		sess                           Session
		expiresAt, lastSeen, createdAt int64
		lastLogin                      sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.expires_at, s.last_seen_at, u.id, u.username, u.role, u.quota_requests, u.quota_gb, u.created_at, u.last_login_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`, hashToken(token),
	).Scan(&sess.ID, &expiresAt, &lastSeen, &u.ID, &u.Username, &u.Role, &u.QuotaRequests, &u.QuotaGB, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrInvalidSession
	}
	if err != nil {
		return User{}, Session{}, err
	}

	now := s.now()
	if now.UnixMilli() >= expiresAt {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
		return User{}, Session{}, ErrInvalidSession
	}
	if now.Sub(time.UnixMilli(lastSeen)) >= sessionTouchInterval {
		expiresAt = now.Add(s.sessionTTL).UnixMilli()
		if _, err := s.db.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
			now.UnixMilli(), expiresAt, sess.ID); err != nil {
			return User{}, Session{}, err
		}
	}

	sess.UserID = u.ID
	sess.ExpiresAt = time.UnixMilli(expiresAt)
	u.CreatedAt = time.UnixMilli(createdAt)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		u.LastLoginAt = &t
	}
	return u, sess, nil
}

// DeleteSession ends the session belonging to token.
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// ChangePassword sets a new password after verifying the current one. All
// other sessions of the user are ended; keepSessionID stays signed in.
func (s *Service) ChangePassword(ctx context.Context, userID, keepSessionID int64, current, next string) error {
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if len(current) > MaxPasswordLength {
		return ErrInvalidCredentials
	}
	ok, err := VerifyPassword(current, hash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return s.setPassword(ctx, `id = ?`, userID, next, keepSessionID)
}

// ResetPassword sets a new password without knowing the old one (CLI
// recovery) and ends all sessions of the user.
func (s *Service) ResetPassword(ctx context.Context, username, next string) error {
	return s.setPassword(ctx, `username = ?`, username, next, 0)
}

func (s *Service) setPassword(ctx context.Context, where string, arg any, password string, keepSessionID int64) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password, s.params)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var userID int64
	err = tx.QueryRowContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE `+where+` RETURNING id`,
		hash, s.now().UnixMilli(), arg,
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepSessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_grants WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_links WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// PurgeExpiredSessions deletes sessions that can no longer be used.
func (s *Service) PurgeExpiredSessions(ctx context.Context) error {
	for _, stmt := range []string{`DELETE FROM sessions WHERE expires_at <= ?`, `DELETE FROM oauth_codes WHERE expires_at <= ?`, `DELETE FROM oauth_tokens WHERE expires_at <= ?`} {
		if _, err := s.db.ExecContext(ctx, stmt, s.now().UnixMilli()); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_grants WHERE revoked=1 OR (NOT EXISTS (SELECT 1 FROM oauth_codes c WHERE c.grant_id=oauth_grants.id) AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id=oauth_grants.id))`)
	return err
}

func checkPassword(p string) error {
	if len(p) < MinPasswordLength || len(p) > MaxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
