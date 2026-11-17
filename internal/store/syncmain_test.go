package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The sync run the sync write tests store, the merge its prepare made and origin's commit that merge holds.
const (
	syncRunPath = "plan/M1"
	syncMerge   = "fedcba9876543210fedcba9876543210fedcba98"
	syncOrigin  = "00112233445566778899aabbccddeeff00112233"
	syncResult  = `{"origin":"` + syncOrigin + `","base":"` + integrationBase + `","merge":"` + syncMerge + `"}`
)

// startSyncRun stores the sync run of integrateEpic at attempt 0, running.
func startSyncRun(t *testing.T, s *Store) {
	t.Helper()
	startIntegrateRuns(t, s, NodeRun{Path: syncRunPath, ID: "sync-run", Node: "sync", Kind: "sync"})
}

// TestPrepareSyncRecordsResultAndMergeOnce: the prepare stores the result and the merge as the revision in one write.
// A repeat, a prepare at another attempt and one of a run that is not running change nothing.
func TestPrepareSyncRecordsResultAndMergeOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 1, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync at another attempt: %v", err)
	}
	if run := integrateRun(t, s, syncRunPath); run.Result != "" || run.Revision != "" {
		t.Fatalf("run = %+v after a prepare at another attempt, want it unprepared", run)
	}
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, "", `{"base":"other"}`); err != nil {
		t.Fatalf("PrepareSync repeated: %v", err)
	}
	run := integrateRun(t, s, syncRunPath)
	if run.Result != syncResult || run.Revision != syncMerge {
		t.Errorf("run = %+v, want the first prepare's result and merge", run)
	}
}

// TestEndSyncPassedMovesTheStoredBaseOnce: a passed sync ends its run, moves the stored base to its merge, clears the
// candidate, records origin's commit as synced and tells nothing. A repeat changes nothing.
func TestEndSyncPassedMovesTheStoredBaseOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	end := SyncEnd{Path: syncRunPath, Outcome: "passed", Output: "merged", Synced: syncOrigin}
	if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync: %v", err)
	}
	run := integrateRun(t, s, syncRunPath)
	if run.State != NodeEnded || run.Outcome != "passed" || run.Output != "merged" ||
		!run.Ended.Equal(integrateEndedAt()) {
		t.Errorf("run = %+v, want ended passed with its output at the end time", run)
	}
	epic := integrateEpicRow(t, s)
	if epic.StoredBase != syncMerge || epic.Synced != syncOrigin || epic.Candidate != "" ||
		epic.Base != integrationBase {
		t.Errorf("epic base %q, stored base %q, synced %q, candidate %q, want the base kept, the merge, %q and none",
			epic.Base, epic.StoredBase, epic.Synced, epic.Candidate, syncOrigin)
	}
	if tells := integrateTells(t, s); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
	again := SyncEnd{Path: syncRunPath, Outcome: "failed", Output: "again", Tell: "told twice"}
	if err := s.EndSync(ctx, integrateEpic, again, integrateParkedAt()); err != nil {
		t.Fatalf("EndSync repeated: %v", err)
	}
	if got := integrateRun(t, s, syncRunPath); !reflect.DeepEqual(got, run) {
		t.Errorf("run after a repeat = %+v, want %+v", got, run)
	}
	if tells := integrateTells(t, s); len(tells) != 0 {
		t.Errorf("tells after a repeat = %q, want none", tells)
	}
}

// TestEndSyncFailedTellsAndMovesNothing: a failed sync ends its run, adds its tell, cut to TurnReasonMax bytes, and moves
// neither the stored base nor the synced commit. An end at another attempt changes nothing.
func TestEndSyncFailedTellsAndMovesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	tell := "workflow node plan/M1 failed: " + strings.Repeat("x", TurnReasonMax)
	end := SyncEnd{Path: syncRunPath, Attempt: 1, Outcome: "failed", Output: "conflicts", Tell: tell}
	if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync at another attempt: %v", err)
	}
	if run := integrateRun(t, s, syncRunPath); run.State != NodeRunning {
		t.Fatalf("run = %+v after an end at another attempt, want running", run)
	}
	end.Attempt = 0
	if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync: %v", err)
	}
	if run := integrateRun(t, s, syncRunPath); run.State != NodeEnded || run.Outcome != "failed" {
		t.Errorf("run = %+v, want ended failed", run)
	}
	epic := integrateEpicRow(t, s)
	if epic.StoredBase != integrationBase || epic.Synced != "" || epic.Parked != "" {
		t.Errorf("epic stored base %q, synced %q, parked %q, want unmoved and not parked", epic.StoredBase,
			epic.Synced, epic.Parked)
	}
	if tells := integrateTells(t, s); len(tells) != 1 || tells[0] != tell[:TurnReasonMax] {
		t.Errorf("tells = %q, want the tell cut to %d bytes", tells, TurnReasonMax)
	}
}

