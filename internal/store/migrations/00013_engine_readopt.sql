-- +goose Up
ALTER TABLE sessions ADD COLUMN consumed INTEGER;
ALTER TABLE sessions ADD COLUMN replay_from INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN background BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN turn_output TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN prompts_from INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN write_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE events DROP COLUMN write_id;
ALTER TABLE sessions DROP COLUMN prompts_from;
ALTER TABLE sessions DROP COLUMN turn_output;
ALTER TABLE sessions DROP COLUMN background;
ALTER TABLE sessions DROP COLUMN replay_from;
ALTER TABLE sessions DROP COLUMN consumed;
