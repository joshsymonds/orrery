-- +goose Up
CREATE TABLE requests (
    id INTEGER PRIMARY KEY,
    started_at INTEGER NOT NULL,
    completed_at INTEGER,
    account TEXT NOT NULL,
    seat TEXT NOT NULL,
    context TEXT NOT NULL,
    requested_model TEXT NOT NULL,
    resolved_model TEXT NOT NULL DEFAULT '',
    claude_session TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    billed_cost_nano_usd INTEGER NULL,
    list_cost_nano_usd INTEGER NULL
);

CREATE INDEX requests_claude_session ON requests (claude_session);
CREATE INDEX requests_started_at ON requests (started_at);

CREATE TABLE request_claims (
    request_id INTEGER NOT NULL REFERENCES requests(id),
    claim TEXT NOT NULL,
    utilization REAL,
    reset_at INTEGER,
    status TEXT NOT NULL,
    PRIMARY KEY (request_id, claim)
);

CREATE TABLE limit_readings (
    account TEXT NOT NULL,
    claim TEXT NOT NULL,
    utilization REAL,
    reset_at INTEGER,
    status TEXT NOT NULL,
    observed_at INTEGER NOT NULL,
    request_id INTEGER NOT NULL,
    dropped INTEGER NOT NULL DEFAULT 0 CHECK (dropped IN (0,1)),
    PRIMARY KEY (account, claim)
);

CREATE TABLE limit_claim_models (
    account TEXT NOT NULL,
    claim TEXT NOT NULL,
    model TEXT NOT NULL,
    PRIMARY KEY (account, claim, model)
);

-- +goose Down
DROP TABLE limit_claim_models;
DROP TABLE limit_readings;
DROP TABLE request_claims;
DROP INDEX requests_started_at;
DROP INDEX requests_claude_session;
DROP TABLE requests;
