-- +goose Up
-- A design is one set of contracts that a conversation planned together. Each design has one row. Its conversation
-- names the sessions row that wrote it by id alone, with no reference to sessions, as contract_versions does. created
-- is unix seconds.
CREATE TABLE designs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    conversation TEXT NOT NULL,    -- a sessions row id; no reference to sessions
    created INTEGER NOT NULL
);
-- A contract belongs to at most one design, and position orders the contracts of a design. added is unix seconds, the
-- time the contract joined the design.
CREATE TABLE design_contracts (
    contract TEXT PRIMARY KEY REFERENCES contracts (id),
    design TEXT NOT NULL REFERENCES designs (id),
    position INTEGER NOT NULL,
    added INTEGER NOT NULL,
    UNIQUE (design, position)
);
-- An edge says that contract starts after after, and both name contracts of design. Each pair of contracts has at most
-- one edge. created is unix seconds, the time the edge was added.
CREATE TABLE design_edges (
    design TEXT NOT NULL REFERENCES designs (id),
    contract TEXT NOT NULL,
    after TEXT NOT NULL,
    created INTEGER NOT NULL,
    PRIMARY KEY (contract, after)
);

-- +goose Down
DROP TABLE design_edges;
DROP TABLE design_contracts;
DROP TABLE designs;
