-- +goose Up
-- The lifecycle states of the prompts sent to an engine session, keyed by the session and the prompt's write id. A row
-- records one state a prompt takes, and seq orders a prompt's rows, so its newest state is the row with the greatest seq.
CREATE TABLE prompt_states (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    write_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'started', 'completed', 'canceled', 'discarded'))
);
CREATE INDEX prompt_states_write ON prompt_states (session_id, write_id, seq);

-- +goose Down
DROP TABLE prompt_states;
