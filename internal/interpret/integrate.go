package interpret

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
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

// batchLeftOut is a task that did not compose, and why. Conflicts are the paths its merge conflicted at, which a task
// left out for a conflict is sent back for; a result claimed before they were stored has none.
type batchLeftOut struct {
	Task      int64    `json:"task"`
	Reason    string   `json:"reason"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// prepareBatch composes the batch of an integrate run and pins the candidate under the run. The batch composes on the
// epic's batch base (batchBase), and holds, with the tasks act names, each task whose work a failed batch composed and
// the batch base does not hold (strandedTasks), so that work is composed again or left out with its reason, never
// lost. The tasks compose in the order composeOrder gives: a producer before its consumer, and otherwise in seq order.
// It claims the batch with the candidate, the refs as they are now and the result compose made, and claims every task
// of the batch: the tasks act names, and each stranded task, whose claim on its failed batch the claim moves to this
// one, so a failed batch never holds a task that a later batch composed.
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
	order, err := composeOrder(epic.Tasks, seqs)
	if err != nil {
		return fmt.Errorf("order batch of node %s: %w", act.Path, err)
	}
	tasks, err := batchTasks(epic, order)
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

// startStranded returns, in seq order, the done tasks of epic whose work a failed batch composed and the base a fresh
// task starts on does not hold (strandedTasks). That base is the epic's candidate when it has one, and otherwise the
// branch tip, as decideFresh starts a task. No run is skipped. An epic with no branch returns none, since no task starts
// without one.
func startStranded(ctx context.Context, epic store.EpicRow, runs []store.NodeRun) ([]int64, error) {
	tip, ok, err := build.BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return nil, fmt.Errorf("read branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return nil, nil
	}
	base := epic.Candidate
	if base == "" {
		base = tip
	}
	return strandedTasks(ctx, epic, runs, "", base)
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

// composeOrder returns the tasks seqs name, in the order a batch composes them: each task after every task of the batch
// that it depends on, directly or through another batch task, and otherwise the lowest seq first. An entry of depends_on
// that names a task outside the batch orders nothing. seqs is in seq order and holds no task twice. Tasks that depend on
// one another in a cycle have no order, so the batch is refused.
func composeOrder(tasks []store.EpicTask, seqs []int64) ([]int64, error) {
	waiting, dependents := batchWaits(tasks, seqs)
	ready := slices.DeleteFunc(slices.Clone(seqs), func(seq int64) bool { return waiting[seq] > 0 })
	order := make([]int64, 0, len(seqs))
	for len(ready) > 0 {
		slices.Sort(ready)
		next := ready[0]
		ready = ready[1:]
		order = append(order, next)
		for _, dependent := range dependents[next] {
			waiting[dependent]--
			if waiting[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(order) < len(seqs) {
		return nil, fmt.Errorf(
			"tasks %v have no order: their depends_on forms a cycle", blockedSeqs(seqs, waiting),
		)
	}
	return order, nil
}

// batchWaits returns, for each task of seqs, the number of batch tasks it depends on, and for each batch task, the
// tasks that depend on it directly. A depends_on entry that names a task outside seqs is not counted.
func batchWaits(tasks []store.EpicTask, seqs []int64) (map[int64]int, map[int64][]int64) {
	inBatch := make(map[int64]bool, len(seqs))
	for _, seq := range seqs {
		inBatch[seq] = true
	}
	waiting := make(map[int64]int, len(seqs))
	dependents := make(map[int64][]int64, len(seqs))
	for _, task := range tasks {
		if !inBatch[task.Seq] {
			continue
		}
		for _, dep := range task.DependsOn {
			if inBatch[dep] {
				waiting[task.Seq]++
				dependents[dep] = append(dependents[dep], task.Seq)
			}
		}
	}
	return waiting, dependents
}

// blockedSeqs returns the tasks of seqs that still wait on another task, in seq order.
func blockedSeqs(seqs []int64, waiting map[int64]int) []int64 {
	var blocked []int64
	for _, seq := range seqs {
		if waiting[seq] > 0 {
			blocked = append(blocked, seq)
		}
	}
	return blocked
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
		out.LeftOut = append(out.LeftOut, batchLeftOut{Task: left.Seq, Reason: left.Reason, Conflicts: left.Conflicts})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode batch result: %w", err)
	}
	return string(data), nil
}

// composedRound returns result, the result an integrate run claimed its batch with, with task moved from the tasks it left
// out to the tasks it composed, into commit. It is the result a round writes when its merged commit composes the task, so
// the task is no longer left out, and sendBacks never sends it back.
func composedRound(result string, seq int64, commit string) (string, error) {
	var batch batchResult
	if err := json.Unmarshal([]byte(result), &batch); err != nil {
		return "", fmt.Errorf("integrate result does not decode: %w", err)
	}
	out := batchResult{
		Base: batch.Base, Candidate: batch.Candidate,
		Composed: slices.Concat(batch.Composed, []batchCommit{{Task: seq, Commit: commit}}),
		LeftOut:  make([]batchLeftOut, 0, len(batch.LeftOut)),
	}
	for _, left := range batch.LeftOut {
		if left.Task != seq {
			out.LeftOut = append(out.LeftOut, left)
		}
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

// sendBacks returns the tasks result, the result an integrate run claimed its batch with, left out for a merge
// conflict, each with the reason it was left out, so the batch's end sends each back to its implementer. A task left
// out for any other reason is not sent back, and a result that is not a batch result sends none.
func sendBacks(result string) []store.BatchSendBack {
	var batch batchResult
	if json.Unmarshal([]byte(result), &batch) != nil {
		return nil
	}
	var sends []store.BatchSendBack
	for _, left := range batch.LeftOut {
		if len(left.Conflicts) > 0 {
			sends = append(sends, store.BatchSendBack{Seq: left.Task, Reason: left.Reason})
		}
	}
	return sends
}

// sendBackNoteMax and sendBackDiffMax bound the two parts a send_back reason adds to the reason compose gave a task whose
// task integrator rounds ended without composing it: how the rounds ended, and the candidate's change to its conflicted
// paths. With the compose line they fit store.TurnReasonMax, the most a task's failure keeps.
const (
	sendBackNoteMax = 384
	sendBackDiffMax = 448
)

// batchSendBacks returns the tasks act's run left out for a merge conflict, as sendBacks gives them, each with the reason
// it is sent back for. A task whose task integrator rounds ended without composing it (kinds.TaskEnding) has that reason
// extended by sendBackReason, with the candidate's change to its conflicted paths since the merge base of the candidate
// and the task's revision. A task whose rounds did not end that way, such as one whose round's step never started, keeps
// the reason compose gave it.
func (r *Runner) batchSendBacks(ctx context.Context, epic store.EpicRow, act Action) ([]store.BatchSendBack, error) {
	sends := sendBacks(act.Run.Result)
	if len(sends) == 0 {
		return nil, nil
	}
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return nil, fmt.Errorf("read the integrate rounds of epic %s: %w", epic.ID, err)
	}
	steps, err := r.store.LabelledSteps(ctx, epic.ID, act.Path)
	if err != nil {
		return nil, fmt.Errorf("read the task integrator steps of node %s of epic %s: %w", act.Path, epic.ID, err)
	}
	rows := kinds.Rows{Rounds: flow.Rounds, Steps: steps}
	conflicts := conflictsOf(act.Run.Result)
	for i, send := range sends {
		ending := kinds.TaskEnding(rows, act.Run, send.Seq)
		if ending == "" {
			continue
		}
		task, found := taskBySeq(epic.Tasks, send.Seq)
		if !found {
			return nil, fmt.Errorf("task %d of epic %s is not stored", send.Seq, epic.ID)
		}
		base, change, diffErr := candidateChange(
			ctx, epic.Repository, act.Run.Revision, task.Build.Revision, conflicts[send.Seq],
		)
		if diffErr != nil {
			return nil, diffErr
		}
		sends[i].Reason = sendBackReason(send.Reason, ending, base, change)
	}
	return sends, nil
}

// conflictsOf returns, by task, the paths each task result left out for a merge conflict conflicted at. A result that is not
// a batch result names none.
func conflictsOf(result string) map[int64][]string {
	conflicts := make(map[int64][]string)
	var batch batchResult
	if json.Unmarshal([]byte(result), &batch) != nil {
		return conflicts
	}
	for _, left := range batch.LeftOut {
		conflicts[left.Task] = left.Conflicts
	}
	return conflicts
}

// candidateChange returns the merge base of candidate and revision, the commit a merge of revision into candidate starts
// from, and the change candidate makes to paths since that base, as git diff prints it. It is the side a task's merge met.
func candidateChange(ctx context.Context, repo, candidate, revision string, paths []string) (string, string, error) {
	out, err := gitOutput(ctx, repo, "merge-base", candidate, revision)
	if err != nil {
		return "", "", fmt.Errorf("find the merge base of %s and %s: %w", candidate, revision, err)
	}
	base := strings.TrimSpace(out)
	args := append([]string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", base, candidate, "--"}, paths...)
	change, err := gitOutput(ctx, repo, args...)
	if err != nil {
		return "", "", fmt.Errorf("diff candidate %s against its merge base %s: %w", candidate, base, err)
	}
	return base, change, nil
}

// sendBackReason returns the reason a task whose rounds ended without composing it is sent back for: conflict, the compose
// line that names its conflicted paths, then ending, how its rounds ended, then the candidate's change to those paths since
// base, cut to sendBackDiffMax bytes. A candidate that changes none of them since base says so. The reason is cut to
// store.TurnReasonMax.
func sendBackReason(conflict, ending, base, change string) string {
	changed := "the candidate changes none of the conflicted paths since the merge base " + base
	if diff := strings.TrimRight(change, "\n"); diff != "" {
		changed = fmt.Sprintf("the candidate's change to the conflicted paths since the merge base %s:\n%s",
			base, cutText(diff, sendBackDiffMax))
	}
	return cutText(conflict+"\n"+cutText(ending, sendBackNoteMax)+"\n"+changed, store.TurnReasonMax)
}

// endBatch ends the integrate run of act, which ended without an advance, with its outcome, and keeps the epic's
// candidate on its stored base. A failed run that composed a task leaves as the candidate its revision joined with the
// stored base (compose.Join): the revision itself when it builds on the stored base, and otherwise the merge of the
// stored base into it, as a batch composed on a stored base that a newer batch has since moved needs. A revision that
// does not merge with the stored base leaves the candidate as it is, and the next batch composes the run's tasks again
// (strandedTasks). Any other run ends with the candidate as it is.
func (r *Runner) endBatch(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	sends, sendErr := r.batchSendBacks(ctx, epic, act)
	if sendErr != nil {
		return sendErr
	}
	end := store.BatchEnd{
		Path: act.Path, Attempt: act.Attempt, Outcome: act.Outcome, Output: act.Output, Over: epic.StoredBase,
		SendBacks: sends,
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
	sends, err := r.batchSendBacks(ctx, epic, act)
	if err != nil {
		return err
	}
	end := store.BatchEnd{
		Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomePassed, Output: act.Output, Over: act.Run.Revision,
		SendBacks: sends,
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
