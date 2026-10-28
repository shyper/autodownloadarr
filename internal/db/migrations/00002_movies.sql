-- Phase 2: providers, settings, quality profiles and the movie lifecycle.
-- Columns ending in _enc hold values encrypted by internal/secrets.

-- +goose Up
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL, -- JSON
    updated_at INTEGER NOT NULL
);

CREATE TABLE usenet_servers (
    id                   INTEGER PRIMARY KEY,
    name                 TEXT    NOT NULL,
    host                 TEXT    NOT NULL,
    port                 INTEGER NOT NULL,
    tls                  INTEGER NOT NULL DEFAULT 1,
    insecure_skip_verify INTEGER NOT NULL DEFAULT 0,
    username             TEXT    NOT NULL DEFAULT '',
    password_enc         TEXT    NOT NULL DEFAULT '',
    connections          INTEGER NOT NULL DEFAULT 20,
    priority             INTEGER NOT NULL DEFAULT 0,
    enabled              INTEGER NOT NULL DEFAULT 1,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

CREATE TABLE indexers (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL,
    url              TEXT    NOT NULL,
    api_key_enc      TEXT    NOT NULL DEFAULT '',
    movie_categories TEXT    NOT NULL DEFAULT '[2000]', -- JSON array
    priority         INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE media_servers (
    id            INTEGER PRIMARY KEY,
    kind          TEXT    NOT NULL CHECK (kind IN ('plex')),
    name          TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    token_enc     TEXT    NOT NULL DEFAULT '',
    movie_library TEXT    NOT NULL DEFAULT '', -- library (section) key
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE path_mappings (
    id              INTEGER PRIMARY KEY,
    media_server_id INTEGER NOT NULL REFERENCES media_servers (id) ON DELETE CASCADE,
    local_path      TEXT    NOT NULL,
    remote_path     TEXT    NOT NULL
);

CREATE TABLE quality_profiles (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    config     TEXT    NOT NULL, -- JSON, see internal/release.Profile
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE archive_passwords (
    id           INTEGER PRIMARY KEY,
    password_enc TEXT    NOT NULL,
    hits         INTEGER NOT NULL DEFAULT 0,
    last_hit_at  INTEGER,
    created_at   INTEGER NOT NULL
);

CREATE TABLE movies (
    id             INTEGER PRIMARY KEY,
    tmdb_id        INTEGER NOT NULL UNIQUE,
    imdb_id        TEXT    NOT NULL DEFAULT '',
    title          TEXT    NOT NULL,
    original_title TEXT    NOT NULL DEFAULT '',
    year           INTEGER NOT NULL DEFAULT 0,
    overview       TEXT    NOT NULL DEFAULT '',
    poster_path    TEXT    NOT NULL DEFAULT '',
    runtime        INTEGER NOT NULL DEFAULT 0, -- minutes
    alt_titles     TEXT    NOT NULL DEFAULT '[]', -- JSON array, for text searches
    profile_id     INTEGER REFERENCES quality_profiles (id) ON DELETE SET NULL,
    requested_by   INTEGER REFERENCES users (id) ON DELETE SET NULL,
    state          TEXT    NOT NULL,
    keep           INTEGER NOT NULL DEFAULT 0,
    attempts       INTEGER NOT NULL DEFAULT 0, -- failed sources since the last success
    last_error     TEXT    NOT NULL DEFAULT '',
    next_search_at INTEGER,
    release_title  TEXT    NOT NULL DEFAULT '', -- release being or last downloaded
    folder         TEXT    NOT NULL DEFAULT '', -- local library folder
    media_key      TEXT    NOT NULL DEFAULT '', -- Plex ratingKey
    available_at   INTEGER,
    watched_at     INTEGER,
    delete_at      INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX movies_state ON movies (state);

CREATE TABLE releases (
    id            INTEGER PRIMARY KEY,
    movie_id      INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    indexer_id    INTEGER REFERENCES indexers (id) ON DELETE SET NULL,
    indexer_name  TEXT    NOT NULL DEFAULT '',
    guid          TEXT    NOT NULL,
    title         TEXT    NOT NULL,
    size          INTEGER NOT NULL DEFAULT 0,
    nzb_url       TEXT    NOT NULL,
    published_at  INTEGER,
    grabs         INTEGER NOT NULL DEFAULT 0,
    passworded    INTEGER NOT NULL DEFAULT 0,
    fingerprint   TEXT    NOT NULL,
    score         INTEGER NOT NULL DEFAULT 0,
    rejected      TEXT    NOT NULL DEFAULT '', -- reason, empty if acceptable
    searched_at   INTEGER NOT NULL
);
CREATE INDEX releases_movie ON releases (movie_id);

CREATE TABLE blocklist (
    id          INTEGER PRIMARY KEY,
    movie_id    INTEGER REFERENCES movies (id) ON DELETE CASCADE,
    fingerprint TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    reason      TEXT    NOT NULL, -- error kind, e.g. incomplete, password, fake
    message     TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX blocklist_fingerprint ON blocklist (fingerprint);

CREATE TABLE attempts (
    id            INTEGER PRIMARY KEY,
    movie_id      INTEGER NOT NULL REFERENCES movies (id) ON DELETE CASCADE,
    release_title TEXT    NOT NULL,
    indexer_name  TEXT    NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    result        TEXT    NOT NULL DEFAULT 'running', -- running, success, failed, cancelled
    error_kind    TEXT    NOT NULL DEFAULT '',
    message       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX attempts_movie ON attempts (movie_id);

-- Every file AutoDownloadarr created in a library. Only these are deleted.
CREATE TABLE files (
    id         INTEGER PRIMARY KEY,
    movie_id   INTEGER REFERENCES movies (id) ON DELETE SET NULL,
    path       TEXT    NOT NULL UNIQUE,
    size       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE files;
DROP TABLE attempts;
DROP TABLE blocklist;
DROP TABLE releases;
DROP TABLE movies;
DROP TABLE archive_passwords;
DROP TABLE quality_profiles;
DROP TABLE path_mappings;
DROP TABLE media_servers;
DROP TABLE indexers;
DROP TABLE usenet_servers;
DROP TABLE settings;
