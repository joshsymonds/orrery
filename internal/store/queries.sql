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
SELECT seq, session_id, kind, at, payload, write_id, answered FROM events WHERE seq > ? ORDER BY seq LIMIT ?;

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

-- name: GetEngineFirstPrompt :one
SELECT CAST(COALESCE((
    SELECT json_extract(events.payload, '$.text') FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
    ORDER BY events.seq LIMIT 1
), '') AS TEXT) AS first_prompt
FROM sessions WHERE sessions.id = ?;

-- name: SetSessionTitle :exec
UPDATE sessions SET title = ? WHERE id = ? AND kind = 'engine' AND title = '';

-- name: ListUntitledEngineSessions :many
SELECT sessions.id, sessions.dir, CAST(COALESCE((
    SELECT json_extract(events.payload, '$.text') FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
    ORDER BY events.seq LIMIT 1
), '') AS TEXT) AS first_prompt
FROM sessions
WHERE sessions.kind = 'engine' AND sessions.title = ''
  AND EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt')
ORDER BY sessions.id;

-- name: GetUntitledEngineSession :one
SELECT sessions.id, sessions.dir, CAST(COALESCE((
    SELECT json_extract(events.payload, '$.text') FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
    ORDER BY events.seq LIMIT 1
), '') AS TEXT) AS first_prompt
FROM sessions
WHERE sessions.kind = 'engine' AND sessions.title = ''
  AND EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt')
  AND sessions.id = ?;

-- name: SetSessionHeadline :exec
UPDATE sessions SET headline = sqlc.arg(headline), headline_at = sqlc.arg(headline_at)
WHERE id = sqlc.arg(id) AND kind = 'engine';

-- name: ListHeadlineDueSessions :many
SELECT sessions.id, sessions.dir, sessions.headline_at, CAST(COALESCE((
    SELECT MAX(events.at) FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_turn_ended'
), 0) AS INTEGER) AS turn_at, CAST(COALESCE((
    SELECT json_extract(events.payload, '$.text') FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
    ORDER BY events.seq LIMIT 1
), '') AS TEXT) AS first_prompt
FROM sessions
WHERE sessions.kind = 'engine'
  AND EXISTS (
      SELECT 1 FROM events
      WHERE events.session_id = sessions.id AND events.kind = 'engine_turn_ended' AND events.at > sessions.headline_at
  )
ORDER BY sessions.id;

-- name: GetHeadlineDueSession :one
SELECT sessions.id, sessions.dir, sessions.headline_at, CAST(COALESCE((
    SELECT MAX(events.at) FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_turn_ended'
), 0) AS INTEGER) AS turn_at, CAST(COALESCE((
    SELECT json_extract(events.payload, '$.text') FROM events
    WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
    ORDER BY events.seq LIMIT 1
), '') AS TEXT) AS first_prompt
FROM sessions
WHERE sessions.kind = 'engine'
  AND EXISTS (
      SELECT 1 FROM events
      WHERE events.session_id = sessions.id AND events.kind = 'engine_turn_ended' AND events.at > sessions.headline_at
  )
  AND sessions.id = ?;

-- name: ListRecentTurns :many
SELECT CAST(CASE WHEN events.kind = 'engine_prompt' THEN 'user' ELSE 'assistant' END AS TEXT) AS role,
       CAST(json_extract(events.payload, '$.text') AS TEXT) AS text
FROM events
WHERE events.session_id = sqlc.arg(id)
  AND (events.kind = 'engine_prompt' OR (
      events.kind = 'engine_block'
      AND json_extract(events.payload, '$.kind') = 'text'
      AND json_extract(events.payload, '$.role') = 'assistant'
      AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = ''
  ))
ORDER BY events.seq DESC
LIMIT sqlc.arg(items);

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
    sessions.title, sessions.headline,
    sessions.latest_reply AS reply,
    sessions.newest_reply_at AS newest_reply,
    sessions.first_prompt_text AS first_prompt,
    sessions.latest_request_id AS request_id
FROM sessions
WHERE sessions.kind = 'engine'
  AND EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= sqlc.arg(cutoff))
ORDER BY sessions.id;

-- name: UpdateEngineSession :execrows
UPDATE sessions
SET state = ?, changed = ?, seen = ?, question = ?, claude_session = ?, reason = ?
WHERE id = ? AND kind IN ('engine', 'step');

-- name: SetSessionLatestReply :exec
UPDATE sessions
SET latest_reply = sqlc.arg(latest_reply),
    newest_reply_at = MAX(newest_reply_at, CAST(sqlc.arg(at) AS INTEGER))
WHERE id = sqlc.arg(id);

-- name: SetSessionLatestRequest :exec
UPDATE sessions
SET latest_request_id = sqlc.arg(request_id),
    newest_reply_at = MAX(newest_reply_at, CAST(sqlc.arg(at) AS INTEGER))
WHERE id = sqlc.arg(id);

-- name: SetSessionFirstPrompt :exec
UPDATE sessions
SET first_prompt_text = sqlc.arg(text)
WHERE id = sqlc.arg(id) AND first_prompt_text = '';

-- name: UpdateEngineConsumed :execrows
UPDATE sessions
SET consumed = sqlc.arg(consumed),
    replay_from = COALESCE(sqlc.narg(replay_from), replay_from),
    turn_output = COALESCE(sqlc.narg(turn_output), turn_output),
    background = COALESCE(sqlc.narg(background), background),
    awaited = COALESCE(sqlc.narg(awaited), awaited)
WHERE id = sqlc.arg(id) AND kind IN ('engine', 'step');

-- name: UpdateEngineLaunch :execrows
UPDATE sessions
SET consumed = 0, replay_from = 0, background = 0, awaited = 0, prompts_from = sqlc.arg(prompts_from)
WHERE id = sqlc.arg(id) AND kind IN ('engine', 'step');

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
SELECT seq, session_id, kind, at, payload, write_id, answered FROM events
WHERE session_id = ? AND seq > ?
ORDER BY seq LIMIT ?;

-- name: SessionEventsBefore :many
SELECT seq, session_id, kind, at, payload, write_id, answered FROM events
WHERE session_id = ? AND seq < ?
ORDER BY seq DESC LIMIT ?;

-- name: LastTurnEnds :many
SELECT events.session_id, CAST(MAX(events.at) AS INTEGER) AS at FROM events
WHERE events.kind = 'session_changed' AND json_extract(events.payload, '$.fields.state') = 'done'
GROUP BY events.session_id
ORDER BY events.session_id;

-- name: CountEnginePrompts :one
SELECT COUNT(*) FROM events AS prompts
WHERE prompts.session_id = sqlc.arg(session_id) AND prompts.kind = 'engine_prompt'
  AND json_extract(prompts.payload, '$.promptId') = sqlc.arg(prompt_id)
  AND NOT (COALESCE(json_extract(prompts.payload, '$.sender'), '') = 'event' AND EXISTS (
      SELECT 1 FROM prompt_states AS states
      WHERE states.session_id = prompts.session_id AND states.write_id = prompts.write_id
        AND states.state IN ('canceled', 'discarded')
        AND states.seq = (SELECT MAX(newest.seq) FROM prompt_states AS newest
                          WHERE newest.session_id = states.session_id AND newest.write_id = states.write_id)
  ));

-- name: CountSessionPrompts :one
SELECT COUNT(*) FROM events WHERE session_id = sqlc.arg(session_id) AND kind = 'engine_prompt';

-- name: InsertEnginePromptEvent :exec
INSERT INTO events (session_id, kind, at, payload, write_id) VALUES (?, 'engine_prompt', ?, ?, ?);

-- name: InsertEngineTurnEndedEvent :exec
INSERT INTO events (session_id, kind, at, payload, answered) VALUES (?, 'engine_turn_ended', ?, ?, ?);

-- name: ListHolderPrompts :many
SELECT events.write_id, CAST(json_extract(events.payload, '$.text') AS TEXT) AS text FROM events
WHERE events.session_id = sqlc.arg(id) AND events.kind = 'engine_prompt' AND events.write_id <> ''
  AND events.seq > (SELECT sessions.prompts_from FROM sessions WHERE sessions.id = sqlc.arg(id))
ORDER BY events.seq;

-- name: CountHolderPromptsUnanswered :one
SELECT COUNT(*) FROM events AS prompts
WHERE prompts.session_id = sqlc.arg(id) AND prompts.kind = 'engine_prompt' AND prompts.write_id <> ''
  AND prompts.seq > (SELECT sessions.prompts_from FROM sessions WHERE sessions.id = sqlc.arg(id))
  AND NOT EXISTS (
      SELECT 1 FROM events AS turns
      WHERE turns.session_id = sqlc.arg(id) AND turns.kind = 'engine_turn_ended' AND turns.seq > prompts.seq
        AND (turns.answered IS NULL OR EXISTS (
            SELECT 1 FROM json_each(turns.answered) WHERE json_each.value = prompts.write_id))
  )
  AND NOT EXISTS (
      SELECT 1 FROM prompt_states AS states
      WHERE states.session_id = sqlc.arg(id) AND states.write_id = prompts.write_id
        AND states.state IN ('canceled', 'discarded')
        AND states.seq = (SELECT MAX(newest.seq) FROM prompt_states AS newest
                          WHERE newest.session_id = states.session_id AND newest.write_id = states.write_id)
  );

-- name: ListQuietSessions :many
SELECT sessions.id FROM sessions
WHERE sessions.kind = 'engine' AND EXISTS (
    SELECT 1 FROM events AS interrupts
    WHERE interrupts.session_id = sessions.id AND interrupts.kind = 'engine_interrupt'
      AND interrupts.seq > COALESCE((
          SELECT MAX(owners.seq) FROM events AS owners
          WHERE owners.session_id = sessions.id AND owners.kind = 'engine_prompt'
            AND COALESCE(json_extract(owners.payload, '$.sender'), '') = 'owner'
      ), 0)
)
ORDER BY sessions.id;

-- name: InsertEnginePromptFailed :exec
INSERT INTO engine_prompt_failed (session_id, prompt_id, reason) VALUES (?, ?, ?)
ON CONFLICT (session_id, prompt_id) DO NOTHING;

-- name: GetEnginePromptFailure :one
SELECT reason FROM engine_prompt_failed WHERE session_id = ? AND prompt_id = ?;

-- name: InsertStep :exec
INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree, checkout_head, checkout_hash,
                   done_value, read_only, owned, unowned, gate, gate_name, gate_source, gate_needs, gate_limit,
                   model_limit, started, checks, result, status, end_value, reason, ended, doing, verdicts, epic, task,
                   loop_stage, loop_stage_from, agents, caller)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '', '', '', '', 0, '', '[]', ?, ?, ?, ?, ?, ?);

-- name: GetStep :one
SELECT * FROM steps WHERE id = ?;

-- name: ListOpenSteps :many
SELECT * FROM steps WHERE end_value = '' ORDER BY id;

-- name: ListLabelledSteps :many
SELECT * FROM steps
WHERE epic <> '' AND (CAST(sqlc.arg(task) AS TEXT) = '' OR task = CAST(sqlc.arg(task) AS TEXT))
ORDER BY started, id;

-- name: ListEpicSteps :many
SELECT * FROM steps WHERE epic = sqlc.arg(epic) ORDER BY started, id;

-- name: ListEpicTaskSteps :many
SELECT * FROM steps WHERE epic = sqlc.arg(epic) AND task IN (sqlc.slice(tasks)) ORDER BY started, id;

-- name: ListLabelledStepListings :many
SELECT * FROM step_listings
WHERE epic <> '' AND (CAST(sqlc.arg(task) AS TEXT) = '' OR task = CAST(sqlc.arg(task) AS TEXT))
ORDER BY started, id;

-- name: ListEpicStepListings :many
SELECT * FROM step_listings WHERE epic = sqlc.arg(epic) ORDER BY started, id;

-- name: ListEpicTaskStepListings :many
SELECT * FROM step_listings WHERE epic = sqlc.arg(epic) AND task = sqlc.arg(task) ORDER BY started, id;

-- name: ListOpenStepListings :many
SELECT * FROM step_listings WHERE end_value = '' AND epic <> '' ORDER BY started, id;

