-- +goose Up
CREATE TABLE epics (
    id TEXT PRIMARY KEY,
    repository TEXT NOT NULL,
    title TEXT NOT NULL,
    approved TEXT NOT NULL,
    intent TEXT NOT NULL,
    care TEXT NOT NULL,
    approach TEXT NOT NULL,
    release_then TEXT NOT NULL,
    checks TEXT NOT NULL,
    phase TEXT NOT NULL,
    created INTEGER NOT NULL,
    changed INTEGER NOT NULL
);

CREATE TABLE epic_failures (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    id TEXT NOT NULL,
    text TEXT NOT NULL,
    rating TEXT NOT NULL,
    response TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_additions (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    location TEXT NOT NULL,
    what TEXT NOT NULL,
    why TEXT NOT NULL,
    cost TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_requirements (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    id TEXT NOT NULL,
    text TEXT NOT NULL,
    evidence TEXT NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_premises (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    id TEXT NOT NULL,
    text TEXT NOT NULL,
    if_wrong TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_exclusions (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_alternatives (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    alternative TEXT NOT NULL,
    why_not TEXT NOT NULL,
    reconsider TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_release_steps (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    step TEXT NOT NULL,
    target TEXT NOT NULL,
    undo TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_decisions (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    client_id TEXT NOT NULL,
    text TEXT NOT NULL,
    time INTEGER NOT NULL,
    PRIMARY KEY (epic, seq),
    UNIQUE (epic, client_id)
);

-- +goose StatementBegin
CREATE TRIGGER epic_decisions_no_update BEFORE UPDATE ON epic_decisions
BEGIN
    SELECT RAISE(ABORT, 'epic decisions are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_decisions_no_delete BEFORE DELETE ON epic_decisions
BEGIN
    SELECT RAISE(ABORT, 'epic decisions are append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER epic_decisions_no_delete;
DROP TRIGGER epic_decisions_no_update;
DROP TABLE epic_decisions;
DROP TABLE epic_release_steps;
DROP TABLE epic_alternatives;
DROP TABLE epic_exclusions;
DROP TABLE epic_premises;
DROP TABLE epic_requirements;
DROP TABLE epic_additions;
DROP TABLE epic_failures;
DROP TABLE epics;
