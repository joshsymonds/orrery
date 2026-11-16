-- +goose Up
ALTER TABLE epic_questions ADD COLUMN label TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN choices TEXT NOT NULL DEFAULT '[]';
ALTER TABLE epic_questions ADD COLUMN recommended TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN why TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN confidence TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN replaces INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_questions ADD COLUMN replaced_by INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_questions ADD COLUMN choice TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tells ADD COLUMN question INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epic_tells DROP COLUMN question;
ALTER TABLE epic_questions DROP COLUMN choice;
ALTER TABLE epic_questions DROP COLUMN replaced_by;
ALTER TABLE epic_questions DROP COLUMN replaces;
ALTER TABLE epic_questions DROP COLUMN confidence;
ALTER TABLE epic_questions DROP COLUMN why;
ALTER TABLE epic_questions DROP COLUMN recommended;
ALTER TABLE epic_questions DROP COLUMN choices;
ALTER TABLE epic_questions DROP COLUMN label;
