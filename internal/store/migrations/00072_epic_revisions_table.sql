-- +goose Up
-- An epic's revision moves to a table of its own, epic_revisions, one narrow row per epic, and no trigger moves it: the
-- store's write connection records, in its pre-update hook, each epic a row written in a store write names, and the
-- write moves each of those epics' revision by one before it commits (store.epicMoves, store.runWrite). A revision
-- trigger rewrote the epic's whole row, contract and workflow text among it, on every write to any of its parts, and
-- every connection parses every trigger as it opens, as serve's reader does on each read, so this drops every one
-- 00069_epic_revisions.sql, 00070_task_ladder.sql and 00071_park_question_runs.sql made. An epic with no row is at
-- revision 0.
CREATE TABLE epic_revisions (
    epic TEXT PRIMARY KEY,
    revision INTEGER NOT NULL
) WITHOUT ROWID;

INSERT INTO epic_revisions (epic, revision) SELECT id, revision FROM epics WHERE revision > 0;

-- +goose StatementBegin
DROP TRIGGER epic_additions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_additions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_additions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_alternatives_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_alternatives_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_alternatives_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_amendments_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_amendments_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_amendments_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_closures_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_closures_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_closures_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_decisions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_decisions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_decisions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_exclusions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_exclusions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_exclusions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_failures_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_failures_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_failures_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gate_lists_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gate_lists_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gate_lists_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gates_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gates_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_gates_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_ledger_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_ledger_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_ledger_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_met_at_base_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_met_at_base_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_met_at_base_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_checks_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_checks_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_checks_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_runs_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_runs_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_node_runs_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_parks_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_parks_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_parks_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_premises_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_premises_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_premises_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_questions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_questions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_questions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_runs_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_runs_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_runs_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_slots_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_slots_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_slots_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_steps_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_steps_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_release_steps_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_requirements_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_requirements_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_requirements_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_holds_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_holds_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_holds_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_rounds_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_rounds_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_rounds_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_verdicts_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_verdicts_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_review_verdicts_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviewers_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviewers_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviewers_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviews_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviews_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_reviews_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_seen_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_seen_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_seen_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_sessions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_sessions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_sessions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_contributes_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_contributes_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_contributes_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_files_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_files_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_files_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_findings_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_findings_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_findings_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_requirements_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_requirements_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_requirements_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_reviews_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_reviews_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_reviews_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rung_outcomes_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rung_outcomes_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rung_outcomes_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rungs_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rungs_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_rungs_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_test_changes_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_test_changes_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_test_changes_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_versions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_versions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_task_versions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tasks_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tasks_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tasks_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tells_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tells_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_tells_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_triggers_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_triggers_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_triggers_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turn_decisions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turn_decisions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turn_decisions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turns_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turns_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_turns_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_workflow_nodes_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_workflow_nodes_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_workflow_nodes_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epics_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_artifacts_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_artifacts_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_artifacts_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_runs_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_runs_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER evidence_runs_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER planner_actions_revision_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER planner_actions_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER planner_actions_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_park_question_runs_revision_insert;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_park_question_runs_revision_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER epic_park_question_runs_revision_delete;
-- +goose StatementEnd

ALTER TABLE epics DROP COLUMN revision;

-- +goose Down
ALTER TABLE epics ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

UPDATE epics SET revision = (SELECT revision FROM epic_revisions WHERE epic = epics.id)
WHERE id IN (SELECT epic FROM epic_revisions);

DROP TABLE epic_revisions;

