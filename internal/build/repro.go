package build

import (
	"context"
	"errors"

	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproduce starts the reproduction of the green unit of a test item that action names at its commit, and reports
// whether the check started. The reproduction is the task's base with the commit's test files, pinned under the repro
// ref of the check's id, and the item's capture runs there as the check under that id. A failure is reported, and the
// task waits for the next pass, which asks again.
func (r *Runner) reproduce(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	if r.checks == nil {
		r.report(ctx, epic.ID, seq, "run reproduction", errors.New("the runner has no checks to run a reproduction"))
		return false
	}
	item := view.Task.Evidence
	ref := refsOrreryPrefix + Slug(epic.ID) + "/repro/" + action.ID
	host, err := r.hostOf(epic)
	if err != nil {
		r.report(ctx, epic.ID, seq, "make reproduction overlay", err)
		return false
	}
	overlay, err := compose.Overlay(
		ctx, host, epic.Repository, view.Task.Build.Base, action.Revision, item.Tests, ref,
	)
	if err != nil {
		r.report(ctx, epic.ID, seq, "make reproduction overlay", err)
		return false
	}
	_, err = r.checks.Run(ctx, store.NodeCheck{
		ID: action.ID, Epic: epic.ID, Path: taskLabel(seq), Revision: overlay, Argv: item.Capture, Limit: item.Limit,
	})
	return r.written(ctx, epic.ID, seq, "run reproduction", err)
}
