-- +goose Up
-- No read suppresses a step's end, so every end comes back to its caller and the read mark goes.
ALTER TABLE steps DROP COLUMN caller_read;

-- +goose Down
ALTER TABLE steps ADD COLUMN caller_read INTEGER NOT NULL DEFAULT 0;
