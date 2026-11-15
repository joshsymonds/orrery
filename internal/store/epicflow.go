package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// agentKind is the kind of an agent node, whose node runs record the step they ran under.
const agentKind = "agent"

// EpicFlow is one epic's rows as the interpreter decides from them: the epic as Epic returns it, its node runs as
// NodeRuns returns them, its checks as NodeChecks returns them, its evidence runs as EvidenceRuns returns them, less
// the runs whose kept spec is no longer current with the item now stored under their Item, the step each agent node
// run ran under as Step returns it, for the runs that have one, and how many of its triggers are pending.
type EpicFlow struct {
	Epic     EpicRow
	Runs     []NodeRun
	Checks   []NodeCheck
	Evidence []EvidenceRun
	Steps    []StepRow
	Pending  int
}

// EpicFlow returns the stored epic with id with its node runs, checks, evidence runs, agent steps and its pending
// trigger count. All of them are read in one read transaction, so they describe one state of the store. It returns an
// error matching ErrNoEpic when no epic has that id.
func (s *Store) EpicFlow(ctx context.Context, id string) (EpicFlow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EpicFlow{}, fmt.Errorf("begin epic read: %w", err)
	}
	flow, err := readEpicFlow(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return EpicFlow{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return EpicFlow{}, fmt.Errorf("end epic read: %w", err)
	}
	return flow, nil
}

// readEpicFlow reads the epic with id, its node runs, checks, evidence runs, agent steps and its pending trigger count
// with queries.
func readEpicFlow(ctx context.Context, queries *sqlcgen.Queries, id string) (EpicFlow, error) {
	epic, err := readEpic(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	runs, err := readNodeRuns(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	checks, err := readNodeChecks(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	stored, err := readEvidenceRuns(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	steps, err := readAgentSteps(ctx, queries, runs)
	if err != nil {
		return EpicFlow{}, err
	}
	pending, err := queries.CountPendingEpicTriggers(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending triggers of epic %s: %w", id, err)
	}
	return EpicFlow{
		Epic: epic, Runs: runs, Checks: checks, Evidence: currentRuns(stored, epic), Steps: steps,
		Pending: int(pending),
	}, nil
}

// readEvidenceRuns returns the evidence runs of epic as queries read them, ordered by created, then id, each with its
// artifacts.
func readEvidenceRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]EvidenceRun, error) {
	stored, err := queries.ListEvidenceRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list evidence runs of epic %s: %w", epic, err)
	}
	return evidenceRunsOf(ctx, queries, stored)
}

// currentRuns returns the runs of epic whose kept spec is current with the item now stored under their Item, in the
// order given. A Requirement's run is named by the Requirement's id and holds its item; a task's run is named T and the
// task's seq and holds the task's evidence. A run whose Item names nothing stored is not current.
func currentRuns(runs []EvidenceRun, epic EpicRow) []EvidenceRun {
	items := make(map[string]evidence.Item, len(epic.Requirements)+len(epic.Tasks))
	for _, requirement := range epic.Requirements {
		items[requirement.ID] = requirement.Item
	}
	for _, task := range epic.Tasks {
		items[fmt.Sprintf("T%d", task.Seq)] = task.Evidence
	}
	var current []EvidenceRun
	for _, run := range runs {
		if item, ok := items[run.Item]; ok && evidence.Current(run.Spec, item) {
			current = append(current, run)
		}
	}
	return current
}

// readAgentSteps returns, in the order of runs, the step of each agent node run that has one: the step stored under
// NodeWorkID of the run's id and attempt. A run of another kind, or an agent run with no step, has none here.
func readAgentSteps(ctx context.Context, queries *sqlcgen.Queries, runs []NodeRun) ([]StepRow, error) {
	var steps []StepRow
	for _, run := range runs {
		if run.Kind != agentKind {
			continue
		}
		row, err := requireStep(ctx, queries, NodeWorkID(run.ID, run.Attempt, "step"))
		if errors.Is(err, ErrNoStep) {
			continue
		}
		if err != nil {
			return nil, err
		}
		step, err := toStepRow(row)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}
