-- +goose Up
-- step_listings drops red_evidence and green_evidence, which only the epic chart's slices read, and the slices are
-- gone: a task's place and tone are read from the task. The view is otherwise as 00068_step_listings.sql made it.
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

-- +goose Down
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
    CAST(CASE WHEN json_valid(steps.result) THEN COALESCE((
        SELECT last.type = 'text' AND last.text <> '' AND upper(substr(last.text, 1, 7)) <> 'NOT RUN'
        FROM (
            SELECT field.type, ltrim(field.value, char(9, 10, 11, 12, 13, 32, 133, 160, 5760, 8192, 8193, 8194, 8195,
                8196, 8197, 8198, 8199, 8200, 8201, 8202, 8232, 8233, 8239, 8287, 12288)) AS text
            FROM json_each(steps.result) AS field
            WHERE field.key = 'red_evidence'
            ORDER BY field.id DESC
            LIMIT 1
        ) AS last
    ), 0) ELSE 0 END AS BOOLEAN) AS red_evidence,
    CAST(CASE WHEN json_valid(steps.result) THEN COALESCE((
        SELECT last.type = 'text' AND last.text <> '' AND upper(substr(last.text, 1, 7)) <> 'NOT RUN'
        FROM (
            SELECT field.type, ltrim(field.value, char(9, 10, 11, 12, 13, 32, 133, 160, 5760, 8192, 8193, 8194, 8195,
                8196, 8197, 8198, 8199, 8200, 8201, 8202, 8232, 8233, 8239, 8287, 12288)) AS text
            FROM json_each(steps.result) AS field
            WHERE field.key = 'green_evidence'
            ORDER BY field.id DESC
            LIMIT 1
        ) AS last
    ), 0) ELSE 0 END AS BOOLEAN) AS green_evidence
FROM steps;
