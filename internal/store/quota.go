package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrQuotaExceeded is returned when a user's request quota is used up.
var ErrQuotaExceeded = errors.New("request quota exceeded")

// CheckUserQuota verifies the optional limits of a user before a new movie
// or series is requested. quota_requests counts active movies and series,
// quota_gb sums the files stored for them. Zero disables a limit.
func (s *Store) CheckUserQuota(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return nil
	}
	var maxRequests, maxGB int
	err := s.db.QueryRowContext(ctx, `SELECT quota_requests, quota_gb FROM users WHERE id = ?`, userID).
		Scan(&maxRequests, &maxGB)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if maxRequests == 0 && maxGB == 0 {
		return nil
	}

	var (
		requests int
		bytes    int64
	)
	err = s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM movies WHERE requested_by = ? AND state <> ?)
			+ (SELECT COUNT(*) FROM series WHERE requested_by = ?),
			COALESCE((SELECT SUM(f.size) FROM files f JOIN movies m ON m.id = f.movie_id
				WHERE m.requested_by = ? AND m.state <> ?), 0)
			+ COALESCE((SELECT SUM(f.size) FROM files f JOIN episodes e ON e.id = f.episode_id
				JOIN series se ON se.id = e.series_id WHERE se.requested_by = ?), 0)`,
		userID, MovieDeleted, userID,
		userID, MovieDeleted, userID,
	).Scan(&requests, &bytes)
	if err != nil {
		return err
	}

	if maxRequests > 0 && requests >= maxRequests {
		return fmt.Errorf("%w: %d of %d requests used", ErrQuotaExceeded, requests, maxRequests)
	}
	if maxGB > 0 && bytes >= int64(maxGB)<<30 {
		return fmt.Errorf("%w: %s of %d GB used", ErrQuotaExceeded, formatGB(bytes), maxGB)
	}
	return nil
}

func formatGB(n int64) string {
	return fmt.Sprintf("%.1f", float64(n)/(1<<30))
}
