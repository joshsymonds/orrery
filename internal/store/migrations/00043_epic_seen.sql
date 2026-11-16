-- +goose Up
CREATE TABLE epic_seen (
    epic TEXT NOT NULL PRIMARY KEY,
    at INTEGER NOT NULL
);

-- Every epic already ended counts as seen at the moment it reached that phase.
INSERT INTO epic_seen (epic, at)
SELECT id, phase_at FROM epics
WHERE phase IN ('met_at_base', 'clean', 'ended_with_gaps', 'released', 'done', 'stopped');

-- +goose Down
DROP TABLE epic_seen;
