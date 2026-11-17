-- +goose Up
-- The messages a Say holds for a headless session whose turn is running, until the turn that follows writes them. seq
-- orders a session's held messages, and prompt_id is the Say's own id or one orreryd assigns, unique within its session.
CREATE TABLE engine_held (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    text TEXT NOT NULL,
    UNIQUE (session_id, prompt_id)
);

-- +goose Down
DROP TABLE engine_held;
