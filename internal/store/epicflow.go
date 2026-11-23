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

// releaseKind is the kind of a release node, whose node runs record the step of each release step and of the judge.
const releaseKind = "release"

// judgePart is the part of a release node run's work the judge ran under.
const judgePart = "judge"

// flowReviewKind, flowClosureKind and flowRerunKind are the kinds of the review, closure and rerun nodes, whose node
// runs label their steps with their path. A release node run labels its release integrator's steps with its path too.
const (
	flowReviewKind  = "review"
	flowClosureKind = "closure"
	flowRerunKind   = "rerun"
)

// EpicFlow is one epic's rows as the interpreter decides from them: the epic as Epic returns it, its review as
// EpicReview returns it, its node runs as NodeRuns returns them, its checks as NodeChecks returns them, its evidence
// runs as EvidenceRuns returns them, less the runs whose kept spec is no longer current with the item now stored under
// their Item, its steps, its release runs as ReleaseRuns returns them, how many of its triggers and of its
// amend_evidence actions are pending, and how many of its planner actions are pending.
// GapTurns, Unresolvable and Reruns hold its gap turns, its unresolvable gaps and its review reruns as GapTurns,
// UnresolvableGaps and ReviewReruns return them. Rounds holds its integrate rounds as readIntegrateRounds reads them.
// Steps holds the steps its agent and release node runs ran under as Step returns them, for the runs that have them,
// then every step labeled with the epic and the path of one of its review, closure or release node runs, in the order
// of the runs.
type EpicFlow struct {
	Epic     EpicRow
	Review   Review
	Runs     []NodeRun
	Checks   []NodeCheck
	Evidence []EvidenceRun
	Steps    []StepRow
	Releases []ReleaseRun
	Pending  int
	// AmendActions counts the epic's amend_evidence planner actions that are still pending, and Actions every planner
	// action of the epic that is still pending, those among it.
	AmendActions int
	Actions      int
	GapTurns     []GapTurn
	Unresolvable []GapDeclaration
	Reruns       []ReviewRerun
	Rounds       []IntegrateRound
}

