package store

import (
	"context"
	"testing"
	"time"
)

// sendBackBatchWalk is the integrate run of the late walk of task 1 at its first reopen generation, the batch that
// integrates the tasks a send_back left unintegrated.
const sendBackBatchWalk = "plan/L1g1/integrate"

// TestSendBackBatchClaimTakesTasksAFailedBatchClaimed checks that ClaimBatch claims a named done task that a failed
// batch claimed, as the batch that composes a task the failed batch stranded does, so that claim does not block it. A
// named task claimed by a batch that passed or that is still running keeps its claim.
func TestSendBackBatchClaimTakesTasksAFailedBatchClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const (
		failed  = "plan/S3/integrate"
		passed  = "plan/S1/integrate"
		running = "plan/S5/integrate"
	)
	startIntegrateRuns(t, s,
		NodeRun{Path: failed, ID: "id-s3", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: passed, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: running, ID: "id-s5", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: sendBackBatchWalk, ID: "id-l1g1", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	if err := s.EndNodes(ctx, integrateEpic, []NodeEnd{
		{Path: failed, Outcome: "failed"}, {Path: passed, Outcome: "passed"},
	}, integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	addIntegrateTask(t, s, 1, "done", "")
	addIntegrateTask(t, s, 2, "done", failed)
	addIntegrateTask(t, s, 3, "done", passed)
	addIntegrateTask(t, s, 4, "done", running)

	if err := s.ClaimBatch(ctx, integrateEpic, sendBackBatchWalk, 1, "rev-1", "refs-1", `{"composed":[]}`,
		[]int64{1, 2, 3, 4}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	requireIntegrateBatches(t, s, map[int64]string{
		1: sendBackBatchWalk, 2: sendBackBatchWalk, 3: passed, 4: running,
	})
}

// TestSendBackBatchRetryRerunsTheReopenWalksFailedIntegrate checks that the integrate run of a reopen generation's late
// walk, under L<seq>g<generation>, counts as the newest walk's run: it started after the failed settle walk at S3, and
// RetryNode of its node reruns it, not the settle walk's run.
func TestSendBackBatchRetryRerunsTheReopenWalksFailedIntegrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	failIntegrateRun(t, s, "plan/S3/integrate", "id-s3", "rev-earlier", "earlier red")
	failIntegrateRun(t, s, sendBackBatchWalk, "id-l1g1", "rev-1", "lint failed")
	parkIntegrateEpic(t, s)
	if err := s.RetryNode(
		ctx, integrateEpic, "integrate", Maker{By: MakerOwner}, time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("RetryNode: %v", err)
	}
	if run := integrateRun(t, s, sendBackBatchWalk); run.State != NodeRunning || run.Attempt != 2 {
		t.Errorf("reopen walk's run state %q attempt %d, want running at attempt 2", run.State, run.Attempt)
	}
	if run := integrateRun(t, s, "plan/S3/integrate"); run.State != NodeEnded || run.Attempt != 1 {
		t.Errorf("settle run state %q attempt %d, want it left ended at attempt 1", run.State, run.Attempt)
	}
}
