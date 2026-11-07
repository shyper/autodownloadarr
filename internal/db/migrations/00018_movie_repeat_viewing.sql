-- +goose Up
ALTER TABLE movies ADD COLUMN delete_not_before INTEGER;

-- +goose Down
ALTER TABLE movies DROP COLUMN delete_not_before;
