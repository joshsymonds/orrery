-- +goose Up
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    server TEXT NOT NULL,
    pane TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT '',
    changed TEXT NOT NULL DEFAULT '',
    seen TEXT NOT NULL DEFAULT '',
    question TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT '',
    tool TEXT NOT NULL DEFAULT '',
    claude_session TEXT NOT NULL DEFAULT '',
    transcript TEXT NOT NULL DEFAULT ''
);

CREATE TABLE events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    at INTEGER NOT NULL,
    payload TEXT NOT NULL
);

CREATE INDEX events_at ON events (at);

-- +goose Down
DROP INDEX events_at;
DROP TABLE events;
DROP TABLE sessions;
