-- +goose Up
CREATE TABLE bot_links (
 id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, external_id TEXT NOT NULL,
 chat_id TEXT NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 language TEXT NOT NULL DEFAULT 'de', notifications INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL, UNIQUE(provider,external_id), UNIQUE(provider,user_id)
);
CREATE TABLE bot_link_codes (
 hash BLOB PRIMARY KEY, provider TEXT NOT NULL, external_id TEXT NOT NULL, chat_id TEXT NOT NULL,
 language TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE bot_actions (
 hash BLOB PRIMARY KEY, provider TEXT NOT NULL, external_id TEXT NOT NULL,
 payload TEXT NOT NULL, expires_at INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE bot_updates (
 provider TEXT NOT NULL, id TEXT NOT NULL, payload_enc TEXT NOT NULL,
 response_enc TEXT NOT NULL DEFAULT '', done INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL, PRIMARY KEY(provider,id)
);

-- +goose Down
DROP TABLE bot_updates;
DROP TABLE bot_actions;
DROP TABLE bot_link_codes;
DROP TABLE bot_links;
