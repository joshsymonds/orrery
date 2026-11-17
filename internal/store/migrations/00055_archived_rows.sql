-- +goose Up
-- The owner's archive marks on list rows, by kind (epic, session or headless) and id. A row is archived while archived_at
-- is set and unarchived_at is 0, and an unarchive keeps its time in unarchived_at, so the owner's unarchive is remembered.
-- Neither time refers to a row in another table: an epic with only a record has a mark like any other.
CREATE TABLE archived_rows (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    archived_at INTEGER NOT NULL DEFAULT 0,
    unarchived_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (kind, id)
);

-- +goose Down
DROP TABLE archived_rows;
