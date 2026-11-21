-- +goose Up
-- The texts serve sends to a pane, one row per send, with the client that sent it. text is cut to 4096 bytes and client
-- to 64 bytes before a row is written. Nothing prunes a row, so the sends to a pane stay as they were sent.
CREATE TABLE pane_sends (
    id INTEGER PRIMARY KEY,
    pane TEXT NOT NULL,
    text TEXT NOT NULL,
    client TEXT NOT NULL,
    at INTEGER NOT NULL
);
CREATE INDEX pane_sends_pane_at ON pane_sends (pane, at);

-- +goose Down
DROP TABLE pane_sends;
