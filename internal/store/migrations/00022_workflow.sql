-- +goose Up
ALTER TABLE epics ADD COLUMN workflow TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN workflow_text TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN workflow_sha256 TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_workflow_nodes (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    id TEXT NOT NULL,
    parent TEXT NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

-- Every epic stored before this migration pins the built-in epic workflow, whose canonical text is the literal below.
UPDATE epics SET workflow = 'epic', workflow_text = 'name: epic
nodes:
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
', workflow_sha256 = '8e56cd7c52ea852c4deb42ce575ba11e4e6c0e0ca4f201ff03cf24ba0afd8e1e' WHERE workflow = '';
INSERT INTO epic_workflow_nodes (epic, position, id, parent, kind) SELECT id, 0, 'plan', '', 'plan' FROM epics WHERE workflow = 'epic';
INSERT INTO epic_workflow_nodes (epic, position, id, parent, kind) SELECT id, 1, 'build', 'plan', 'build' FROM epics WHERE workflow = 'epic';

-- +goose Down
DROP TABLE epic_workflow_nodes;
ALTER TABLE epics DROP COLUMN workflow_sha256;
ALTER TABLE epics DROP COLUMN workflow_text;
ALTER TABLE epics DROP COLUMN workflow;