// TestEndSyncPassedWithNoMergeIsAnError: a sync that passes must have its merge as its revision, so one with none is an
// error and the write changes nothing.
func TestEndSyncPassedWithNoMergeIsAnError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	end := SyncEnd{Path: syncRunPath, Outcome: "passed", Synced: syncOrigin}
	if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err == nil {
		t.Fatal("EndSync of a passed sync with no merge succeeded, want an error")
	}
	if run := integrateRun(t, s, syncRunPath); run.State != NodeRunning {
		t.Errorf("run = %+v, want it still running", run)
	}
}

// The second sync run the planner action tests store, and the refusal of a retry of the node sync, which names no
// parked run and no integrate run.
const (
	syncSecondPath = "plan/M2"
	syncNotParked  = "node sync of epic " + integrateEpic + " is not parked"
)

// endSyncRuns stores two sync runs of integrateEpic and ends them as syncs end: the first failed at its merge, telling
// why, and the second skipped. It returns both runs as stored.
func endSyncRuns(t *testing.T, s *Store) []NodeRun {
	t.Helper()
	ctx := context.Background()
	startSyncRun(t, s)
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	failed := SyncEnd{
		Path: syncRunPath, Outcome: "failed", Output: "Done gate lint failed", Tell: "workflow node plan/M1 failed",
	}
	if err := s.EndSync(ctx, integrateEpic, failed, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync failed: %v", err)
	}
	startIntegrateRuns(t, s, NodeRun{Path: syncSecondPath, ID: "sync-run-2", Node: "sync", Kind: "sync"})
	skipped := SyncEnd{Path: syncSecondPath, Outcome: "skipped", Output: "origin's commit was tried by plan/M1"}
	if err := s.EndSync(ctx, integrateEpic, skipped, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync skipped: %v", err)
	}
	return []NodeRun{integrateRun(t, s, syncRunPath), integrateRun(t, s, syncSecondPath)}
}

// requireSyncRunsKept fails unless the sync runs of integrateEpic are still runs, in every field.
func requireSyncRunsKept(t *testing.T, s *Store, runs []NodeRun) {
	t.Helper()
	for _, run := range runs {
		if got := integrateRun(t, s, run.Path); !reflect.DeepEqual(got, run) {
			t.Errorf("sync run %s = %+v, want it kept as %+v", run.Path, got, run)
		}
	}
}

// TestSyncRunIsNoParkAndNoTargetOfTheOwnersOrThePlannersRetry: a sync that ended failed or skipped parks nothing and
// wakes no planner, since its end writes no trigger. The owner's RetryNode of the node sync, and the planner's
// retry_node action that names it, are refused, since a sync run is never parked and is no integrate run: the runs are
// kept as they ended, the epic is not parked, and the refused action is recorded as any refusal is.
func TestSyncRunIsNoParkAndNoTargetOfTheOwnersOrThePlannersRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	pending := epicFlowOf(t, s).Pending
	runs := endSyncRuns(t, s)
	epic := integrateEpicRow(t, s)
	if epic.Parked != "" || epic.ParkClass != "" || len(epic.Questions) != 0 {
		t.Fatalf("epic parked %q as %q with questions %+v, want no park after the syncs ended", epic.Parked,
			epic.ParkClass, epic.Questions)
	}
	if got := epicFlowOf(t, s).Pending; got != pending {
		t.Fatalf("pending triggers = %d after the syncs ended, want the %d before them", got, pending)
	}

	var refusal RefusalError
	if err := s.RetryNode(ctx, integrateEpic, "sync"); !errors.As(err, &refusal) || string(refusal) != syncNotParked {
		t.Errorf("RetryNode of the node sync = %v, want the refusal %q", err, syncNotParked)
	}
	requireSyncRunsKept(t, s, runs)

	action := storeActions(t, s, integrateEpic, EpicActionInput{
		Kind: ActionRetryNode, Node: "sync", Reason: "the sync's gate failed",
	})[0]
	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || len(stop) != 0 {
		t.Fatalf("CarryOutAction = %v, %v, want a refusal with no step to stop", stop, err)
	}
	requireRefusedAction(t, s, action, "retry node sync", syncNotParked)
	requireSyncRunsKept(t, s, runs)
	if epic = integrateEpicRow(t, s); epic.Parked != "" || epic.ParkClass != "" {
		t.Errorf("epic parked %q as %q after the refused action, want no park", epic.Parked, epic.ParkClass)
	}
}

