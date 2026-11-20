-- +goose Up
-- An epic's steerer is the session that steers it. Each existing epic is steered by its creator: the session of its newest epic_sessions row with creator = 1, ordered as GetEpicCreator
-- orders them, at DESC and then by session. The session is named by its id alone and is not joined to sessions, so a
-- creator whose sessions row is gone still steers. An epic with no creator row keeps the empty steerer, so an epic that
-- ended before this migration with no creator gets none.
-- A decision's maker and maker_session name who made it and the session of that maker. A decision that exists now
-- keeps both empty: the writes that record a maker set them, and this migration does not backfill them.
ALTER TABLE epics ADD COLUMN steerer TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_decisions ADD COLUMN maker TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_decisions ADD COLUMN maker_session TEXT NOT NULL DEFAULT '';

UPDATE epics SET steerer = COALESCE((
    SELECT epic_sessions.session FROM epic_sessions
    WHERE epic_sessions.epic = epics.id AND epic_sessions.creator = 1
    ORDER BY epic_sessions.at DESC, epic_sessions.session
    LIMIT 1
), '');

-- +goose Down
ALTER TABLE epic_decisions DROP COLUMN maker_session;
ALTER TABLE epic_decisions DROP COLUMN maker;
ALTER TABLE epics DROP COLUMN steerer;
