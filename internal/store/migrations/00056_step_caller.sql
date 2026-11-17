-- +goose Up
-- The sessions row id of the session that started the step, '' when the start named no caller or the caller had no row.
ALTER TABLE steps ADD COLUMN caller TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE steps DROP COLUMN caller;
