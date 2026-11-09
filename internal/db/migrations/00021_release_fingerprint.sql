-- +goose Up
ALTER TABLE movies ADD COLUMN release_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN release_fingerprint TEXT NOT NULL DEFAULT '';
UPDATE movies SET release_fingerprint = COALESCE((SELECT fingerprint FROM releases r WHERE r.movie_id = movies.id AND movies.release_key = CAST(COALESCE(r.indexer_id, 0) AS TEXT) || CASE WHEN r.guid != '' THEN ':guid:' || r.guid ELSE ':fp:' || r.fingerprint END LIMIT 1), '');
UPDATE episodes SET release_fingerprint = COALESCE((SELECT fingerprint FROM releases r WHERE r.episode_id = episodes.id AND episodes.release_key = CAST(COALESCE(r.indexer_id, 0) AS TEXT) || CASE WHEN r.guid != '' THEN ':guid:' || r.guid ELSE ':fp:' || r.fingerprint END LIMIT 1), '');

-- +goose Down
ALTER TABLE episodes DROP COLUMN release_fingerprint;
ALTER TABLE movies DROP COLUMN release_fingerprint;
