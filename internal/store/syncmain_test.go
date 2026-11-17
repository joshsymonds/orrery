package store

import (
	"context"
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