-- name: CountLabelledSteps :many
SELECT epic, task, COUNT(*) AS steps FROM steps WHERE epic <> '' GROUP BY epic, task;

-- name: ListLimitedSteps :many
SELECT * FROM steps WHERE end_value = '' AND loop_limited = 1 ORDER BY started, id;

-- name: ListCallerSteps :many
SELECT sqlc.embed(steps), sessions.id AS caller_id, sessions.kind AS caller_kind, sessions.server AS caller_server,
       sessions.pane AS caller_pane, sessions.state AS caller_state
FROM steps
JOIN sessions ON sessions.id = steps.caller
WHERE steps.end_value <> '' AND steps.caller <> ''
  AND NOT EXISTS (
      SELECT 1 FROM caller_events
      WHERE caller_events.event = 'step:' || steps.id AND caller_events.session = steps.caller
  )
ORDER BY steps.ended, steps.id;

-- name: ListPendingCallerSteps :many
SELECT * FROM steps
WHERE steps.end_value <> '' AND steps.caller <> '' AND steps.caller = ?
  AND NOT EXISTS (
      SELECT 1 FROM caller_events
      WHERE caller_events.event = 'step:' || steps.id AND caller_events.session = steps.caller
  )
ORDER BY steps.ended, steps.id;

-- name: ListRunningCallerSteps :many
SELECT * FROM steps WHERE end_value = '' AND caller = ? ORDER BY started, id;

-- name: ListStepFacts :many
SELECT id, loop_round, started, ended, lines_counted, lines_files, lines_added, lines_removed
FROM steps WHERE id IN (sqlc.slice(ids)) ORDER BY id;

-- name: ListCallerStepListings :many
SELECT * FROM step_listings
WHERE step_listings.caller = sqlc.arg(caller)
  AND (CAST(sqlc.arg(epic) AS TEXT) = '' OR step_listings.epic = CAST(sqlc.arg(epic) AS TEXT))
  AND (step_listings.end_value = '' OR step_listings.id IN (
      SELECT recent.id FROM step_listings AS recent
      WHERE recent.caller = sqlc.arg(caller) AND recent.end_value <> ''
        AND (CAST(sqlc.arg(epic) AS TEXT) = '' OR recent.epic = CAST(sqlc.arg(epic) AS TEXT))
      ORDER BY recent.ended DESC, recent.id DESC
      LIMIT 20))
ORDER BY started, id;

-- name: UpdateStepReturn :exec
UPDATE steps
SET result = ?, checks = ?,
    loop_seq = (SELECT CAST(COALESCE(MAX(events.seq), 0) AS INTEGER) FROM events WHERE events.session_id = steps.id),
    gate_grant = '', gate_holder = '', gate_round = 0
WHERE id = ?;

-- name: UpdateStepLoop :exec
UPDATE steps
SET loop_round = ?, loop_continues = ?, loop_nudge = ?, loop_owner_nudge = ?, loop_nudged = ?, loop_held = ?,
    loop_judge_sent = ?, loop_limited = ?, loop_limit_kind = ?, loop_limit_resets = ?, loop_gate_from = ?,
    loop_stage = ?, loop_stage_from = ?
WHERE id = ?;

-- name: UpdateStepGate :exec
UPDATE steps SET gate_grant = ?, gate_holder = ?, gate_round = ? WHERE id = ?;

-- name: GetStepTurnEnd :one
SELECT events.seq, CAST(json_extract(events.payload, '$.isError') AS INTEGER) AS is_error, sessions.turn_output,
       sessions.awaited, steps.loop_seq
FROM events
JOIN sessions ON sessions.id = events.session_id
JOIN steps ON steps.id = events.session_id
WHERE events.session_id = sqlc.arg(id) AND events.kind = 'engine_turn_ended'
  AND events.seq > (
      SELECT CAST(COALESCE(MAX(prompts.seq), 0) AS INTEGER) FROM events AS prompts
      WHERE prompts.session_id = sqlc.arg(id) AND prompts.kind = 'engine_prompt'
  )
ORDER BY events.seq DESC
LIMIT 1;

-- name: ListStepBlocks :many
SELECT at, payload FROM events
WHERE session_id = sqlc.arg(id) AND kind = 'engine_block'
ORDER BY seq DESC
LIMIT sqlc.arg(count);

-- name: UpdateStepWatch :exec
UPDATE steps SET verdicts = ?, doing = ? WHERE id = ?;

-- name: UpdateStepEnd :exec
UPDATE steps SET end_value = ?, status = ?, reason = ?, ended = ? WHERE id = ?;

-- name: UpdateStepLines :exec
UPDATE steps SET lines_counted = ?, lines_files = ?, lines_added = ?, lines_removed = ? WHERE id = ?;

-- name: InsertGrant :exec
INSERT INTO grants (id, project, client_pid, client_start, pools, command, granted, holder)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

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
INSERT INTO pending_notices (id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, source, silent, epic, limit_key)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET pane = excluded.pane, session = excluded.session, state = excluded.state,
    time = excluded.time, phase = excluded.phase, due = excluded.due, delay_served = excluded.delay_served,
    watch_logged = excluded.watch_logged, source_kind = excluded.source_kind, source = excluded.source,
    silent = excluded.silent, epic = excluded.epic, limit_key = excluded.limit_key;

-- name: RaiseNoticeIDFloor :exec
UPDATE pending_notice_ids SET high = MAX(high, sqlc.arg(id)) WHERE id = 1;

-- name: DeletePendingNotice :exec
DELETE FROM pending_notices WHERE id = ?;

-- name: ListPendingNotices :many
SELECT * FROM pending_notices ORDER BY id;

-- name: GetNoticeIDFloor :one
SELECT high FROM pending_notice_ids WHERE id = 1;

-- name: UpsertPushedNotice :exec
INSERT INTO pushed_notices (pane, session, time) VALUES (?, ?, ?)
ON CONFLICT (pane, session) DO UPDATE SET time = excluded.time;

-- name: DeletePushedNotice :exec
DELETE FROM pushed_notices WHERE pane = ? AND session = ?;

-- name: ListPushedNotices :many
SELECT * FROM pushed_notices ORDER BY pane, session;

-- name: UpsertPushedEpicNotice :exec
INSERT INTO pushed_epic_notices (epic, time) VALUES (?, ?)
ON CONFLICT (epic) DO UPDATE SET time = excluded.time;

-- name: DeletePushedEpicNotice :exec
DELETE FROM pushed_epic_notices WHERE epic = ?;

-- name: ListPushedEpicNotices :many
SELECT * FROM pushed_epic_notices ORDER BY epic;

-- name: UpsertPushedLimitNotice :exec
INSERT INTO pushed_limit_notices (limit_key, time) VALUES (?, ?)
ON CONFLICT (limit_key) DO UPDATE SET time = excluded.time;

-- name: DeletePushedLimitNotice :exec
DELETE FROM pushed_limit_notices WHERE limit_key = ?;

-- name: ListPushedLimitNotices :many
SELECT * FROM pushed_limit_notices ORDER BY limit_key;

-- name: UpsertPushedAlertNotice :exec
INSERT INTO pushed_alert_notices (alert, time) VALUES (?, ?)
ON CONFLICT (alert) DO UPDATE SET time = excluded.time;

-- name: DeletePushedAlertNotice :exec
DELETE FROM pushed_alert_notices WHERE alert = ?;

-- name: ListPushedAlertNotices :many
SELECT * FROM pushed_alert_notices ORDER BY alert;

-- name: UpdateUnreadNoticeHistory :execrows
UPDATE notice_history SET state = ?, title = ?, body = ?, block = ?, block_ref = ?, time = ?
WHERE id = (
    SELECT newest.id FROM notice_history AS newest
    WHERE newest.pane = ? AND newest.session = ? AND newest.epic = ? AND newest.limit_key = ? AND newest.alert = ?
    ORDER BY newest.id DESC LIMIT 1
) AND read = 0;

