package store

import (
	"context"
	"reflect"
	"testing"
)

// branchMergePath is the integrate run that merges the batch's tasks in the branch merge tests.
const branchMergePath = "plan/S1/integrate"

// branchMergeReason is the reason a batch gives for leaving a task out for a merge conflict.
const branchMergeReason = "the task's branch conflicts with the candidate's merge"

// branchMergeTask returns task seq of integrateEpic as the store reads it.
func branchMergeTask(t *testing.T, s *Store, seq int64) EpicTask {
	t.Helper()
	for _, task := range integrateEpicRow(t, s).Tasks {
		if task.Seq == seq {
			return task
		}
	}
	t.Fatalf("epic %s has no task %d", integrateEpic, seq)
	return EpicTask{}
}

// requireBranchMergeReopened fails unless task seq of integrateEpic is open on implement at attempt 1 with base as its
// base and branchMergeReason as its failure, holds no batch, carries rung 1 moved by send_back, and its last version is
// a send_back version with no decision and branchMergeReason as its reason.
func requireBranchMergeReopened(t *testing.T, s *Store, seq int64, base string) {
	t.Helper()
	task := branchMergeTask(t, s, seq)
	if task.Status != taskOpen {
		t.Errorf("task %d status = %q, want open", seq, task.Status)
	}
	want := TaskBuild{Stage: "implement", Base: base, Attempt: 1, Failure: branchMergeReason}
	if !reflect.DeepEqual(task.Build, want) {
		t.Errorf("task %d build = %+v, want %+v", seq, task.Build, want)
	}
	if task.Batch != "" {
		t.Errorf("task %d batch = %q, want none", seq, task.Batch)
	}
	rungs := []TaskRung{{
		Rung: 1, Kind: RungAttempt, MovedBy: MovedBySendBack, Cause: branchMergeReason, Created: integrateEndedAt(),
	}}
	if !reflect.DeepEqual(task.Rungs, rungs) {
		t.Errorf("task %d rungs:\n got %+v\nwant %+v", seq, task.Rungs, rungs)
	}
	if len(task.Versions) == 0 {
		t.Fatalf("task %d versions = none, want a send_back version", seq)
	}
	last := task.Versions[len(task.Versions)-1]
	if last.Source != SourceSendBack || last.Decision != "" || last.Reason != branchMergeReason ||
		last.State != VersionApplied || len(last.Changed()) != 0 {
		t.Errorf("task %d last version = %+v, want an applied send_back version with no decision that changes no field",
			seq, last)
	}
}

// TestEndBatchBranchMergeSendsBackConflictedTaskOnTheCandidate checks that a failed batch that ends with a candidate
// reopens each task it left out for a merge conflict on that candidate, with the batch's reason, and clears its claim.
// The candidate is kept, and the batch's trigger is the only one added.
func TestEndBatchBranchMergeSendsBackConflictedTaskOnTheCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: branchMergePath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	addIntegrateTask(t, s, 1, "done", "")
	addIntegrateTask(t, s, 2, "done", "")
	if err := s.ClaimBatch(ctx, integrateEpic, branchMergePath, 1, "rev-1", "refs-1",
		`{"composed":[{"task":2,"commit":"c2"}],"leftOut":[1]}`, []int64{1, 2}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	pendingBefore := integratePending(t, s)

	trigger := NodeTrigger{ID: "workflow:S1", Cause: "workflow node " + branchMergePath + " failed: merge conflict"}
	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: branchMergePath, Attempt: 1, Outcome: "failed", Output: "merge conflict", Candidate: "rev-cand",
		Over: integrationBase, SendBacks: []BatchSendBack{{Seq: 1, Reason: branchMergeReason}},
	}, &trigger, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}

	requireBranchMergeReopened(t, s, 1, "rev-cand")
	if got := integrateEpicRow(t, s).Candidate; got != "rev-cand" {
		t.Errorf("candidate after the send back = %q, want rev-cand kept", got)
	}
	if state, cause, ok := integrateTrigger(t, s, trigger.ID); !ok || state != "pending" || cause != trigger.Cause {
		t.Errorf("trigger %s = %q %q stored %t, want pending %q", trigger.ID, state, cause, ok, trigger.Cause)
	}
	if got := integratePending(t, s); got != pendingBefore+1 {
		t.Errorf("pending triggers = %d, want %d: the batch's trigger only", got, pendingBefore+1)
	}
}

// TestEndBatchBranchMergeSendsBackOnTheStoredBaseWithNoCandidate checks that a passed batch with no candidate reopens
// each task it left out for a merge conflict on the stored base the batch moved to, which is the run's revision.
func TestEndBatchBranchMergeSendsBackOnTheStoredBaseWithNoCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: branchMergePath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	addIntegrateTask(t, s, 1, "done", "")
	if err := s.ClaimBatch(ctx, integrateEpic, branchMergePath, 1, "rev-passed", "refs-1",
		`{"composed":[],"leftOut":[1]}`, []int64{1}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}

	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: branchMergePath, Attempt: 1, Outcome: "passed", Output: "green", Over: "rev-passed",
		SendBacks: []BatchSendBack{{Seq: 1, Reason: branchMergeReason}},
	}, nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}

	if row := integrateEpicRow(t, s); row.StoredBase != "rev-passed" || row.Candidate != "" {
		t.Fatalf("stored base and candidate after the passed batch = %q and %q, want rev-passed and none",
			row.StoredBase, row.Candidate)
	}
	requireBranchMergeReopened(t, s, 1, "rev-passed")
}

