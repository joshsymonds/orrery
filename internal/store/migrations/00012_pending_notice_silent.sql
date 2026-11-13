-- +goose Up
ALTER TABLE pending_notices ADD COLUMN silent BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE pending_notices DROP COLUMN silent;
