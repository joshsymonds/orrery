package build

import (
	"context"
	"strings"

	"github.com/joshsymonds/orrery/internal/guard"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskFailed is the status of a task that failed. Its evidence is no longer guarded.
const taskFailed = "failed"

// guardSpec returns what the guard judges task's commit against: the test files and captures of the epic's Requirements'
// items and of each task not failed, this task included, the task's own item's test files, and its test changes.
func guardSpec(epic store.EpicRow, task store.EpicTask) guard.Spec {
	var spec guard.Spec
	for _, requirement := range epic.Requirements {
		spec.Guarded = append(spec.Guarded, requirement.Item.Tests...)
		spec.CaptureArgs = append(spec.CaptureArgs, requirement.Item.Capture...)
	}
	for _, other := range epic.Tasks {
		if other.Status == taskFailed {
			continue
		}
		spec.Guarded = append(spec.Guarded, other.Evidence.Tests...)
		spec.CaptureArgs = append(spec.CaptureArgs, other.Evidence.Capture...)
	}
	spec.Own = task.Evidence.Tests
	spec.Owned = task.TestChanges
	return spec
}

// guardCommit checks the commit action.Next holds green against the guard before that build is stored. A commit the
// guard passes stores the build. A refusal stores its text as the build's guard, then spends the attempt with the text
// as the failure: the first attempt moves to the second with the refusal in its brief, and the second attempt fails the
// task. A check that errs is reported, and the task waits for the next pass.
func (r *Runner) guardCommit(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	refusals, err := guard.Check(ctx, epic.Repository, from.Base, action.Next.Revision, guardSpec(epic, view.Task))
	if err != nil {
		r.report(ctx, epic.ID, seq, "guard implementer commit", err)
		return false
	}
	if len(refusals) == 0 {
		return r.written(ctx, epic.ID, seq, "move build", r.store.SetTaskBuild(ctx, epic.ID, seq, from, action.Next))
	}
	text := guardText(refusals)
	refused := from
	refused.Guard = text
	if !r.written(ctx, epic.ID, seq, "store guard refusal", r.store.SetTaskBuild(ctx, epic.ID, seq, from, refused)) {
		return false
	}
	view.Task.Build = refused
	return r.execute(ctx, epic, view, spend(view, text))
}

// guardText returns the refusal text of a guard that refused: `guard refused: ` and each refusal in order, joined with
// `; `.
func guardText(refusals []guard.Refusal) string {
	texts := make([]string, 0, len(refusals))
	for _, refusal := range refusals {
		texts = append(texts, refusal.String())
	}
	return "guard refused: " + strings.Join(texts, "; ")
}
