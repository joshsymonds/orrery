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
SELECT seq, session_id, kind, at, payload, write_id FROM events WHERE seq > ? ORDER BY seq LIMIT ?;

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

-- name: UpdateEngineConsumed :execrows
UPDATE sessions
SET consumed = sqlc.arg(consumed),
    replay_from = COALESCE(sqlc.narg(replay_from), replay_from),
    turn_output = COALESCE(sqlc.narg(turn_output), turn_output),
    background = COALESCE(sqlc.narg(background), background)
WHERE id = sqlc.arg(id) AND kind IN ('engine', 'step');

-- name: UpdateEngineLaunch :execrows
UPDATE sessions
SET consumed = 0, replay_from = 0, background = 0, prompts_from = sqlc.arg(prompts_from)
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
SELECT seq, session_id, kind, at, payload, write_id FROM events
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

-- name: InsertEnginePromptEvent :exec
INSERT INTO events (session_id, kind, at, payload, write_id) VALUES (?, 'engine_prompt', ?, ?, ?);

-- name: ListHolderPrompts :many
SELECT events.write_id, CAST(json_extract(events.payload, '$.text') AS TEXT) AS text FROM events
WHERE events.session_id = sqlc.arg(id) AND events.kind = 'engine_prompt' AND events.write_id <> ''
  AND events.seq > (SELECT sessions.prompts_from FROM sessions WHERE sessions.id = sqlc.arg(id))
ORDER BY events.seq;

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
UPDATE steps
SET result = ?, checks = ?,
    loop_seq = (SELECT CAST(COALESCE(MAX(events.seq), 0) AS INTEGER) FROM events WHERE events.session_id = steps.id),
    gate_grant = '', gate_holder = '', gate_round = 0
WHERE id = ?;

-- name: UpdateStepLoop :exec
UPDATE steps
SET loop_round = ?, loop_continues = ?, loop_nudge = ?, loop_owner_nudge = ?, loop_nudged = ?, loop_held = ?,
    loop_judge_sent = ?
WHERE id = ?;

-- name: UpdateStepGate :exec
UPDATE steps SET gate_grant = ?, gate_holder = ?, gate_round = ? WHERE id = ?;

-- name: GetStepTurnEnd :one
SELECT events.seq, CAST(json_extract(events.payload, '$.isError') AS INTEGER) AS is_error, sessions.turn_output,
       sessions.background, steps.loop_seq
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

-- name: UpdateStepWatch :exec
UPDATE steps SET verdicts = ?, doing = ? WHERE id = ?;

-- name: UpdateStepEnd :exec
UPDATE steps SET end_value = ?, status = ?, reason = ?, ended = ? WHERE id = ?;

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
INSERT INTO pending_notices (id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, source, silent, epic)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET pane = excluded.pane, session = excluded.session, state = excluded.state,
    time = excluded.time, phase = excluded.phase, due = excluded.due, delay_served = excluded.delay_served,
    watch_logged = excluded.watch_logged, source_kind = excluded.source_kind, source = excluded.source,
    silent = excluded.silent, epic = excluded.epic;

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

-- name: InsertEpic :exec
INSERT INTO epics (id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountEpic :one
SELECT COUNT(*) FROM epics WHERE id = ?;

-- name: GetEpic :one
SELECT * FROM epics WHERE id = ?;

-- name: ListEpics :many
SELECT * FROM epics ORDER BY id;

-- name: UpdateEpicChanged :exec
UPDATE epics SET changed = ? WHERE id = ?;

-- name: InsertEpicFailure :exec
INSERT INTO epic_failures (epic, position, id, text, rating, response) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListEpicFailures :many
SELECT * FROM epic_failures WHERE epic = ? ORDER BY position;

-- name: InsertEpicAddition :exec
INSERT INTO epic_additions (epic, position, location, what, why, cost) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListEpicAdditions :many
SELECT * FROM epic_additions WHERE epic = ? ORDER BY position;

-- name: InsertEpicRequirement :exec
INSERT INTO epic_requirements (epic, position, id, text, evidence, kind) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListEpicRequirements :many
SELECT * FROM epic_requirements WHERE epic = ? ORDER BY position;

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

-- name: InsertEpicTrigger :exec
INSERT INTO epic_triggers (epic, id, cause, state, turn, created) VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertEpicDecision :exec
INSERT INTO epic_decisions (epic, seq, client_id, text, time, question) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetEpicDecisionByClientID :one
SELECT * FROM epic_decisions WHERE epic = ? AND client_id = ?;

-- name: MaxEpicDecisionSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_decisions WHERE epic = ?;

-- name: ListEpicDecisions :many
SELECT * FROM epic_decisions WHERE epic = ? ORDER BY seq;

-- name: ListEpicPlanStates :many
SELECT epics.id, epics.paused, epics.parked,
    CAST(EXISTS (SELECT 1 FROM epic_turns WHERE epic_turns.epic = epics.id AND epic_turns.state = 'running') AS INTEGER) AS running,
    CAST((SELECT COUNT(*) FROM epic_triggers WHERE epic_triggers.epic = epics.id AND epic_triggers.state = 'pending') AS INTEGER) AS pending
FROM epics
WHERE EXISTS (SELECT 1 FROM epic_triggers WHERE epic_triggers.epic = epics.id AND epic_triggers.state = 'pending')
ORDER BY epics.id;

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
INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, task_check, status) VALUES (?, ?, ?, ?, ?, ?, 'open');

-- name: InsertEpicTaskFile :exec
INSERT INTO epic_task_files (epic, task, position, path) VALUES (?, ?, ?, ?);

-- name: InsertEpicTaskRequirement :exec
INSERT INTO epic_task_requirements (epic, task, position, requirement) VALUES (?, ?, ?, ?);

-- name: MaxEpicQuestionSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_questions WHERE epic = ?;

-- name: InsertEpicQuestion :exec
INSERT INTO epic_questions (epic, seq, turn, text, state) VALUES (?, ?, ?, ?, 'open');

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
UPDATE epics SET parked = ? WHERE id = ?;

-- name: PauseEpic :exec
UPDATE epics SET paused = 1 WHERE id = ?;

-- name: ResumeEpic :exec
UPDATE epics SET paused = 0 WHERE id = ?;

-- name: RetryEpic :exec
UPDATE epics SET parked = '', retried = ? WHERE id = ?;

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

-- name: ListEpicQuestions :many
SELECT * FROM epic_questions WHERE epic = ? ORDER BY seq;

-- name: ListOpenEpicQuestions :many
SELECT * FROM epic_questions WHERE epic = ? AND state = 'open' ORDER BY seq;

-- name: GetEpicQuestion :one
SELECT * FROM epic_questions WHERE epic = ? AND seq = ?;

-- name: SetEpicQuestionAnswered :exec
UPDATE epic_questions SET state = 'answered' WHERE epic = ? AND seq = ?;

-- name: ListEpicTurnTriggers :many
SELECT * FROM epic_triggers WHERE epic = ? AND turn = ? ORDER BY id;

-- name: UpsertEngineRateLimit :exec
INSERT INTO engine_rate_limits (session_id, status, kind, resets_at, at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (session_id)
DO UPDATE SET status = excluded.status, kind = excluded.kind, resets_at = excluded.resets_at, at = excluded.at;

-- name: GetEngineRateLimit :one
SELECT * FROM engine_rate_limits WHERE session_id = ?;
