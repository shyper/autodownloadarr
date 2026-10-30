-- +goose Up
ALTER TABLE movies ADD COLUMN release_key TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN release_key TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN release_key TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE attempts DROP COLUMN release_key;
ALTER TABLE episodes DROP COLUMN release_key;
ALTER TABLE movies DROP COLUMN release_key;
