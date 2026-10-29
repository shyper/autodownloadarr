-- The Plex account token is needed for the watchlist on discover.provider.plex.tv.
-- It differs from the server token when the server is shared with the account.

-- +goose Up
ALTER TABLE media_servers ADD COLUMN account_token_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE media_servers DROP COLUMN account_token_enc;
