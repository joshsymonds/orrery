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

-- name: CountEnginePrompts :one
SELECT COUNT(*) FROM events
WHERE session_id = sqlc.arg(session_id) AND kind = 'engine_prompt' AND json_extract(payload, '$.promptId') = sqlc.arg(prompt_id);

-- name: InsertEnginePromptFailed :exec
INSERT INTO engine_prompt_failed (session_id, prompt_id, reason) VALUES (?, ?, ?)
ON CONFLICT (session_id, prompt_id) DO NOTHING;

-- name: GetEnginePromptFailure :one
SELECT reason FROM engine_prompt_failed WHERE session_id = ? AND prompt_id = ?;
