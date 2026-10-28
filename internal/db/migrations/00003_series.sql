-- Phase 4: series and episodes. Releases and attempts now belong to a movie
-- or an episode, so their tables are rebuilt with a nullable movie_id.

-- +goose Up
ALTER TABLE indexers ADD COLUMN tv_categories TEXT NOT NULL DEFAULT '[5000]';
ALTER TABLE media_servers ADD COLUMN tv_library TEXT NOT NULL DEFAULT '';

CREATE TABLE series (
    id             INTEGER PRIMARY KEY,
    tmdb_id        INTEGER NOT NULL UNIQUE,
    tvdb_id        INTEGER NOT NULL DEFAULT 0,
    imdb_id        TEXT    NOT NULL DEFAULT '',
    title          TEXT    NOT NULL,
    original_title TEXT    NOT NULL DEFAULT '',
    year           INTEGER NOT NULL DEFAULT 0,
    overview       TEXT    NOT NULL DEFAULT '',
    poster_path    TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL DEFAULT '',
    runtime        INTEGER NOT NULL DEFAULT 0,
    alt_titles     TEXT    NOT NULL DEFAULT '[]',
    profile_id     INTEGER REFERENCES quality_profiles (id) ON DELETE SET NULL,
    requested_by   INTEGER REFERENCES users (id) ON DELETE SET NULL,
    seasons        TEXT    NOT NULL DEFAULT '[]', -- JSON: selected seasons, empty = all
    start_season   INTEGER NOT NULL DEFAULT 1,
    start_episode  INTEGER NOT NULL DEFAULT 1,
    window_size    INTEGER NOT NULL DEFAULT 3,
    delete_trigger TEXT    NOT NULL DEFAULT 'progress', -- progress, minutes, watched
    trigger_value  INTEGER NOT NULL DEFAULT 10,         -- percent or minutes
    keep           INTEGER NOT NULL DEFAULT 0,
    paused         INTEGER NOT NULL DEFAULT 0,
    folder         TEXT    NOT NULL DEFAULT '',
    media_key      TEXT    NOT NULL DEFAULT '',
    refreshed_at   INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE episodes (
    id             INTEGER PRIMARY KEY,
    series_id      INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    season         INTEGER NOT NULL,
    episode        INTEGER NOT NULL,
    title          TEXT    NOT NULL DEFAULT '',
    air_date       TEXT    NOT NULL DEFAULT '', -- YYYY-MM-DD
    runtime        INTEGER NOT NULL DEFAULT 0,
    state          TEXT    NOT NULL DEFAULT 'pending',
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT    NOT NULL DEFAULT '',
    next_search_at INTEGER,
    release_title  TEXT    NOT NULL DEFAULT '',
    file           TEXT    NOT NULL DEFAULT '',
    media_key      TEXT    NOT NULL DEFAULT '',
    progress       REAL    NOT NULL DEFAULT 0, -- 0..1 from the media server
    started_at     INTEGER,
    available_at   INTEGER,
    watched_at     INTEGER,
    delete_at      INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (series_id, season, episode)
);
CREATE INDEX episodes_state ON episodes (state);

CREATE TABLE releases_new (
    id           INTEGER PRIMARY KEY,
    movie_id     INTEGER REFERENCES movies (id) ON DELETE CASCADE,
    episode_id   INTEGER REFERENCES episodes (id) ON DELETE CASCADE,
    indexer_id   INTEGER REFERENCES indexers (id) ON DELETE SET NULL,
    indexer_name TEXT    NOT NULL DEFAULT '',
    guid         TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    nzb_url      TEXT    NOT NULL,
    published_at INTEGER,
    grabs        INTEGER NOT NULL DEFAULT 0,
    passworded   INTEGER NOT NULL DEFAULT 0,
    fingerprint  TEXT    NOT NULL,
    score        INTEGER NOT NULL DEFAULT 0,
    rejected     TEXT    NOT NULL DEFAULT '',
    searched_at  INTEGER NOT NULL
);
INSERT INTO releases_new (id, movie_id, indexer_id, indexer_name, guid, title, size, nzb_url, published_at, grabs, passworded,
    fingerprint, score, rejected, searched_at)
SELECT id, movie_id, indexer_id, indexer_name, guid, title, size, nzb_url, published_at, grabs, passworded,
    fingerprint, score, rejected, searched_at FROM releases;
DROP TABLE releases;
ALTER TABLE releases_new RENAME TO releases;
CREATE INDEX releases_movie ON releases (movie_id);
CREATE INDEX releases_episode ON releases (episode_id);

CREATE TABLE attempts_new (
    id            INTEGER PRIMARY KEY,
    movie_id      INTEGER REFERENCES movies (id) ON DELETE CASCADE,
    episode_id    INTEGER REFERENCES episodes (id) ON DELETE CASCADE,
    release_title TEXT    NOT NULL,
    indexer_name  TEXT    NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    result        TEXT    NOT NULL DEFAULT 'running',
    error_kind    TEXT    NOT NULL DEFAULT '',
    message       TEXT    NOT NULL DEFAULT ''
);
INSERT INTO attempts_new (id, movie_id, release_title, indexer_name, started_at, finished_at, result, error_kind, message)
SELECT id, movie_id, release_title, indexer_name, started_at, finished_at, result, error_kind, message FROM attempts;
DROP TABLE attempts;
ALTER TABLE attempts_new RENAME TO attempts;
CREATE INDEX attempts_movie ON attempts (movie_id);
CREATE INDEX attempts_episode ON attempts (episode_id);

ALTER TABLE blocklist ADD COLUMN episode_id INTEGER REFERENCES episodes (id) ON DELETE CASCADE;
ALTER TABLE files ADD COLUMN episode_id INTEGER REFERENCES episodes (id) ON DELETE SET NULL;

-- +goose Down
SELECT 'down migration not supported for 00003';
