package store

import (
	"context"
	"testing"
)

// The paths of the integrate runs the retry tests start: two settle walks of one plan, the second the newer.
const (
	retryEarlierBatch = "plan/S1/integrate"
	retryLatestBatch  = "plan/S2/integrate"
)

// retryComposed is the Result a failed integrate run claims: one task composed on the candidate.
const retryComposed = `{"base":"base-1","candidate":"rev-1","composed":[{"task":1,"commit":"c1"}],"leftOut":[]}`

// retryNotParked is the refusal of a RetryNode that has no integrate run of its node to rerun.
const retryNotParked = "node integrate of epic " + integrateEpic + " is not parked"

// retryParkReason is the park reason the retry tests park integrateEpic with.
const retryParkReason = "epic not built: R1 is not green"

// failIntegrateRun starts the integrate run at path as attempt 1, claims it at revision with retryComposed, checks its
// refs and ends it failed with output, as an integrate run whose Done gate failed does. The epic's candidate becomes
// revision.
func failIntegrateRun(t *testing.T, s *Store, path, id, revision, output string) {
	t.Helper()
	ctx := context.Background()
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: id, Node: "integrate", Kind: "integrate", Attempt: 1})
	if err := s.PrepareNode(ctx, integrateEpic, path, 1, revision, "refs-1"); err != nil {
		t.Fatalf("PrepareNode %s: %v", path, err)
	}
	if err := s.RecordNodeResult(ctx, integrateEpic, path, 1, retryComposed); err != nil {
		t.Fatalf("RecordNodeResult %s: %v", path, err)
	}
	if err := s.CheckNodeRefs(ctx, integrateEpic, path, 1, "", nil, integrateEndedAt()); err != nil {
		t.Fatalf("CheckNodeRefs %s: %v", path, err)
	}
	trigger := NodeTrigger{ID: "workflow:failed:" + id, Cause: "workflow node " + path + " failed: " + output}
	if err := s.EndBatch(ctx, integrateEpic, path, 1, "failed", output, &trigger, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch %s: %v", path, err)
	}
}

// parkIntegrateEpic parks integrateEpic with retryParkReason, as a planner trigger's wait does, so a retry has a park to
// end.
func parkIntegrateEpic(t *testing.T, s *Store) {
	t.Helper()
	trigger := NodeTrigger{ID: "workflow:park:" + retryParkReason, Cause: retryParkReason}
	if err := s.ParkEpicWaking(context.Background(), integrateEpic, retryParkReason, trigger,
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkEpicWaking: %v", err)
	}
}

// requireParkOfIntegrateEpic fails unless integrateEpic is parked with reason.
func requireParkOfIntegrateEpic(t *testing.T, s *Store, reason string) {
	t.Helper()
	if parked := integrateEpicRow(t, s).Parked; parked != reason {
		t.Errorf("parked of epic %s = %q, want %q", integrateEpic, parked, reason)
	}
}

// TestRetryFailedIntegrateRerunsLatestFailedRunKeepingRevisionAndResult checks that RetryNode of an integrate run that
// ended failed, in the newest settle walk, starts it at its next attempt, clearing its outcome, output, refs checked and
// end and keeping its revision, refs and result. The epic's candidate stays the revision and the epic is unparked. The
// earlier walk's run is left as it was, and a second RetryNode of the rerun, now running, is refused.
func TestRetryFailedIntegrateRerunsLatestFailedRunKeepingRevisionAndResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	failIntegrateRun(t, s, retryEarlierBatch, "id-s1", "rev-earlier", "earlier red")
	failIntegrateRun(t, s, retryLatestBatch, "id-s2", "rev-1", "lint failed")
	parkIntegrateEpic(t, s)

	if err := s.RetryNode(ctx, integrateEpic, "integrate"); err != nil {
		t.Fatalf("RetryNode of a failed integrate run: %v", err)
	}
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{
		Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 2,
		Revision: "rev-1", Refs: "refs-1", Result: retryComposed,
		State: NodeRunning, Started: integrateStartedAt(),
	})
	requireIntegrateRun(t, s, retryEarlierBatch, NodeRun{
		Path: retryEarlierBatch, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1,
		Revision: "rev-earlier", Refs: "refs-1", Result: retryComposed, RefsChecked: true,
		State: NodeEnded, Outcome: "failed", Output: "earlier red",
		Started: integrateStartedAt(), Ended: integrateEndedAt(),
	})
	row := integrateEpicRow(t, s)
	if row.Parked != "" {
		t.Errorf("parked of epic %s after RetryNode = %q, want empty", integrateEpic, row.Parked)
	}
	if row.Candidate != "rev-1" {
		t.Errorf("candidate of epic %s after RetryNode = %q, want the revision %q",
			integrateEpic, row.Candidate, "rev-1")
	}
	if row.Phase != "accepted" {
		t.Errorf("phase of epic %s after RetryNode = %q, want accepted", integrateEpic, row.Phase)
	}

	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "integrate"), retryNotParked)
}