// EpicFlow returns the stored epic with id with its review, node runs, checks, evidence runs, steps, release runs and
// its pending trigger and planner action counts. All of them are read in one read transaction,
// so they describe one state of the store. It returns an error matching ErrNoEpic when no epic has that id.
func (s *Store) EpicFlow(ctx context.Context, id string) (EpicFlow, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
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

// readEpicFlow reads the epic with id, its review, node runs, checks, evidence runs, steps, release runs and its
// pending trigger and planner action counts with queries.
func readEpicFlow(ctx context.Context, queries *sqlcgen.Queries, id string) (EpicFlow, error) {
	epic, err := readEpic(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	review, err := readReview(ctx, queries, id)
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
	steps, err := readRunSteps(ctx, queries, runs, len(epic.ReleaseSteps))
	if err != nil {
		return EpicFlow{}, err
	}
	releases, err := readReleaseRuns(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	labeled, err := readLabeledSteps(ctx, queries, id, runs)
	if err != nil {
		return EpicFlow{}, err
	}
	steps = append(steps, labeled...)
	pending, err := queries.CountPendingEpicTriggers(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending triggers of epic %s: %w", id, err)
	}
	amends, err := queries.CountPendingAmendActions(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending amend actions of epic %s: %w", id, err)
	}
	actions, err := queries.CountPendingEpicPlannerActions(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending planner actions of epic %s: %w", id, err)
	}
	gapTurns, unresolvable, reruns, err := readGapRecords(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	rounds, err := readIntegrateRounds(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	return EpicFlow{
		Epic: epic, Review: review, Runs: runs, Checks: checks, Evidence: currentRuns(stored, epic), Steps: steps,
		Releases: releases, Pending: int(pending), AmendActions: int(amends), Actions: int(actions),
		GapTurns: gapTurns, Unresolvable: unresolvable, Reruns: reruns, Rounds: rounds,
	}, nil
}

// readGapRecords returns the gap turns, unresolvable gaps and review reruns of epic as queries read them, in one
// read: the three helpers GapTurns, UnresolvableGaps and ReviewReruns read through.
func readGapRecords(
	ctx context.Context, queries *sqlcgen.Queries, id string,
) ([]GapTurn, []GapDeclaration, []ReviewRerun, error) {
	gapTurns, err := readGapTurns(ctx, queries, id)
	if err != nil {
		return nil, nil, nil, err
	}
	unresolvable, err := readUnresolvable(ctx, queries, id)
	if err != nil {
		return nil, nil, nil, err
	}
	reruns, err := readReviewReruns(ctx, queries, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return gapTurns, unresolvable, reruns, nil
}

// readEvidenceRuns returns the evidence runs of epic as queries read them, ordered by created, then id, each with its
// artifacts. It issues two queries, one for the runs and one for the artifacts of all of them.
func readEvidenceRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]EvidenceRun, error) {
	stored, err := queries.ListEvidenceRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list evidence runs of epic %s: %w", epic, err)
	}
	artifacts, err := queries.ListEpicEvidenceArtifacts(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list evidence artifacts of epic %s: %w", epic, err)
	}
	return evidenceRunsOf(stored, artifacts)
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

// readRunSteps returns, in the order of runs, each step a node run has: for each part workParts gives the run's kind,
// the step stored under NodeWorkID of the run's id and attempt, when one is stored. A part with no step, and a run of
// a kind with no parts, has none here.
func readRunSteps(ctx context.Context, queries *sqlcgen.Queries, runs []NodeRun, releaseSteps int) ([]StepRow, error) {
	var steps []StepRow
	for _, run := range runs {
		for _, part := range workParts(run.Kind, releaseSteps) {
			row, err := requireStep(ctx, queries, NodeWorkID(run.ID, run.Attempt, part))
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
	}
	return steps, nil
}

// workParts returns, in order, the parts of the work a node run of kind records steps under: `step` for an agent run,
// `release <n>` for each of the releaseSteps release steps numbered from 1 and then `judge` for a release run, and none
// for any other kind.
func workParts(kind string, releaseSteps int) []string {
	switch kind {
	case agentKind:
		return []string{"step"}
	case releaseKind:
		parts := make([]string, 0, releaseSteps+1)
		for n := range releaseSteps {
			parts = append(parts, fmt.Sprintf("release %d", n+1))
		}
		return append(parts, judgePart)
	default:
		return nil
	}
}

// readLabeledSteps returns, in the order of runs, the steps labeled with epic and the path of each review, closure or
// release, closure or rerun node run, each run's steps ordered by start time then id. They are read in one search of
// the steps by their labels, and none is read when runs has no such run. Node run paths are unique to an epic, so each
// step is read once. A release run's release steps and judge carry no path label, so only its release integrator's
// steps are read here.
func readLabeledSteps(ctx context.Context, queries *sqlcgen.Queries, epic string, runs []NodeRun) ([]StepRow, error) {
	var paths []string
	for _, run := range runs {
		if run.Kind == flowReviewKind || run.Kind == flowClosureKind || run.Kind == flowRerunKind ||
			run.Kind == releaseKind {
			paths = append(paths, run.Path)
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	rows, err := queries.ListEpicTaskSteps(ctx, sqlcgen.ListEpicTaskStepsParams{Epic: epic, Tasks: paths})
	if err != nil {
		return nil, fmt.Errorf("list steps of the review, closure, rerun and release runs of epic %s: %w", epic, err)
	}
	byPath := make(map[string][]sqlcgen.Step, len(paths))
	for _, row := range rows {
		byPath[row.Task] = append(byPath[row.Task], row)
	}
	var steps []StepRow
	for _, path := range paths {
		labeled, convErr := toStepRows(byPath[path])
		if convErr != nil {
			return nil, convErr
		}
		steps = append(steps, labeled...)
	}
	return steps, nil
}
