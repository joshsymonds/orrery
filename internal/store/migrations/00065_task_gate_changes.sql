-- +goose Up
-- The gate-needed changes a task's implementers declared: the changes the task's own gate or evidence needed that its
-- Deliverable does not name, as a JSON array of objects with path and reason, one per file in the order first declared,
-- and [] while none are declared. Reviewers and verifiers read a declared change as in scope when the gate needs it.
ALTER TABLE epic_tasks ADD COLUMN gate_changes TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE epic_tasks DROP COLUMN gate_changes;
