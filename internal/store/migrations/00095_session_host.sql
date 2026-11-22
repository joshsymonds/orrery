-- +goose Up
-- host is the orrery name of the host a session runs on: a name hosts.toml gives a machine, or the controller's own
-- name. It is '' until a session is named, and a session stored before the column existed reads as ''.
ALTER TABLE sessions ADD COLUMN host TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN host;
