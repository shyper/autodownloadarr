-- +goose Up
-- Set when the files of a series nobody watched were removed; the series
-- stays listed and can be downloaded again.
ALTER TABLE series ADD COLUMN expired_at INTEGER;

-- +goose Down
ALTER TABLE series DROP COLUMN expired_at;
