-- +goose Up
-- engine_held goes: a Say is written to Claude at once, and Claude's own queue replaces the held set, so no message
-- waits in the store for a turn to end.
DROP TABLE engine_held;

-- +goose Down
CREATE TABLE engine_held (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    text TEXT NOT NULL,
    UNIQUE (session_id, prompt_id)
);
