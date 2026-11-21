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

// prepareBatch composes the batch of an integrate run and pins the candidate under the run. The batch composes on the
// epic's batch base (batchBase), and holds, with the tasks act names, each task whose work a failed batch composed and
// the batch base does not hold (strandedTasks), so that work is composed again or left out with its reason, never
// lost. The tasks compose in seq order. It claims the batch with the candidate, the refs as they are now and the result
// compose made, and claims every task of the batch: the tasks act names, and each stranded task, whose claim on its
// failed batch the claim moves to this one, so a failed batch never holds a task that a later batch composed.
func (r *Runner) prepareBatch(ctx context.Context, epic store.EpicRow, act Action) error {
	base, err := batchBase(ctx, epic)
	if err != nil {
		return err
	}
	runs, err := r.store.NodeRuns(ctx, epic.ID)
	if err != nil {
		return fmt.Errorf("read node runs of epic %s: %w", epic.ID, err)
	}
	stranded, err := strandedTasks(ctx, epic, runs, act.Path, base)
	if err != nil {
		return err
	}
	seqs := slices.Concat(stranded, act.Tasks)
	slices.Sort(seqs)
	seqs = slices.Compact(seqs)
	tasks, err := batchTasks(epic, seqs)
	if err != nil {
		return err
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
		ctx, epic.ID, act.Path, act.Run.Attempt, result.Candidate, refs, encoded, seqs,
	); err != nil {
		return fmt.Errorf("claim batch of node %s: %w", act.Path, err)
	}
	return nil
}

// batchBase returns the commit the next batch of epic composes on, which always builds on the epic's stored base: the
// stored base when the epic has no candidate, and otherwise the candidate joined with the stored base (compose.Join),
// which is the candidate when it builds on the stored base and the merge of the stored base into it when it does not.
// A candidate that does not merge with the stored base gives the stored base, and the work of the candidate's tasks
// that the stored base lacks is then composed again (strandedTasks).
func batchBase(ctx context.Context, epic store.EpicRow) (string, error) {
	if epic.Candidate == "" {
		return epic.StoredBase, nil
	}
	joined, ok, err := compose.Join(ctx, epic.Repository, epic.StoredBase, epic.Candidate, joinPrefix(epic))
	if err != nil {
		return "", fmt.Errorf(
			"join candidate %s of epic %s with its stored base %s: %w", epic.Candidate, epic.ID, epic.StoredBase, err,
		)
	}
	if !ok {
		return epic.StoredBase, nil
	}
	return joined, nil
}

// strandedTasks returns, in seq order, the done tasks of epic whose work a failed batch composed and base does not
// hold: the integrate run that claimed the task ended failed, no other integrate run that composed it is still running
// or parked, and no commit an integrate run composed it into since a send_back last reopened it is base or an ancestor
// of it. An open task is not stranded: a send_back reopened it, and its work is the next batch's to compose. A failed batch whose
// revision did not join the stored base, or whose candidate a passed batch cleared without holding it, leaves such
// tasks. The integrate run at path, the one being prepared, is not read.
func strandedTasks(
	ctx context.Context, epic store.EpicRow, runs []store.NodeRun, path, base string,
) ([]int64, error) {
	batches := readBatches(runs, path)
	var stranded []int64
	for _, task := range epic.Tasks {
		commits, ok := batches.failedWork(task)
		if !ok {
			continue
		}
		held, err := holdsAny(ctx, epic.Repository, base, commits)
		if err != nil {
			return nil, err
		}
		if !held {
			stranded = append(stranded, task.Seq)
		}
	}
	return stranded, nil
}

// batches is what strandedTasks reads of an epic's integrate runs: each run by its path, the commits the runs composed
// each task into with the start of the run that composed it, and the tasks a run that has not ended composed.
type batches struct {
	runs     map[string]store.NodeRun
	commits  map[int64][]composedCommit
	inFlight map[int64]bool
}

// composedCommit is a commit an integrate run composed a task into, and when that run started.
type composedCommit struct {
	commit  string
	started time.Time
}

// readBatches returns the batches of the integrate runs among runs, all but the one at path. A run whose result is not
// a batch result composed nothing.
func readBatches(runs []store.NodeRun, path string) batches {
	read := batches{
		runs: make(map[string]store.NodeRun), commits: make(map[int64][]composedCommit), inFlight: make(map[int64]bool),
	}
	for _, run := range runs {
		if run.Kind != kindIntegrate || run.Path == path {
			continue
		}
		read.runs[run.Path] = run
		var result batchResult
		if json.Unmarshal([]byte(run.Result), &result) != nil {
			continue
		}
		for _, composed := range result.Composed {
			read.commits[composed.Task] = append(
				read.commits[composed.Task], composedCommit{commit: composed.Commit, started: run.Started},
			)
			read.inFlight[composed.Task] = read.inFlight[composed.Task] || run.State != store.NodeEnded
		}
	}
	return read
}

// commitsSince returns the commits the integrate runs composed task into since a send_back last reopened it. A run that
// started at or before that reopen composed the task's work from before it, which the task no longer holds, so a base
// that holds such a commit does not hold the task's correction.
func (b batches) commitsSince(task store.EpicTask) []string {
	reopened := reopenedAt(task)
	var commits []string
	for _, composed := range b.commits[task.Seq] {
		if composed.started.After(reopened) {
			commits = append(commits, composed.commit)
		}
	}
	return commits
}

