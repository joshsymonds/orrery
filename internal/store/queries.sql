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
SELECT seq, session_id, kind, at, payload, write_id, answered FROM events
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
  );

-- name: InsertEnginePromptFailed :exec
INSERT INTO engine_prompt_failed (session_id, prompt_id, reason) VALUES (?, ?, ?)
ON CONFLICT (session_id, prompt_id) DO NOTHING;

-- name: GetEnginePromptFailure :one
SELECT reason FROM engine_prompt_failed WHERE session_id = ? AND prompt_id = ?;

-- name: InsertStep :exec
INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree, checkout_head, checkout_hash,
                   done_value, read_only, owned, unowned, gate, gate_name, gate_source, gate_needs, gate_limit,
                   model_limit, started, checks, result, status, end_value, reason, ended, doing, verdicts, epic, task,
                   loop_stage, loop_stage_from, agents)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '', '', '', '', 0, '', '[]', ?, ?, ?, ?, ?);

-- name: GetStep :one
SELECT * FROM steps WHERE id = ?;

-- name: ListOpenSteps :many
SELECT * FROM steps WHERE end_value = '' ORDER BY id;

-- name: ListLabelledSteps :many
SELECT * FROM steps
WHERE epic <> '' AND (CAST(sqlc.arg(epic) AS TEXT) = '' OR epic = CAST(sqlc.arg(epic) AS TEXT))
  AND (CAST(sqlc.arg(task) AS TEXT) = '' OR task = CAST(sqlc.arg(task) AS TEXT))
ORDER BY started, id;

-- name: ListLimitedSteps :many
SELECT * FROM steps WHERE end_value = '' AND loop_limited = 1 ORDER BY started, id;

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

-- name: InsertToldLimitStep :exec
INSERT INTO told_limit_steps (limit_key, step) VALUES (?, ?)
ON CONFLICT (limit_key, step) DO NOTHING;

-- name: DeleteToldLimit :exec
DELETE FROM told_limit_steps WHERE limit_key = ?;

-- name: ListToldLimitSteps :many
SELECT * FROM told_limit_steps ORDER BY limit_key, step;

-- name: InsertEpic :exec
INSERT INTO epics (id, repository, base, stored_base, title, approved, intent, care, approach, release_then, checks,
    phase, created, changed, workflow, workflow_text, workflow_sha256)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountEpic :one
SELECT COUNT(*) FROM epics WHERE id = ?;

-- name: GetEpic :one
SELECT * FROM epics WHERE id = ?;

-- name: ListEpics :many
SELECT * FROM epics ORDER BY id;

-- name: ListBuildEpics :many
SELECT epics.id FROM epics
WHERE epics.base <> '' AND epics.phase NOT IN ('met_at_base', 'clean', 'ended_with_gaps', 'released', 'done', 'stopped')
  AND EXISTS (SELECT 1 FROM epic_tasks WHERE epic_tasks.epic = epics.id AND epic_tasks.status = 'open')
ORDER BY epics.id;

-- name: UpdateEpicChanged :exec
UPDATE epics SET changed = ? WHERE id = ?;

-- name: SetEpicBranch :exec
UPDATE epics SET branch = ? WHERE id = ?;

-- name: ListUnsummarizedEpics :many
SELECT * FROM epics WHERE summary = '' ORDER BY id;

-- name: SetEpicSummary :exec
UPDATE epics SET summary = ? WHERE id = ? AND summary = '';

-- name: MoveEpicStoredBase :exec
UPDATE epics SET stored_base = ?, candidate = '' WHERE id = ?;

-- name: SetEpicCandidate :exec
UPDATE epics SET candidate = ? WHERE id = ?;

-- name: SetEpicPhase :exec
UPDATE epics SET phase = ? WHERE id = ?;

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
INSERT INTO epic_release_slots (repository, epic) VALUES (?, ?)
ON CONFLICT (repository) DO NOTHING;

-- name: GetEpicReleaseSlotHolder :one
SELECT epic FROM epic_release_slots WHERE repository = ?;

-- name: FreeEpicReleaseSlot :exec
DELETE FROM epic_release_slots WHERE epic = ?;

-- name: StartEpicRelease :exec
UPDATE epics SET phase = 'releasing' WHERE id = ? AND phase = 'clean';

-- name: ResumeEpicRelease :exec
UPDATE epics SET phase = 'releasing' WHERE id = ? AND phase = 'stopped';

-- name: StopEpicRelease :exec
UPDATE epics SET phase = 'stopped' WHERE id = ? AND phase = 'releasing';

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

-- name: InsertEpicNodeRun :exec
INSERT INTO epic_node_runs (epic, path, id, node, kind, task, round, state, outcome, loop_round, granted, mark, started, ended, attempt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (epic, path) DO NOTHING;

-- name: ListEpicNodeRuns :many
SELECT * FROM epic_node_runs WHERE epic = ? ORDER BY path;

-- name: EndEpicNodeRun :exec
UPDATE epic_node_runs SET state = 'ended', outcome = ?, ended = ?, output = ? WHERE epic = ? AND path = ? AND state = 'running';

-- name: PrepareEpicNodeRun :exec
UPDATE epic_node_runs SET revision = ?, refs = ?
WHERE epic = ? AND path = ? AND attempt = ? AND state = 'running' AND revision = '';

-- name: RecordEpicNodeResult :exec
UPDATE epic_node_runs SET result = ?
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
INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, gate, evidence, status) VALUES (?, ?, ?, ?, ?, ?, ?, 'open');

