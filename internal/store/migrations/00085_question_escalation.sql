-- +goose Up
-- A question is escalated when the chain that answers questions could not answer it and passes it on. escalated is the
-- Unix second of the escalation, 0 while the question is not escalated. The reason, and the maker and its session, are
-- what the escalation names, and escalation_told is set once the owner has been told of it. Existing rows keep the
-- defaults: no backfill, so an earlier question reads as never escalated.
ALTER TABLE epic_questions ADD COLUMN escalated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_questions ADD COLUMN escalation_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN escalation_maker TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN escalation_session TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_questions ADD COLUMN escalation_told INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epic_questions DROP COLUMN escalation_told;
ALTER TABLE epic_questions DROP COLUMN escalation_session;
ALTER TABLE epic_questions DROP COLUMN escalation_maker;
ALTER TABLE epic_questions DROP COLUMN escalation_reason;
ALTER TABLE epic_questions DROP COLUMN escalated;
