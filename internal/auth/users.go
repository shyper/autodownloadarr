package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Errors returned by the user management methods.
var (
	ErrUserExists  = errors.New("username is already taken")
	ErrInvalidRole = errors.New("role must be admin or user")
	ErrLastAdmin   = errors.New("the last administrator cannot be removed")
)

// ValidRole reports whether role is one of the known roles.
func ValidRole(role Role) bool { return role == RoleAdmin || role == RoleUser }

// Users lists all accounts, sorted by name.
func (s *Service) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, role, created_at, last_login_at FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// User returns one account.
func (s *Service) User(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, role, created_at, last_login_at FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

// CreateUser adds an account. Only administrators should be allowed to call
// this; the API enforces that.
func (s *Service) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	if !usernamePattern.MatchString(username) {
		return User{}, ErrInvalidUsername
	}
	if !ValidRole(role) {
		return User{}, ErrInvalidRole
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password, s.params)
	if err != nil {
		return User{}, err
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, hash, role, now.UnixMilli(), now.UnixMilli())
	if isUniqueViolation(err) {
		return User{}, ErrUserExists
	}
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Role: role, CreatedAt: time.UnixMilli(now.UnixMilli())}, nil
}

// UpdateRole changes the role of an account. The last administrator cannot
// be demoted.
func (s *Service) UpdateRole(ctx context.Context, id int64, role Role) (User, error) {
	if !ValidRole(role) {
		return User{}, ErrInvalidRole
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var current Role
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	if current == RoleAdmin && role != RoleAdmin {
		n, err := countAdmins(ctx, tx, id)
		if err != nil {
			return User{}, err
		}
		if n == 0 {
			return User{}, ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, s.now().UnixMilli(), id); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}

// DeleteUser removes an account. Its sessions and requests are removed or
// detached; the last administrator cannot be deleted.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var role Role
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if role == RoleAdmin {
		n, err := countAdmins(ctx, tx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserPassword replaces the password of an account and ends all of its
// sessions. Administrators use it for password resets.
func (s *Service) SetUserPassword(ctx context.Context, id int64, next string) error {
	if err := s.setPassword(ctx, `id = ?`, id, next, 0); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	return nil
}

// countAdmins counts the other administrators so that the final one cannot
// be removed or demoted.
func countAdmins(ctx context.Context, tx *sql.Tx, exceptID int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ? AND id != ?`, RoleAdmin, exceptID).Scan(&n)
	return n, err
}

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (User, error) {
	var (
		u         User
		createdAt int64
		lastLogin sql.NullInt64
	)
	if err := row.Scan(&u.ID, &u.Username, &u.Role, &createdAt, &lastLogin); err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(createdAt)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		u.LastLoginAt = &t
	}
	return u, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
