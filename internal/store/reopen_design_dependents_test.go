package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// dependentBatchPath is the integrate run that composes the revision in the dependents tests.
const dependentBatchPath = "plan/S1/integrate"

// revisionReason is the reason the dependents of T1 are sent back with, once revision, a revision of T1, integrates.
func revisionReason(revision int64) string {
	return fmt.Sprintf("design T1 was revised by T%d: %s", revision, reopenDeliverable)
}

// addDependentsTask stores task seq of integrateEpic, of kind, with the depends_on dependsOn encodes and the status, so
// a reopen_design action can revise it when it is a design task.
func addDependentsTask(t *testing.T, s *Store, seq int64, kind, status, dependsOn string) {
	t.Helper()
	ctx := context.Background()
	item, err := encodeItem(reopenItem())
	if err != nil {
		t.Fatalf("encode evidence of task %d: %v", seq, err)
	}
	if err = s.fixtureQueries().InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
		Epic: integrateEpic, Seq: seq, Turn: 1, Title: fmt.Sprintf("Task %d", seq),
		Deliverable: fmt.Sprintf("the work of task %d", seq), Gate: "review", Evidence: item,
		DependsOn: dependsOn, Kind: kind,
	}); err != nil {
		t.Fatalf("insert task %d of epic %s: %v", seq, integrateEpic, err)
	}
	setDependentsStatus(t, s, seq, status)
}

// setDependentsStatus stores status as the status of task seq of integrateEpic.
func setDependentsStatus(t *testing.T, s *Store, seq int64, status string) {
	t.Helper()
	if _, err := s.fixtureExec(context.Background(),
		`UPDATE epic_tasks SET status = ? WHERE epic = ? AND seq = ?`, status, integrateEpic, seq); err != nil {
		t.Fatalf("set status of task %d of epic %s: %v", seq, integrateEpic, err)
	}
}

// reviseDesignAt stores a reopen_design action of the design task T1 of integrateEpic at turn, carries it out, and
// returns the seq of the revision it adds.
func reviseDesignAt(t *testing.T, s *Store, turn int64) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return insertActions(ctx, queries, integrateEpic, turn, []EpicActionInput{{
			Kind: ActionReopenDesign, Task: 1, Deliverable: reopenDeliverable, Reason: reopenReason,
		}})
	}); err != nil {
		t.Fatalf("insert reopen_design action of turn %d of epic %s: %v", turn, integrateEpic, err)
	}
	var action PlannerAction
	for _, pending := range pendingActionsOf(t, s, integrateEpic) {
		if pending.Turn == turn {
			action = pending
		}
	}
	if action.Turn != turn {
		t.Fatalf("epic %s has no pending action of turn %d", integrateEpic, turn)
	}
	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || stop != nil {
		t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
	}
	var revision int64
	for _, task := range integrateEpicRow(t, s).Tasks {
		if task.Revises == 1 {
			revision = task.Seq
		}
	}
	if revision == 0 {
		t.Fatalf("epic %s has no revision of task 1", integrateEpic)
	}
	return revision
}

// markDoneTask sets task seq of integrateEpic done, as the build that composes it leaves it.
func markDoneTask(t *testing.T, s *Store, seq int64) {
	t.Helper()
	setDependentsStatus(t, s, seq, taskDone)
}

// claimComposing starts the integrate run at path and claims the done tasks composed, each composed into a commit, at
// revision, so the run's batch is the one that composes them.
func claimComposing(t *testing.T, s *Store, path, revision string, composed []int64) {
	t.Helper()
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-" + path, Node: "integrate", Kind: integrateKind, Attempt: 1})
	commits := make([]string, len(composed))
	for i, seq := range composed {
		commits[i] = fmt.Sprintf(`{"task":%d,"commit":"c%d"}`, seq, seq)
	}
	result := fmt.Sprintf(`{"composed":[%s],"leftOut":[]}`, strings.Join(commits, ","))
	if err := s.ClaimBatch(
		context.Background(), integrateEpic, path, 1, revision, "refs", result, composed,
	); err != nil {
		t.Fatalf("ClaimBatch %s: %v", path, err)
	}
}