-- name: InsertNoticeHistory :exec
INSERT INTO notice_history (pane, session, epic, limit_key, alert, state, title, body, block, block_ref, time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: MarkNoticeHistoryRead :exec
UPDATE notice_history SET read = 1
WHERE pane = ? AND session = ? AND epic = ? AND limit_key = ? AND alert = ? AND time <= ?;

-- name: CountUnreadNoticeHistory :one
SELECT COUNT(*) FROM notice_history
WHERE pane = ? AND session = ? AND epic = ? AND limit_key = ? AND alert = ? AND read = 0;

-- name: MarkAllNoticeHistoryRead :exec
UPDATE notice_history SET read = 1;

-- name: ListNoticeHistorySince :many
SELECT * FROM notice_history WHERE time >= ? ORDER BY time DESC, id DESC;

-- name: ListUnreadNoticeHistoryKeys :many
SELECT DISTINCT pane, session, epic, limit_key, alert FROM notice_history
WHERE read = 0 ORDER BY pane, session, epic, limit_key, alert;

-- name: InsertToldLimitStep :exec
INSERT INTO told_limit_steps (limit_key, step) VALUES (?, ?)
ON CONFLICT (limit_key, step) DO NOTHING;

-- name: DeleteToldLimit :exec
DELETE FROM told_limit_steps WHERE limit_key = ?;

-- name: ListToldLimitSteps :many
SELECT * FROM told_limit_steps ORDER BY limit_key, step;

-- name: InsertEpic :exec
INSERT INTO epics (id, repository, base, stored_base, title, approved, intent, care, approach, release_then, checks,
    phase, created, changed, workflow, workflow_text, workflow_sha256, phase_at, steerer)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountEpic :one
SELECT COUNT(*) FROM epics WHERE id = ?;

-- name: GetEpic :one
SELECT * FROM epics WHERE id = ?;

-- name: ListEpics :many
SELECT sqlc.embed(epics), CAST(COALESCE(epic_revisions.revision, 0) AS INTEGER) AS revision
FROM epics LEFT JOIN epic_revisions ON epic_revisions.epic = epics.id
ORDER BY epics.id;

-- name: ListEpicListings :many
SELECT epics.id, epics.repository, epics.title, epics.phase, epics.phase_at, epics.created, epics.changed,
    epics.steerer, epics.paused, epics.pause_source, epics.parked,
    CAST(COALESCE(epic_revisions.revision, 0) AS INTEGER) AS revision, epic_seen.at AS seen_at,
    CAST((SELECT COUNT(*) FROM epic_questions WHERE epic_questions.epic = epics.id AND epic_questions.state = 'open')
        AS INTEGER) AS open_questions,
    CAST(EXISTS (SELECT 1 FROM sessions WHERE sessions.id = epics.steerer AND sessions.state <> 'stopped')
        AS INTEGER) AS steerer_heard
FROM epics
LEFT JOIN epic_revisions ON epic_revisions.epic = epics.id
LEFT JOIN epic_seen ON epic_seen.epic = epics.id
ORDER BY epics.id;

-- name: ListBuildEpics :many
SELECT epics.id FROM epics
WHERE epics.base <> '' AND epics.phase NOT IN ('met_at_base', 'clean', 'ended_with_gaps', 'released', 'done', 'stopped',
    'closed_superseded', 'closed_shipped', 'closed_abandoned')
  AND EXISTS (SELECT 1 FROM epic_tasks WHERE epic_tasks.epic = epics.id AND epic_tasks.status = 'open')
ORDER BY epics.id;

-- name: MoveEpicRevision :exec
INSERT INTO epic_revisions (epic, revision) VALUES (?, 1)
ON CONFLICT (epic) DO UPDATE SET revision = epic_revisions.revision + 1;

-- name: ListEvidenceRunEpics :many
SELECT DISTINCT epic FROM evidence_runs WHERE id IN (sqlc.slice(runs));

-- name: GetEpicRevision :one
SELECT revision FROM epic_revisions WHERE epic = ?;

-- name: UpdateEpicChanged :exec
UPDATE epics SET changed = ? WHERE id = ?;

-- name: SetEpicBranch :exec
UPDATE epics SET branch = ? WHERE id = ?;

-- name: ListUnsummarizedEpics :many
SELECT * FROM epics WHERE summary = '' ORDER BY id;

-- name: SetEpicSummary :exec
UPDATE epics SET summary = ? WHERE id = ? AND summary = '';

-- name: ListUnreportedEpics :many
SELECT * FROM epics
WHERE (phase IN ('released', 'done', 'ended_with_gaps')
       OR (phase IN ('closed_superseded', 'closed_shipped', 'closed_abandoned') AND closed_by = 'planner'))
  AND report_phase <> phase
ORDER BY id;

-- name: GetUnreportedEpic :one
SELECT * FROM epics
WHERE (phase IN ('released', 'done', 'ended_with_gaps')
       OR (phase IN ('closed_superseded', 'closed_shipped', 'closed_abandoned') AND closed_by = 'planner'))
  AND report_phase <> phase
  AND id = ?;

-- name: SetEpicReport :exec
UPDATE epics
SET report_phase = sqlc.arg(report_phase), report_shipped = sqlc.arg(shipped),
    report_not_shipped = sqlc.arg(not_shipped), report_why = sqlc.arg(why)
WHERE id = sqlc.arg(id) AND phase = sqlc.arg(report_phase) AND report_phase <> sqlc.arg(report_phase);

-- name: MoveEpicStoredBase :exec
UPDATE epics SET stored_base = ?, candidate = '' WHERE id = ?;

-- name: SyncEpicStoredBase :exec
UPDATE epics SET stored_base = ?, candidate = '', synced = ? WHERE id = ?;

-- name: SetEpicOriginRead :exec
UPDATE epics
SET origin_read_base = sqlc.arg(base), origin_read_commit = sqlc.arg(commit), origin_read_state = sqlc.arg(state),
    origin_read_at = sqlc.arg(at)
WHERE id = sqlc.arg(id);

-- name: SetEpicCandidate :exec
UPDATE epics SET candidate = ? WHERE id = ?;

-- name: SetEpicPhase :exec
UPDATE epics
SET phase = sqlc.arg(phase), phase_at = CASE WHEN phase = sqlc.arg(phase) THEN phase_at ELSE sqlc.arg(at) END
WHERE id = sqlc.arg(id);

-- name: SetEpicClosed :exec
UPDATE epics SET closed_by = sqlc.arg(closed_by), superseded_by = sqlc.arg(superseded_by), parked = '', park_class = ''
WHERE id = sqlc.arg(id);

-- name: EndRunningEpicNodeRuns :exec
UPDATE epic_node_runs SET state = 'ended', outcome = '', ended = sqlc.arg(ended)
WHERE epic = sqlc.arg(epic) AND state = 'running';

-- name: ConsumePendingEpicTriggers :exec
UPDATE epic_triggers SET state = 'consumed' WHERE epic = ? AND state = 'pending';

-- name: InsertEpicFailure :exec
INSERT INTO epic_failures (epic, position, id, text, rating, response) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListEpicFailures :many
SELECT * FROM epic_failures WHERE epic = ? ORDER BY position;

-- name: InsertEpicAddition :exec
INSERT INTO epic_additions (epic, position, location, what, why, cost) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListEpicAdditions :many
SELECT * FROM epic_additions WHERE epic = ? ORDER BY position;

-- name: InsertEpicRequirement :exec
INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListEpicRequirements :many
SELECT * FROM epic_requirements WHERE epic = ? ORDER BY position;

-- name: DropEpicRequirement :exec
UPDATE epic_requirements SET dropped_by = sqlc.arg(decision) WHERE epic = sqlc.arg(epic) AND id = sqlc.arg(id);

-- name: SetEpicRequirementItem :execrows
UPDATE epic_requirements SET item = sqlc.arg(item) WHERE epic = sqlc.arg(epic) AND id = sqlc.arg(id);

-- name: InsertEpicPremise :exec
INSERT INTO epic_premises (epic, position, id, text, if_wrong) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicPremises :many
SELECT * FROM epic_premises WHERE epic = ? ORDER BY position;

-- name: InsertEpicExclusion :exec
INSERT INTO epic_exclusions (epic, position, text) VALUES (?, ?, ?);

-- name: ListEpicExclusions :many
SELECT * FROM epic_exclusions WHERE epic = ? ORDER BY position;

-- name: InsertEpicAlternative :exec
INSERT INTO epic_alternatives (epic, position, alternative, why_not, reconsider) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicAlternatives :many
SELECT * FROM epic_alternatives WHERE epic = ? ORDER BY position;

-- name: InsertEpicReleaseStep :exec
INSERT INTO epic_release_steps (epic, position, step, target, undo) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicReleaseSteps :many
SELECT * FROM epic_release_steps WHERE epic = ? ORDER BY position;

-- name: InsertEpicReleaseRun :exec
INSERT INTO epic_release_runs (epic, part, position, attempt, step, start, head, finish, commits, evidence, passed,
    reason, at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, part, position, attempt) DO NOTHING;

-- name: ListEpicReleaseRuns :many
SELECT * FROM epic_release_runs WHERE epic = ? ORDER BY attempt, part, position;

-- name: InsertEpicReleaseSlot :exec
INSERT INTO epic_release_slots (repository, epic, holder_kind) VALUES (?, ?, 'epic')
ON CONFLICT (repository) DO NOTHING;

-- name: GetEpicReleaseSlotHolder :one
SELECT * FROM epic_release_slots WHERE repository = ?;

-- name: FreeEpicReleaseSlot :exec
DELETE FROM epic_release_slots WHERE epic = ? AND holder_kind = 'epic';

-- name: HoldOutsideReleaseSlot :exec
INSERT INTO epic_release_slots (repository, epic, holder_kind, label, deadline) VALUES (?, '', 'outside', ?, ?)
ON CONFLICT (repository) DO UPDATE SET label = excluded.label, deadline = excluded.deadline
WHERE epic_release_slots.holder_kind = 'outside' AND epic_release_slots.label = excluded.label;

-- name: GiveOutsideReleaseSlot :execrows
DELETE FROM epic_release_slots WHERE repository = ? AND holder_kind = 'outside' AND label = ?;

-- name: FreeExpiredReleaseSlots :many
DELETE FROM epic_release_slots WHERE holder_kind = 'outside' AND deadline <= ? RETURNING repository;

-- name: NextOutsideReleaseSlotDeadline :one
SELECT deadline FROM epic_release_slots WHERE holder_kind = 'outside' ORDER BY deadline LIMIT 1;

-- name: StartEpicRelease :exec
UPDATE epics SET phase = 'releasing', phase_at = ? WHERE id = ? AND phase = 'clean';

-- name: ResumeEpicRelease :exec
UPDATE epics SET phase = 'releasing', phase_at = ? WHERE id = ? AND phase = 'stopped';

-- name: StopEpicRelease :exec
UPDATE epics SET phase = 'stopped', phase_at = ? WHERE id = ? AND phase = 'releasing';

-- name: ReplanEpicRelease :execrows
UPDATE epics SET phase = 'correcting', phase_at = ? WHERE id = ? AND phase IN ('releasing', 'stopped');

-- name: CleanReplannedEpic :exec
UPDATE epics SET phase = 'clean', phase_at = ? WHERE id = ? AND phase = 'correcting';

-- name: InsertEpicGate :exec
INSERT INTO epic_gates (epic, position, name, argv, limit_seconds, needs, agent, must, must_not)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEpicGates :many
SELECT * FROM epic_gates WHERE epic = ? ORDER BY position;

-- name: InsertEpicGateList :exec
INSERT INTO epic_gate_lists (epic, list, position, gate) VALUES (?, ?, ?, ?);

-- name: ListEpicDoneGates :many
SELECT * FROM epic_gate_lists WHERE epic = ? AND list = 'done' ORDER BY position;

-- name: ListEpicReviewGates :many
SELECT * FROM epic_gate_lists WHERE epic = ? AND list = 'review' ORDER BY position;

-- name: InsertEpicWorkflowNode :exec
INSERT INTO epic_workflow_nodes (epic, position, id, parent, kind) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicWorkflowNodes :many
SELECT * FROM epic_workflow_nodes WHERE epic = ? ORDER BY position;

-- name: ListAllEpicWorkflowNodes :many
SELECT * FROM epic_workflow_nodes ORDER BY epic, position;

-- name: InsertEpicNodeRun :exec
INSERT INTO epic_node_runs (epic, path, id, node, kind, task, round, state, outcome, loop_round, granted, mark, started, ended, attempt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, path) DO NOTHING;

-- name: ListEpicNodeRuns :many
SELECT * FROM epic_node_runs WHERE epic = ? ORDER BY path;

-- name: ListOpenEpicNodeRunStates :many
SELECT epic_node_runs.epic, epic_node_runs.path, epic_node_runs.kind, epic_node_runs.state, epic_node_runs.outcome,
       epic_node_runs.attempt
FROM epics
CROSS JOIN epic_node_runs ON epic_node_runs.epic = epics.id
WHERE epics.phase NOT IN ('closed_superseded', 'closed_shipped', 'closed_abandoned')
ORDER BY epics.id, epic_node_runs.path;

-- name: EndEpicNodeRun :exec
UPDATE epic_node_runs SET state = 'ended', outcome = ?, ended = ?, output = ? WHERE epic = ? AND path = ? AND state = 'running';

-- name: PrepareEpicNodeRun :exec
UPDATE epic_node_runs SET revision = ?, refs = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND revision = '';

-- name: ClearEpicNodeRevision :execrows
UPDATE epic_node_runs SET revision = '', result = ''
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running';

-- name: RecordEpicNodeResult :exec
UPDATE epic_node_runs SET result = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND result = '';

-- name: PrepareEpicSyncRun :execrows
UPDATE epic_node_runs SET revision = ?, result = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND result = '';

-- name: CheckEpicNodeRefs :execrows
UPDATE epic_node_runs SET refs_checked = 1
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND refs_checked = 0;

-- name: ParkEpicNodeRun :execrows
UPDATE epic_node_runs SET state = 'parked', outcome = ?, output = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running';

-- name: ClaimEpicNodeRunWork :execrows
UPDATE epic_node_runs SET revision = ?, refs = ?, result = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND revision = '';

-- name: EndEpicNodeRunAttempt :execrows
UPDATE epic_node_runs SET state = 'ended', outcome = ?, ended = ?, output = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running';

-- name: GetEpicNodeRun :one
SELECT * FROM epic_node_runs WHERE epic = ? AND path = ?;

-- name: CountRunningEpicNodeRun :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_node_runs WHERE epic = ? AND path = ? AND state = 'running';

-- name: RetryEpicNode :execrows
UPDATE epic_node_runs SET state = 'running', attempt = attempt + 1, outcome = '', output = '', revision = '',
    refs = '', refs_checked = 0, result = '', ended = 0
WHERE epic = ? AND node = ? AND kind <> 'loop' AND state = 'parked';

-- name: RetryEpicNodeRun :execrows
UPDATE epic_node_runs SET state = 'running', attempt = attempt + 1, outcome = '', output = '', revision = '',
    refs = '', refs_checked = 0, result = '', ended = 0
WHERE epic = ? AND path = ? AND kind <> 'loop' AND state = 'parked';

-- name: RebuildEpicTaskRun :execrows
UPDATE epic_node_runs SET state = 'running', attempt = attempt + 1, outcome = '', output = '', revision = '',
    refs = '', refs_checked = 0, result = '', ended = 0
WHERE epic = ? AND path = ? AND attempt = ? AND kind = 'build' AND state = 'ended';

-- name: NextEpicNodeRunAttempt :execrows
UPDATE epic_node_runs SET attempt = attempt + 1, outcome = '', output = '', revision = '', refs = '', refs_checked = 0,
    result = '', ended = 0
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running';

-- name: UnparkEpicNodeRun :execrows
UPDATE epic_node_runs SET state = 'running', outcome = '', output = ''
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'parked';

-- name: RetryFailedEpicIntegrateRun :execrows
UPDATE epic_node_runs SET state = 'running', attempt = attempt + 1, outcome = '', output = '', refs_checked = 0, ended = 0
WHERE epic = ? AND path = ? AND kind = 'integrate' AND state = 'ended' AND outcome = 'failed';

-- name: MoveEpicLoopRound :exec
UPDATE epic_node_runs SET loop_round = loop_round + 1
WHERE epic = ? AND path = ? AND kind = 'loop' AND state = 'running' AND loop_round = ?;

-- name: ParkEpicLoopRun :execrows
UPDATE epic_node_runs SET state = 'parked'
WHERE epic = ? AND path = ? AND kind = 'loop' AND state = 'running' AND loop_round = ?;

-- name: InsertEpicTrigger :exec
INSERT INTO epic_triggers (epic, id, cause, state, turn, created) VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertEpicTriggerOnce :exec
INSERT INTO epic_triggers (epic, id, cause, state, turn, created) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, id) DO NOTHING;

