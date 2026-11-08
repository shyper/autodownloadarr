-- +goose Up
CREATE TABLE trakt_links (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL, account_id TEXT NOT NULL, username TEXT NOT NULL,
 access_enc TEXT NOT NULL, refresh_enc TEXT NOT NULL, expires_at INTEGER NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, watchlist INTEGER NOT NULL DEFAULT 0,
 list_ids TEXT NOT NULL DEFAULT '[]', poll_minutes INTEGER NOT NULL DEFAULT 30,
 last_sync_at INTEGER NOT NULL DEFAULT 0, next_sync_at INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', requested INTEGER NOT NULL DEFAULT 0,
 skipped INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE trakt_oauth_states (
 state_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL, verifier_enc TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE automatic_series_exclusions (tmdb_id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE automatic_series_exclusions;
DROP TABLE trakt_oauth_states;
DROP TABLE trakt_links;
