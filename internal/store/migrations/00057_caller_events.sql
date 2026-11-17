-- +goose Up
-- creator marks the session that created the epic: 1 for the session whose newest call of the epic is epic_create, and
-- kept at 1 by a later call of that session. Rows stored before this migration are marked from their newest call.
ALTER TABLE epic_sessions ADD COLUMN creator INTEGER NOT NULL DEFAULT 0;
UPDATE epic_sessions SET creator = 1 WHERE call = 'epic_create';

-- caller_read is 1 once MarkStepRead has marked the step read for its caller.
ALTER TABLE steps ADD COLUMN caller_read INTEGER NOT NULL DEFAULT 0;

-- caller_events holds the events delivered to a caller, keyed by the event's id and the caller's sessions row id.
CREATE TABLE caller_events (
    event TEXT NOT NULL,
    session TEXT NOT NULL,
    at INTEGER NOT NULL,
    PRIMARY KEY (event, session)
);

-- The creator of each existing epic is not woken for state that predates this migration. Every question of the epic, in
-- any state, and the epic's phase when it has ended, are marked delivered to the creator under the ids the epic
-- caller-event pass sends. Only questions and phases that come later wake it.
INSERT OR IGNORE INTO caller_events (event, session, at)
SELECT 'epic:' || q.epic || ':Q' || q.seq, es.session, es.at
FROM epic_sessions es
JOIN epic_questions q ON q.epic = es.epic
WHERE es.creator = 1;

INSERT OR IGNORE INTO caller_events (event, session, at)
SELECT 'epic:' || es.epic || ':' || e.phase, es.session, es.at
FROM epic_sessions es
JOIN epics e ON e.id = es.epic
WHERE es.creator = 1 AND e.phase IN ('met_at_base', 'ended_with_gaps', 'released', 'done', 'stopped');

-- +goose Down
DROP TABLE caller_events;
ALTER TABLE steps DROP COLUMN caller_read;
ALTER TABLE epic_sessions DROP COLUMN creator;
