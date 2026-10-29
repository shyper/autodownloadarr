-- Series can follow seasons that appear later. known_seasons is the highest
-- season that existed when the selection was made; later ones are "future".

-- +goose Up
ALTER TABLE series ADD COLUMN future_seasons INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN known_seasons INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE series DROP COLUMN known_seasons;
ALTER TABLE series DROP COLUMN future_seasons;
