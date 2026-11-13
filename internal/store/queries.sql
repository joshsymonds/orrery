-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessions :many
SELECT * FROM sessions WHERE kind = '' ORDER BY id;

-- name: ListServerSessions :many
SELECT * FROM sessions WHERE server = ? AND kind = '' ORDER BY id;

-- name: InsertSession :exec
INSERT INTO sessions (id, server, pane) VALUES (?, ?, ?);

-- name: UpdateSessionFields :exec
UPDATE sessions
SET state = ?, changed = ?, seen = ?, question = ?, summary = ?,
    label = ?, context = ?, tool = ?, claude_session = ?, transcript = ?
WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: InsertEvent :exec
INSERT INTO events (session_id, kind, at, payload) VALUES (?, ?, ?, ?);

-- name: ListEvents :many
SELECT * FROM events ORDER BY seq;

-- name: NewestEventSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) AS seq FROM events;

-- name: OldestEventSeq :one
SELECT CAST(COALESCE(MIN(seq), 0) AS INTEGER) AS seq FROM events;

-- name: EventPageAfter :many
SELECT seq, session_id, kind, at, payload FROM events WHERE seq > ? ORDER BY seq LIMIT ?;

-- name: PruneEvents :exec
DELETE FROM events WHERE events.at < ? AND events.seq < (SELECT MAX(newest.seq) FROM events AS newest);

-- name: DeleteStaleEngineText :exec
DELETE FROM engine_text
WHERE session_id IN (
    SELECT sessions.id FROM sessions
    WHERE sessions.kind = 'engine'
      AND NOT EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= sqlc.arg(cutoff))
      AND sessions.id NOT IN (SELECT value FROM json_each(sqlc.arg(live)))
);

-- name: DeleteStaleEnginePromptFailures :exec
DELETE FROM engine_prompt_failed
WHERE session_id IN (
    SELECT sessions.id FROM sessions
    WHERE sessions.kind = 'engine'
      AND NOT EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= sqlc.arg(cutoff))
      AND sessions.id NOT IN (SELECT value FROM json_each(sqlc.arg(live)))
);

-- name: DeleteStaleEngineSessions :exec
DELETE FROM sessions
WHERE sessions.kind = 'engine'
  AND NOT EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= sqlc.arg(cutoff))
  AND sessions.id NOT IN (SELECT value FROM json_each(sqlc.arg(live)));

-- name: InsertEngineSession :exec
INSERT INTO sessions (id, server, pane, kind, dir, state, changed) VALUES (?, '', '', ?, ?, ?, ?);

-- name: GetEngineSession :one
SELECT * FROM sessions WHERE id = ? AND kind IN ('engine', 'step');

-- name: ListEngineSessions :many
SELECT * FROM sessions WHERE kind IN ('engine', 'step') ORDER BY id;

-- name: ListKnownDirs :many
SELECT known.dir FROM (
    SELECT sessions.dir AS dir, CAST(sessions.changed AS INTEGER) AS at FROM sessions
    WHERE sessions.kind = 'engine'
    UNION ALL
    SELECT steps.workspace AS dir, steps.started AS at FROM steps
) AS known
WHERE known.dir <> ''
GROUP BY known.dir
ORDER BY MAX(known.at) DESC, known.dir;

-- name: ListHeadlessSessions :many
SELECT
    sessions.id, sessions.dir, sessions.state, sessions.changed, sessions.seen, sessions.question, sessions.reason,
    CAST(COALESCE((
        SELECT json_extract(events.payload, '$.text') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_block'
          AND json_extract(events.payload, '$.kind') = 'text'
          AND json_extract(events.payload, '$.role') = 'assistant'
          AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = ''
        ORDER BY events.at DESC, events.seq DESC LIMIT 1
    ), '') AS TEXT) AS reply,
    CAST(COALESCE((
        SELECT MAX(events.at) FROM events
        WHERE events.session_id = sessions.id AND (
            (events.kind = 'engine_block' AND json_extract(events.payload, '$.kind') = 'text'
             AND json_extract(events.payload, '$.role') = 'assistant'
             AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = '')
            OR (events.kind = 'engine_request' AND json_extract(events.payload, '$.refused') IS NOT 1)
        )
    ), 0) AS INTEGER) AS newest_reply
FROM sessions
WHERE sessions.kind = 'engine'
  AND EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= sqlc.arg(cutoff))
ORDER BY sessions.id;

-- name: UpdateEngineSession :execrows
UPDATE sessions
SET state = ?, changed = ?, seen = ?, question = ?, claude_session = ?, reason = ?
WHERE id = ? AND kind IN ('engine', 'step');

-- name: UpsertEngineText :exec
INSERT INTO engine_text (session_id, message_id, block_index, parent_tool_use_id, kind, text)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, message_id, block_index, parent_tool_use_id)
DO UPDATE SET kind = excluded.kind, text = excluded.text;

