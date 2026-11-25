-- +goose Up
-- Two declared fields of an epic's gate. always is 1 for a command gate that runs on every verification of an
-- integrate candidate or a task integrator round, whatever the verification choice picks, and 0 otherwise. note says
-- what the gate covers, for the judge that makes that choice to read, and is '' for none. Both are added at the end
-- of epic_gates with defaults, so every stored gate reads back as not always and with no note, as it ran before.
ALTER TABLE epic_gates ADD COLUMN always INTEGER NOT NULL DEFAULT 0 CHECK (always IN (0, 1));
ALTER TABLE epic_gates ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_gates DROP COLUMN note;
ALTER TABLE epic_gates DROP COLUMN always;
