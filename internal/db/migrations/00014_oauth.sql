-- +goose Up
-- Preserve known request owners; adopt legacy and watchlist requests for the first account.
UPDATE movies SET requested_by = (SELECT id FROM users ORDER BY id LIMIT 1) WHERE requested_by IS NULL;
UPDATE series SET requested_by = (SELECT id FROM users ORDER BY id LIMIT 1) WHERE requested_by IS NULL;
CREATE TABLE oauth_clients (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, redirect_uris TEXT NOT NULL
);
CREATE TABLE oauth_grants (
 id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL, client_name TEXT NOT NULL, scopes TEXT NOT NULL,
 created_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE oauth_codes (
 hash BLOB PRIMARY KEY, grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
 redirect_uri TEXT NOT NULL, challenge TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE oauth_tokens (
 hash BLOB PRIMARY KEY, grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
 kind TEXT NOT NULL, expires_at INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX oauth_grants_user ON oauth_grants(user_id);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
