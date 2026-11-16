-- +goose Up
UPDATE epic_requirements SET item = CASE
    WHEN json_array_length(item, '$.capture') > 0 THEN json_set(item, '$.kind', 'capture')
    WHEN json_array_length(item, '$.supplied') > 0 THEN json_set(item, '$.kind', 'supplied')
    ELSE item
END
WHERE item <> '';

UPDATE epic_tasks SET evidence = CASE
    WHEN json_array_length(evidence, '$.capture') > 0 THEN json_set(evidence, '$.kind', 'capture')
    WHEN json_array_length(evidence, '$.supplied') > 0 THEN json_set(evidence, '$.kind', 'supplied')
    ELSE evidence
END
WHERE evidence <> '';

UPDATE evidence_runs SET spec = CASE
    WHEN json_array_length(spec, '$.capture') > 0 THEN json_set(spec, '$.kind', 'capture')
    WHEN json_array_length(spec, '$.supplied') > 0 THEN json_set(spec, '$.kind', 'supplied')
    ELSE spec
END
WHERE spec <> '';

CREATE TABLE epic_task_contributes (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    position INTEGER NOT NULL,
    requirement TEXT NOT NULL,
    PRIMARY KEY (epic, task, position)
);

INSERT INTO epic_task_contributes (epic, task, position, requirement)
SELECT r.epic, r.task, r.position, r.requirement
FROM epic_task_requirements AS r
JOIN epic_tasks AS t ON t.epic = r.epic AND t.seq = r.task
WHERE t.status <> 'failed' AND EXISTS (
    SELECT 1
    FROM epic_task_requirements AS earlier
    JOIN epic_tasks AS earlier_task ON earlier_task.epic = earlier.epic AND earlier_task.seq = earlier.task
    WHERE earlier.epic = r.epic AND earlier.requirement = r.requirement AND earlier_task.status <> 'failed'
        AND (earlier.task < r.task OR (earlier.task = r.task AND earlier.position < r.position))
);

DELETE FROM epic_task_requirements
WHERE (epic, task, position) IN (SELECT epic, task, position FROM epic_task_contributes);

-- +goose Down
INSERT INTO epic_task_requirements (epic, task, position, requirement)
SELECT c.epic, c.task, c.position + COALESCE((
    SELECT MAX(r.position) FROM epic_task_requirements AS r WHERE r.epic = c.epic AND r.task = c.task
), 0), c.requirement
FROM epic_task_contributes AS c;

DROP TABLE epic_task_contributes;

UPDATE epic_requirements SET item = json_remove(item, '$.kind') WHERE item <> '';
UPDATE epic_tasks SET evidence = json_remove(evidence, '$.kind') WHERE evidence <> '';
UPDATE evidence_runs SET spec = json_remove(spec, '$.kind') WHERE spec <> '';
