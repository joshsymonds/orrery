package enginehost

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// RunTask runs the evidence item of task T<task> of epic at revision, in phase red or green, and stores the run under the
// caller's id. It is Run for a task's item: the run's Item is T<task> and its Spec is the item the task names. A stored run
// with id is returned as it is, and nothing starts; the id is checked first, and a stored run is returned whatever the other
// arguments are. An id that is not 32 lowercase hex digits, a task the epic does not have, a task whose item is the zero
// value, and a bad phase are refused with an error matching ErrEvidenceRefused, and store no run.
func (e *Evidence) RunTask(
	ctx context.Context, id, epic string, task int64, revision, phase string,
) (store.EvidenceRun, error) {
	if err := checkID(id); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("%w: id %q is not 32 lowercase hex digits", ErrEvidenceRefused, id)
	}
	stored, err := e.store.EvidenceRun(ctx, id)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, store.ErrNoEvidenceRun) {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	row, err := e.store.Epic(ctx, epic)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("evidence run of epic %s: %w", epic, err)
	}
	spec, err := evidenceRunTaskItemOf(row, task)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	if phase != evidence.PhaseRed && phase != evidence.PhaseGreen {
		return store.EvidenceRun{}, fmt.Errorf("%w: phase %q is not red or green", ErrEvidenceRefused, phase)
	}
	commit, err := stepGitResolve(ctx, row.Repository, revision)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf(
			"%w: revision %q does not resolve: %w", ErrEvidenceRefused, revision, err,
		)
	}
	run := store.EvidenceRun{
		ID: id, Epic: epic, Item: fmt.Sprintf("T%d", task), Revision: commit, Phase: phase,
		Source: evidenceRunSource(spec), Spec: spec, Created: e.now(),
	}
	if err = e.store.AddEvidenceRun(ctx, run); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("add evidence run %s: %w", id, err)
	}
	stored, err = e.store.EvidenceRun(ctx, id)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	err = e.start(ctx, row.Repository, stored)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	return stored, nil
}

// RunItem runs the evidence item of Requirement item R<n> of epic at revision, in phase red or green, and stores the run under
// the caller's id. It is Run under the caller's id: the run's Item is R<n> and its Spec is the item the Requirement names. A
// stored run with id is returned as it is, and nothing starts; the id is checked first, and a stored run is returned whatever
// the other arguments are. An id that is not 32 lowercase hex digits, an item that is not an R<n> or names a Requirement with no
// item, and a bad phase are refused with an error matching ErrEvidenceRefused, and store no run.
func (e *Evidence) RunItem(
	ctx context.Context, id, epic, item, revision, phase string,
) (store.EvidenceRun, error) {
	if err := checkID(id); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("%w: id %q is not 32 lowercase hex digits", ErrEvidenceRefused, id)
	}
	stored, err := e.store.EvidenceRun(ctx, id)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, store.ErrNoEvidenceRun) {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	row, err := e.store.Epic(ctx, epic)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("evidence run of epic %s: %w", epic, err)
	}
	spec, err := evidenceRunItemOf(row, item)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	if phase != evidence.PhaseRed && phase != evidence.PhaseGreen {
		return store.EvidenceRun{}, fmt.Errorf("%w: phase %q is not red or green", ErrEvidenceRefused, phase)
	}
	commit, err := stepGitResolve(ctx, row.Repository, revision)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf(
			"%w: revision %q does not resolve: %w", ErrEvidenceRefused, revision, err,
		)
	}
	run := store.EvidenceRun{
		ID: id, Epic: epic, Item: item, Revision: commit, Phase: phase,
		Source: evidenceRunSource(spec), Spec: spec, Created: e.now(),
	}
	if err = e.store.AddEvidenceRun(ctx, run); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("add evidence run %s: %w", id, err)
	}
	stored, err = e.store.EvidenceRun(ctx, id)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	err = e.start(ctx, row.Repository, stored)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	return stored, nil
}

// evidenceRunTaskItemOf returns the evidence item task T<task> of row was planned with. A task the epic does not have, and
// a task whose item is the zero value, are refused with an error matching ErrEvidenceRefused that names the task.
func evidenceRunTaskItemOf(row store.EpicRow, task int64) (evidence.Item, error) {
	for _, planned := range row.Tasks {
		if planned.Seq != task {
			continue
		}
		if reflect.ValueOf(planned.Evidence).IsZero() {
			return evidence.Item{}, fmt.Errorf("%w: task T%d has no evidence item", ErrEvidenceRefused, task)
		}
		return planned.Evidence, nil
	}
	return evidence.Item{}, fmt.Errorf("%w: no task T%d", ErrEvidenceRefused, task)
}
