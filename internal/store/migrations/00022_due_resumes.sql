-- +goose Up
CREATE TABLE due_resumes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('step', 'epic')),
    target TEXT NOT NULL,
    resets INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('due', 'done'))
);

ALTER TABLE epics ADD COLUMN pause_source TEXT NOT NULL DEFAULT '' CHECK (pause_source IN ('', 'owner', 'limit'));

-- +goose Down
ALTER TABLE epics DROP COLUMN pause_source;
DROP TABLE due_resumes;
