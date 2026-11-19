-- +goose Up
-- The owner's put-off marks on questions, one row per question the owner put off. kind is headless or pane; question is
-- a headless request's request_id, or a pane's question key. A mark is inert once its question closes, and nothing
-- prunes it.
CREATE TABLE put_off_questions (
    kind TEXT NOT NULL,
    session_id TEXT NOT NULL,
    question TEXT NOT NULL,
    put_off_at INTEGER NOT NULL,
    PRIMARY KEY (kind, session_id, question)
);

-- +goose Down
DROP TABLE put_off_questions;
