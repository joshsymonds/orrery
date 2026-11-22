-- +goose Up
-- closure_verdicts holds the newest closure verdict of each pane or headless session, replaced on each verdict.
CREATE TABLE closure_verdicts (
    session_id TEXT PRIMARY KEY,
    changed INTEGER NOT NULL,
    closed INTEGER NOT NULL,
    why TEXT NOT NULL,
    at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE closure_verdicts;
