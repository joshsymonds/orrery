package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// agentKind is the kind of an agent node, whose node runs record the step they ran under.
const agentKind = "agent"

// releaseKind is the kind of a release node, whose node runs record the step of each release step and of the judge.
const releaseKind = "release"

// judgePart is the part of a release node run's work the judge ran under.
const judgePart = "judge"

// EpicFlow is one epic's rows as the interpreter decides from them: the epic as Epic returns it, its node runs as
// NodeRuns returns them, its checks as NodeChecks returns them, its evidence runs as EvidenceRuns returns them, the
// steps its agent and release node runs ran under as Step returns them, for the runs that have them, its release runs
// as ReleaseRuns returns them, the holder of its repository's release slot, and how many of its triggers are pending.
type EpicFlow struct {
	Epic     EpicRow
	Runs     []NodeRun
	Checks   []NodeCheck
	Evidence []EvidenceRun
	Steps    []StepRow
	Releases []ReleaseRun
	Slot     string
	Pending  int
}

// EpicFlow returns the stored epic with id with its node runs, checks, evidence runs, node run steps, release runs,
// release slot holder and its pending trigger count. All of them are read in one read transaction, so they describe one
// state of the store. It returns an error matching ErrNoEpic when no epic has that id.
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

// readEpicFlow reads the epic with id, its node runs, checks, evidence runs, node run steps, release runs, release slot
// holder and its pending trigger count with queries.
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
	evidence, err := readEvidenceRuns(ctx, queries, id)
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
	slot, err := releaseSlotHolder(ctx, queries, epic.Repository)
	if err != nil {
		return EpicFlow{}, err
	}
	pending, err := queries.CountPendingEpicTriggers(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending triggers of epic %s: %w", id, err)
	}
	return EpicFlow{
		Epic: epic, Runs: runs, Checks: checks, Evidence: evidence, Steps: steps, Releases: releases, Slot: slot,
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
