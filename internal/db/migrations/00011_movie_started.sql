-- +goose Up
ALTER TABLE movies ADD COLUMN started_at INTEGER;

-- +goose Down
ALTER TABLE movies DROP COLUMN started_at;