-- name: CountEpicTrigger :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_triggers WHERE epic = ? AND id = ?;

-- name: InsertEpicDecision :exec
INSERT INTO epic_decisions (epic, seq, client_id, text, time, question, maker, maker_session, recipient)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEpicDecisionByClientID :one
SELECT * FROM epic_decisions WHERE epic = ? AND client_id = ?;

-- name: MaxEpicDecisionSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_decisions WHERE epic = ?;

-- name: ListEpicDecisions :many
SELECT * FROM epic_decisions WHERE epic = ? ORDER BY seq;

-- name: ListAddressedEpicDecisions :many
SELECT epic_decisions.*, epics.title FROM epic_decisions
JOIN epics ON epics.id = epic_decisions.epic
WHERE epic_decisions.recipient != ''
ORDER BY epic_decisions.time, epic_decisions.epic, epic_decisions.seq;

-- name: ListSessionEpicDecisions :many
SELECT epic_decisions.*, epics.title FROM epic_decisions
JOIN epics ON epics.id = epic_decisions.epic
WHERE epic_decisions.recipient = CAST(sqlc.arg(session) AS TEXT)
ORDER BY epic_decisions.time, epic_decisions.epic, epic_decisions.seq;

-- name: InsertEpicAction :exec
INSERT INTO epic_actions (epic, seq, kind, maker, maker_session, from_session, to_session, recipient, at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: MaxEpicActionSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_actions WHERE epic = ?;

-- name: ListEpicActions :many
SELECT * FROM epic_actions WHERE epic = ? ORDER BY seq;

-- name: ListAddressedEpicActions :many
SELECT epic_actions.*, epics.title FROM epic_actions
JOIN epics ON epics.id = epic_actions.epic
WHERE epic_actions.recipient != '' OR epic_actions.kind = 'move'
ORDER BY epic_actions.at, epic_actions.epic, epic_actions.seq;

-- name: ListSessionEpicActions :many
SELECT epic_actions.*, epics.title FROM epic_actions
JOIN epics ON epics.id = epic_actions.epic
WHERE epic_actions.recipient = CAST(sqlc.arg(session) AS TEXT)
    OR (epic_actions.kind = 'move' AND (epic_actions.from_session = CAST(sqlc.arg(session) AS TEXT)
        OR epic_actions.to_session = CAST(sqlc.arg(session) AS TEXT)))
ORDER BY epic_actions.at, epic_actions.epic, epic_actions.seq;

-- name: SetEpicSteerer :exec
UPDATE epics SET steerer = ? WHERE id = ?;

-- name: ListEpicPlanStates :many
SELECT epics.id, epics.paused, epics.parked, epics.park_class,
    CAST(EXISTS (SELECT 1 FROM epic_turns WHERE epic_turns.epic = epics.id AND epic_turns.state = 'running') AS INTEGER) AS running,
    CAST((SELECT COUNT(*) FROM epic_triggers WHERE epic_triggers.epic = epics.id AND epic_triggers.state = 'pending') AS INTEGER) AS pending
FROM epics
WHERE EXISTS (SELECT 1 FROM epic_triggers WHERE epic_triggers.epic = epics.id AND epic_triggers.state = 'pending')
ORDER BY epics.id;

-- name: CountEpicTriggers :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_triggers WHERE epic = ?;

-- name: CountPendingEpicTriggers :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_triggers WHERE epic = ? AND state = 'pending';

-- name: CountRunningEpicTurns :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_turns WHERE epic = ? AND state = 'running';

-- name: MaxEpicTurnSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_turns WHERE epic = ?;

-- name: InsertEpicTurn :exec
INSERT INTO epic_turns (epic, seq, state, seen, started) VALUES (?, ?, 'running', ?, ?);

-- name: ClaimEpicTriggers :exec
UPDATE epic_triggers SET state = 'claimed', turn = ? WHERE epic = ? AND state = 'pending';

-- name: SetEpicTurnStep :execrows
UPDATE epic_turns SET step = ? WHERE epic = ? AND seq = ? AND state = 'running';

-- name: SetEpicTurnStop :exec
UPDATE epic_turns SET stop = 1 WHERE epic = ? AND seq = ? AND state = 'running';

-- name: GetEpicTurnStop :one
SELECT stop FROM epic_turns WHERE epic = ? AND seq = ?;

-- name: CompleteEpicTurn :execrows
UPDATE epic_turns SET state = 'succeeded', outline = ?, outcome = ?, ended = ? WHERE epic = ? AND seq = ? AND state = 'running';

-- name: InsertEpicTurnDecision :exec
INSERT INTO epic_turn_decisions (epic, turn, position, text) VALUES (?, ?, ?, ?);

-- name: MaxEpicTaskSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_tasks WHERE epic = ?;

-- name: InsertEpicTask :exec
INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, gate, evidence, status, depends_on, kind, revises)
VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?);

-- name: InsertEpicTaskFile :exec
INSERT INTO epic_task_files (epic, task, position, path) VALUES (?, ?, ?, ?);

-- name: DeleteEpicTaskFile :exec
DELETE FROM epic_task_files WHERE epic = ? AND task = ? AND path = ?;

-- name: InsertEpicTaskRequirement :exec
INSERT INTO epic_task_requirements (epic, task, position, requirement) VALUES (?, ?, ?, ?);

-- name: InsertEpicTaskContribute :exec
INSERT INTO epic_task_contributes (epic, task, position, requirement) VALUES (?, ?, ?, ?);

-- name: InsertEpicTaskTestChange :exec
INSERT INTO epic_task_test_changes (epic, task, position, path) VALUES (?, ?, ?, ?);

-- name: SetEpicTaskBuild :execrows
UPDATE epic_tasks
SET stage = sqlc.arg(to_stage), base = sqlc.arg(to_base), attempt = sqlc.arg(to_attempt),
    rerun = sqlc.arg(to_rerun), no_verdict = sqlc.arg(to_no_verdict), held = sqlc.arg(to_held),
    step = sqlc.arg(to_step), red_run = sqlc.arg(to_red_run), green_run = sqlc.arg(to_green_run),
    revision = sqlc.arg(to_revision), failure = sqlc.arg(to_failure), review = sqlc.arg(to_review),
    refs = sqlc.arg(to_refs), gate_pos = sqlc.arg(to_gate_pos), finding_pos = sqlc.arg(to_finding_pos),
    guard = sqlc.arg(to_guard), decisions = sqlc.arg(to_decisions), after_tasks = sqlc.arg(to_after_tasks),
    question = sqlc.arg(to_question), rebriefs = sqlc.arg(to_rebriefs)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND status = 'open'
  AND stage = sqlc.arg(from_stage) AND base = sqlc.arg(from_base) AND attempt = sqlc.arg(from_attempt)
  AND rerun = sqlc.arg(from_rerun) AND no_verdict = sqlc.arg(from_no_verdict) AND held = sqlc.arg(from_held)
  AND step = sqlc.arg(from_step) AND red_run = sqlc.arg(from_red_run) AND green_run = sqlc.arg(from_green_run)
  AND revision = sqlc.arg(from_revision) AND failure = sqlc.arg(from_failure) AND review = sqlc.arg(from_review)
  AND refs = sqlc.arg(from_refs) AND gate_pos = sqlc.arg(from_gate_pos) AND finding_pos = sqlc.arg(from_finding_pos)
  AND guard = sqlc.arg(from_guard) AND decisions = sqlc.arg(from_decisions)
  AND after_tasks = sqlc.arg(from_after_tasks) AND question = sqlc.arg(from_question)
  AND rebriefs = sqlc.arg(from_rebriefs);

-- name: SetEpicTaskStatus :exec
UPDATE epic_tasks SET status = sqlc.arg(status) WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: SetEpicTaskNeedsFiles :exec
UPDATE epic_tasks SET needs_files = sqlc.arg(needs_files) WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: SetEpicTaskGateChanges :exec
UPDATE epic_tasks SET gate_changes = sqlc.arg(gate_changes) WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: ClaimEpicTaskBatch :exec
UPDATE epic_tasks SET batch = sqlc.arg(batch)
WHERE epic_tasks.epic = sqlc.arg(epic) AND epic_tasks.seq = sqlc.arg(seq) AND epic_tasks.status = 'done' AND (
    epic_tasks.batch = '' OR epic_tasks.batch = sqlc.arg(batch) OR epic_tasks.batch IN (
        SELECT runs.path FROM epic_node_runs AS runs
        WHERE runs.epic = sqlc.arg(epic) AND runs.kind = 'integrate' AND runs.state = 'ended' AND runs.outcome = 'failed'
    )
);

-- name: InsertEpicTaskReview :exec
INSERT INTO epic_task_reviews (epic, task, generation, attempt, position, gate, step, verdict)
VALUES (
    sqlc.arg(epic), sqlc.arg(task),
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = sqlc.arg(epic) AND tasks.seq = sqlc.arg(task)),
    sqlc.arg(attempt), sqlc.arg(position), sqlc.arg(gate), sqlc.arg(step), sqlc.arg(verdict)
);

-- name: InsertEpicTaskFinding :exec
INSERT INTO epic_task_findings (epic, task, generation, attempt, review, position, id, claim, citation, location,
                                evidence, verify_by, step, result, reason, unverified)
VALUES (
    sqlc.arg(epic), sqlc.arg(task),
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = sqlc.arg(epic) AND tasks.seq = sqlc.arg(task)),
    sqlc.arg(attempt), sqlc.arg(review), sqlc.arg(position), sqlc.arg(id), sqlc.arg(claim), sqlc.arg(citation),
    sqlc.arg(location), sqlc.arg(evidence), sqlc.arg(verify_by), sqlc.arg(step), sqlc.arg(result), sqlc.arg(reason),
    sqlc.arg(unverified)
);

-- name: InsertEpicReviewer :exec
INSERT INTO epic_reviewers (epic, position, name, enabled, stage, must, must_not, prompt, agent, model, tools, effort,
                            limit_seconds, verify)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEpicReviewers :many
SELECT * FROM epic_reviewers WHERE epic = ? ORDER BY position;

-- name: SetEpicTaskFindingResult :execrows
UPDATE epic_task_findings
SET step = sqlc.arg(step), result = sqlc.arg(result), reason = sqlc.arg(reason)
WHERE epic_task_findings.epic = sqlc.arg(epic) AND epic_task_findings.task = sqlc.arg(task)
  AND epic_task_findings.attempt = sqlc.arg(attempt) AND epic_task_findings.review = sqlc.arg(review)
  AND epic_task_findings.position = sqlc.arg(position) AND epic_task_findings.result = ''
  AND epic_task_findings.generation = (
      SELECT tasks.generation FROM epic_tasks AS tasks
      WHERE tasks.epic = epic_task_findings.epic AND tasks.seq = epic_task_findings.task
  );

-- name: MaxEpicQuestionSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_questions WHERE epic = ?;

-- name: InsertEpicQuestion :exec
INSERT INTO epic_questions (epic, seq, turn, text, state, label, choices, recommended, why, confidence, replaces)
VALUES (?, ?, ?, ?, 'open', ?, ?, ?, ?, ?, ?);

-- name: ConsumeEpicTriggers :exec
UPDATE epic_triggers SET state = 'consumed' WHERE epic = ? AND turn = ? AND state = 'claimed';

-- name: EndEpicTurn :execrows
UPDATE epic_turns SET state = ?, reason = ?, ended = ? WHERE epic = ? AND seq = ? AND state = 'running';

