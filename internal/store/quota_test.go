package store

import (
	"context"
	"errors"
	"testing"
)

func TestUserQuotas(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES ('alice', 'x', 'user', 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()

	if err := s.CheckUserQuota(ctx, userID); err != nil {
		t.Fatalf("no quota: %v", err)
	}
	m, err := s.CreateMovie(ctx, Movie{TMDBID: 1, Title: "Dune", State: MovieSearching}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET quota_requests = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckUserQuota(ctx, userID); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("request quota: got %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE users SET quota_requests = 0, quota_gb = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.TrackFile(ctx, m.ID, "/movies/Dune.mkv", 2<<30); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckUserQuota(ctx, userID); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("size quota: got %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET quota_gb = 0 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckUserQuota(ctx, userID); err != nil {
		t.Errorf("limits disabled: %v", err)
	}
	// A deleted movie frees its slot again.
	m.State = MovieDeleted
	if err := s.UpdateMovie(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET quota_requests = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckUserQuota(ctx, userID); err != nil {
		t.Errorf("deleted movie still counts: %v", err)
	}
}
