-- Plex keeps the watch history of a title when its file is deleted and
-- added again. The watch state Plex reports when a new file first shows up
-- is stored as a baseline; only changes against it count as watching.
-- Movies also keep their playback progress for the UI.

-- +goose Up
ALTER TABLE movies ADD COLUMN progress REAL NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN base_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN base_views INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN base_offset INTEGER NOT NULL DEFAULT 0; -- ms
ALTER TABLE episodes ADD COLUMN base_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE episodes ADD COLUMN base_views INTEGER NOT NULL DEFAULT 0;
ALTER TABLE episodes ADD COLUMN base_offset INTEGER NOT NULL DEFAULT 0;

-- Keep what was already detected: watched titles and episodes in progress
-- get an empty baseline, so their state stays as it is.
UPDATE movies SET base_set = 1 WHERE state = 'watched';
UPDATE episodes SET base_set = 1 WHERE state = 'watched' OR progress > 0;

-- +goose Down
ALTER TABLE episodes DROP COLUMN base_offset;
ALTER TABLE episodes DROP COLUMN base_views;
ALTER TABLE episodes DROP COLUMN base_set;
ALTER TABLE movies DROP COLUMN base_offset;
ALTER TABLE movies DROP COLUMN base_views;
ALTER TABLE movies DROP COLUMN base_set;
ALTER TABLE movies DROP COLUMN progress;
