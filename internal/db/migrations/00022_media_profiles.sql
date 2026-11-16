-- Profiles of the media server (Plex Home members, Jellyfin/Emby users).
-- An admin assigns a profile to an AutoDownloadarr user; its watchlist is
-- imported for that user and the user's requests use its watch state.
-- base_account records whose watch state a baseline was taken from, so a
-- baseline is not compared against another account's view counts.

-- +goose Up
CREATE TABLE media_profiles (
    id                INTEGER PRIMARY KEY,
    media_server_id   INTEGER NOT NULL REFERENCES media_servers (id) ON DELETE CASCADE,
    external_id       TEXT    NOT NULL,
    name              TEXT    NOT NULL,
    thumb             TEXT    NOT NULL DEFAULT '',
    owner             INTEGER NOT NULL DEFAULT 0,
    protected         INTEGER NOT NULL DEFAULT 0,
    server_token_enc  TEXT    NOT NULL DEFAULT '',
    account_token_enc TEXT    NOT NULL DEFAULT '',
    user_id           INTEGER UNIQUE REFERENCES users (id) ON DELETE SET NULL,
    watchlist         INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    UNIQUE (media_server_id, external_id)
);
ALTER TABLE movies ADD COLUMN base_account TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN base_account TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE episodes DROP COLUMN base_account;
ALTER TABLE movies DROP COLUMN base_account;
DROP TABLE media_profiles;
