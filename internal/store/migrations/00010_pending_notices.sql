-- +goose Up
CREATE TABLE pending_notices (
    id INTEGER PRIMARY KEY,
    pane TEXT NOT NULL,
    session TEXT NOT NULL,
    state TEXT NOT NULL,
    time INTEGER NOT NULL,
    phase TEXT NOT NULL,
    due INTEGER NOT NULL,
    delay_served BOOLEAN NOT NULL,
    watch_logged BOOLEAN NOT NULL,
    source_kind TEXT NOT NULL,
    source BLOB
);

CREATE TABLE pending_notice_ids (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    high INTEGER NOT NULL
);

INSERT INTO pending_notice_ids (id, high) VALUES (1, 0);

-- +goose Down
DROP TABLE pending_notice_ids;
DROP TABLE pending_notices;