-- name: ReleaseEpicTriggers :exec
UPDATE epic_triggers SET state = 'pending', turn = 0 WHERE epic = ? AND turn = ? AND state = 'claimed';

-- name: ListRecentEpicTurnStates :many
SELECT state FROM epic_turns
WHERE epic = sqlc.arg(epic) AND state <> 'free'
  AND seq > (SELECT epics.retried FROM epics WHERE epics.id = sqlc.arg(epic))
ORDER BY seq DESC LIMIT 3;

-- name: SetEpicParked :execrows
UPDATE epics SET parked = ?, park_class = ? WHERE id = ?;

-- name: PauseEpic :exec
UPDATE epics SET paused = 1 WHERE id = ?;

-- name: SetEpicPauseSource :exec
UPDATE epics SET pause_source = ? WHERE id = ?;

-- name: ResumeEpic :exec
UPDATE epics SET paused = 0, pause_source = '' WHERE id = ?;

-- name: RetryEpic :exec
UPDATE epics SET parked = '', park_class = '', retried = ? WHERE id = ?;

-- name: GrantEpicParkedLoops :exec
UPDATE epic_node_runs SET state = 'running', granted = granted + 1
WHERE epic = ? AND kind = 'loop' AND state = 'parked';

-- name: ListEpicTurns :many
SELECT * FROM epic_turns WHERE epic = ? ORDER BY seq;

-- name: ListRunningEpicTurns :many
SELECT * FROM epic_turns WHERE state = 'running' ORDER BY epic, seq;

-- name: GetRunningEpicTurn :one
SELECT * FROM epic_turns WHERE epic = ? AND state = 'running';

-- name: ListEpicTurnDecisionsOf :many
SELECT * FROM epic_turn_decisions WHERE epic = ? AND turn = ? ORDER BY position;

-- name: ListEpicTasks :many
SELECT * FROM epic_tasks WHERE epic = ? ORDER BY seq;

-- name: ListEpicTaskFiles :many
SELECT * FROM epic_task_files WHERE epic = ? ORDER BY task, position;

-- name: ListEpicTaskRequirements :many
SELECT * FROM epic_task_requirements WHERE epic = ? ORDER BY task, position;

-- name: ListEpicTaskContributes :many
SELECT * FROM epic_task_contributes WHERE epic = ? ORDER BY task, position;

-- name: ListEpicTaskTestChanges :many
SELECT * FROM epic_task_test_changes WHERE epic = ? ORDER BY task, position;

-- name: ListEpicTaskReviews :many
SELECT * FROM epic_task_reviews AS reviews
WHERE reviews.epic = ? AND reviews.generation = (
    SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = reviews.epic AND tasks.seq = reviews.task
)
ORDER BY reviews.task, reviews.attempt, reviews.position;

-- name: ListEpicTaskFindings :many
SELECT * FROM epic_task_findings AS findings
WHERE findings.epic = ? AND findings.generation = (
    SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = findings.epic AND tasks.seq = findings.task
)
ORDER BY findings.task, findings.attempt, findings.review, findings.position;

-- name: InsertEpicTaskVersion :exec
INSERT INTO epic_task_versions (
    epic, task, version, source, decision, step, reason, deliverable, files, test_changes, gate, evidence,
    state, created, settled
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SettleEpicTaskVersion :execrows
UPDATE epic_task_versions SET state = ?, red_run = ?, refusal = ?, settled = ?
WHERE epic = ? AND task = ? AND version = ? AND state = 'checking';

-- name: ListEpicTaskVersions :many
SELECT * FROM epic_task_versions WHERE epic = ? ORDER BY task, version;

-- name: MaxEpicTaskVersion :one
SELECT CAST(COALESCE(MAX(version), 1) AS INTEGER) FROM epic_task_versions WHERE epic = ? AND task = ?;

-- name: InsertEpicTaskRung :execrows
INSERT INTO epic_task_rungs (epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created)
VALUES (
    sqlc.arg(epic), sqlc.arg(task),
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = sqlc.arg(epic) AND tasks.seq = sqlc.arg(task)),
    sqlc.arg(rung), sqlc.arg(kind), sqlc.arg(moved_by), sqlc.arg(cause), sqlc.arg(gate), sqlc.arg(assertion),
    sqlc.arg(step), sqlc.arg(created)
)
ON CONFLICT (epic, task, generation, rung) DO NOTHING;

-- name: InsertEpicTaskRungOutcome :execrows
INSERT INTO epic_task_rung_outcomes (epic, task, generation, rung, outcome, reason, ended)
VALUES (
    sqlc.arg(epic), sqlc.arg(task),
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = sqlc.arg(epic) AND tasks.seq = sqlc.arg(task)),
    sqlc.arg(rung), sqlc.arg(outcome), sqlc.arg(reason), sqlc.arg(ended)
)
ON CONFLICT (epic, task, generation, rung) DO NOTHING;

-- name: ListEpicTaskRungs :many
SELECT rungs.epic, rungs.task, rungs.rung, rungs.kind, rungs.moved_by, rungs.cause, rungs.gate, rungs.assertion,
       rungs.step, rungs.created, outcomes.outcome, outcomes.reason, outcomes.ended
FROM epic_task_rungs AS rungs
LEFT JOIN epic_task_rung_outcomes AS outcomes
    ON outcomes.epic = rungs.epic AND outcomes.task = rungs.task AND outcomes.generation = rungs.generation
        AND outcomes.rung = rungs.rung
WHERE rungs.epic = ? AND rungs.generation = (
    SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = rungs.epic AND tasks.seq = rungs.task
)
ORDER BY rungs.task, rungs.rung;

-- name: InsertEpicTaskRefused :exec
INSERT INTO epic_task_refused (epic, task, path, decision, created) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (epic, task, path) DO NOTHING;

-- name: DeleteEpicTaskRefused :exec
DELETE FROM epic_task_refused WHERE epic = ? AND task = ? AND path = ?;

-- name: ListEpicTaskRefused :many
SELECT * FROM epic_task_refused WHERE epic = ? ORDER BY task, created, path;

-- name: MaxEpicTellSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_tells WHERE epic = ?;

-- name: InsertEpicTell :exec
INSERT INTO epic_tells (epic, seq, text, at, told) VALUES (?, ?, ?, ?, 0);

-- name: InsertEpicQuestionTell :exec
INSERT INTO epic_tells (epic, seq, text, at, told, question) VALUES (?, ?, ?, ?, 0, ?);

-- name: ListEpicTells :many
SELECT * FROM epic_tells WHERE epic = ? ORDER BY seq;

-- name: MarkEpicTellTold :execrows
UPDATE epic_tells SET told = 1 WHERE epic = ? AND seq = ?;

-- name: ListEpicQuestions :many
SELECT * FROM epic_questions WHERE epic = ? ORDER BY seq;

-- name: ListOpenEpicQuestions :many
SELECT * FROM epic_questions WHERE epic = ? AND state = 'open' ORDER BY seq;

-- name: GetEpicQuestion :one
SELECT * FROM epic_questions WHERE epic = ? AND seq = ?;

-- name: InsertEpicMetAtBase :exec
INSERT INTO epic_met_at_base (epic, question, path, attempt, requirements) VALUES (?, ?, ?, ?, ?);

-- name: GetEpicMetAtBase :one
SELECT * FROM epic_met_at_base WHERE epic = ? AND question = ?;

-- name: InsertEpicParkQuestionRun :exec
INSERT INTO epic_park_question_runs (epic, question, path) VALUES (?, ?, ?);

-- name: GetEpicParkQuestionRun :one
SELECT path FROM epic_park_question_runs WHERE epic = ? AND question = ?;

-- name: SetEpicQuestionAnswered :exec
UPDATE epic_questions SET state = 'answered', choice = ? WHERE epic = ? AND seq = ?;

-- name: SetEpicQuestionReplaced :execrows
UPDATE epic_questions SET state = 'replaced', replaced_by = ? WHERE epic = ? AND seq = ? AND state = 'open';

-- name: EscalateEpicQuestion :execrows
UPDATE epic_questions SET escalated = ?, escalation_reason = ?, escalation_maker = ?, escalation_session = ?
WHERE epic = ? AND seq = ? AND state = 'open' AND escalated = 0;

-- name: SetEpicQuestionEscalationTold :execrows
UPDATE epic_questions SET escalation_told = 1 WHERE epic = ? AND seq = ?;

-- name: ListOpenEpicParkQuestions :many
SELECT * FROM epic_questions WHERE epic = ? AND state = 'open' AND turn = 0 AND label = 'park' ORDER BY seq;

-- name: ListEpicQuestionFollows :many
SELECT label, CAST(COUNT(*) AS INTEGER) AS answered,
    CAST(SUM(CASE WHEN choice = recommended THEN 1 ELSE 0 END) AS INTEGER) AS followed
FROM epic_questions
WHERE label <> '' AND recommended <> '' AND state = 'answered'
GROUP BY label ORDER BY label;

-- name: ListEpicTurnTriggers :many
SELECT * FROM epic_triggers WHERE epic = ? AND turn = ? ORDER BY id;

-- name: ListEpicTriggers :many
SELECT * FROM epic_triggers WHERE epic = ? ORDER BY created, id;

-- name: ListEpicsWithTurnsOrUntoldTells :many
SELECT * FROM epics
WHERE EXISTS (SELECT 1 FROM epic_turns WHERE epic_turns.epic = epics.id)
   OR EXISTS (SELECT 1 FROM epic_tells WHERE epic_tells.epic = epics.id AND epic_tells.told = 0)
ORDER BY epics.id;

-- name: GetLatestEndedEpicTurn :one
SELECT * FROM epic_turns WHERE epic = ? AND state <> 'running' ORDER BY seq DESC LIMIT 1;

-- name: CountEpicQuestionsOfTurn :one
SELECT CAST(COUNT(*) AS INTEGER) FROM epic_questions WHERE epic = ? AND turn = ?;

-- name: MarkEpicTurnTold :execrows
UPDATE epic_turns SET told = 1 WHERE epic = ? AND seq = ?;

-- name: SetEpicTurnResets :exec
UPDATE epic_turns SET resets = ? WHERE epic = ? AND seq = ?;

