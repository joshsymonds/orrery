-- +goose Up
-- epics.revision counts the writes that change what an epic's read answers, so a reader that lists the epics can tell,
-- by comparing one number, that an epic changed in any part since it last read it. Every write to an epic's own row,
-- other than one that only moves its revision, and every insert, update and delete of a row of a table that holds an
-- epic's parts, keyed by that epic, moves it by one, in the write's own transaction: its contract, questions, parks,
-- tasks, turns, workflow nodes, ledger, reviews, releases, sessions, seen mark, planner actions and evidence runs, and
-- the artifacts of those runs through their run's epic. The steps are left out: a list of the epics carries each epic's
-- running steps and a count of its ended ones, and a step row is written often while it runs. CreateEpic sets a new
-- epic back to 0 once it has written its parts, so only later writes count.
ALTER TABLE epics ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

-- +goose StatementBegin
CREATE TRIGGER epics_revision_update AFTER UPDATE ON epics WHEN NEW.revision = OLD.revision
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.id;
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
CREATE TRIGGER epic_additions_revision_delete AFTER DELETE ON epic_additions
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
CREATE TRIGGER epic_alternatives_revision_delete AFTER DELETE ON epic_alternatives
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
CREATE TRIGGER epic_amendments_revision_delete AFTER DELETE ON epic_amendments
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
CREATE TRIGGER epic_closures_revision_delete AFTER DELETE ON epic_closures
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
CREATE TRIGGER epic_decisions_revision_delete AFTER DELETE ON epic_decisions
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
CREATE TRIGGER epic_exclusions_revision_delete AFTER DELETE ON epic_exclusions
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
CREATE TRIGGER epic_failures_revision_delete AFTER DELETE ON epic_failures
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
CREATE TRIGGER epic_gate_lists_revision_delete AFTER DELETE ON epic_gate_lists
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
CREATE TRIGGER epic_gates_revision_delete AFTER DELETE ON epic_gates
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
CREATE TRIGGER epic_ledger_revision_delete AFTER DELETE ON epic_ledger
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
CREATE TRIGGER epic_met_at_base_revision_delete AFTER DELETE ON epic_met_at_base
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
CREATE TRIGGER epic_node_checks_revision_delete AFTER DELETE ON epic_node_checks
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
CREATE TRIGGER epic_node_runs_revision_delete AFTER DELETE ON epic_node_runs
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
CREATE TRIGGER epic_parks_revision_delete AFTER DELETE ON epic_parks
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
CREATE TRIGGER epic_premises_revision_delete AFTER DELETE ON epic_premises
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
CREATE TRIGGER epic_questions_revision_delete AFTER DELETE ON epic_questions
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
CREATE TRIGGER epic_release_runs_revision_delete AFTER DELETE ON epic_release_runs
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
CREATE TRIGGER epic_release_slots_revision_delete AFTER DELETE ON epic_release_slots
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
CREATE TRIGGER epic_release_steps_revision_delete AFTER DELETE ON epic_release_steps
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
CREATE TRIGGER epic_requirements_revision_delete AFTER DELETE ON epic_requirements
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
CREATE TRIGGER epic_review_holds_revision_delete AFTER DELETE ON epic_review_holds
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
CREATE TRIGGER epic_review_rounds_revision_delete AFTER DELETE ON epic_review_rounds
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
CREATE TRIGGER epic_review_verdicts_revision_delete AFTER DELETE ON epic_review_verdicts
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
CREATE TRIGGER epic_reviewers_revision_delete AFTER DELETE ON epic_reviewers
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
CREATE TRIGGER epic_reviews_revision_delete AFTER DELETE ON epic_reviews
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
CREATE TRIGGER epic_seen_revision_delete AFTER DELETE ON epic_seen
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
CREATE TRIGGER epic_sessions_revision_delete AFTER DELETE ON epic_sessions
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
CREATE TRIGGER epic_task_contributes_revision_delete AFTER DELETE ON epic_task_contributes
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
CREATE TRIGGER epic_task_files_revision_delete AFTER DELETE ON epic_task_files
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
CREATE TRIGGER epic_task_findings_revision_delete AFTER DELETE ON epic_task_findings
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
CREATE TRIGGER epic_task_requirements_revision_delete AFTER DELETE ON epic_task_requirements
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
CREATE TRIGGER epic_task_reviews_revision_delete AFTER DELETE ON epic_task_reviews
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
CREATE TRIGGER epic_task_test_changes_revision_delete AFTER DELETE ON epic_task_test_changes
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
CREATE TRIGGER epic_task_versions_revision_delete AFTER DELETE ON epic_task_versions
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
CREATE TRIGGER epic_tasks_revision_delete AFTER DELETE ON epic_tasks
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
CREATE TRIGGER epic_tells_revision_delete AFTER DELETE ON epic_tells
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
CREATE TRIGGER epic_triggers_revision_delete AFTER DELETE ON epic_triggers
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
CREATE TRIGGER epic_turn_decisions_revision_delete AFTER DELETE ON epic_turn_decisions
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
CREATE TRIGGER epic_turns_revision_delete AFTER DELETE ON epic_turns
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
CREATE TRIGGER epic_workflow_nodes_revision_delete AFTER DELETE ON epic_workflow_nodes
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
CREATE TRIGGER evidence_runs_revision_delete AFTER DELETE ON evidence_runs
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
CREATE TRIGGER planner_actions_revision_delete AFTER DELETE ON planner_actions
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
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
CREATE TRIGGER evidence_artifacts_revision_delete AFTER DELETE ON evidence_artifacts
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = (SELECT epic FROM evidence_runs WHERE id = OLD.run);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER epic_additions_revision_delete;
DROP TRIGGER epic_additions_revision_update;
DROP TRIGGER epic_additions_revision_insert;
DROP TRIGGER epic_alternatives_revision_delete;
DROP TRIGGER epic_alternatives_revision_update;
DROP TRIGGER epic_alternatives_revision_insert;
DROP TRIGGER epic_amendments_revision_delete;
DROP TRIGGER epic_amendments_revision_update;
DROP TRIGGER epic_amendments_revision_insert;
DROP TRIGGER epic_closures_revision_delete;
DROP TRIGGER epic_closures_revision_update;
DROP TRIGGER epic_closures_revision_insert;
DROP TRIGGER epic_decisions_revision_delete;
DROP TRIGGER epic_decisions_revision_update;
DROP TRIGGER epic_decisions_revision_insert;
DROP TRIGGER epic_exclusions_revision_delete;
DROP TRIGGER epic_exclusions_revision_update;
DROP TRIGGER epic_exclusions_revision_insert;
DROP TRIGGER epic_failures_revision_delete;
DROP TRIGGER epic_failures_revision_update;
DROP TRIGGER epic_failures_revision_insert;
DROP TRIGGER epic_gate_lists_revision_delete;
DROP TRIGGER epic_gate_lists_revision_update;
DROP TRIGGER epic_gate_lists_revision_insert;
DROP TRIGGER epic_gates_revision_delete;
DROP TRIGGER epic_gates_revision_update;
DROP TRIGGER epic_gates_revision_insert;
DROP TRIGGER epic_ledger_revision_delete;
DROP TRIGGER epic_ledger_revision_update;
DROP TRIGGER epic_ledger_revision_insert;
DROP TRIGGER epic_met_at_base_revision_delete;
DROP TRIGGER epic_met_at_base_revision_update;
DROP TRIGGER epic_met_at_base_revision_insert;
DROP TRIGGER epic_node_checks_revision_delete;
DROP TRIGGER epic_node_checks_revision_update;
DROP TRIGGER epic_node_checks_revision_insert;
DROP TRIGGER epic_node_runs_revision_delete;
DROP TRIGGER epic_node_runs_revision_update;
DROP TRIGGER epic_node_runs_revision_insert;
DROP TRIGGER epic_parks_revision_delete;
DROP TRIGGER epic_parks_revision_update;
DROP TRIGGER epic_parks_revision_insert;
DROP TRIGGER epic_premises_revision_delete;
DROP TRIGGER epic_premises_revision_update;
DROP TRIGGER epic_premises_revision_insert;
DROP TRIGGER epic_questions_revision_delete;
DROP TRIGGER epic_questions_revision_update;
DROP TRIGGER epic_questions_revision_insert;
DROP TRIGGER epic_release_runs_revision_delete;
DROP TRIGGER epic_release_runs_revision_update;
DROP TRIGGER epic_release_runs_revision_insert;
DROP TRIGGER epic_release_slots_revision_delete;
DROP TRIGGER epic_release_slots_revision_update;
DROP TRIGGER epic_release_slots_revision_insert;
DROP TRIGGER epic_release_steps_revision_delete;
DROP TRIGGER epic_release_steps_revision_update;
DROP TRIGGER epic_release_steps_revision_insert;
DROP TRIGGER epic_requirements_revision_delete;
DROP TRIGGER epic_requirements_revision_update;
DROP TRIGGER epic_requirements_revision_insert;
DROP TRIGGER epic_review_holds_revision_delete;
DROP TRIGGER epic_review_holds_revision_update;
DROP TRIGGER epic_review_holds_revision_insert;
DROP TRIGGER epic_review_rounds_revision_delete;
DROP TRIGGER epic_review_rounds_revision_update;
DROP TRIGGER epic_review_rounds_revision_insert;
DROP TRIGGER epic_review_verdicts_revision_delete;
DROP TRIGGER epic_review_verdicts_revision_update;
DROP TRIGGER epic_review_verdicts_revision_insert;
DROP TRIGGER epic_reviewers_revision_delete;
DROP TRIGGER epic_reviewers_revision_update;
DROP TRIGGER epic_reviewers_revision_insert;
DROP TRIGGER epic_reviews_revision_delete;
DROP TRIGGER epic_reviews_revision_update;
DROP TRIGGER epic_reviews_revision_insert;
DROP TRIGGER epic_seen_revision_delete;
DROP TRIGGER epic_seen_revision_update;
DROP TRIGGER epic_seen_revision_insert;
DROP TRIGGER epic_sessions_revision_delete;
DROP TRIGGER epic_sessions_revision_update;
DROP TRIGGER epic_sessions_revision_insert;
DROP TRIGGER epic_task_contributes_revision_delete;
DROP TRIGGER epic_task_contributes_revision_update;
DROP TRIGGER epic_task_contributes_revision_insert;
DROP TRIGGER epic_task_files_revision_delete;
DROP TRIGGER epic_task_files_revision_update;
DROP TRIGGER epic_task_files_revision_insert;
DROP TRIGGER epic_task_findings_revision_delete;
DROP TRIGGER epic_task_findings_revision_update;
DROP TRIGGER epic_task_findings_revision_insert;
DROP TRIGGER epic_task_requirements_revision_delete;
DROP TRIGGER epic_task_requirements_revision_update;
DROP TRIGGER epic_task_requirements_revision_insert;
DROP TRIGGER epic_task_reviews_revision_delete;
DROP TRIGGER epic_task_reviews_revision_update;
DROP TRIGGER epic_task_reviews_revision_insert;
DROP TRIGGER epic_task_test_changes_revision_delete;
DROP TRIGGER epic_task_test_changes_revision_update;
DROP TRIGGER epic_task_test_changes_revision_insert;
DROP TRIGGER epic_task_versions_revision_delete;
DROP TRIGGER epic_task_versions_revision_update;
DROP TRIGGER epic_task_versions_revision_insert;
DROP TRIGGER epic_tasks_revision_delete;
DROP TRIGGER epic_tasks_revision_update;
DROP TRIGGER epic_tasks_revision_insert;
DROP TRIGGER epic_tells_revision_delete;
DROP TRIGGER epic_tells_revision_update;
DROP TRIGGER epic_tells_revision_insert;
DROP TRIGGER epic_triggers_revision_delete;
DROP TRIGGER epic_triggers_revision_update;
DROP TRIGGER epic_triggers_revision_insert;
DROP TRIGGER epic_turn_decisions_revision_delete;
DROP TRIGGER epic_turn_decisions_revision_update;
DROP TRIGGER epic_turn_decisions_revision_insert;
DROP TRIGGER epic_turns_revision_delete;
DROP TRIGGER epic_turns_revision_update;
DROP TRIGGER epic_turns_revision_insert;
DROP TRIGGER epic_workflow_nodes_revision_delete;
DROP TRIGGER epic_workflow_nodes_revision_update;
DROP TRIGGER epic_workflow_nodes_revision_insert;
DROP TRIGGER evidence_runs_revision_delete;
DROP TRIGGER evidence_runs_revision_update;
DROP TRIGGER evidence_runs_revision_insert;
DROP TRIGGER planner_actions_revision_delete;
DROP TRIGGER planner_actions_revision_update;
DROP TRIGGER planner_actions_revision_insert;
DROP TRIGGER evidence_artifacts_revision_delete;
DROP TRIGGER evidence_artifacts_revision_update;
DROP TRIGGER evidence_artifacts_revision_insert;
DROP TRIGGER epics_revision_update;
ALTER TABLE epics DROP COLUMN revision;
