-- +goose Up
ALTER TABLE movies ADD COLUMN genres TEXT NOT NULL DEFAULT 'null';
ALTER TABLE series ADD COLUMN genres TEXT NOT NULL DEFAULT 'null';

-- +goose Down
ALTER TABLE series DROP COLUMN genres;
ALTER TABLE movies DROP COLUMN genres;
