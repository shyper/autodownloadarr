-- +goose Up
ALTER TABLE episodes ADD COLUMN still_path TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN season_poster_path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE episodes DROP COLUMN season_poster_path;
ALTER TABLE episodes DROP COLUMN still_path;
