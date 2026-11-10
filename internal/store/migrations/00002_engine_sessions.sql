-- +goose Up
ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN dir TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN reason TEXT NOT NULL DEFAULT '';

CREATE TABLE engine_text (
    session_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    block_index INTEGER NOT NULL,
    parent_tool_use_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (session_id, message_id, block_index, parent_tool_use_id)
);

CREATE INDEX events_session ON events (session_id, seq);

-- +goose Down
DROP INDEX events_session;
DROP TABLE engine_text;
ALTER TABLE sessions DROP COLUMN reason;
ALTER TABLE sessions DROP COLUMN dir;
ALTER TABLE sessions DROP COLUMN kind;
