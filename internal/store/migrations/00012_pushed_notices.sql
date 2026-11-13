-- +goose Up
CREATE TABLE pushed_notices (
    pane TEXT NOT NULL,
    session TEXT NOT NULL,
    time INTEGER NOT NULL,
    PRIMARY KEY (pane, session)
);

-- +goose Down
DROP TABLE pushed_notices;
