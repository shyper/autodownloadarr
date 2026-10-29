-- Usenet servers can check every article of a release before it is
-- downloaded (on by default).

-- +goose Up
ALTER TABLE usenet_servers ADD COLUMN full_check INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE usenet_servers DROP COLUMN full_check;