-- +goose StatementBegin
CREATE TRIGGER epic_additions_revision_delete AFTER DELETE ON epic_additions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_additions_revision_insert AFTER INSERT ON epic_additions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_additions_revision_update AFTER UPDATE ON epic_additions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_alternatives_revision_delete AFTER DELETE ON epic_alternatives
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_alternatives_revision_insert AFTER INSERT ON epic_alternatives
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_alternatives_revision_update AFTER UPDATE ON epic_alternatives
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_amendments_revision_delete AFTER DELETE ON epic_amendments
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_amendments_revision_insert AFTER INSERT ON epic_amendments
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_amendments_revision_update AFTER UPDATE ON epic_amendments
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_closures_revision_delete AFTER DELETE ON epic_closures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_closures_revision_insert AFTER INSERT ON epic_closures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_closures_revision_update AFTER UPDATE ON epic_closures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_decisions_revision_delete AFTER DELETE ON epic_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_decisions_revision_insert AFTER INSERT ON epic_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_decisions_revision_update AFTER UPDATE ON epic_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_exclusions_revision_delete AFTER DELETE ON epic_exclusions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_exclusions_revision_insert AFTER INSERT ON epic_exclusions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_exclusions_revision_update AFTER UPDATE ON epic_exclusions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_failures_revision_delete AFTER DELETE ON epic_failures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_failures_revision_insert AFTER INSERT ON epic_failures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_failures_revision_update AFTER UPDATE ON epic_failures
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gate_lists_revision_delete AFTER DELETE ON epic_gate_lists
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gate_lists_revision_insert AFTER INSERT ON epic_gate_lists
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gate_lists_revision_update AFTER UPDATE ON epic_gate_lists
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gates_revision_delete AFTER DELETE ON epic_gates
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gates_revision_insert AFTER INSERT ON epic_gates
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_gates_revision_update AFTER UPDATE ON epic_gates
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_ledger_revision_delete AFTER DELETE ON epic_ledger
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_ledger_revision_insert AFTER INSERT ON epic_ledger
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_ledger_revision_update AFTER UPDATE ON epic_ledger
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_met_at_base_revision_delete AFTER DELETE ON epic_met_at_base
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_met_at_base_revision_insert AFTER INSERT ON epic_met_at_base
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_met_at_base_revision_update AFTER UPDATE ON epic_met_at_base
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_checks_revision_delete AFTER DELETE ON epic_node_checks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_checks_revision_insert AFTER INSERT ON epic_node_checks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_checks_revision_update AFTER UPDATE ON epic_node_checks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_runs_revision_delete AFTER DELETE ON epic_node_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_runs_revision_insert AFTER INSERT ON epic_node_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_node_runs_revision_update AFTER UPDATE ON epic_node_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_parks_revision_delete AFTER DELETE ON epic_parks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_parks_revision_insert AFTER INSERT ON epic_parks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_parks_revision_update AFTER UPDATE ON epic_parks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_premises_revision_delete AFTER DELETE ON epic_premises
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_premises_revision_insert AFTER INSERT ON epic_premises
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_premises_revision_update AFTER UPDATE ON epic_premises
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_questions_revision_delete AFTER DELETE ON epic_questions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_questions_revision_insert AFTER INSERT ON epic_questions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_questions_revision_update AFTER UPDATE ON epic_questions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_runs_revision_delete AFTER DELETE ON epic_release_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_runs_revision_insert AFTER INSERT ON epic_release_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_runs_revision_update AFTER UPDATE ON epic_release_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_slots_revision_delete AFTER DELETE ON epic_release_slots
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_slots_revision_insert AFTER INSERT ON epic_release_slots
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_slots_revision_update AFTER UPDATE ON epic_release_slots
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_steps_revision_delete AFTER DELETE ON epic_release_steps
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_steps_revision_insert AFTER INSERT ON epic_release_steps
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_release_steps_revision_update AFTER UPDATE ON epic_release_steps
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_requirements_revision_delete AFTER DELETE ON epic_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_requirements_revision_insert AFTER INSERT ON epic_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_requirements_revision_update AFTER UPDATE ON epic_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_holds_revision_delete AFTER DELETE ON epic_review_holds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_holds_revision_insert AFTER INSERT ON epic_review_holds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_holds_revision_update AFTER UPDATE ON epic_review_holds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_rounds_revision_delete AFTER DELETE ON epic_review_rounds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_rounds_revision_insert AFTER INSERT ON epic_review_rounds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_rounds_revision_update AFTER UPDATE ON epic_review_rounds
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_verdicts_revision_delete AFTER DELETE ON epic_review_verdicts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_verdicts_revision_insert AFTER INSERT ON epic_review_verdicts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_review_verdicts_revision_update AFTER UPDATE ON epic_review_verdicts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviewers_revision_delete AFTER DELETE ON epic_reviewers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviewers_revision_insert AFTER INSERT ON epic_reviewers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviewers_revision_update AFTER UPDATE ON epic_reviewers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviews_revision_delete AFTER DELETE ON epic_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviews_revision_insert AFTER INSERT ON epic_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_reviews_revision_update AFTER UPDATE ON epic_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_seen_revision_delete AFTER DELETE ON epic_seen
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_seen_revision_insert AFTER INSERT ON epic_seen
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_seen_revision_update AFTER UPDATE ON epic_seen
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_sessions_revision_delete AFTER DELETE ON epic_sessions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_sessions_revision_insert AFTER INSERT ON epic_sessions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_sessions_revision_update AFTER UPDATE ON epic_sessions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_contributes_revision_delete AFTER DELETE ON epic_task_contributes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_contributes_revision_insert AFTER INSERT ON epic_task_contributes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_contributes_revision_update AFTER UPDATE ON epic_task_contributes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_files_revision_delete AFTER DELETE ON epic_task_files
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_files_revision_insert AFTER INSERT ON epic_task_files
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_files_revision_update AFTER UPDATE ON epic_task_files
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_findings_revision_delete AFTER DELETE ON epic_task_findings
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_findings_revision_insert AFTER INSERT ON epic_task_findings
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_findings_revision_update AFTER UPDATE ON epic_task_findings
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_requirements_revision_delete AFTER DELETE ON epic_task_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_requirements_revision_insert AFTER INSERT ON epic_task_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_requirements_revision_update AFTER UPDATE ON epic_task_requirements
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_reviews_revision_delete AFTER DELETE ON epic_task_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_reviews_revision_insert AFTER INSERT ON epic_task_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_reviews_revision_update AFTER UPDATE ON epic_task_reviews
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rung_outcomes_revision_delete AFTER DELETE ON epic_task_rung_outcomes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rung_outcomes_revision_insert AFTER INSERT ON epic_task_rung_outcomes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rung_outcomes_revision_update AFTER UPDATE ON epic_task_rung_outcomes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rungs_revision_delete AFTER DELETE ON epic_task_rungs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rungs_revision_insert AFTER INSERT ON epic_task_rungs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_rungs_revision_update AFTER UPDATE ON epic_task_rungs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_test_changes_revision_delete AFTER DELETE ON epic_task_test_changes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_test_changes_revision_insert AFTER INSERT ON epic_task_test_changes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_test_changes_revision_update AFTER UPDATE ON epic_task_test_changes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_versions_revision_delete AFTER DELETE ON epic_task_versions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_versions_revision_insert AFTER INSERT ON epic_task_versions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_task_versions_revision_update AFTER UPDATE ON epic_task_versions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tasks_revision_delete AFTER DELETE ON epic_tasks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tasks_revision_insert AFTER INSERT ON epic_tasks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tasks_revision_update AFTER UPDATE ON epic_tasks
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tells_revision_delete AFTER DELETE ON epic_tells
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tells_revision_insert AFTER INSERT ON epic_tells
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_tells_revision_update AFTER UPDATE ON epic_tells
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_triggers_revision_delete AFTER DELETE ON epic_triggers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_triggers_revision_insert AFTER INSERT ON epic_triggers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_triggers_revision_update AFTER UPDATE ON epic_triggers
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turn_decisions_revision_delete AFTER DELETE ON epic_turn_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turn_decisions_revision_insert AFTER INSERT ON epic_turn_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turn_decisions_revision_update AFTER UPDATE ON epic_turn_decisions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turns_revision_delete AFTER DELETE ON epic_turns
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turns_revision_insert AFTER INSERT ON epic_turns
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_turns_revision_update AFTER UPDATE ON epic_turns
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_workflow_nodes_revision_delete AFTER DELETE ON epic_workflow_nodes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_workflow_nodes_revision_insert AFTER INSERT ON epic_workflow_nodes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_workflow_nodes_revision_update AFTER UPDATE ON epic_workflow_nodes
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epics_revision_update AFTER UPDATE ON epics WHEN NEW.revision = OLD.revision
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_artifacts_revision_delete AFTER DELETE ON evidence_artifacts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = (SELECT epic FROM evidence_runs WHERE id = OLD.run);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_artifacts_revision_insert AFTER INSERT ON evidence_artifacts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = (SELECT epic FROM evidence_runs WHERE id = NEW.run);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_artifacts_revision_update AFTER UPDATE ON evidence_artifacts
BEGIN
    UPDATE epics SET revision = revision + 1
    WHERE id IN (SELECT epic FROM evidence_runs WHERE id IN (OLD.run, NEW.run));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_runs_revision_delete AFTER DELETE ON evidence_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_runs_revision_insert AFTER INSERT ON evidence_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER evidence_runs_revision_update AFTER UPDATE ON evidence_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER planner_actions_revision_delete AFTER DELETE ON planner_actions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER planner_actions_revision_insert AFTER INSERT ON planner_actions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER planner_actions_revision_update AFTER UPDATE ON planner_actions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_insert AFTER INSERT ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_update AFTER UPDATE ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_delete AFTER DELETE ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd
