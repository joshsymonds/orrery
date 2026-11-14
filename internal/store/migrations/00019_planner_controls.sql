-- +goose Up
ALTER TABLE epics ADD COLUMN retried INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_turns ADD COLUMN told INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_turns ADD COLUMN stop INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_turns ADD COLUMN resets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_decisions ADD COLUMN question INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pending_notices ADD COLUMN epic TEXT NOT NULL DEFAULT '';

CREATE TABLE pushed_epic_notices (
    epic TEXT PRIMARY KEY,
    time INTEGER NOT NULL
);

-- +goose Down
DROP TABLE pushed_epic_notices;
ALTER TABLE pending_notices DROP COLUMN epic;
ALTER TABLE epic_decisions DROP COLUMN question;
ALTER TABLE epic_turns DROP COLUMN resets;
ALTER TABLE epic_turns DROP COLUMN stop;
ALTER TABLE epic_turns DROP COLUMN told;
ALTER TABLE epics DROP COLUMN retried;
