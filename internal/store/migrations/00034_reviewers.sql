-- +goose Up
ALTER TABLE steps ADD COLUMN agents TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_reviewers (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    stage TEXT NOT NULL,
    must TEXT NOT NULL,
    must_not TEXT NOT NULL,
    prompt TEXT NOT NULL,
    agent TEXT NOT NULL,
    model TEXT NOT NULL,
    tools TEXT NOT NULL,
    effort TEXT NOT NULL,
    limit_seconds INTEGER NOT NULL,
    verify INTEGER NOT NULL,
    PRIMARY KEY (epic, position),
    UNIQUE (epic, name)
);

ALTER TABLE epic_task_findings ADD COLUMN unverified INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epic_task_findings DROP COLUMN unverified;
DROP TABLE epic_reviewers;
ALTER TABLE steps DROP COLUMN agents;
