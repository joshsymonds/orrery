-- +goose Up
-- epic_question_calls holds the call each planner turn made on an open question a planner turn asked, one row for each
-- question a turn called: stands, the question still waits on its answer, or overtaken, the turn went on without it and
-- closed it as overtaken. reason is the turn's reason for the call. A call on a question that was no longer open when
-- its turn completed is not stored. Like every epic table, its epic column moves the epic's revision through the store's
-- pre-update hook, so no trigger is made (see 00072_epic_revisions_table.sql).
CREATE TABLE epic_question_calls (
    epic TEXT NOT NULL,
    question INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    call TEXT NOT NULL CHECK (call IN ('stands', 'overtaken')),
    reason TEXT NOT NULL,
    PRIMARY KEY (epic, question, turn)
);

-- Every pending trigger of an epic in a final phase (met_at_base, released, done or a closed phase) is consumed. No
-- planner turn runs for such an epic, so a trigger left pending there, such as the answer: trigger of an answer recorded
-- after the epic ended, is claimed by none, and the planner listed the epic on every pass for it. An epic that enters a
-- final phase now consumes its pending triggers in that write.
UPDATE epic_triggers SET state = 'consumed'
WHERE state = 'pending' AND epic IN (
    SELECT id FROM epics
    WHERE phase IN ('met_at_base', 'released', 'done', 'closed_superseded', 'closed_shipped', 'closed_abandoned')
);

-- +goose Down
-- The calls go. The consumed triggers are fixed forward, not restored: they stay consumed, since no turn would claim
-- them.
DROP TABLE epic_question_calls;
