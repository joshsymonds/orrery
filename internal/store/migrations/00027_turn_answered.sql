-- +goose Up
ALTER TABLE events ADD COLUMN answered TEXT;

-- +goose Down
ALTER TABLE events DROP COLUMN answered;
