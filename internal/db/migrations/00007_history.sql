-- Download history: attempts outlive their movie or episode (the link is
-- cleared instead) and carry what the history page shows.

-- +goose Up
CREATE TABLE attempts_new (
    id            INTEGER PRIMARY KEY,
    movie_id      INTEGER REFERENCES movies (id) ON DELETE SET NULL,
    episode_id    INTEGER REFERENCES episodes (id) ON DELETE SET NULL,
    series_id     INTEGER REFERENCES series (id) ON DELETE SET NULL,
    kind          TEXT    NOT NULL DEFAULT 'movie', -- movie, episode
    label         TEXT    NOT NULL DEFAULT '',      -- "Title (Year)" or "Show · S01E02"
    size          INTEGER NOT NULL DEFAULT 0,       -- release size in bytes
    release_title TEXT    NOT NULL,
    indexer_name  TEXT    NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    result        TEXT    NOT NULL DEFAULT 'running',
    error_kind    TEXT    NOT NULL DEFAULT '',
    message       TEXT    NOT NULL DEFAULT ''
);
INSERT INTO attempts_new (id, movie_id, episode_id, series_id, kind, label, release_title, indexer_name, started_at, finished_at,
    result, error_kind, message)
SELECT a.id, a.movie_id, a.episode_id, e.series_id,
    CASE WHEN a.episode_id IS NULL THEN 'movie' ELSE 'episode' END,
    COALESCE(
        (SELECT m.title || CASE WHEN m.year > 0 THEN ' (' || m.year || ')' ELSE '' END FROM movies m WHERE m.id = a.movie_id),
        (SELECT s.title || ' · S' || printf('%02d', e.season) || 'E' || printf('%02d', e.episode) FROM series s WHERE s.id = e.series_id),
        ''),
    a.release_title, a.indexer_name, a.started_at, a.finished_at, a.result, a.error_kind, a.message
FROM attempts a LEFT JOIN episodes e ON e.id = a.episode_id;
DROP TABLE attempts;
ALTER TABLE attempts_new RENAME TO attempts;
CREATE INDEX attempts_movie ON attempts (movie_id);
CREATE INDEX attempts_episode ON attempts (episode_id);

-- +goose Down
SELECT 'down migration not supported for 00007';
