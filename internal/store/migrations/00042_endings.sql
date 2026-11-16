-- +goose Up
ALTER TABLE epics ADD COLUMN phase_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epics ADD COLUMN report_phase TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN report_shipped TEXT NOT NULL DEFAULT '[]';
ALTER TABLE epics ADD COLUMN report_not_shipped TEXT NOT NULL DEFAULT '[]';
ALTER TABLE epics ADD COLUMN report_why TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epics DROP COLUMN report_why;
ALTER TABLE epics DROP COLUMN report_not_shipped;
ALTER TABLE epics DROP COLUMN report_shipped;
ALTER TABLE epics DROP COLUMN report_phase;
ALTER TABLE epics DROP COLUMN phase_at;
