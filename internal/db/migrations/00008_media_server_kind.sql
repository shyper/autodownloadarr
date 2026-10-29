-- Allow Jellyfin and Emby next to Plex. SQLite cannot change a CHECK
-- constraint in place, so the table is rebuilt. The pragma only works
-- outside a transaction, hence NO TRANSACTION.

-- +goose Up
-- +goose NO TRANSACTION
PRAGMA foreign_keys = OFF;
CREATE TABLE media_servers_new (
    id            INTEGER PRIMARY KEY,
    kind          TEXT    NOT NULL CHECK (kind IN ('plex', 'jellyfin', 'emby')),
    name          TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    token_enc     TEXT    NOT NULL DEFAULT '',
    movie_library TEXT    NOT NULL DEFAULT '',
    tv_library    TEXT    NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
INSERT INTO media_servers_new (id, kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at)
SELECT id, kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at FROM media_servers;
DROP TABLE media_servers;
ALTER TABLE media_servers_new RENAME TO media_servers;
PRAGMA foreign_keys = ON;

-- +goose Down
-- +goose NO TRANSACTION
PRAGMA foreign_keys = OFF;
DELETE FROM media_servers WHERE kind <> 'plex';
CREATE TABLE media_servers_old (
    id            INTEGER PRIMARY KEY,
    kind          TEXT    NOT NULL CHECK (kind IN ('plex')),
    name          TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    token_enc     TEXT    NOT NULL DEFAULT '',
    movie_library TEXT    NOT NULL DEFAULT '',
    tv_library    TEXT    NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
INSERT INTO media_servers_old (id, kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at)
SELECT id, kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at FROM media_servers;
DROP TABLE media_servers;
ALTER TABLE media_servers_old RENAME TO media_servers;
PRAGMA foreign_keys = ON;
