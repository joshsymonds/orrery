-- +goose Up
-- headline is the one-line summary of what an engine session has been doing, written by the headline pass. headline_at
-- is the unix seconds of that write, 0 until the first one, so a turn that ends after it makes the session due again.
ALTER TABLE sessions ADD COLUMN headline TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN headline_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN headline_at;
ALTER TABLE sessions DROP COLUMN headline;
