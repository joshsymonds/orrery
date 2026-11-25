-- +goose Up
-- An approach a design weighed, numbered by seq within the design. name, shape, cost, wins, loses and reconsider are the
-- approach's text. research is a JSON array of the step ids the approach rests on. recommended is 1 when the design
-- recommends the approach and 0 when it does not, and reason says why. session is a sessions row id, and at is unix
-- seconds.
CREATE TABLE design_approaches (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    name TEXT NOT NULL,
    shape TEXT NOT NULL,
    cost TEXT NOT NULL,
    wins TEXT NOT NULL,
    loses TEXT NOT NULL,
    reconsider TEXT NOT NULL,
    research TEXT NOT NULL,        -- JSON array of step ids
    recommended INTEGER NOT NULL CHECK (recommended IN (0, 1)),
    reason TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (design, seq)
);
-- The choice of an approach, numbered by seq within the design. approach is the seq of a design_approaches row of the
-- same design. words is what the choice said. maker is owner_serve, owner_conversation or conversation, and quote,
-- maker_reason and delegation are the maker columns design_questions holds. session is a sessions row id, and at is
-- unix seconds.
CREATE TABLE design_approach_choices (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    approach INTEGER NOT NULL,
    words TEXT NOT NULL,
    maker TEXT NOT NULL CHECK (maker IN ('owner_serve', 'owner_conversation', 'conversation')),
    quote TEXT NOT NULL,
    maker_reason TEXT NOT NULL,
    delegation TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (design, seq)
);
-- The owner's delegation of a design's decisions, numbered by seq within the design. kinds is a JSON array of strings,
-- and message is the owner's delegating message whole. session is a sessions row id, and at is unix seconds.
CREATE TABLE design_delegations (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    kinds TEXT NOT NULL,           -- JSON array of strings
    message TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (design, seq)
);
-- A conversation a design has had with the owner, numbered by seq within the design. The newest row names the design's
-- working conversation. session is the sessions row id of the conversation, and previous is the sessions row id of the
-- conversation it replaced, or '' for the first. at is unix seconds.
CREATE TABLE design_conversations (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    previous TEXT NOT NULL,        -- a sessions row id, or '' for the first; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (design, seq)
);
-- A line of a design's log, numbered by seq within the design. maker is owner, conversation or orreryd. what says what
-- the line records, and ids is a JSON array of strings naming the records it concerns. words is the text the line
-- carries. session is a sessions row id, and at is unix seconds.
CREATE TABLE design_log (
    design TEXT NOT NULL,
    seq INTEGER NOT NULL,
    at INTEGER NOT NULL,
    maker TEXT NOT NULL CHECK (maker IN ('owner', 'conversation', 'orreryd')),
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    what TEXT NOT NULL,
    ids TEXT NOT NULL,             -- JSON array of strings
    words TEXT NOT NULL,
    PRIMARY KEY (design, seq)
);
-- A design step a contract's stage read, with the contract version it read the step over. version is 0 for a draft.
-- The steps a contract read are listed by its contract column, which the index serves.
CREATE TABLE design_step_reads (
    step TEXT NOT NULL,
    contract TEXT NOT NULL,
    version INTEGER NOT NULL,      -- 0 for a draft
    PRIMARY KEY (step, contract)
);
CREATE INDEX design_step_reads_contract ON design_step_reads (contract);
-- The working copy of a contract the stage is writing, one row for each contract. version is the version it is over.
-- body is the EpicContract in protobuf JSON, and decisions is a JSON array of store.ContractDecision. writer is a
-- sessions row id, with no reference to sessions. created and updated are unix seconds, and an update keeps created.
CREATE TABLE contract_working (
    contract TEXT PRIMARY KEY,
    version INTEGER NOT NULL,
    body TEXT NOT NULL,
    decisions TEXT NOT NULL,
    writer TEXT NOT NULL,
    created INTEGER NOT NULL,
    updated INTEGER NOT NULL
);
-- A write to a row of a contract, numbered by seq within the contract. version is the version it belongs to, and 0 for a
-- draft. row names the row, and action is write, remove, keep or strike. depends and words are text the write records
-- with it. maker is owner_serve, owner_conversation or conversation, and '' when the writing session made the write on
-- its own account. quote, maker_reason and delegation are the maker columns design_questions holds. session is a sessions
-- row id, and at is unix seconds.
CREATE TABLE contract_row_writes (
    contract TEXT NOT NULL,
    seq INTEGER NOT NULL,
    version INTEGER NOT NULL,      -- 0 for a draft
    row TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('write', 'remove', 'keep', 'strike')),
    depends TEXT NOT NULL,
    words TEXT NOT NULL,
    maker TEXT NOT NULL CHECK (maker IN ('', 'owner_serve', 'owner_conversation', 'conversation')),
    quote TEXT NOT NULL,
    maker_reason TEXT NOT NULL,
    delegation TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (contract, seq)
);
-- A propose the stage made that the store refused, numbered by seq within the contract. version is the version the
-- refused propose would have made. field and text are what the refusal names and says. session is a sessions row id,
-- and at is unix seconds.
CREATE TABLE contract_refusals (
    contract TEXT NOT NULL,
    seq INTEGER NOT NULL,
    version INTEGER NOT NULL,
    field TEXT NOT NULL,
    text TEXT NOT NULL,
    session TEXT NOT NULL,         -- a sessions row id; no reference to sessions
    at INTEGER NOT NULL,
    PRIMARY KEY (contract, seq)
);
-- A row of a contract version the stage showed the owner. at is unix seconds.
CREATE TABLE contract_shown (
    contract TEXT NOT NULL,
    version INTEGER NOT NULL,
    row TEXT NOT NULL,
    at INTEGER NOT NULL,
    PRIMARY KEY (contract, version, row)
);
-- A state's maker is owner_serve, owner_conversation or conversation, and '' when the state has none, as every state
-- stored before this migration reads. quote, maker_reason and delegation are the maker columns design_questions holds,
-- and read '' on every state stored before this migration.
ALTER TABLE contract_states ADD COLUMN maker TEXT NOT NULL DEFAULT '' CHECK (maker IN ('', 'owner_serve', 'owner_conversation', 'conversation'));
ALTER TABLE contract_states ADD COLUMN quote TEXT NOT NULL DEFAULT '';
ALTER TABLE contract_states ADD COLUMN maker_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE contract_states ADD COLUMN delegation TEXT NOT NULL DEFAULT '';

-- +goose Down
-- The stage's record tables are dropped, and so are the maker columns of contract_states. Each contract keeps its other
-- columns and states, and the index on design_step_reads goes with its table.
DROP TABLE contract_shown;
DROP TABLE contract_refusals;
DROP TABLE contract_row_writes;
DROP TABLE contract_working;
DROP TABLE design_step_reads;
DROP TABLE design_log;
DROP TABLE design_conversations;
DROP TABLE design_delegations;
DROP TABLE design_approach_choices;
DROP TABLE design_approaches;
ALTER TABLE contract_states DROP COLUMN delegation;
ALTER TABLE contract_states DROP COLUMN maker_reason;
ALTER TABLE contract_states DROP COLUMN quote;
ALTER TABLE contract_states DROP COLUMN maker;
