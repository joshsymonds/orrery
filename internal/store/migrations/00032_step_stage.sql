-- +goose Up
ALTER TABLE steps ADD COLUMN loop_stage TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN loop_stage_from INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE steps DROP COLUMN loop_stage_from;
ALTER TABLE steps DROP COLUMN loop_stage;
