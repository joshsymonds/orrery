-- +goose Up
ALTER TABLE steps ADD COLUMN unowned INTEGER NOT NULL DEFAULT 0;

CREATE TABLE epic_release_runs (
    epic TEXT NOT NULL,
    part TEXT NOT NULL,
    position INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    step TEXT NOT NULL,
    start TEXT NOT NULL,
    head TEXT NOT NULL,
    finish TEXT NOT NULL,
    commits TEXT NOT NULL,
    evidence TEXT NOT NULL,
    passed BOOLEAN NOT NULL,
    reason TEXT NOT NULL,
    at INTEGER NOT NULL,
    PRIMARY KEY (epic, part, position, attempt)
);

CREATE TABLE epic_release_slots (
    repository TEXT PRIMARY KEY,
    epic TEXT NOT NULL UNIQUE
);

-- +goose Down
DROP TABLE epic_release_slots;
DROP TABLE epic_release_runs;
ALTER TABLE steps DROP COLUMN unowned;
