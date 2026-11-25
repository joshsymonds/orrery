-- +goose Up
-- The triage of a gate check that ran and failed: one row per triaged check, keyed by the epic and the check's id. path
-- is the node run the check belongs to, and step the read-only step that judged the failure. verdict is environment
-- when the failure came from the machine the gate ran on, flake when the gate failed by chance, and change when the
-- change under test failed it, which is also the verdict of a triage that did not start or whose answer was refused.
-- evidence is the line or lines of the check's output an environment or flake verdict quotes, citation the failing test
-- or lint line a change verdict cites, and reason what the judge, or the refusal, says. Each text column is '' when the
-- triage had none. Nothing deletes or updates a row, so each triage stays as it was recorded: a repeat writes nothing.
CREATE TABLE gate_triages (
    epic TEXT NOT NULL,
    check_id TEXT NOT NULL,
    path TEXT NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('environment', 'flake', 'change')),
    evidence TEXT NOT NULL DEFAULT '',
    citation TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, check_id)
);

-- +goose Down
DROP TABLE gate_triages;
