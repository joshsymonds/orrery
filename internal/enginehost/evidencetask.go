package enginehost

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// RunTask runs the evidence item of task T<task> of epic at revision, in phase red or green, and stores the run under
// the caller's id. It is Run for a task's item: the run's Item is T<task>. Its Spec is the item the task names, except
// in phase red while the task has a checking version (store.EpicTask.Checking): the red run then judges that version's
// item, and its Spec is that item. A stored run with id is returned as it is, and nothing starts; the id is checked
// first, and a stored run is returned whatever the other arguments are. An id that is not 32 lowercase hex digits, a
// task the epic does not have, a task whose item is the zero value, and a bad phase are refused with an error matching
// ErrEvidenceRefused, and store no run.
func (e *Evidence) RunTask(
	ctx context.Context, id, epic string, task int64, revision, phase string,
) (store.EvidenceRun, error) {
	return e.runUnder(
		ctx,
		id,
		epic,
		fmt.Sprintf("T%d", task),
		revision,
		phase,
		func(row store.EpicRow) (evidence.Item, error) {
			return evidenceRunTaskItemOf(row, task, phase)
		},
	)
}

// RunItem runs the evidence item of Requirement item R<n> of epic at revision, in phase red or green, and stores the run under
// the caller's id. It is Run under the caller's id: the run's Item is R<n> and its Spec is the item the Requirement names. A
// stored run with id is returned as it is, and nothing starts; the id is checked first, and a stored run is returned whatever
// the other arguments are. An id that is not 32 lowercase hex digits, an item that is not an R<n> or names a Requirement with no
// item, and a bad phase are refused with an error matching ErrEvidenceRefused, and store no run.
func (e *Evidence) RunItem(
	ctx context.Context, id, epic, item, revision, phase string,
) (store.EvidenceRun, error) {
	return e.runUnder(ctx, id, epic, item, revision, phase, func(row store.EpicRow) (evidence.Item, error) {
		return evidenceRunItemOf(row, item)
	})
}

// RunAmendment runs spec, the item an evidence amendment asks Requirement requirement of epic to hold, at revision, in phase
// red or green, and stores the run under the caller's id. It is RunItem with the amendment's item as the run's Spec in place
// of the item the Requirement holds: the run's Item is the Requirement's id, so the run is current once the Requirement holds
// spec, and stale until then (see evidence.Current). A stored run with id is returned as it is, and nothing starts, whatever
// the other arguments are. An id that is not 32 lowercase hex digits, a Requirement the epic does not hold, a spec that is the
// zero value, and a bad phase are refused with an error matching ErrEvidenceRefused, and store no run.
func (e *Evidence) RunAmendment(
	ctx context.Context, id, epic, requirement string, spec evidence.Item, revision, phase string,
) (store.EvidenceRun, error) {
	return e.runUnder(ctx, id, epic, requirement, revision, phase, func(row store.EpicRow) (evidence.Item, error) {
		if !slices.ContainsFunc(row.Requirements, func(r store.EpicRequirement) bool { return r.ID == requirement }) {
			return evidence.Item{}, fmt.Errorf("%w: no requirement %s", ErrEvidenceRefused, requirement)
		}
		if reflect.ValueOf(spec).IsZero() {
			return evidence.Item{}, fmt.Errorf("%w: the amended item of %s is empty", ErrEvidenceRefused, requirement)
		}
		return spec, nil
	})
}

// runUnder runs the item specOf gives for the epic, named item, at revision in phase, under the caller's id, as RunTask,
// RunItem and RunAmendment give. A stored run with id is returned as it is and nothing starts. An id that is not 32
// lowercase hex digits, a refusal of specOf, a bad phase and a revision that does not resolve are refused with an error
// matching ErrEvidenceRefused, and store no run. Otherwise the run is stored capturing, with the commit revision resolves
// to, and driven in the background.
func (e *Evidence) runUnder(
	ctx context.Context, id, epic, item, revision, phase string, specOf func(store.EpicRow) (evidence.Item, error),
) (store.EvidenceRun, error) {
	defer e.calls.enter()()
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
	spec, err := specOf(row)
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

// evidenceRunTaskItemOf returns the evidence item a run of task T<task> of row in phase is held to. A task the epic
// does not have, and an item that is the zero value, are refused with an error matching ErrEvidenceRefused that names
// the task.
func evidenceRunTaskItemOf(row store.EpicRow, task int64, phase string) (evidence.Item, error) {
	for _, planned := range row.Tasks {
		if planned.Seq != task {
			continue
		}
		item := taskRunItem(planned, phase)
		if reflect.ValueOf(item).IsZero() {
			return evidence.Item{}, fmt.Errorf("%w: task T%d has no evidence item", ErrEvidenceRefused, task)
		}
		return item, nil
	}
	return evidence.Item{}, fmt.Errorf("%w: no task T%d", ErrEvidenceRefused, task)
}

// taskRunItem returns the evidence item a run of task in phase is held to: in phase red while the task has a checking
// version, the item of that version, which its red run judges, and otherwise the task's current item.
func taskRunItem(task store.EpicTask, phase string) evidence.Item {
	if version, ok := task.Checking(); ok && phase == evidence.PhaseRed && version.Evidence != nil {
		return *version.Evidence
	}
	return task.Evidence
}