-- name: UpsertEngineRateLimit :exec
INSERT INTO engine_rate_limits (session_id, status, kind, resets_at, at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (session_id)
DO UPDATE SET status = excluded.status, kind = excluded.kind, resets_at = excluded.resets_at, at = excluded.at;

-- name: DeleteEngineRateLimit :exec
DELETE FROM engine_rate_limits WHERE session_id = ?;

-- name: GetEngineRateLimit :one
SELECT * FROM engine_rate_limits WHERE session_id = ?;

-- name: GetStepRateLimit :one
SELECT * FROM engine_rate_limits
WHERE engine_rate_limits.session_id = sqlc.arg(id) AND engine_rate_limits.status = 'rejected'
  AND engine_rate_limits.at >= COALESCE((
      SELECT prompts.at FROM events AS prompts
      WHERE prompts.session_id = sqlc.arg(id) AND prompts.kind = 'engine_prompt'
      ORDER BY prompts.seq DESC
      LIMIT 1
  ), 0);

-- name: InsertEvidenceRun :exec
INSERT INTO evidence_runs (id, epic, item, revision, phase, source, state, spec, created)
VALUES (?, ?, ?, ?, ?, ?, 'capturing', ?, ?);

-- name: GetEvidenceRunState :one
SELECT state FROM evidence_runs WHERE id = ?;

-- name: SetEvidenceRunGrant :exec
UPDATE evidence_runs SET grant_id = ?, holder = ? WHERE id = ?;

-- name: RecordEvidenceRunCapture :exec
UPDATE evidence_runs SET exit = ?, timed_out = ?, not_captured = ?, log_hash = ?, log_bytes = ?, stderr_hash = ?,
    stderr_bytes = ?, state = ?, ended = ?
WHERE id = ?;

-- name: InsertEvidenceArtifact :exec
INSERT INTO evidence_artifacts (run, position, path, state, hash, blob, size) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListEvidenceArtifacts :many
SELECT * FROM evidence_artifacts WHERE run = ? ORDER BY position;

-- name: ListEpicEvidenceArtifacts :many
SELECT * FROM evidence_artifacts
WHERE run IN (SELECT evidence_runs.id FROM evidence_runs WHERE evidence_runs.epic = ?)
ORDER BY run, position;

-- name: ListOpenEvidenceArtifacts :many
SELECT * FROM evidence_artifacts
WHERE run IN (SELECT evidence_runs.id FROM evidence_runs WHERE evidence_runs.state <> 'ended')
ORDER BY run, position;

-- name: SetEvidenceRunJudge :exec
UPDATE evidence_runs SET judge_step = ?, judges = judges + 1 WHERE id = ?;

-- name: RecordEvidenceRunVerdict :exec
UPDATE evidence_runs SET verdict = ?, judge_reason = ?, passed = ?, state = 'ended', ended = ? WHERE id = ?;

-- name: GetEvidenceRun :one
SELECT * FROM evidence_runs WHERE id = ?;

-- name: ListEvidenceRuns :many
SELECT * FROM evidence_runs WHERE epic = ? ORDER BY created, id;

-- name: ListOpenEvidenceRuns :many
SELECT * FROM evidence_runs WHERE state <> 'ended' ORDER BY created, id;

-- name: InsertDueResume :exec
INSERT INTO due_resumes (id, kind, target, resets, state)
SELECT sqlc.arg(id), sqlc.arg(kind), sqlc.arg(target), sqlc.arg(resets), 'due'
WHERE NOT EXISTS (
    SELECT 1 FROM due_resumes
    WHERE due_resumes.kind = sqlc.arg(kind) AND due_resumes.target = sqlc.arg(target)
      AND due_resumes.resets >= sqlc.arg(resets)
);

-- name: GetDueResume :one
SELECT * FROM due_resumes WHERE id = ?;

-- name: ListDueResumes :many
SELECT * FROM due_resumes WHERE state = 'due' ORDER BY resets, id;

-- name: CloseDueResume :exec
UPDATE due_resumes SET state = 'done' WHERE id = ?;

-- name: CloseDueResumesOf :exec
UPDATE due_resumes SET state = 'done' WHERE kind = ? AND target = ?;

-- name: InsertEpicNodeCheck :exec
INSERT INTO epic_node_checks (id, epic, path, revision, argv, limit_seconds, needs, state, not_run, created, ended)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO NOTHING;

-- name: GetEpicNodeCheck :one
SELECT * FROM epic_node_checks WHERE id = ?;

-- name: ListEpicNodeChecks :many
SELECT * FROM epic_node_checks WHERE epic = ? ORDER BY created, id;

-- name: ListOpenEpicNodeChecks :many
SELECT * FROM epic_node_checks WHERE state = 'running' ORDER BY created, id;

-- name: SetEpicNodeCheckGrant :exec
UPDATE epic_node_checks SET grant_id = ?, holder = ? WHERE id = ? AND state = 'running';

-- name: EndEpicNodeCheck :exec
UPDATE epic_node_checks SET state = 'ended', exit = ?, timed_out = ?, not_run = ?, stdout = ?, stderr = ?, ended = ?
WHERE id = ? AND state = 'running';

-- name: InsertEpicReview :exec
INSERT INTO epic_reviews (epic, frozen) VALUES (?, ?)
ON CONFLICT (epic) DO NOTHING;

-- name: GetEpicReview :one
SELECT * FROM epic_reviews WHERE epic = ?;

-- name: SetEpicReviewClean :exec
UPDATE epic_reviews SET clean = ? WHERE epic = ? AND clean = '';

-- name: InsertEpicReviewVerdict :exec
INSERT INTO epic_review_verdicts (epic, position, reviewer, step, verdict, reason) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, position) DO NOTHING;

-- name: ListEpicReviewVerdicts :many
SELECT * FROM epic_review_verdicts WHERE epic = ? ORDER BY position;

-- name: InsertEpicLedgerFinding :exec
INSERT INTO epic_ledger (epic, id, position, reviewer, finding, claim, citation, location, evidence, verify_by, step,
    result, unverified, reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, id) DO NOTHING;

-- name: ListEpicLedger :many
SELECT * FROM epic_ledger WHERE epic = ? ORDER BY position;

-- name: InsertEpicReviewRound :exec
INSERT INTO epic_review_rounds (epic, round, revision, tasks, outcome) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (epic, round) DO NOTHING;

-- name: ListEpicReviewRounds :many
SELECT * FROM epic_review_rounds WHERE epic = ? ORDER BY round;

-- name: InsertEpicClosure :exec
INSERT INTO epic_closures (epic, round, finding, step, result, reason) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, round, finding) DO NOTHING;

-- name: ListEpicClosures :many
SELECT * FROM epic_closures WHERE epic = ? ORDER BY round, finding;

-- name: InsertEpicReviewHold :execrows
INSERT INTO epic_review_holds (epic, step) VALUES (?, ?)
ON CONFLICT (epic, step) DO NOTHING;

-- name: ListEpicReviewHolds :many
SELECT * FROM epic_review_holds WHERE epic = ? ORDER BY step;

-- name: FreezeEpicNodeRun :execrows
UPDATE epic_node_runs SET revision = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND revision = '';

-- name: GetPaneSession :one
SELECT id FROM sessions WHERE kind = '' AND server = ? AND pane = ? ORDER BY id LIMIT 1;

-- name: UpsertEpicSession :exec
INSERT INTO epic_sessions (epic, session, call, at, creator) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (epic, session) DO UPDATE SET call = excluded.call, at = excluded.at,
    creator = MAX(epic_sessions.creator, excluded.creator);

-- name: GetEpicSteerer :one
SELECT sessions.id, sessions.kind, sessions.server, sessions.pane, sessions.state FROM epics JOIN sessions ON sessions.id = epics.steerer WHERE epics.id = ?;

-- name: ListSteeredEpics :many
SELECT * FROM epics WHERE epics.steerer != '' AND epics.steerer = CAST(sqlc.arg(session) AS TEXT) ORDER BY epics.id;

-- name: ListEpicSessions :many
SELECT sessions.id AS session, sessions.kind, sessions.server, sessions.pane, sessions.label, sessions.dir,
       sessions.title, CAST(COALESCE((
           SELECT json_extract(events.payload, '$.text') FROM events
           WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
           ORDER BY events.seq LIMIT 1
       ), '') AS TEXT) AS first_prompt,
       epic_sessions.call, epic_sessions.at, epic_sessions.creator
FROM epic_sessions
JOIN sessions ON sessions.id = epic_sessions.session
WHERE epic_sessions.epic = ?
ORDER BY epic_sessions.at DESC, epic_sessions.session;

-- name: InsertCallerEvent :exec
INSERT INTO caller_events (event, session, at)
SELECT sqlc.arg(event), sqlc.arg(session), sqlc.arg(at)
WHERE NOT EXISTS (
    SELECT 1 FROM events AS prompts
    WHERE prompts.session_id = sqlc.arg(session) AND prompts.kind = 'engine_prompt'
      AND json_extract(prompts.payload, '$.promptId') = sqlc.arg(event)
      AND prompts.seq = (SELECT MAX(newest.seq) FROM events AS newest
                         WHERE newest.session_id = prompts.session_id AND newest.kind = 'engine_prompt'
                           AND json_extract(newest.payload, '$.promptId') = sqlc.arg(event))
      AND COALESCE(json_extract(prompts.payload, '$.sender'), '') = 'event'
      AND EXISTS (
          SELECT 1 FROM prompt_states AS states
          WHERE states.session_id = prompts.session_id AND states.write_id = prompts.write_id
            AND states.state IN ('canceled', 'discarded')
            AND states.seq = (SELECT MAX(latest.seq) FROM prompt_states AS latest
                              WHERE latest.session_id = states.session_id AND latest.write_id = states.write_id)
      )
)
ON CONFLICT (event, session) DO NOTHING;

-- name: CountCallerEvent :one
SELECT COUNT(*) FROM caller_events WHERE event = ? AND session = ?;

-- name: UpsertEpicSeen :exec
INSERT INTO epic_seen (epic, at) VALUES (?, ?)
ON CONFLICT (epic) DO UPDATE SET at = excluded.at;

-- name: GetEpicSeen :one
SELECT at FROM epic_seen WHERE epic = ?;

-- name: ListEpicSeen :many
SELECT epic, at FROM epic_seen;