// endComposingBatch claims the batch of claimComposing at path, and ends the run with outcome, at revision.
func endComposingBatch(t *testing.T, s *Store, path, outcome, revision string, composed []int64) {
	t.Helper()
	claimComposing(t, s, path, revision, composed)
	if err := s.EndBatch(context.Background(), integrateEpic, BatchEnd{
		Path: path, Attempt: 1, Outcome: outcome, Output: outcome, Over: revision,
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch %s: %v", path, err)
	}
}

// taskWithSeq returns the task of tasks with seq.
func taskWithSeq(t *testing.T, tasks []EpicTask, seq int64) EpicTask {
	t.Helper()
	for _, task := range tasks {
		if task.Seq == seq {
			return task
		}
	}
	t.Fatalf("epic %s has no task %d", integrateEpic, seq)
	return EpicTask{}
}

// dependentTaskOf returns task seq of integrateEpic as the store reads it.
func dependentTaskOf(t *testing.T, s *Store, seq int64) EpicTask {
	t.Helper()
	return taskWithSeq(t, integrateEpicRow(t, s).Tasks, seq)
}

// requireDependentReopened fails unless task seq of integrateEpic is open at generation, on implement at attempt 1
// built on base with reason as its failure, holds no batch, has rung 1 moved by send_back as its only rung of that
// generation, and its last version is a send_back version with no decision that changes no field and gives reason.
func requireDependentReopened(t *testing.T, s *Store, seq, generation int64, base, reason string) {
	t.Helper()
	task := dependentTaskOf(t, s, seq)
	if task.Generation != generation {
		t.Errorf("task %d generation = %d, want %d", seq, task.Generation, generation)
	}
	if task.Status != taskOpen {
		t.Errorf("task %d status = %q, want open", seq, task.Status)
	}
	if task.Batch != "" {
		t.Errorf("task %d batch = %q, want none", seq, task.Batch)
	}
	want := TaskBuild{Stage: "implement", Base: base, Attempt: 1, Failure: reason}
	if !reflect.DeepEqual(task.Build, want) {
		t.Errorf("task %d build = %+v, want %+v", seq, task.Build, want)
	}
	rungs := []TaskRung{{
		Rung: 1, Kind: RungAttempt, MovedBy: MovedBySendBack, Cause: reason, Created: integrateEndedAt(),
	}}
	if !reflect.DeepEqual(task.Rungs, rungs) {
		t.Errorf("task %d rungs:\n got %+v\nwant %+v", seq, task.Rungs, rungs)
	}
	if len(task.Versions) == 0 {
		t.Fatalf("task %d versions = none, want a send_back version", seq)
	}
	last := task.Versions[len(task.Versions)-1]
	if last.Source != SourceSendBack || last.Decision != "" || last.Reason != reason ||
		last.State != VersionApplied || len(last.Changed()) != 0 {
		t.Errorf("task %d last version = %+v, want an applied send_back version with no decision that changes no field",
			seq, last)
	}
}

// TestReopenDesignDependentsSendsBackDirectAndTransitiveDependents checks that a passed batch composing a revision of
// design task T1 sends back each open or done task that depends on T1, directly or through another dependent, at its
// next generation, with the reason naming the revision: T2 directly and done, T3 directly and open, and T4 through T3
// and done. T5, which depends on nothing, and T1 are left as they were, and so is the revision, which keeps its status
// and batch.
func TestReopenDesignDependentsSendsBackDirectAndTransitiveDependents(t *testing.T) {
	t.Parallel()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	addDependentsTask(t, s, 3, "", taskOpen, "2")
	addDependentsTask(t, s, 4, "", taskDone, "3")
	addDependentsTask(t, s, 5, "", taskDone, "")
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	before := integrateEpicRow(t, s).Tasks

	endComposingBatch(t, s, dependentBatchPath, nodePassedOutcome, "rev-1", []int64{revision})

	reason := revisionReason(revision)
	for _, seq := range []int64{2, 3, 4} {
		requireDependentReopened(t, s, seq, 1, "rev-1", reason)
	}
	for _, seq := range []int64{1, 5} {
		if got := dependentTaskOf(t, s, seq); !reflect.DeepEqual(got, taskWithSeq(t, before, seq)) {
			t.Errorf("task %d after the revision = %+v, want it unchanged: %+v", seq, got, taskWithSeq(t, before, seq))
		}
	}
	got := dependentTaskOf(t, s, revision)
	if got.Status != taskDone || got.Batch != dependentBatchPath || got.Generation != 0 || got.Revises != 1 {
		t.Errorf("revision %d = status %q batch %q generation %d revises %d, want done in %s at generation 0",
			revision, got.Status, got.Batch, got.Generation, got.Revises, dependentBatchPath)
	}
}

// TestReopenDesignDependentsLeavesOutFailedGapAndTheRevision checks that a passed batch sends back only the dependents
// that are open or done. A failed task and a task that ended as a gap, each depending on T1, are left as they are, and
// so is the revision even when its depends_on names T1. A done task depending on the failed task is still a dependent
// of T1, reached through it, so it is sent back.
func TestReopenDesignDependentsLeavesOutFailedGapAndTheRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	addDependentsTask(t, s, 3, "", taskFailed, "1")
	addDependentsTask(t, s, 4, "", TaskGap, "1")
	addDependentsTask(t, s, 5, "", taskDone, "3")
	revision := reviseDesignAt(t, s, 1)
	if _, err := s.fixtureExec(ctx,
		`UPDATE epic_tasks SET depends_on = '1' WHERE epic = ? AND seq = ?`, integrateEpic, revision); err != nil {
		t.Fatalf("set depends_on of revision %d: %v", revision, err)
	}
	markDoneTask(t, s, revision)
	before := integrateEpicRow(t, s).Tasks

	endComposingBatch(t, s, dependentBatchPath, nodePassedOutcome, "rev-1", []int64{revision})

	reason := revisionReason(revision)
	requireDependentReopened(t, s, 2, 1, "rev-1", reason)
	requireDependentReopened(t, s, 5, 1, "rev-1", reason)
	for _, seq := range []int64{3, 4} {
		if got := dependentTaskOf(t, s, seq); !reflect.DeepEqual(got, taskWithSeq(t, before, seq)) {
			t.Errorf("task %d after the revision = %+v, want it unchanged: %+v", seq, got, taskWithSeq(t, before, seq))
		}
	}
	if got := dependentTaskOf(t, s, revision); got.Status != taskDone || got.Batch != dependentBatchPath ||
		got.Generation != 0 {
		t.Errorf("revision %d = status %q batch %q generation %d, want done in %s at generation 0",
			revision, got.Status, got.Batch, got.Generation, dependentBatchPath)
	}
}

