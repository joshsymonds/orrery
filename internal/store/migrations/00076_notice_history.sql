-- +goose Up
-- The notices the router delivered, kept as the history a client reads back: each row is one delivered notice, with its
-- key (the pane, session, epic, usage limit or alert it is about, exactly one of which is set), its state, title and
-- body, the block and block reference it names, the unix second the router delivered it, and whether a client has read
-- it. A notice recorded under a key whose newest row is still unread replaces that row's text and time rather than
-- adding one, so a key holds at most one unread row, and a row a client has read stays as a record of what was told.
CREATE TABLE notice_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pane TEXT NOT NULL DEFAULT '',
    session TEXT NOT NULL DEFAULT '',
    epic TEXT NOT NULL DEFAULT '',
    limit_key TEXT NOT NULL DEFAULT '',
    alert TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    block TEXT NOT NULL DEFAULT '',
    block_ref TEXT NOT NULL DEFAULT '',
    time INTEGER NOT NULL,
    read BOOLEAN NOT NULL DEFAULT 0
);

CREATE INDEX notice_history_time ON notice_history (time);

CREATE INDEX notice_history_key ON notice_history (pane, session, epic, limit_key, alert, id);

-- +goose Down
DROP INDEX notice_history_key;
DROP INDEX notice_history_time;
DROP TABLE notice_history;