-- name: InsertEpicTaskFile :exec
INSERT INTO epic_task_files (epic, task, position, path) VALUES (?, ?, ?, ?);

-- name: InsertEpicTaskRequirement :exec
INSERT INTO epic_task_requirements (epic, task, position, requirement) VALUES (?, ?, ?, ?);

-- name: SetEpicTaskBuild :execrows
UPDATE epic_tasks
SET stage = sqlc.arg(to_stage), base = sqlc.arg(to_base), attempt = sqlc.arg(to_attempt),
    rerun = sqlc.arg(to_rerun), no_verdict = sqlc.arg(to_no_verdict), held = sqlc.arg(to_held),
    step = sqlc.arg(to_step), red_run = sqlc.arg(to_red_run), green_run = sqlc.arg(to_green_run),
    revision = sqlc.arg(to_revision), failure = sqlc.arg(to_failure), review = sqlc.arg(to_review),
    refs = sqlc.arg(to_refs), gate_pos = sqlc.arg(to_gate_pos), finding_pos = sqlc.arg(to_finding_pos)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND status = 'open'
  AND stage = sqlc.arg(from_stage) AND base = sqlc.arg(from_base) AND attempt = sqlc.arg(from_attempt)
  AND rerun = sqlc.arg(from_rerun) AND no_verdict = sqlc.arg(from_no_verdict) AND held = sqlc.arg(from_held)
  AND step = sqlc.arg(from_step) AND red_run = sqlc.arg(from_red_run) AND green_run = sqlc.arg(from_green_run)
  AND revision = sqlc.arg(from_revision) AND failure = sqlc.arg(from_failure) AND review = sqlc.arg(from_review)
  AND refs = sqlc.arg(from_refs) AND gate_pos = sqlc.arg(from_gate_pos) AND finding_pos = sqlc.arg(from_finding_pos);

-- name: SetEpicTaskStatus :exec
UPDATE epic_tasks SET status = sqlc.arg(status) WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq);

-- name: ClaimEpicTaskBatch :exec
UPDATE epic_tasks SET batch = sqlc.arg(batch)
WHERE epic = sqlc.arg(epic) AND seq = sqlc.arg(seq) AND status = 'done' AND (batch = '' OR batch = sqlc.arg(batch));

-- name: InsertEpicTaskReview :exec
INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertEpicTaskFinding :exec
INSERT INTO epic_task_findings (epic, task, attempt, review, position, id, claim, citation, location, evidence, verify_by,
                                step, result, reason, unverified)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertEpicReviewer :exec
INSERT INTO epic_reviewers (epic, position, name, enabled, stage, must, must_not, prompt, agent, model, tools, effort,
                            limit_seconds, verify)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEpicReviewers :many
SELECT * FROM epic_reviewers WHERE epic = ? ORDER BY position;

-- name: SetEpicTaskFindingResult :execrows
UPDATE epic_task_findings
SET step = sqlc.arg(step), result = sqlc.arg(result), reason = sqlc.arg(reason)
WHERE epic = sqlc.arg(epic) AND task = sqlc.arg(task) AND attempt = sqlc.arg(attempt)
  AND review = sqlc.arg(review) AND position = sqlc.arg(position) AND result = '';

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

-- name: SetEpicPauseSource :exec
UPDATE epics SET pause_source = ? WHERE id = ?;

-- name: ResumeEpic :exec
UPDATE epics SET paused = 0, pause_source = '' WHERE id = ?;

-- name: RetryEpic :exec
UPDATE epics SET parked = '', retried = ? WHERE id = ?;

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

-- name: ListEpicTaskReviews :many
SELECT * FROM epic_task_reviews WHERE epic = ? ORDER BY task, attempt, position;

-- name: ListEpicTaskFindings :many
SELECT * FROM epic_task_findings WHERE epic = ? ORDER BY task, attempt, review, position;

-- name: MaxEpicTellSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM epic_tells WHERE epic = ?;

-- name: InsertEpicTell :exec
INSERT INTO epic_tells (epic, seq, text, at, told) VALUES (?, ?, ?, ?, 0);

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

-- name: SetEpicQuestionAnswered :exec
UPDATE epic_questions SET state = 'answered' WHERE epic = ? AND seq = ?;

-- name: ListEpicTurnTriggers :many
SELECT * FROM epic_triggers WHERE epic = ? AND turn = ? ORDER BY id;

-- name: ListEpicTriggers :many
SELECT * FROM epic_triggers WHERE epic = ? ORDER BY created, id;

-- name: ListEpicsWithTurns :many
SELECT * FROM epics
WHERE EXISTS (SELECT 1 FROM epic_turns WHERE epic_turns.epic = epics.id)
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
