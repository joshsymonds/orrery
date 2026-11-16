-- +goose Up
CREATE TABLE epic_sessions (
    epic TEXT NOT NULL,
    session TEXT NOT NULL,
    call TEXT NOT NULL,
    at INTEGER NOT NULL,
    PRIMARY KEY (epic, session)
);

-- +goose Down
DROP TABLE epic_sessions;