-- name: InsertPlannerAction :exec
INSERT INTO planner_actions (
    epic, turn, position, kind, node, task, close_kind, by_epic, undo_none, reason, state, refusal, requirement, item,
    deliverable, files, remove_files, reviewer
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', ?, ?, ?, ?, ?, ?);

-- name: ListPendingPlannerActions :many
SELECT * FROM planner_actions WHERE state = 'pending' ORDER BY epic, turn, position;

-- name: CountPendingEpicPlannerActions :one
SELECT CAST(COUNT(*) AS INTEGER) FROM planner_actions WHERE state = 'pending' AND epic = ?;

-- name: SetPlannerActionState :execrows
UPDATE planner_actions SET state = sqlc.arg(state), refusal = sqlc.arg(refusal), park = sqlc.arg(park)
WHERE epic = sqlc.arg(epic) AND turn = sqlc.arg(turn) AND position = sqlc.arg(position) AND state = 'pending';

-- name: MaxTurnActionPark :one
SELECT CAST(COALESCE(MAX(park), 0) AS INTEGER) FROM planner_actions WHERE epic = ? AND turn = ?;

-- name: InsertEpicPark :exec
INSERT INTO epic_parks (epic, seq, class, subject, chain, reason, created) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: MaxEpicParkSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_parks WHERE epic = ?;

-- name: GetLatestEpicPark :one
SELECT * FROM epic_parks WHERE epic = ? ORDER BY seq DESC LIMIT 1;

-- name: GetLatestEpicParkOf :one
SELECT * FROM epic_parks WHERE epic = sqlc.arg(epic) AND class = sqlc.arg(class) AND subject = sqlc.arg(subject)
ORDER BY seq DESC LIMIT 1;

-- name: ListEpicChainParks :many
SELECT * FROM epic_parks WHERE epic = sqlc.arg(epic) AND chain = sqlc.arg(chain) ORDER BY seq;

-- name: ListEpicChainActions :many
SELECT planner_actions.* FROM planner_actions
JOIN epic_parks ON epic_parks.epic = planner_actions.epic AND epic_parks.seq = planner_actions.park
WHERE planner_actions.epic = sqlc.arg(epic) AND epic_parks.chain = sqlc.arg(chain)
  AND planner_actions.state IN ('taken', 'refused')
ORDER BY planner_actions.turn, planner_actions.position;

-- ReplacedEpicTask returns the lowest seq of a task the task named by subject replaces, or 0. Task F is replaced by the
-- task N named by subject when the planner failed F by a taken fail_task action, N was planned in the turn that consumed
-- the trigger T<F>-failed, N shares a claimed or contributed Requirement with F, and F was planned before N, so the
-- replacement chain runs back in seq order.
-- name: ReplacedEpicTask :one
SELECT CAST(COALESCE(MIN(failed.seq), 0) AS INTEGER)
FROM epic_tasks AS failed
JOIN epic_tasks AS planned ON planned.epic = failed.epic AND failed.seq < planned.seq
WHERE planned.epic = sqlc.arg(epic) AND 'T' || planned.seq = CAST(sqlc.arg(subject) AS TEXT)
  AND EXISTS (
      SELECT 1 FROM planner_actions AS action
      WHERE action.epic = failed.epic AND action.kind = 'fail_task' AND action.task = failed.seq
        AND action.state = 'taken'
  )
  AND EXISTS (
      SELECT 1 FROM epic_triggers AS trigger
      WHERE trigger.epic = failed.epic AND trigger.id = 'T' || failed.seq || '-failed'
        AND trigger.turn = planned.turn
  )
  AND EXISTS (
      SELECT 1 FROM (
          SELECT epic, task, requirement FROM epic_task_requirements
          UNION ALL
          SELECT epic, task, requirement FROM epic_task_contributes
      ) AS claimed
      JOIN (
          SELECT epic, task, requirement FROM epic_task_requirements
          UNION ALL
          SELECT epic, task, requirement FROM epic_task_contributes
      ) AS shared ON shared.epic = claimed.epic AND shared.requirement = claimed.requirement
      WHERE claimed.epic = failed.epic AND claimed.task = failed.seq
        AND shared.epic = planned.epic AND shared.task = planned.seq
  );

-- name: GetEpicTask :one
SELECT * FROM epic_tasks WHERE epic = ? AND seq = ?;

-- name: ListOpenEpicSteps :many
SELECT id FROM steps WHERE epic = ? AND end_value = '' ORDER BY started, id;

-- name: ListOpenEpicTaskSteps :many
SELECT id FROM steps WHERE epic = ? AND task = ? AND end_value = '' ORDER BY started, id;

-- name: UpsertArchivedRow :exec
INSERT INTO archived_rows (kind, id, archived_at, unarchived_at) VALUES (?, ?, ?, 0)
ON CONFLICT (kind, id) DO UPDATE SET archived_at = excluded.archived_at, unarchived_at = 0;

-- name: UpsertUnarchivedRow :exec
INSERT INTO archived_rows (kind, id, archived_at, unarchived_at) VALUES (?, ?, 0, ?)
ON CONFLICT (kind, id) DO UPDATE SET archived_at = 0, unarchived_at = excluded.unarchived_at;

-- name: ListArchivedRows :many
SELECT * FROM archived_rows ORDER BY kind, id;

-- name: UpsertPutOffQuestion :exec
INSERT INTO put_off_questions (kind, session_id, question, put_off_at) VALUES (?, ?, ?, ?)
ON CONFLICT(kind, session_id, question) DO UPDATE SET put_off_at = excluded.put_off_at;

-- name: DeletePutOffQuestion :exec
DELETE FROM put_off_questions WHERE kind = ? AND session_id = ? AND question = ?;

-- name: ListPutOffQuestions :many
SELECT * FROM put_off_questions ORDER BY kind, session_id, question;

-- name: InsertPaneSend :exec
INSERT INTO pane_sends (pane, text, client, at) VALUES (?, ?, ?, ?);

-- name: ListPaneSends :many
SELECT * FROM pane_sends WHERE pane = sqlc.arg(pane) AND at >= sqlc.arg(from_at) AND at <= sqlc.arg(to_at)
ORDER BY at, id;

-- name: UpsertClosureVerdict :exec
INSERT INTO closure_verdicts (session_id, changed, closed, why, at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET changed = excluded.changed, closed = excluded.closed, why = excluded.why, at = excluded.at;

-- name: ListClosureVerdicts :many
SELECT * FROM closure_verdicts ORDER BY session_id;

-- name: GetClosureEngineSession :one
SELECT state, changed FROM sessions WHERE id = ? AND kind = 'engine';

-- name: ListEpicIntegrateRunPaths :many
SELECT path FROM epic_node_runs WHERE epic = ? AND node = ? AND kind = 'integrate' ORDER BY rowid;

-- name: InsertEpicAmendment :exec
INSERT INTO epic_amendments (
    epic, seq, requirement, amender, turn, position, prior_item, item, reason, base, state, red_run, created
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'checking', ?, ?);

-- name: MaxEpicAmendmentSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_amendments WHERE epic = ?;

-- name: ListEpicAmendments :many
SELECT * FROM epic_amendments WHERE epic = ? ORDER BY seq;

-- name: ListOpenEpicAmendments :many
SELECT * FROM epic_amendments WHERE state IN ('checking', 'capturing') ORDER BY epic, seq;

-- name: GetEpicAmendment :one
SELECT * FROM epic_amendments WHERE epic = ? AND seq = ?;

-- name: GetActionAmendment :one
SELECT * FROM epic_amendments WHERE epic = ? AND amender = 'planner' AND turn = ? AND position = ?;

-- name: CountOpenRequirementAmendments :one
SELECT COUNT(*) FROM epic_amendments
WHERE epic = ? AND requirement = ? AND state IN ('checking', 'capturing');

-- name: RetryEpicAmendmentRed :execrows
UPDATE epic_amendments SET red_attempt = sqlc.arg(red_attempt), red_run = sqlc.arg(red_run)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND state = 'checking';

-- name: RetryEpicAmendmentGreen :execrows
UPDATE epic_amendments
SET green_attempt = sqlc.arg(green_attempt), green_run = sqlc.arg(green_run), repro_check = sqlc.arg(repro_check)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND state = 'capturing';

-- name: RefuseEpicAmendment :execrows
UPDATE epic_amendments SET state = 'refused', refusal = sqlc.arg(refusal), settled = sqlc.arg(settled)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND state = 'checking';

-- name: CaptureEpicAmendment :execrows
UPDATE epic_amendments
SET state = 'capturing', stored_base = sqlc.arg(stored_base), green_run = sqlc.arg(green_run),
    repro_check = sqlc.arg(repro_check), settled = sqlc.arg(settled)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND state = 'checking';

-- name: ApplyEpicAmendment :execrows
UPDATE epic_amendments SET state = 'applied', settled = CASE WHEN settled = 0 THEN sqlc.arg(settled) ELSE settled END
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND state IN ('checking', 'capturing');

-- CountWorkingEvidenceNodeRuns counts the running node runs of an epic that capture its Requirements under ids of their
-- own: an evidence, integrate or sync run with a revision, and a release run of a releasing epic whose release
-- integrator's latest round that passed at the run's attempt made a merge not yet recorded as a candidate of that
-- attempt, since the release walks that merge's Done gates and captures until it records the merge as its candidate.
-- A release run that is doing anything else, such as waiting on the plan its replan started, captures nothing.
-- name: CountWorkingEvidenceNodeRuns :one
SELECT COUNT(*) FROM epic_node_runs AS run
WHERE run.epic = sqlc.arg(epic) AND run.state = 'running'
  AND (
    (run.kind IN ('evidence', 'integrate', 'sync') AND run.revision != '')
    OR (
      run.kind = 'release'
      AND EXISTS (SELECT 1 FROM epics WHERE epics.id = run.epic AND epics.phase = 'releasing')
      AND EXISTS (
        SELECT 1 FROM epic_release_runs AS merged
        WHERE merged.epic = run.epic AND merged.part = 'integrator' AND merged.attempt = run.attempt AND merged.passed
          AND NOT EXISTS (
            SELECT 1 FROM epic_release_runs AS later
            WHERE later.epic = run.epic AND later.part = 'integrator' AND later.attempt = run.attempt AND later.passed
              AND later.position > merged.position
          )
          AND NOT EXISTS (
            SELECT 1 FROM epic_release_runs AS candidate
            WHERE candidate.epic = run.epic AND candidate.part = 'candidate' AND candidate.attempt = run.attempt
              AND candidate.passed AND candidate.head = merged.head
          )
      )
    )
  );

-- name: CountPendingAmendActions :one
SELECT COUNT(*) FROM planner_actions WHERE epic = ? AND kind = 'amend_evidence' AND state = 'pending';

-- name: GetPlannerActionPark :one
SELECT park FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?;

-- name: LinkPlannerActionPark :exec
UPDATE planner_actions SET park = sqlc.arg(park)
WHERE epic = sqlc.arg(epic) AND turn = sqlc.arg(turn) AND position = sqlc.arg(position) AND state = 'pending';

-- name: InsertCronJob :execrows
INSERT INTO cron_jobs (job_id, session, tool_use_id, cron, prompt, recurring, created_at, last_fire_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session, tool_use_id) DO NOTHING;

-- name: GetCronJobByToolUse :one
SELECT * FROM cron_jobs WHERE session = ? AND tool_use_id = ?;

-- name: DeleteCronJob :execrows
DELETE FROM cron_jobs WHERE session = ? AND job_id = ?;

-- name: ListSessionCronJobs :many
SELECT * FROM cron_jobs WHERE session = ? ORDER BY created_at, rowid;

-- name: ListCronJobs :many
SELECT * FROM cron_jobs ORDER BY created_at, rowid;

-- name: SetCronJobFired :exec
UPDATE cron_jobs SET last_fire_at = ? WHERE job_id = ?;

-- name: DeleteSessionCronJobs :execrows
DELETE FROM cron_jobs WHERE session = ?;

-- name: InsertPromptState :execrows
INSERT INTO prompt_states (session_id, write_id, state)
SELECT sqlc.arg(session_id), sqlc.arg(write_id), sqlc.arg(state)
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_states AS states
    WHERE states.session_id = sqlc.arg(session_id) AND states.write_id = sqlc.arg(write_id)
      AND states.state = 'started')
   OR ('canceled' <> sqlc.arg(state) AND 'discarded' <> sqlc.arg(state));

-- name: DiscardUnstartedPrompts :exec
INSERT INTO prompt_states (session_id, write_id, state)
SELECT prompts.session_id, prompts.write_id, 'discarded' FROM events AS prompts
WHERE prompts.session_id = sqlc.arg(id) AND prompts.kind = 'engine_prompt' AND prompts.write_id <> ''
  AND prompts.seq > (SELECT sessions.prompts_from FROM sessions WHERE sessions.id = sqlc.arg(id))
  AND NOT EXISTS (
      SELECT 1 FROM prompt_states AS states
      WHERE states.session_id = prompts.session_id AND states.write_id = prompts.write_id
        AND states.state <> 'queued'
  );

-- name: ListPromptStates :many
SELECT CAST(COALESCE(json_extract(prompts.payload, '$.promptId'), '') AS TEXT) AS prompt_id,
       CAST(json_extract(prompts.payload, '$.text') AS TEXT) AS text,
       CAST(COALESCE(json_extract(prompts.payload, '$.sender'), '') AS TEXT) AS sender,
       states.state
FROM prompt_states AS states
JOIN events AS prompts ON prompts.session_id = states.session_id AND prompts.kind = 'engine_prompt'
  AND prompts.write_id = states.write_id
WHERE states.session_id = sqlc.arg(id)
  AND states.seq IN (SELECT MAX(newest.seq) FROM prompt_states AS newest
                     WHERE newest.session_id = sqlc.arg(id) GROUP BY newest.write_id)
ORDER BY prompts.seq;

-- name: GetPromptWrite :one
SELECT events.write_id, CAST(json_extract(events.payload, '$.text') AS TEXT) AS text,
       CAST(COALESCE(json_extract(events.payload, '$.sender'), '') AS TEXT) AS sender
FROM events
WHERE events.session_id = sqlc.arg(session_id) AND events.kind = 'engine_prompt'
  AND json_extract(events.payload, '$.promptId') = sqlc.arg(prompt_id)
ORDER BY events.seq
LIMIT 1;

-- name: ListPromptsOfWrites :many
SELECT events.write_id, CAST(COALESCE(json_extract(events.payload, '$.promptId'), '') AS TEXT) AS prompt_id,
       CAST(json_extract(events.payload, '$.text') AS TEXT) AS text,
       CAST(COALESCE(json_extract(events.payload, '$.sender'), '') AS TEXT) AS sender
FROM events
WHERE events.session_id = sqlc.arg(id) AND events.kind = 'engine_prompt'
  AND events.write_id IN (SELECT value FROM json_each(sqlc.arg(write_ids)))
ORDER BY events.seq;

-- name: UnmarkEventOfWrite :exec
DELETE FROM caller_events
WHERE caller_events.session = sqlc.arg(session_id) AND caller_events.event IN (
    SELECT json_extract(prompts.payload, '$.promptId') FROM events AS prompts
    WHERE prompts.session_id = sqlc.arg(session_id) AND prompts.kind = 'engine_prompt'
      AND prompts.write_id <> '' AND prompts.write_id = sqlc.arg(write_id)
      AND COALESCE(json_extract(prompts.payload, '$.sender'), '') = 'event'
      AND COALESCE(json_extract(prompts.payload, '$.promptId'), '') <> ''
);

-- name: UnmarkUnstartedEvents :exec
DELETE FROM caller_events
WHERE caller_events.session = sqlc.arg(id) AND caller_events.event IN (
    SELECT json_extract(prompts.payload, '$.promptId') FROM events AS prompts
    WHERE prompts.session_id = sqlc.arg(id) AND prompts.kind = 'engine_prompt' AND prompts.write_id <> ''
      AND prompts.seq > (SELECT sessions.prompts_from FROM sessions WHERE sessions.id = sqlc.arg(id))
      AND COALESCE(json_extract(prompts.payload, '$.sender'), '') = 'event'
      AND COALESCE(json_extract(prompts.payload, '$.promptId'), '') <> ''
      AND NOT EXISTS (
          SELECT 1 FROM prompt_states AS states
          WHERE states.session_id = prompts.session_id AND states.write_id = prompts.write_id
            AND states.state <> 'queued'
      )
);

-- name: InsertPlannerUnresolvable :exec
INSERT INTO planner_unresolvable (epic, turn, position, gap, reason, recommended) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListUnresolvableOfLastTurn :many
SELECT * FROM planner_unresolvable
WHERE planner_unresolvable.epic = sqlc.arg(epic)
  AND planner_unresolvable.turn = (
      SELECT MAX(epic_turns.seq) FROM epic_turns
      WHERE epic_turns.epic = sqlc.arg(epic) AND epic_turns.state = 'succeeded'
  )
ORDER BY planner_unresolvable.position;

-- name: ListSyncUnresolvable :many
SELECT * FROM planner_unresolvable
WHERE planner_unresolvable.epic = sqlc.arg(epic)
  AND planner_unresolvable.gap GLOB 'sync:*'
  AND planner_unresolvable.turn IN (
      SELECT epic_turns.seq FROM epic_turns
      WHERE epic_turns.epic = sqlc.arg(epic) AND epic_turns.state = 'succeeded'
  )
ORDER BY planner_unresolvable.turn, planner_unresolvable.position;

-- name: ListEpicGapTurnsOf :many
SELECT * FROM epic_gap_turns WHERE epic = ? AND gap = ? ORDER BY turn;

-- name: InsertEpicGapTurn :exec
INSERT INTO epic_gap_turns (epic, gap, turn, reason, created) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicGapTurns :many
SELECT * FROM epic_gap_turns WHERE epic = ? ORDER BY gap, turn;

-- name: InsertEpicReviewRerun :exec
INSERT INTO epic_review_reruns (epic, reviewer, turn, position, created) VALUES (?, ?, ?, ?, ?);

-- name: ListEpicReviewReruns :many
SELECT * FROM epic_review_reruns WHERE epic = ? ORDER BY turn, position;

-- name: DeleteEpicReviewVerdictOf :execrows
DELETE FROM epic_review_verdicts WHERE epic = ? AND reviewer = ?;

-- name: DeleteEpicClosuresOfReviewer :exec
DELETE FROM epic_closures WHERE epic_closures.epic = sqlc.arg(epic) AND epic_closures.finding IN (
    SELECT epic_ledger.id FROM epic_ledger
    WHERE epic_ledger.epic = sqlc.arg(epic) AND epic_ledger.reviewer = sqlc.arg(reviewer)
);

-- name: DeleteEpicLedgerOfReviewer :exec
DELETE FROM epic_ledger WHERE epic = ? AND reviewer = ?;

-- name: SetEpicTaskNotes :exec
UPDATE epic_tasks SET notes = sqlc.arg(notes) WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: SetEpicTaskReviewReason :exec
UPDATE epic_task_reviews SET reason = sqlc.arg(reason)
WHERE epic_task_reviews.epic = sqlc.arg(epic) AND epic_task_reviews.task = sqlc.arg(task)
  AND epic_task_reviews.attempt = sqlc.arg(attempt) AND epic_task_reviews.position = sqlc.arg(position)
  AND epic_task_reviews.generation = (
      SELECT tasks.generation FROM epic_tasks AS tasks
      WHERE tasks.epic = epic_task_reviews.epic AND tasks.seq = epic_task_reviews.task
  );

-- name: ClearEpicTaskBatch :exec
UPDATE epic_tasks SET batch = '' WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: NextEpicTaskGeneration :exec
UPDATE epic_tasks SET generation = generation + 1 WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: CountContract :one
SELECT CAST(COUNT(*) AS INTEGER) FROM contracts WHERE id = ?;

-- name: InsertContract :exec
INSERT INTO contracts (id, created) VALUES (?, ?);

-- name: InsertContractVersion :exec
INSERT INTO contract_versions (contract, version, repository, title, care, body, decisions, open_decisions, words,
                               writer, at, changed_rows, changed_sections)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertContractState :exec
INSERT INTO contract_states (contract, seq, state, version, session, words, at, base) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListContractListings :many
SELECT contracts.id, contracts.created,
       newest.repository, newest.title, newest.care, newest.version, newest.open_decisions,
       CAST(COALESCE((
           SELECT opening.writer FROM contract_versions AS opening
           WHERE opening.contract = contracts.id AND opening.version = 1
       ), '') AS TEXT) AS conversation,
       CAST((
           SELECT newest_state.state FROM contract_states AS newest_state
           WHERE newest_state.contract = contracts.id ORDER BY newest_state.seq DESC LIMIT 1
       ) AS TEXT) AS state,
       CAST(MAX(newest.at, (
           SELECT newest_state.at FROM contract_states AS newest_state
           WHERE newest_state.contract = contracts.id ORDER BY newest_state.seq DESC LIMIT 1
       )) AS INTEGER) AS changed
FROM contracts
JOIN contract_versions AS newest ON newest.contract = contracts.id
WHERE newest.version = (
    SELECT MAX(versions.version) FROM contract_versions AS versions WHERE versions.contract = contracts.id
)
ORDER BY contracts.created, contracts.id;

-- name: GetContractListing :one
SELECT contracts.id, contracts.created,
       newest.repository, newest.title, newest.care, newest.version, newest.open_decisions,
       CAST(COALESCE((
           SELECT opening.writer FROM contract_versions AS opening
           WHERE opening.contract = contracts.id AND opening.version = 1
       ), '') AS TEXT) AS conversation,
       CAST((
           SELECT newest_state.state FROM contract_states AS newest_state
           WHERE newest_state.contract = contracts.id ORDER BY newest_state.seq DESC LIMIT 1
       ) AS TEXT) AS state,
       CAST(MAX(newest.at, (
           SELECT newest_state.at FROM contract_states AS newest_state
           WHERE newest_state.contract = contracts.id ORDER BY newest_state.seq DESC LIMIT 1
       )) AS INTEGER) AS changed
FROM contracts
JOIN contract_versions AS newest ON newest.contract = contracts.id
WHERE contracts.id = ? AND newest.version = (
    SELECT MAX(versions.version) FROM contract_versions AS versions WHERE versions.contract = contracts.id
);

-- name: ListContractVersions :many
SELECT * FROM contract_versions WHERE contract = ? ORDER BY version;

-- name: ListContractStates :many
SELECT * FROM contract_states WHERE contract = ? ORDER BY seq;

-- name: CountContractStates :one
SELECT CAST(COUNT(*) AS INTEGER) FROM contract_states WHERE contract = ?;

-- name: GetContractVersion :one
SELECT * FROM contract_versions WHERE contract = ? AND version = ?;

-- name: ListContractChangeEvents :many
SELECT states.contract, states.seq, states.state, states.version, states.session, states.words,
       entered.title, opening.writer AS conversation
FROM contract_states AS states
JOIN contract_versions AS entered ON entered.contract = states.contract AND entered.version = states.version
JOIN contract_versions AS opening ON opening.contract = states.contract AND opening.version = 1
WHERE states.session <> opening.writer
ORDER BY states.at, states.contract, states.seq;

-- name: InsertIntegrateRound :exec
INSERT INTO integrate_rounds (epic, path, attempt, task, round, step, verdict, head, explanation, outcome, failure, created)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, path, attempt, task, round) DO NOTHING;

