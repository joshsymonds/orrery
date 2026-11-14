-- +goose Up
ALTER TABLE epics ADD COLUMN base TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_gates (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    argv TEXT NOT NULL,
    limit_seconds INTEGER NOT NULL,
    needs TEXT NOT NULL,
    agent TEXT NOT NULL,
    must TEXT NOT NULL,
    must_not TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_gate_lists (
    epic TEXT NOT NULL,
    list TEXT NOT NULL CHECK (list IN ('done', 'review')),
    position INTEGER NOT NULL,
    gate TEXT NOT NULL,
    PRIMARY KEY (epic, list, position)
);

ALTER TABLE epic_requirements ADD COLUMN item TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_requirements DROP COLUMN item;
DROP TABLE epic_gate_lists;
DROP TABLE epic_gates;
ALTER TABLE epics DROP COLUMN base;
