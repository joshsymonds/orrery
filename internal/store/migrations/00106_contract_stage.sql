-- +goose Up
-- A contract the contract stage proposes has staged 1. Each contract written before this migration reads 0, so the
-- default leaves every existing row as it was.
ALTER TABLE contracts ADD COLUMN staged INTEGER NOT NULL DEFAULT 0;
-- A draft is a contract the stage is writing and has not yet proposed. Each draft has one row. design names the design
-- it belongs to, and '' means none. body is the EpicContract in protobuf JSON, and decisions is a JSON array of
-- store.ContractDecision, as contract_versions keeps them. writer names a sessions row by its id alone, with no
-- reference to sessions. created and updated are unix seconds, and an update keeps created.
CREATE TABLE contract_drafts (
    id TEXT PRIMARY KEY,
    design TEXT NOT NULL,
    repository TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    decisions TEXT NOT NULL,
    words TEXT NOT NULL,
    writer TEXT NOT NULL,
    created INTEGER NOT NULL,
    updated INTEGER NOT NULL
);
-- A step is one call the contract stage records against a design. kind is research, reproduce, discovery or closure.
-- subject is the research question, or the reproduce command. repository, revision and delta are text the step records
-- with it. created is unix seconds.
CREATE TABLE design_steps (
    step TEXT PRIMARY KEY,
    design TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('research', 'reproduce', 'discovery', 'closure')),
    subject TEXT NOT NULL,
    repository TEXT NOT NULL,
    revision TEXT NOT NULL,
    delta TEXT NOT NULL,
    created INTEGER NOT NULL
);
CREATE INDEX design_steps_design ON design_steps (design);
-- A question is one question the stage asked about a design, numbered by seq within the design. contract names the
-- contract the question names, and '' means it names none. options is a JSON array of strings. asked is unix seconds.
-- A question is unanswered while answered is 0. An answer sets answer, accepted, maker, quote, maker_reason, delegation,
-- answerer and answered together, and maker is '' until then. maker is one of owner_serve, owner_conversation or
-- conversation.
CREATE TABLE design_questions (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    contract TEXT NOT NULL,
    text TEXT NOT NULL,
    options TEXT NOT NULL,         -- JSON array of strings
    recommendation TEXT NOT NULL,
    asker TEXT NOT NULL,
    asked INTEGER NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    accepted INTEGER NOT NULL DEFAULT 0,
    maker TEXT NOT NULL DEFAULT '' CHECK (maker IN ('', 'owner_serve', 'owner_conversation', 'conversation')),
    quote TEXT NOT NULL DEFAULT '',
    maker_reason TEXT NOT NULL DEFAULT '',
    delegation TEXT NOT NULL DEFAULT '',
    answerer TEXT NOT NULL DEFAULT '',
    answered INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (design, seq)
);
-- A disposition says what the design did with an item, numbered by seq within the design. Rows only append, and the
-- newest row for an item is its disposition. maker is owner_serve, owner_conversation or conversation. at is unix
-- seconds.
CREATE TABLE design_dispositions (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    item TEXT NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition IN ('adopted', 'rejected', 'deferred', 'open', 'revised_after_closure',
                                                     'accepted_as_risk')),
    reason TEXT NOT NULL,
    boundary TEXT NOT NULL,
    maker TEXT NOT NULL CHECK (maker IN ('owner_serve', 'owner_conversation', 'conversation')),
    quote TEXT NOT NULL,
    maker_reason TEXT NOT NULL,
    delegation TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (design, seq)
);
-- A closure skip is the record that a design's closure was skipped, with its reason. Each design has at most one. maker
-- is owner_serve, owner_conversation or conversation. at is unix seconds.
CREATE TABLE design_closure_skips (
    design TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    maker TEXT NOT NULL CHECK (maker IN ('owner_serve', 'owner_conversation', 'conversation')),
    quote TEXT NOT NULL,
    maker_reason TEXT NOT NULL,
    delegation TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL
);

-- +goose Down
-- The stage's tables are dropped, and the staged column is dropped with them. Each contract keeps its other columns.
DROP TABLE design_closure_skips;
DROP TABLE design_dispositions;
DROP TABLE design_questions;
DROP TABLE design_steps;
DROP TABLE contract_drafts;
ALTER TABLE contracts DROP COLUMN staged;
