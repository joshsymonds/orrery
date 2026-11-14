-- +goose Up
ALTER TABLE steps ADD COLUMN loop_limited BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE steps ADD COLUMN loop_limit_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN loop_limit_resets INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE steps DROP COLUMN loop_limit_resets;
ALTER TABLE steps DROP COLUMN loop_limit_kind;
ALTER TABLE steps DROP COLUMN loop_limited;