// TestRetryFailedIntegrateRefusesRunningRun checks that RetryNode of an integrate run that is running is a
// RefusalError that changes nothing: the run and the epic's park stay as they were.
func TestRetryFailedIntegrateRefusesRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate",
		Attempt: 1})
	parkIntegrateEpic(t, s)

	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "integrate"), retryNotParked)
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate",
		Kind: "integrate", Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
	requireParkOfIntegrateEpic(t, s, retryParkReason)
}

// TestRetryFailedIntegrateRefusesPassedRun checks that RetryNode of an integrate run that passed is a RefusalError that
// changes nothing: the run and the epic's park stay as they were.
func TestRetryFailedIntegrateRefusesPassedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate",
		Attempt: 1})
	if err := s.EndBatch(ctx, integrateEpic, retryLatestBatch, 1, "passed", "", nil,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch passed: %v", err)
	}
	parkIntegrateEpic(t, s)

	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "integrate"), retryNotParked)
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate",
		Kind: "integrate", Attempt: 1, Outcome: "passed", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateEndedAt()})
	requireParkOfIntegrateEpic(t, s, retryParkReason)
}

// TestRetryFailedIntegrateRefusesSupersededRun checks that RetryNode of an earlier failed integrate run, which a later
// run of the node superseded, is a RefusalError that changes nothing: neither run and the epic's park move.
func TestRetryFailedIntegrateRefusesSupersededRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	failIntegrateRun(t, s, retryEarlierBatch, "id-s1", "rev-earlier", "earlier red")
	startIntegrateRuns(t, s, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate",
		Attempt: 1})
	parkIntegrateEpic(t, s)

	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "integrate"), retryNotParked)
	requireIntegrateRun(t, s, retryEarlierBatch, NodeRun{
		Path: retryEarlierBatch, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1,
		Revision: "rev-earlier", Refs: "refs-1", Result: retryComposed, RefsChecked: true,
		State: NodeEnded, Outcome: "failed", Output: "earlier red",
		Started: integrateStartedAt(), Ended: integrateEndedAt(),
	})
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate",
		Kind: "integrate", Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
	requireParkOfIntegrateEpic(t, s, retryParkReason)
}

// TestRetryFailedIntegrateLeavesParkedRunsRerunnable checks that RetryNode of an integrate node with a parked run reruns
// that parked run as it did before a failed run could be rerun, even when a later settle walk's run of the node is
// running. The later run stays running.
func TestRetryFailedIntegrateLeavesParkedRunsRerunnable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: retryEarlierBatch, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	const reason = "workflow node plan/S1/integrate: the epic's base moved"
	if err := s.ParkNode(ctx, integrateEpic, retryEarlierBatch, 1, "failed", "moved", reason,
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}

	if err := s.RetryNode(ctx, integrateEpic, "integrate"); err != nil {
		t.Fatalf("RetryNode of a parked integrate run: %v", err)
	}
	requireIntegrateRun(t, s, retryEarlierBatch, NodeRun{
		Path: retryEarlierBatch, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 2,
		State: NodeRunning, Started: integrateStartedAt(),
	})
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate",
		Kind: "integrate", Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
	requireParkOfIntegrateEpic(t, s, "")
}

// TestRetryFailedIntegrateRefusedForEpicEndedAtBase checks that RetryNode of a failed integrate run in an epic the owner
// ended as met at base is refused with the park reason, and changes nothing: the run stays failed and the epic stays
// parked. R4: the phase is set by the owner's end answer, which the park question of a red run no longer does itself.
func TestRetryFailedIntegrateRefusedForEpicEndedAtBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	failIntegrateRun(t, s, retryLatestBatch, "id-s2", "rev-1", "lint failed")
	startIntegrateRuns(t, s, NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1})

	const reason = "workflow node red: R1 met at the base; you can retry, drop or end"
	if err := s.ParkNodeMetAtBase(ctx, integrateEpic, "red", 1, "failed", "R1 met", reason, []string{"R1"},
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, integrateEpic, "D3", "", 1, ChoiceEnd, integrateParkedAt()); err != nil {
		t.Fatalf("AnswerQuestion end: %v", err)
	}

	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "integrate"),
		"epic "+integrateEpic+" is parked for good: "+reason)
	requireIntegrateRun(t, s, retryLatestBatch, NodeRun{
		Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1,
		Revision: "rev-1", Refs: "refs-1", Result: retryComposed, RefsChecked: true,
		State: NodeEnded, Outcome: "failed", Output: "lint failed",
		Started: integrateStartedAt(), Ended: integrateEndedAt(),
	})
	if row := integrateEpicRow(t, s); row.Parked != reason || row.Phase != phaseMetAtBase {
		t.Errorf("parked and phase after the refused retry = %q and %q, want %q and %q",
			row.Parked, row.Phase, reason, phaseMetAtBase)
	}
}