// TestReopenDesignDependentsWaitsForAPassedBatch checks that nothing is sent back until a passed batch composes the
// revision: a batch that fails with the revision composed sends back no dependent, and the dependent goes back once a
// later batch passes with the revision composed.
func TestReopenDesignDependentsWaitsForAPassedBatch(t *testing.T) {
	t.Parallel()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	before := dependentTaskOf(t, s, 2)

	endComposingBatch(t, s, "plan/S1/integrate", "failed", "rev-1", []int64{revision})
	if got := dependentTaskOf(t, s, 2); !reflect.DeepEqual(got, before) {
		t.Fatalf("task 2 after a failed batch = %+v, want it unchanged: %+v", got, before)
	}

	endComposingBatch(t, s, "plan/S2/integrate", nodePassedOutcome, "rev-2", []int64{revision})
	requireDependentReopened(t, s, 2, 1, "rev-2", revisionReason(revision))
}

// TestReopenDesignDependentsRepeatSendsNothingTwice checks that a second end of a passed run changes nothing: the
// dependent keeps the generation, versions and rungs the first end gave it.
func TestReopenDesignDependentsRepeatSendsNothingTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	endComposingBatch(t, s, dependentBatchPath, nodePassedOutcome, "rev-1", []int64{revision})
	reopened := integrateEpicRow(t, s).Tasks

	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: dependentBatchPath, Attempt: 1, Outcome: nodePassedOutcome, Output: "again", Over: "rev-1",
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("repeat EndBatch: %v", err)
	}

	if got := integrateEpicRow(t, s).Tasks; !reflect.DeepEqual(got, reopened) {
		t.Errorf("tasks after a repeat of the end:\n got %+v\nwant %+v", got, reopened)
	}
	requireDependentReopened(t, s, 2, 1, "rev-1", revisionReason(revision))
}

// TestReopenDesignDependentsKeepsEarlierGenerations checks that a send back keeps the history of the dependent: the
// version and the rung of its earlier generation stay stored, and the send back adds its own version and rung at the
// next generation.
func TestReopenDesignDependentsKeepsEarlierGenerations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	gate := "the earlier gate"
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return appendTaskVersion(ctx, queries, integrateEpic, 2, "", TaskVersionInput{
			Source: SourceOrchestrator, Reason: "the first version", Gate: &gate,
		}, integrateStartedAt())
	}); err != nil {
		t.Fatalf("append version of task 2: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_rungs
		(epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created)
		VALUES (?, 2, 0, 1, 'attempt', '', 'the first failure', '', '', '', ?)`,
		integrateEpic, integrateStartedAt().Unix()); err != nil {
		t.Fatalf("insert rung 1 of task 2 at generation 0: %v", err)
	}
	before := dependentTaskOf(t, s, 2)
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)

	endComposingBatch(t, s, dependentBatchPath, nodePassedOutcome, "rev-1", []int64{revision})

	requireDependentReopened(t, s, 2, 1, "rev-1", revisionReason(revision))
	after := dependentTaskOf(t, s, 2)
	if len(after.Versions) != len(before.Versions)+1 {
		t.Fatalf("versions of task 2 = %d, want %d: the earlier ones and the send back", len(after.Versions),
			len(before.Versions)+1)
	}
	if !reflect.DeepEqual(after.Versions[:len(before.Versions)], before.Versions) {
		t.Errorf("earlier versions of task 2 after the send back = %+v, want %+v",
			after.Versions[:len(before.Versions)], before.Versions)
	}
	var earlier int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM epic_task_rungs WHERE epic = ? AND task = 2 AND generation = 0`,
		integrateEpic).Scan(&earlier); err != nil {
		t.Fatalf("count rungs of task 2 at generation 0: %v", err)
	}
	if earlier != 1 {
		t.Errorf("rungs of task 2 at generation 0 = %d, want the 1 stored before the send back", earlier)
	}
}