// TestRetryNodeOfAnIntegrateNodeNamedSyncRerunsOnlyTheIntegrateRun: a workflow may name an integrate node sync, as the
// interpreter names its sync runs' node. A retry_node of it reruns the node's latest failed integrate run, since the
// integrate runs a retry reruns are read by kind, and leaves the sync runs that ended after it as they ended.
func TestRetryNodeOfAnIntegrateNodeNamedSyncRerunsOnlyTheIntegrateRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const batch = "plan/S1/sync"
	startIntegrateRuns(t, s, NodeRun{Path: batch, ID: "id-s1", Node: "sync", Kind: "integrate", Attempt: 1})
	if err := s.PrepareNode(ctx, integrateEpic, batch, 1, "rev-1", "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.RecordNodeResult(ctx, integrateEpic, batch, 1, retryComposed); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	if err := s.EndBatch(ctx, integrateEpic, batch, 1, "failed", "lint failed", nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}
	runs := endSyncRuns(t, s)
	action := storeActions(t, s, integrateEpic, EpicActionInput{
		Kind: ActionRetryNode, Node: "sync", Reason: "the gate was flaky",
	})[0]

	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	requireIntegrateRun(t, s, batch, NodeRun{
		Path: batch, ID: "id-s1", Node: "sync", Kind: "integrate", Attempt: 2, Revision: "rev-1", Refs: "refs-1",
		Result: retryComposed, State: NodeRunning, Started: integrateStartedAt(),
	})
	requireSyncRunsKept(t, s, runs)
}

// TestCloseEndsARunningSyncAndItsLaterWritesChangeNothing: a close ends a running sync run with the epic's other
// running runs. The prepare and the end that the interpreter's pass makes after the close, from rows it read before it,
// change nothing: no result is stored, the stored base and synced commit stay, and nothing is told.
func TestCloseEndsARunningSyncAndItsLaterWritesChangeNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	if _, err := s.CloseEpic(ctx, integrateEpic, "abandoned", "", CloserOwner, closeAt()); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	closed := integrateRun(t, s, syncRunPath)
	if closed.State != NodeEnded || closed.Outcome != "" {
		t.Fatalf("sync run = %+v after the close, want ended with no outcome", closed)
	}

	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult); err != nil {
		t.Fatalf("PrepareSync after the close: %v", err)
	}
	for _, end := range []SyncEnd{
		{Path: syncRunPath, Outcome: "passed", Output: "merged", Synced: syncOrigin},
		{Path: syncRunPath, Outcome: "failed", Output: "conflicts", Tell: "workflow node plan/M1 failed"},
	} {
		if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err != nil {
			t.Fatalf("EndSync %s after the close: %v", end.Outcome, err)
		}
	}
	if got := integrateRun(t, s, syncRunPath); !reflect.DeepEqual(got, closed) {
		t.Errorf("sync run = %+v after the writes, want it kept as the close ended it, %+v", got, closed)
	}
	epic := integrateEpicRow(t, s)
	if epic.StoredBase != integrationBase || epic.Synced != "" || epic.Phase != PhaseClosedAbandoned {
		t.Errorf("epic stored base %q, synced %q, phase %q, want %q, none and closed", epic.StoredBase, epic.Synced,
			epic.Phase, integrationBase)
	}
	if tells := integrateTells(t, s); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
}

// TestEpicFlowCountsPendingPlannerActions: the flow read counts the epic's planner actions that are pending, so an
// action carried out, taken or refused, no longer counts, and another epic's actions never do.
func TestEpicFlowCountsPendingPlannerActions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	createEpic(t, s, fullEpic(actionEpic))
	storeActions(t, s, actionEpic, EpicActionInput{Kind: ActionRetryEpic, Reason: "another epic's action"})
	actions := storeActions(t, s, integrateEpic,
		EpicActionInput{Kind: ActionRetryEpic, Reason: "end the park"},
		EpicActionInput{Kind: ActionFailTask, Task: 9, Reason: "the task cannot build"},
	)
	if got := epicFlowOf(t, s).Actions; got != 2 {
		t.Fatalf("pending actions = %d, want 2", got)
	}
	if _, err := s.CarryOutAction(ctx, actions[0], closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if got := epicFlowOf(t, s).Actions; got != 1 {
		t.Errorf("pending actions = %d after one was carried out, want 1", got)
	}
}

// epicFlowOf returns the flow read of integrateEpic.
func epicFlowOf(t *testing.T, s *Store) EpicFlow {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	return flow
}