-- name: ComposeIntegrateRound :execrows
UPDATE integrate_rounds SET outcome = 'composed'
WHERE integrate_rounds.epic = sqlc.arg(epic) AND integrate_rounds.path = sqlc.arg(path)
  AND integrate_rounds.attempt = sqlc.arg(attempt) AND integrate_rounds.task = sqlc.arg(task)
  AND integrate_rounds.round = sqlc.arg(round) AND integrate_rounds.outcome = ''
  AND EXISTS (
      SELECT 1 FROM epic_node_runs AS runs
      WHERE runs.epic = integrate_rounds.epic AND runs.path = integrate_rounds.path
        AND runs.attempt = integrate_rounds.attempt AND runs.state = 'running'
  );

-- name: SetIntegrateRunWork :exec
UPDATE epic_node_runs SET revision = sqlc.arg(revision), result = sqlc.arg(result)
WHERE epic = sqlc.arg(epic) AND path = sqlc.arg(path) AND attempt = sqlc.arg(attempt) AND state = 'running';

-- name: FailIntegrateRound :execrows
UPDATE integrate_rounds SET outcome = 'failed', failure = sqlc.arg(failure)
WHERE epic = sqlc.arg(epic) AND path = sqlc.arg(path) AND attempt = sqlc.arg(attempt)
  AND task = sqlc.arg(task) AND round = sqlc.arg(round) AND outcome = '';

-- name: ListEpicIntegrateRounds :many
SELECT * FROM integrate_rounds WHERE epic = ? ORDER BY path, attempt, task, round;

-- name: InsertLLMAccount :exec
INSERT INTO llm_accounts (id, name, provider, plan, token, fingerprint, created) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetLLMAccount :one
SELECT id, name, provider, plan, fingerprint, created FROM llm_accounts WHERE id = ?;

-- name: ListLLMAccounts :many
SELECT id, name, provider, plan, fingerprint, created FROM llm_accounts ORDER BY created, id;

-- name: ListLLMAccountIDs :many
SELECT id FROM llm_accounts ORDER BY id;

-- name: CountLLMAccount :one
SELECT CAST(COUNT(*) AS INTEGER) FROM llm_accounts WHERE id = ?;

-- name: RenameLLMAccount :execrows
UPDATE llm_accounts SET name = ? WHERE id = ?;

-- name: DeleteLLMAccount :exec
DELETE FROM llm_accounts WHERE id = ?;

-- name: ListRouteContextsOfAccount :many
SELECT id FROM route_contexts WHERE account = ? AND account <> '' ORDER BY id;

-- name: DeleteRouteBindings :exec
DELETE FROM route_bindings;

-- name: DeleteRouteContexts :exec
DELETE FROM route_contexts;

-- name: DeleteRouteSeats :exec
DELETE FROM route_seats;

-- name: DeleteRouteRegistry :exec
DELETE FROM route_registry;

-- name: InsertRouteRegistry :exec
INSERT INTO route_registry (id, default_context) VALUES (1, ?);

-- name: GetRouteRegistry :one
SELECT * FROM route_registry WHERE id = 1;

-- name: InsertRouteSeat :exec
INSERT INTO route_seats (id, auth_mode, upstream, model, api_key_env_file, aws_profile, aws_region, model_map_env_file,
                         billing, speed, cache_ttl, insecure, strip_cache_control, strict_structured_output,
                         max_input_tokens, strip_betas)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListRouteSeats :many
SELECT * FROM route_seats ORDER BY id;

-- name: InsertRouteContext :exec
INSERT INTO route_contexts (id, default_seat, account, has_subagents, subagent_default_seat) VALUES (?, ?, ?, ?, ?);

-- name: ListRouteContexts :many
SELECT * FROM route_contexts ORDER BY id;

-- name: CountRouteContext :one
SELECT CAST(COUNT(*) AS INTEGER) FROM route_contexts WHERE id = ?;

-- name: SetRouteContextAccount :exec
UPDATE route_contexts SET account = ? WHERE id = ?;

-- name: InsertRouteBinding :exec
INSERT INTO route_bindings (context, scope, selector, seat) VALUES (?, ?, ?, ?);

-- name: ListRouteBindings :many
SELECT * FROM route_bindings ORDER BY context, scope, selector;

-- name: ListRouteAccountTokens :many
SELECT id, token FROM llm_accounts WHERE id IN (SELECT account FROM route_contexts WHERE account <> '') ORDER BY id;
