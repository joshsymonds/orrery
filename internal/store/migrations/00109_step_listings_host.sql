-- +goose Up
-- step_listings carries the host a step's work lives on, the steps row's host, so a listed step names its host as the
-- stored step does. The view is otherwise as 00094_step_listings_no_evidence.sql made it.
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

-- +goose Down
-- The view goes back to the columns 00094_step_listings_no_evidence.sql gave it, without host.
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
    ), '') ELSE '' END AS TEXT) AS summary
FROM steps;
