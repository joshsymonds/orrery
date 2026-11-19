-- +goose Up
-- step_listings is each step as a list of steps reads it: the columns a listed step carries, without the prompt, the
-- output schema, the agents, the owned paths, the gate's argv, source and needs, the checkout and the loop's nudges,
-- which a step row holds and a list never sends. Of the result it keeps only what a list reads of it: summary is the
-- string summary of a result that is a JSON object, and red_evidence and green_evidence say whether the result holds
-- that key as evidence, a string that trimmed of white space is not empty and does not start with NOT RUN in any case.
-- A key a result holds more than once is read at its last value, a value of another type is no summary and no evidence,
-- and a result that is not JSON has neither, so no result fails the read. The white space trimmed is every character
-- Go's unicode.IsSpace reports, and upper changes only ASCII letters, as no other character's upper case is one of
-- NOT RUN.
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

-- +goose Down
DROP VIEW step_listings;
