package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/shyper/autodownloadarr/internal/release"
)

// ReportedRelease resolves a saved fingerprint even after search results changed.
func (s *Store) ReportedRelease(ctx context.Context, owner Owner, key, title, fingerprint string) (BlocklistEntry, error) {
	if fingerprint == "" {
		rels, err := s.ReleasesOf(ctx, owner)
		if err != nil {
			return BlocklistEntry{}, err
		}
		for _, rel := range rels {
			if rel.Identity() == key {
				fingerprint = rel.Fingerprint
				break
			}
		}
	}
	if fingerprint == "" && key != "" {
		// Older downloads predate saved fingerprints. Attempt history retains
		// the title and size needed to recover the same cross-indexer identity.
		var size int64
		err := s.db.QueryRowContext(ctx, `SELECT size FROM attempts WHERE release_key = ? AND release_title = ? ORDER BY id DESC LIMIT 1`, key, title).Scan(&size)
		if err == nil {
			fingerprint = release.Fingerprint(title, size)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return BlocklistEntry{}, err
		}
	}
	if key == "" || fingerprint == "" {
		return BlocklistEntry{}, fmt.Errorf("%w: current release cannot be identified", ErrInvalid)
	}
	entry := BlocklistEntry{Fingerprint: fingerprint, Title: title, Reason: "reported_bad", Message: "Reported as faulty by the user"}
	if owner.col == "movie_id" {
		entry.MovieID = owner.ID
	} else {
		entry.EpisodeID = owner.ID
	}
	return entry, nil
}
