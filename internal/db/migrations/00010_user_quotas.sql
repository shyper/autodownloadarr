-- Optional per-user limits. Zero means unlimited, so existing accounts and
-- installations keep their behaviour.

-- +goose Up
ALTER TABLE users ADD COLUMN quota_requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN quota_gb INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN quota_gb;
ALTER TABLE users DROP COLUMN quota_requests;
