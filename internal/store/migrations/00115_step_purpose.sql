-- +goose Up
-- A step's purpose: what the step is for, from its start's purpose or else from a Haiku call on its prompt. It is empty
-- until one is stored, and SetStepPurpose writes it only while it is empty, so a step's purpose is written once. The
-- column is added at the end of steps with a default, so every stored step reads back with no purpose. step_listings
-- is recreated with the column, since a view reads only the columns its definition names. The view keeps the columns
-- migration 00109 gave it, and adds purpose after task.
ALTER TABLE steps ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
DROP VIEW step_listings;
CREATE VIEW step_listings AS
SELECT
    steps.id,
    steps.agent,
    steps.workspace,
    steps.revision,
    steps.worktree,
    steps.read_only,
    steps.started,
    steps.checks,
    steps.doing,
    steps.verdicts,
    steps.status,
    steps.end_value,
    steps.reason,
    steps.ended,
    steps.epic,
    steps.task,
    steps.purpose,
    steps.gate_name,
    steps.gate_limit,
    steps.loop_limited,
    steps.loop_limit_kind,
    steps.loop_limit_resets,
    steps.loop_stage,
    steps.loop_stage_from,
    steps.caller,
    CAST(CASE WHEN json_valid(steps.result) THEN COALESCE((
        SELECT CASE WHEN field.type = 'text' THEN field.value ELSE '' END
        FROM json_each(steps.result) AS field
        WHERE field.key = 'summary'
        ORDER BY field.id DESC
        LIMIT 1
    ), '') ELSE '' END AS TEXT) AS summary,
    steps.host
FROM steps;

-- +goose Down
-- The view is recreated as migration 00109 defines it, without purpose, before the column is dropped. The view is
-- dropped first because its definition names the column.
DROP VIEW step_listings;
CREATE VIEW step_listings AS
SELECT
    steps.id,
    steps.agent,
    steps.workspace,
    steps.revision,
    steps.worktree,
    steps.read_only,
    steps.started,
    steps.checks,
    steps.doing,
    steps.verdicts,
    steps.status,
    steps.end_value,
    steps.reason,
    steps.ended,
    steps.epic,
    steps.task,
    steps.gate_name,
    steps.gate_limit,
    steps.loop_limited,
    steps.loop_limit_kind,
    steps.loop_limit_resets,
    steps.loop_stage,
    steps.loop_stage_from,
    steps.caller,
    CAST(CASE WHEN json_valid(steps.result) THEN COALESCE((
        SELECT CASE WHEN field.type = 'text' THEN field.value ELSE '' END
        FROM json_each(steps.result) AS field
        WHERE field.key = 'summary'
        ORDER BY field.id DESC
        LIMIT 1
    ), '') ELSE '' END AS TEXT) AS summary,
    steps.host
FROM steps;
ALTER TABLE steps DROP COLUMN purpose;
