package interpret

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// batchResult is the Result an integrate run claims its batch with, in the JSON the integrate kind reads: the base the
// batch composed on, the candidate it made, the tasks that became commits and the tasks left out, each with its reason.
type batchResult struct {
	Base      string         `json:"base"`
	Candidate string         `json:"candidate"`
	Composed  []batchCommit  `json:"composed"`
	LeftOut   []batchLeftOut `json:"leftOut"`
}

// batchCommit is a task that became a commit of the candidate.
type batchCommit struct {
	Task   int64  `json:"task"`
	Commit string `json:"commit"`
}

// batchLeftOut is a task that did not compose, and why.
type batchLeftOut struct {
	Task   int64  `json:"task"`
	Reason string `json:"reason"`
}

// prepareBatch composes the batch of an integrate run on the epic's candidate, or on its stored base when no candidate is
// stored, and pins the candidate under the run. It claims the batch with the candidate, the refs as they are now and the
// result compose made.
func (r *Runner) prepareBatch(ctx context.Context, epic store.EpicRow, act Action) error {
	tasks, err := batchTasks(epic, act.Tasks)
	if err != nil {
		return err
	}
	base := epic.StoredBase
	if epic.Candidate != "" {
		base = epic.Candidate
	}
	result, err := compose.Compose(ctx, epic.Repository, base, tasks, candidateRef(epic, act.Run))
	if err != nil {
		return fmt.Errorf("compose batch of node %s: %w", act.Path, err)
	}
	encoded, err := encodeBatch(result)
	if err != nil {
		return fmt.Errorf("encode batch of node %s: %w", act.Path, err)
	}
	refs, err := r.refSnapshot(ctx, epic)
	if err != nil {
		return err
	}
	if err = r.store.ClaimBatch(
		ctx, epic.ID, act.Path, act.Run.Attempt, result.Candidate, refs, encoded, act.Tasks,
	); err != nil {
		return fmt.Errorf("claim batch of node %s: %w", act.Path, err)
	}
	return nil
}

// batchTasks returns the tasks seqs name, in that order, as compose takes them: each with the base it was built on and
// the revision that holds its work.
func batchTasks(epic store.EpicRow, seqs []int64) ([]compose.Task, error) {
	tasks := make([]compose.Task, 0, len(seqs))
	for _, seq := range seqs {
		i := slices.IndexFunc(epic.Tasks, func(task store.EpicTask) bool { return task.Seq == seq })
		if i < 0 {
			return nil, fmt.Errorf("task %d of epic %s is not stored", seq, epic.ID)
		}
		task := epic.Tasks[i]
		tasks = append(tasks, compose.Task{Seq: seq, Base: task.Build.Base, Revision: task.Build.Revision})
	}
	return tasks, nil
}

// candidateRef returns the ref the candidate of a batch is pinned under. It is named by the run and its attempt, so a
// restarted attempt pins its own candidate.
func candidateRef(epic store.EpicRow, run store.NodeRun) string {
	return fmt.Sprintf("refs/orrery/%s/candidate/%s-%d", build.Slug(epic.ID), run.ID, run.Attempt)
}

// encodeBatch returns result as the JSON an integrate run claims its batch with. Its lists are never null.
func encodeBatch(result compose.Result) (string, error) {
	out := batchResult{
		Base: result.Base, Candidate: result.Candidate,
		Composed: make([]batchCommit, 0, len(result.Composed)),
		LeftOut:  make([]batchLeftOut, 0, len(result.LeftOut)),
	}
	for _, composed := range result.Composed {
		out.Composed = append(out.Composed, batchCommit{Task: composed.Seq, Commit: composed.Commit})
	}
	for _, left := range result.LeftOut {
		out.LeftOut = append(out.LeftOut, batchLeftOut{Task: left.Seq, Reason: left.Reason})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode batch result: %w", err)
	}
	return string(data), nil
}

// capture starts the captures of the Requirements an integrate run's action names, at its candidate, in the green phase.
func (r *Runner) capture(ctx context.Context, epic store.EpicRow, act Action) error {
	if r.evidence == nil {
		return missingService("evidence")
	}
	return r.runItems(ctx, epic, act, evidence.PhaseGreen)
}

// advance moves the epic's branch from the stored base to the candidate of a batch that is green. A branch already at the
// candidate is one a restart left after it moved the branch, so the batch ends passed without a move. A branch that is
// at another commit, or checked out in a worktree, is not moved: the batch parks, and the epic with it, told why.
func (r *Runner) advance(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	tip, ok, err := build.BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return fmt.Errorf("read branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return fmt.Errorf("epic %s has no branch", epic.ID)
	}
	if tip != act.Run.Revision {
		reason, blockErr := advanceBlock(ctx, epic, tip)
		if blockErr != nil {
			return blockErr
		}
		if reason != "" {
			return r.parkBatch(ctx, epic, act, reason, at)
		}
		if err = compose.Advance(
			ctx, epic.Repository, build.BranchRef(epic.ID), act.Run.Revision, epic.StoredBase,
		); err != nil {
			return fmt.Errorf("advance epic %s to %s: %w", epic.ID, act.Run.Revision, err)
		}
	}
	if err = r.store.EndBatch(
		ctx, epic.ID, act.Path, act.Run.Attempt, outcomePassed, act.Output, act.Trigger, at,
	); err != nil {
		return fmt.Errorf("end batch %s: %w", act.Path, err)
	}
	return nil
}

// advanceBlock returns why the epic's branch, at tip, may not move to the candidate: it is not at the stored base, or it
// is checked out in a worktree. It returns "" when the branch may move.
func advanceBlock(ctx context.Context, epic store.EpicRow, tip string) (string, error) {
	slug := build.Slug(epic.ID)
	if tip != epic.StoredBase {
		return fmt.Sprintf("epic/%s moved: at %s, not the stored base %s", slug, tip, epic.StoredBase), nil
	}
	checked, err := build.BranchCheckedOut(ctx, epic.Repository, epic.ID)
	if err != nil {
		return "", fmt.Errorf("read worktrees of epic %s: %w", epic.ID, err)
	}
	if checked {
		return fmt.Sprintf("epic/%s is checked out in a worktree, so it was not moved", slug), nil
	}
	return "", nil
}

// parkBatch parks the batch at the run of act, and the epic with it, for reason, which is told.
func (r *Runner) parkBatch(ctx context.Context, epic store.EpicRow, act Action, reason string, at time.Time) error {
	err := r.store.ParkNode(
		ctx, epic.ID, act.Path, act.Run.Attempt, outcomeFailed, reason, store.ParkClassBranch, reason, at,
	)
	if err != nil {
		return fmt.Errorf("park batch %s: %w", act.Path, err)
	}
	return nil
}