// TestEndBatchBranchMergeSkipsTaskNotInTheBatch checks that a send back of a task the batch does not hold is no error
// and changes that task nothing: a task claimed by another batch, and a task the batch claims that is not done. A task
// the batch claims and has done is reopened in the same write.
func TestEndBatchBranchMergeSkipsTaskNotInTheBatch(t *testing.T) {
	t.Parallel()
	t.Run("claimed by another batch", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openIntegrateStore(t)
		const otherPath = "plan/S0/integrate"
		startIntegrateRuns(t, s,
			NodeRun{Path: otherPath, ID: "id-s0", Node: "integrate", Kind: "integrate", Attempt: 1},
			NodeRun{Path: branchMergePath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		)
		addIntegrateTask(t, s, 1, "done", "")
		addIntegrateTask(t, s, 2, "done", "")
		if err := s.ClaimBatch(ctx, integrateEpic, otherPath, 1, "rev-other", "refs-0",
			`{"composed":[{"task":1,"commit":"c1"}],"leftOut":[]}`, []int64{1}); err != nil {
			t.Fatalf("ClaimBatch of run %s: %v", otherPath, err)
		}
		if err := s.ClaimBatch(ctx, integrateEpic, branchMergePath, 1, "rev-1", "refs-1",
			`{"composed":[],"leftOut":[2]}`, []int64{2}); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		before := branchMergeTask(t, s, 1)
		sendBacks := []BatchSendBack{{Seq: 1, Reason: branchMergeReason}, {Seq: 2, Reason: branchMergeReason}}

		if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
			Path: branchMergePath, Attempt: 1, Outcome: "failed", Output: "merge conflict", Candidate: "rev-cand",
			Over: integrationBase, SendBacks: sendBacks,
		}, nil, integrateEndedAt()); err != nil {
			t.Fatalf("EndBatch: %v", err)
		}

		if got := branchMergeTask(t, s, 1); !reflect.DeepEqual(got, before) {
			t.Errorf("task 1 after the send back = %+v, want %+v unchanged", got, before)
		}
		requireBranchMergeReopened(t, s, 2, "rev-cand")
	})
	t.Run("claimed by the batch but not done", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openIntegrateStore(t)
		startIntegrateRuns(t, s,
			NodeRun{Path: branchMergePath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
		addIntegrateTask(t, s, 1, "open", branchMergePath)
		addIntegrateTask(t, s, 2, "done", "")
		if err := s.ClaimBatch(ctx, integrateEpic, branchMergePath, 1, "rev-1", "refs-1",
			`{"composed":[],"leftOut":[2]}`, []int64{2}); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		before := branchMergeTask(t, s, 1)
		sendBacks := []BatchSendBack{{Seq: 1, Reason: branchMergeReason}, {Seq: 2, Reason: branchMergeReason}}

		if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
			Path: branchMergePath, Attempt: 1, Outcome: "failed", Output: "merge conflict", Candidate: "rev-cand",
			Over: integrationBase, SendBacks: sendBacks,
		}, nil, integrateEndedAt()); err != nil {
			t.Fatalf("EndBatch: %v", err)
		}

		if got := branchMergeTask(t, s, 1); !reflect.DeepEqual(got, before) {
			t.Errorf("task 1 after the send back = %+v, want %+v unchanged", got, before)
		}
		requireBranchMergeReopened(t, s, 2, "rev-cand")
	})
}

// TestEndBatchBranchMergeRepeatSendsNothingBack checks that a second EndBatch for the same attempt changes nothing: the
// task stays as the first end reopened it, the candidate stays as the first end set it, and the second trigger is not
// added, though the second end names the task and carries another candidate.
func TestEndBatchBranchMergeRepeatSendsNothingBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: branchMergePath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	addIntegrateTask(t, s, 1, "done", "")
	if err := s.ClaimBatch(ctx, integrateEpic, branchMergePath, 1, "rev-1", "refs-1",
		`{"composed":[],"leftOut":[1]}`, []int64{1}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	first := NodeTrigger{ID: "workflow:S1", Cause: "workflow node " + branchMergePath + " failed: merge conflict"}
	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: branchMergePath, Attempt: 1, Outcome: "failed", Output: "merge conflict", Candidate: "rev-cand",
		Over: integrationBase, SendBacks: []BatchSendBack{{Seq: 1, Reason: branchMergeReason}},
	}, &first, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}
	requireBranchMergeReopened(t, s, 1, "rev-cand")
	pendingBefore := integratePending(t, s)

	again := NodeTrigger{ID: "workflow:S1-again", Cause: "a second cause"}
	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{
		Path: branchMergePath, Attempt: 1, Outcome: "failed", Output: "again", Candidate: "rev-other",
		Over: integrationBase, SendBacks: []BatchSendBack{{Seq: 1, Reason: branchMergeReason}},
	}, &again, integrateEndedAt()); err != nil {
		t.Fatalf("repeat EndBatch: %v", err)
	}

	requireBranchMergeReopened(t, s, 1, "rev-cand")
	if got := integrateEpicRow(t, s).Candidate; got != "rev-cand" {
		t.Errorf("candidate after the repeat = %q, want rev-cand kept", got)
	}
	if _, _, ok := integrateTrigger(t, s, again.ID); ok {
		t.Errorf("trigger %s stored by the repeat EndBatch, want none", again.ID)
	}
	if got := integratePending(t, s); got != pendingBefore {
		t.Errorf("pending triggers after the repeat = %d, want %d", got, pendingBefore)
	}
}
