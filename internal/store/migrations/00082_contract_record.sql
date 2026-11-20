-- +goose Up
-- A contract is an epic's agreed record before it becomes an epic. Each contract has one row, and each saved version of
-- it and each state change of it appends a row to contract_versions or contract_states. Rows only append: no query
-- updates or deletes them. The writer and session columns name a sessions row by its id alone, with no reference to
-- sessions, so a session whose row is gone still stays on the record.
CREATE TABLE contracts (
    id TEXT PRIMARY KEY,
    created INTEGER NOT NULL
);
CREATE TABLE contract_versions (
    contract TEXT NOT NULL REFERENCES contracts (id),
    version INTEGER NOT NULL,
    repository TEXT NOT NULL,
    title TEXT NOT NULL,
    care TEXT NOT NULL,
    body TEXT NOT NULL,            -- the EpicContract in protobuf JSON, as enginehost wrote it
    decisions TEXT NOT NULL,       -- JSON array of store.ContractDecision
    open_decisions INTEGER NOT NULL,
    words TEXT NOT NULL,
    writer TEXT NOT NULL,          -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    changed_rows TEXT NOT NULL,    -- JSON array of strings
    changed_sections TEXT NOT NULL,-- JSON array of strings
    PRIMARY KEY (contract, version)
);
CREATE TABLE contract_states (
    contract TEXT NOT NULL REFERENCES contracts (id),
    seq INTEGER NOT NULL,          -- counted from 1
    state TEXT NOT NULL,
    version INTEGER NOT NULL,
    session TEXT NOT NULL,         -- '' for the owner; no reference to sessions
    words TEXT NOT NULL,
    at INTEGER NOT NULL,
    PRIMARY KEY (contract, seq)
);

-- +goose Down
DROP TABLE contract_states;
DROP TABLE contract_versions;
DROP TABLE contracts;