-- name: DeleteEngineText :exec
DELETE FROM engine_text
WHERE session_id = ? AND message_id = ? AND block_index = ? AND parent_tool_use_id = ?;

-- name: ClearEngineText :exec
DELETE FROM engine_text WHERE session_id = ?;

-- name: ListEngineText :many
SELECT * FROM engine_text WHERE session_id = ?
ORDER BY message_id, parent_tool_use_id, block_index;

-- name: ListUnfinishedEngineText :many
SELECT * FROM engine_text
ORDER BY session_id, message_id, parent_tool_use_id, block_index;

-- name: SessionEventsAfter :many
SELECT seq, session_id, kind, at, payload FROM events
WHERE session_id = ? AND seq > ?
ORDER BY seq LIMIT ?;

-- name: LastTurnEnds :many
SELECT events.session_id, CAST(MAX(events.at) AS INTEGER) AS at FROM events
WHERE events.kind = 'session_changed' AND json_extract(events.payload, '$.fields.state') = 'done'
GROUP BY events.session_id
ORDER BY events.session_id;

-- name: CountEnginePrompts :one
SELECT COUNT(*) FROM events
WHERE session_id = sqlc.arg(session_id) AND kind = 'engine_prompt' AND json_extract(payload, '$.promptId') = sqlc.arg(prompt_id);

-- name: InsertEnginePromptFailed :exec
INSERT INTO engine_prompt_failed (session_id, prompt_id, reason) VALUES (?, ?, ?)
ON CONFLICT (session_id, prompt_id) DO NOTHING;

-- name: GetEnginePromptFailure :one
SELECT reason FROM engine_prompt_failed WHERE session_id = ? AND prompt_id = ?;

-- name: InsertStep :exec
INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree, done_value, read_only, owned, gate,
                   gate_name, gate_source, gate_needs, gate_limit, model_limit, started, checks, result, status,
                   end_value, reason, ended, doing, verdicts, epic, task)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '', '', '', '', 0, '', '[]', ?, ?);

-- name: GetStep :one
SELECT * FROM steps WHERE id = ?;

-- name: ListOpenSteps :many
SELECT * FROM steps WHERE end_value = '' ORDER BY id;

-- name: ListLabelledSteps :many
SELECT * FROM steps
WHERE epic <> '' AND (CAST(sqlc.arg(epic) AS TEXT) = '' OR epic = CAST(sqlc.arg(epic) AS TEXT))
  AND (CAST(sqlc.arg(task) AS TEXT) = '' OR task = CAST(sqlc.arg(task) AS TEXT))
ORDER BY started, id;

-- name: UpdateStepReturn :exec
UPDATE steps SET result = ?, checks = ? WHERE id = ?;

-- name: UpdateStepWatch :exec
UPDATE steps SET verdicts = ?, doing = ? WHERE id = ?;

-- name: UpdateStepEnd :exec
UPDATE steps SET end_value = ?, status = ?, reason = ?, ended = ? WHERE id = ?;

-- name: InsertGrant :exec
INSERT INTO grants (id, project, client_pid, client_start, pools, command, granted) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertGrantMember :exec
INSERT INTO grant_members (member, grant_id, pool, position) VALUES (?, ?, ?, ?);

-- name: CountMemberGrants :one
SELECT COUNT(*) FROM grant_members WHERE member = ?;

-- name: DeleteGrantMembers :exec
DELETE FROM grant_members WHERE grant_id = ?;

-- name: DeleteGrant :exec
DELETE FROM grants WHERE id = ?;

-- name: ListGrants :many
SELECT * FROM grants ORDER BY granted, id;

-- name: ListGrantMembers :many
SELECT * FROM grant_members ORDER BY grant_id, position;

-- name: InsertHold :exec
INSERT OR IGNORE INTO holds (member, since) VALUES (?, ?);

-- name: DeleteHold :exec
DELETE FROM holds WHERE member = ?;

-- name: ListHolds :many
SELECT * FROM holds ORDER BY member;

-- name: UpsertPendingNotice :exec
INSERT INTO pending_notices (id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, source, silent)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET pane = excluded.pane, session = excluded.session, state = excluded.state,
    time = excluded.time, phase = excluded.phase, due = excluded.due, delay_served = excluded.delay_served,
    watch_logged = excluded.watch_logged, source_kind = excluded.source_kind, source = excluded.source,
    silent = excluded.silent;

-- name: RaiseNoticeIDFloor :exec
UPDATE pending_notice_ids SET high = MAX(high, sqlc.arg(id)) WHERE id = 1;

-- name: DeletePendingNotice :exec
DELETE FROM pending_notices WHERE id = ?;

-- name: ListPendingNotices :many
SELECT * FROM pending_notices ORDER BY id;

-- name: GetNoticeIDFloor :one
SELECT high FROM pending_notice_ids WHERE id = 1;