// reopenedAt returns when a send_back last reopened task, or the zero time when none did.
func reopenedAt(task store.EpicTask) time.Time {
	var at time.Time
	for _, version := range task.Versions {
		if version.Source == store.SourceSendBack {
			at = version.Created
		}
	}
	return at
}

// failedWork returns the commits the integrate runs composed task into since it was last reopened, and true, when task is
// done, the run that claimed it ended failed, and no run that composed it is still running or parked.
func (b batches) failedWork(task store.EpicTask) ([]string, bool) {
	claim, claimed := b.runs[task.Batch]
	failed := claimed && claim.State == store.NodeEnded && claim.Outcome == outcomeFailed
	commits := b.commitsSince(task)
	if task.Status != taskDone || !failed || b.inFlight[task.Seq] || len(commits) == 0 {
		return nil, false
	}
	return commits, true
}

// holdsAny reports whether one of commits is base or an ancestor of it.
func holdsAny(ctx context.Context, repo, base string, commits []string) (bool, error) {
	for _, commit := range commits {
		held, err := isAncestor(ctx, repo, commit, base)
		if err != nil {
			return false, fmt.Errorf("check that %s holds %s: %w", base, commit, err)
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

// joinPrefix returns the namespace a merge of an epic's stored base into its candidate is pinned under, each merge by
// its own id (compose.Join).
func joinPrefix(epic store.EpicRow) string {
	return fmt.Sprintf("refs/orrery/%s/join/", build.Slug(epic.ID))
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

// composedAny reports whether result, the result an integrate run claimed its batch with, names a task compose made
// into a commit. A result that is not a batch result names none.
func composedAny(result string) bool {
	var batch batchResult
	return json.Unmarshal([]byte(result), &batch) == nil && len(batch.Composed) > 0
}

// endBatch ends the integrate run of act, which ended without an advance, with its outcome, and keeps the epic's
// candidate on its stored base. A failed run that composed a task leaves as the candidate its revision joined with the
// stored base (compose.Join): the revision itself when it builds on the stored base, and otherwise the merge of the
// stored base into it, as a batch composed on a stored base that a newer batch has since moved needs. A revision that
// does not merge with the stored base leaves the candidate as it is, and the next batch composes the run's tasks again
// (strandedTasks). Any other run ends with the candidate as it is.
func (r *Runner) endBatch(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	end := store.BatchEnd{
		Path: act.Path, Attempt: act.Attempt, Outcome: act.Outcome, Output: act.Output, Over: epic.StoredBase,
	}
	if act.Outcome == outcomeFailed && composedAny(act.Run.Result) {
		joined, ok, err := compose.Join(ctx, epic.Repository, epic.StoredBase, act.Run.Revision, joinPrefix(epic))
		if err != nil {
			return fmt.Errorf("join batch %s with the stored base %s: %w", act.Path, epic.StoredBase, err)
		}
		if ok {
			end.Candidate = joined
		}
	}
	if err := r.store.EndBatch(ctx, epic.ID, end, act.Trigger, at); err != nil {
		return fmt.Errorf("end batch %s: %w", act.Path, err)
	}
	return nil
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
// at another commit, or checked out in a worktree, is not moved: the batch parks, and the epic with it, told why. The
// batch's revision becomes the stored base. An epic candidate that the revision does not hold, which a failed batch
// leaves while this batch was composed on an older base, stays the candidate joined with the new stored base
// (compose.Join), so its work is kept on a candidate that builds on the stored base. One that does not merge with the
// revision is cleared, and the next batch composes its tasks again (strandedTasks).
func (r *Runner) advance(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	tip, ok, err := build.BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return fmt.Errorf("read branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return fmt.Errorf("epic %s has no branch", epic.ID)
	}
	if tip != act.Run.Revision {
		reason, blockErr := advanceBlock(ctx, epic, tip, act.Run.Revision)
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
	end := store.BatchEnd{
		Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomePassed, Output: act.Output, Over: act.Run.Revision,
	}
	if epic.Candidate != "" {
		joined, joins, joinErr := compose.Join(
			ctx, epic.Repository, act.Run.Revision, epic.Candidate, joinPrefix(epic),
		)
		if joinErr != nil {
			return fmt.Errorf("join candidate %s of epic %s with %s: %w", epic.Candidate, epic.ID,
				act.Run.Revision, joinErr)
		}
		if joins {
			end.Candidate = joined
		}
	}
	if err = r.store.EndBatch(ctx, epic.ID, end, act.Trigger, at); err != nil {
		return fmt.Errorf("end batch %s: %w", act.Path, err)
	}
	return nil
}

// advanceBlock returns why the epic's branch, at tip, may not move to candidate: it is not at the stored base, candidate
// does not build on it, so the move would drop what the branch holds, or it is checked out in a worktree. It returns ""
// when the branch may move.
func advanceBlock(ctx context.Context, epic store.EpicRow, tip, candidate string) (string, error) {
	slug := build.Slug(epic.ID)
	if tip != epic.StoredBase {
		return fmt.Sprintf("epic/%s moved: at %s, not the stored base %s", slug, tip, epic.StoredBase), nil
	}
	builds, err := isAncestor(ctx, epic.Repository, tip, candidate)
	if err != nil {
		return "", fmt.Errorf("compare candidate %s with epic/%s: %w", candidate, slug, err)
	}
	if !builds {
		return fmt.Sprintf("epic/%s is at %s, which the candidate %s does not build on, so it was not moved", slug, tip,
			candidate), nil
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