// TestReopenDesignDependentsLeftOutTaskIsSentBackOnce checks that a dependent that the passed batch composing the
// revision leaves out for a merge conflict is sent back once: the batch's send back keeps its reason, and its
// generation rises by one, not two.
func TestReopenDesignDependentsLeftOutTaskIsSentBackOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	startIntegrateRuns(t, s,
		NodeRun{Path: dependentBatchPath, ID: "id-s1", Node: "integrate", Kind: integrateKind, Attempt: 1})
	if err := s.ClaimBatch(ctx, integrateEpic, dependentBatchPath, 1, "rev-1", "refs-1",
		fmt.Sprintf(`{"composed":[{"task":%d,"commit":"c%d"}],"leftOut":[2]}`, revision, revision),
		[]int64{revision, 2}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}

	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: dependentBatchPath, Attempt: 1, Outcome: nodePassedOutcome, Output: "green", Over: "rev-1",
		SendBacks: []BatchSendBack{{Seq: 2, Reason: branchMergeReason}},
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}

	requireDependentReopened(t, s, 2, 1, "rev-1", branchMergeReason)
}

// TestReopenDesignDependentsEachRevisionSendsBackAgain checks that each revision of design task T1 that a passed batch
// integrates sends its dependents back once more, at the next generation, with the reason naming that revision.
func TestReopenDesignDependentsEachRevisionSendsBackAgain(t *testing.T) {
	t.Parallel()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	first := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, first)
	endComposingBatch(t, s, dependentBatchPath, nodePassedOutcome, "rev-1", []int64{first})
	requireDependentReopened(t, s, 2, 1, "rev-1", revisionReason(first))

	second := reviseDesignAt(t, s, 2)
	markDoneTask(t, s, second)
	endComposingBatch(t, s, "plan/S2/integrate", nodePassedOutcome, "rev-2", []int64{second})
	requireDependentReopened(t, s, 2, 2, "rev-2", revisionReason(second))
}

// TestReopenDesignDependentsClearsTheCandidateForAnUnbasedDependent checks that a passed batch clears the epic's
// candidate when it reopens a dependent the stored base lacks, since the candidate may hold that dependent's commit.
func TestReopenDesignDependentsClearsTheCandidateForAnUnbasedDependent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	const earlier = "plan/S0/integrate"
	claimComposing(t, s, earlier, "rev-0", []int64{2})
	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: earlier, Attempt: 1, Outcome: "failed", Output: "red", Candidate: "rev-cand", Over: integrationBase,
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch %s: %v", earlier, err)
	}
	if got := integrateEpicRow(t, s).Candidate; got != "rev-cand" {
		t.Fatalf("candidate after the failed batch = %q, want rev-cand", got)
	}
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	claimComposing(t, s, dependentBatchPath, "rev-b", []int64{revision})

	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: dependentBatchPath, Attempt: 1, Outcome: nodePassedOutcome, Output: "green",
		Candidate: "rev-joined", Over: "rev-b",
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch %s: %v", dependentBatchPath, err)
	}

	if got := integrateEpicRow(t, s).Candidate; got != "" {
		t.Errorf("candidate after the send back = %q, want it cleared", got)
	}
	requireDependentReopened(t, s, 2, 1, "rev-b", revisionReason(revision))
}

// TestReopenDesignDependentsKeepsTheCandidateForAComposedDependent checks that a passed batch keeps the epic's candidate
// when the batch composed the dependent it reopens, since the stored base holds its commit.
func TestReopenDesignDependentsKeepsTheCandidateForAComposedDependent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	addDependentsTask(t, s, 1, TaskKindDesign, taskDone, "")
	addDependentsTask(t, s, 2, "", taskDone, "1")
	revision := reviseDesignAt(t, s, 1)
	markDoneTask(t, s, revision)
	claimComposing(t, s, dependentBatchPath, "rev-b", []int64{revision, 2})

	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: dependentBatchPath, Attempt: 1, Outcome: nodePassedOutcome, Output: "green",
		Candidate: "rev-joined", Over: "rev-b",
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch %s: %v", dependentBatchPath, err)
	}

	if got := integrateEpicRow(t, s).Candidate; got != "rev-joined" {
		t.Errorf("candidate after the send back = %q, want rev-joined kept", got)
	}
	requireDependentReopened(t, s, 2, 1, "rev-b", revisionReason(revision))
}
