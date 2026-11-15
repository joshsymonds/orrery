-- +goose Up
ALTER TABLE pending_notices ADD COLUMN limit_key TEXT NOT NULL DEFAULT '';

CREATE TABLE told_limit_steps (
    limit_key TEXT NOT NULL,
    step TEXT NOT NULL,
    PRIMARY KEY (limit_key, step)
);

-- +goose Down
DROP TABLE told_limit_steps;
ALTER TABLE pending_notices DROP COLUMN limit_key;
