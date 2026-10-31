-- +goose Up
-- Keep manual removals across deletion of the movie and its history.
CREATE TABLE watchlist_movie_exclusions (
    tmdb_id INTEGER PRIMARY KEY
);

-- +goose Down
DROP TABLE watchlist_movie_exclusions;
