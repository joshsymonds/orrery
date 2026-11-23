-- +goose Up
-- The LLM accounts orrery stores, one row each, keyed by the account's id. token is the account's token in plain text,
-- which an account never returns, and fingerprint identifies that token without showing it. plan is '' when the account
-- has none, and created is when the account was added, in unix seconds.
CREATE TABLE llm_accounts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    plan TEXT NOT NULL DEFAULT '',
    token TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    created INTEGER NOT NULL
);

-- The route registry, a single row whose id the CHECK holds at 1. default_context is the id of the context a request
-- uses when it names none.
CREATE TABLE route_registry (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    default_context TEXT NOT NULL
);

-- The seats of the route registry, one row each, keyed by the seat's id. auth_mode says how a seat authenticates
-- upstream. Each text column is '' when the seat leaves it unset, each flag is 0 or 1, and max_input_tokens is 0 for no
-- limit. strip_betas is a JSON array of the beta feature names the seat removes, and '[]' when it removes none.
CREATE TABLE route_seats (
    id TEXT PRIMARY KEY,
    auth_mode TEXT NOT NULL CHECK (auth_mode IN ('forward', 'inject', 'sigv4')),
    upstream TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    api_key_env_file TEXT NOT NULL DEFAULT '',
    aws_profile TEXT NOT NULL DEFAULT '',
    aws_region TEXT NOT NULL DEFAULT '',
    model_map_env_file TEXT NOT NULL DEFAULT '',
    billing TEXT NOT NULL DEFAULT '',
    speed TEXT NOT NULL DEFAULT '',
    cache_ttl TEXT NOT NULL DEFAULT '',
    insecure INTEGER NOT NULL DEFAULT 0 CHECK (insecure IN (0, 1)),
    strip_cache_control INTEGER NOT NULL DEFAULT 0 CHECK (strip_cache_control IN (0, 1)),
    strict_structured_output INTEGER NOT NULL DEFAULT 0 CHECK (strict_structured_output IN (0, 1)),
    max_input_tokens INTEGER NOT NULL DEFAULT 0,
    strip_betas TEXT NOT NULL DEFAULT '[]'
);

-- The contexts of the route registry, one row each, keyed by the context's id. account is the id of the account whose
-- token the context's forward seats send, and '' means the caller's own login. has_subagents is 1 when the context
-- routes its subagents, and subagent_default_seat is the seat its subagents go through when no binding names another,
-- '' when it has none.
CREATE TABLE route_contexts (
    id TEXT PRIMARY KEY,
    default_seat TEXT NOT NULL,
    account TEXT NOT NULL DEFAULT '',
    has_subagents INTEGER NOT NULL DEFAULT 0 CHECK (has_subagents IN (0, 1)),
    subagent_default_seat TEXT NOT NULL DEFAULT ''
);

-- The seat each context names for a model, for a subagent's agent type and for a subagent's model, one row per
-- binding. scope says which of the three the selector is, and seat is the seat id the binding names. A context names
-- each selector at most once per scope.
CREATE TABLE route_bindings (
    context TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('model', 'subagent_agent_type', 'subagent_model')),
    selector TEXT NOT NULL,
    seat TEXT NOT NULL,
    PRIMARY KEY (context, scope, selector)
);

-- +goose Down
DROP TABLE route_bindings;
DROP TABLE route_contexts;
DROP TABLE route_seats;
DROP TABLE route_registry;
DROP TABLE llm_accounts;
