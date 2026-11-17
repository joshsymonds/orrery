-- +goose Up
ALTER TABLE sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN title;
