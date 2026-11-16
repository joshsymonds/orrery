package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// integrationBase is the base commit the integration tests create their epics at. It is a synthetic sha.
const integrationBase = "0123456789abcdef0123456789abcdef01234567"

// integrateEpic is the epic the integration write tests create at integrationBase.
const integrateEpic = "orrery-6e7f8a9/epic-writes"

// integrateStartedAt is the second the integration write tests start runs at.
func integrateStartedAt() time.Time {
	return time.Unix(1700002000, 0)
}

// integrateEndedAt is the second the integration write tests end runs at.
func integrateEndedAt() time.Time {
	return time.Unix(1700002100, 0)
}

// integrateParkedAt is the second the integration write tests park at.
func integrateParkedAt() time.Time {
	return time.Unix(1700002200, 0)
}

// openIntegrateStore opens a migrated store with integrateEpic created at integrationBase.
func openIntegrateStore(t *testing.T) *Store {
	t.Helper()
	s := openEpicStore(t)
	row := fullEpic(integrateEpic)
	row.Base = integrationBase
	createEpic(t, s, row)
	return s
}

// startIntegrateRuns stores runs as running node runs of integrateEpic.
func startIntegrateRuns(t *testing.T, s *Store, runs ...NodeRun) {
	t.Helper()
	if err := s.StartNodes(context.Background(), integrateEpic, runs, nil, integrateStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", integrateEpic, err)
	}
}

// integrateRun returns the node run at path of integrateEpic.
func integrateRun(t *testing.T, s *Store, path string) NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", integrateEpic, err)
	}
	for _, run := range runs {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("epic %s has no node run at %s", integrateEpic, path)
	return NodeRun{}
}

// requireIntegrateRun fails unless the node run at path of integrateEpic is want, in every field.
func requireIntegrateRun(t *testing.T, s *Store, path string, want NodeRun) {
	t.Helper()
	if got := integrateRun(t, s, path); !reflect.DeepEqual(got, want) {
		t.Errorf("node run %s of epic %s = %+v, want %+v", path, integrateEpic, got, want)
	}
}

// integrateEpicRow reads integrateEpic as the store reads it.
func integrateEpicRow(t *testing.T, s *Store) EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", integrateEpic, err)
	}
	return row
}

// addIntegrateTask stores task seq of integrateEpic with status and batch. The planner stores a task open, and a build
// or an integrate run moves it on, so this stores the state the write under test reads.
func addIntegrateTask(t *testing.T, s *Store, seq int64, status, batch string) {
	t.Helper()
	ctx := context.Background()
	if err := sqlcgen.New(s.db).InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
		Epic: integrateEpic, Seq: seq, Turn: 1, Title: "Synthetic task", Deliverable: "Synthetic deliverable",
		Gate: "check", Evidence: "",
	}); err != nil {
		t.Fatalf("insert task %d of epic %s: %v", seq, integrateEpic, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE epic_tasks SET status = ?, batch = ? WHERE epic = ? AND seq = ?`,
		status, batch, integrateEpic, seq); err != nil {
		t.Fatalf("set task %d of epic %s: %v", seq, integrateEpic, err)
	}
}

// requireIntegrateBatches fails unless the batch of each task of integrateEpic is want, keyed by seq.
func requireIntegrateBatches(t *testing.T, s *Store, want map[int64]string) {
	t.Helper()
	got := map[int64]string{}
	for _, task := range integrateEpicRow(t, s).Tasks {
		got[task.Seq] = task.Batch
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("task batches of epic %s = %v, want %v", integrateEpic, got, want)
	}
}

// integrateTells returns the text of every tell of integrateEpic in seq order.
func integrateTells(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq`, integrateEpic)
	if err != nil {
		t.Fatalf("query tells of epic %s: %v", integrateEpic, err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan tell of epic %s: %v", integrateEpic, err)
		}
		texts = append(texts, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read tells of epic %s: %v", integrateEpic, err)
	}
	return texts
}

// integrateTrigger returns the state and cause of trigger id of integrateEpic, and whether a trigger with the id is
// stored in any state.
func integrateTrigger(t *testing.T, s *Store, id string) (state, cause string, stored bool) {
	t.Helper()
	err := s.db.QueryRowContext(context.Background(),
		`SELECT state, cause FROM epic_triggers WHERE epic = ? AND id = ?`, integrateEpic, id).Scan(&state, &cause)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("read trigger %s of epic %s: %v", id, integrateEpic, err)
	}
	return state, cause, true
}

// integratePending returns how many triggers of integrateEpic are pending.
func integratePending(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM epic_triggers WHERE epic = ? AND state = 'pending'`, integrateEpic,
	).Scan(&count); err != nil {
		t.Fatalf("count pending triggers of epic %s: %v", integrateEpic, err)
	}
	return count
}

// claimIntegrateTrigger stores trigger id of integrateEpic as claimed by turn, as a planner turn claims it.
func claimIntegrateTrigger(t *testing.T, s *Store, id string, turn int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE epic_triggers SET state = 'claimed', turn = ? WHERE epic = ? AND id = ?`, turn, integrateEpic, id,
	); err != nil {
		t.Fatalf("claim trigger %s of epic %s: %v", id, integrateEpic, err)
	}
}

// TestClaimBatchTakesRunWorkAndClaimsDoneTasksOnce checks that ClaimBatch stores the revision, refs and result of the
// running run with no revision and claims each named task that is done and unclaimed or claimed by the run's path. An
// open task, a task claimed by another batch and a task not named stay as they were, and a repeat with other work and a
// task it now names changes nothing.
func TestClaimBatchTakesRunWorkAndClaimsDoneTasksOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	addIntegrateTask(t, s, 1, "done", "")
	addIntegrateTask(t, s, 2, "done", "plan/S0/integrate")
	addIntegrateTask(t, s, 3, "open", "")
	addIntegrateTask(t, s, 4, "done", "")

	if err := s.ClaimBatch(ctx, integrateEpic, path, 1, "rev-1", "refs-1", `{"composed":[]}`,
		[]int64{1, 2, 3}); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	want := NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1,
		Revision: "rev-1", Refs: "refs-1", Result: `{"composed":[]}`,
		State: NodeRunning, Started: integrateStartedAt()}
	requireIntegrateRun(t, s, path, want)
	requireIntegrateBatches(t, s, map[int64]string{1: path, 2: "plan/S0/integrate", 3: "", 4: ""})

	if err := s.ClaimBatch(ctx, integrateEpic, path, 1, "rev-2", "refs-2", "other", []int64{1, 4}); err != nil {
		t.Fatalf("repeat ClaimBatch: %v", err)
	}
	requireIntegrateRun(t, s, path, want)
	requireIntegrateBatches(t, s, map[int64]string{1: path, 2: "plan/S0/integrate", 3: "", 4: ""})
}

// TestClaimBatchChangesNothingAtAnotherAttemptOrEndedRun checks that ClaimBatch at an attempt the run is not at, and
// after the run has ended, stores no revision and claims no task.
func TestClaimBatchChangesNothingAtAnotherAttemptOrEndedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	addIntegrateTask(t, s, 1, "done", "")

	if err := s.ClaimBatch(ctx, integrateEpic, path, 2, "rev-1", "refs-1", "result", []int64{1}); err != nil {
		t.Fatalf("ClaimBatch at another attempt: %v", err)
	}
	requireIntegrateBatches(t, s, map[int64]string{1: ""})
	if got := integrateRun(t, s, path); got.Revision != "" || got.Refs != "" || got.Result != "" {
		t.Fatalf("run after ClaimBatch at another attempt = %+v, want no revision, refs or result", got)
	}

	ended := []NodeEnd{{Path: path, Outcome: "passed"}}
	if err := s.EndNodes(ctx, integrateEpic, ended, integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if err := s.ClaimBatch(ctx, integrateEpic, path, 1, "rev-2", "refs-2", "result", []int64{1}); err != nil {
		t.Fatalf("ClaimBatch of an ended run: %v", err)
	}
	requireIntegrateBatches(t, s, map[int64]string{1: ""})
	if got := integrateRun(t, s, path); got.Revision != "" || got.Refs != "" || got.Result != "" {
		t.Errorf("run after ClaimBatch of an ended run = %+v, want no revision, refs or result", got)
	}
}

// TestEndBatchPassedMovesStoredBaseAndClearsCandidate checks that a failed batch sets the candidate to its revision
// and a passed batch moves the stored base to its revision and clears the candidate, each adding its trigger pending.
// A repeat of the passed batch, past its claimed trigger, changes nothing.
func TestEndBatchPassedMovesStoredBaseAndClearsCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const failedPath, passedPath = "plan/S1/integrate", "plan/S2/integrate"
	startIntegrateRuns(t, s,
		NodeRun{Path: failedPath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: passedPath, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	pendingBefore := integratePending(t, s)

	if err := s.PrepareNode(ctx, integrateEpic, failedPath, 1, "rev-1", "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	red := NodeTrigger{ID: "workflow:S1", Cause: "workflow node " + failedPath + " failed: red"}
	if err := s.EndBatch(ctx, integrateEpic, failedPath, 1, "failed", "red", &red, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch failed: %v", err)
	}
	if row := integrateEpicRow(t, s); row.StoredBase != integrationBase || row.Candidate != "rev-1" {
		t.Fatalf("stored base and candidate after a failed batch = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, integrationBase, "rev-1")
	}

	if err := s.PrepareNode(ctx, integrateEpic, passedPath, 1, "rev-2", "refs-2"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	green := NodeTrigger{ID: "workflow:S2", Cause: "workflow node " + passedPath + " passed: "}
	if err := s.EndBatch(ctx, integrateEpic, passedPath, 1, "passed", "", &green, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch passed: %v", err)
	}
	if row := integrateEpicRow(t, s); row.StoredBase != "rev-2" || row.Candidate != "" {
		t.Fatalf("stored base and candidate after a passed batch = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, "rev-2", "")
	}
	if state, cause, ok := integrateTrigger(t, s, green.ID); !ok || state != "pending" || cause != green.Cause {
		t.Fatalf("trigger %s after a passed batch = %q %q stored %t, want pending %q",
			green.ID, state, cause, ok, green.Cause)
	}
	if got := integratePending(t, s); got != pendingBefore+2 {
		t.Fatalf("pending triggers after both batches = %d, want %d", got, pendingBefore+2)
	}

	claimIntegrateTrigger(t, s, green.ID, 4)
	again := NodeTrigger{ID: green.ID, Cause: "a second cause"}
	if err := s.EndBatch(
		ctx, integrateEpic, passedPath, 1, "failed", "again", &again, integrateParkedAt(),
	); err != nil {
		t.Fatalf("repeat EndBatch: %v", err)
	}
	if row := integrateEpicRow(t, s); row.StoredBase != "rev-2" || row.Candidate != "" {
		t.Errorf("stored base and candidate after a repeat = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, "rev-2", "")
	}
	if state, cause, _ := integrateTrigger(t, s, green.ID); state != "claimed" || cause != green.Cause {
		t.Errorf("trigger %s after a repeat = %q %q, want claimed %q", green.ID, state, cause, green.Cause)
	}
	if got := integratePending(t, s); got != pendingBefore+1 {
		t.Errorf("pending triggers after a repeat = %d, want %d", got, pendingBefore+1)
	}
	requireIntegrateRun(t, s, passedPath, NodeRun{Path: passedPath, ID: "id-s2", Node: "integrate",
		Kind: "integrate", Attempt: 1, Revision: "rev-2", Refs: "refs-2", Outcome: "passed",
		State: NodeEnded, Started: integrateStartedAt(), Ended: integrateEndedAt()})
}

// TestEndBatchFailedSetsCandidateUnlessRevisionIsStoredBase checks that a failed batch at a revision other than the
// stored base sets the candidate to it, and a failed batch at the stored base leaves the candidate empty. A failed
// batch with no trigger adds none.
func TestEndBatchFailedSetsCandidateUnlessRevisionIsStoredBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const atBase, moved = "plan/S1/integrate", "plan/S2/integrate"
	startIntegrateRuns(t, s,
		NodeRun{Path: atBase, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: moved, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	pendingBefore := integratePending(t, s)

	if err := s.PrepareNode(ctx, integrateEpic, atBase, 1, integrationBase, "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.EndBatch(
		ctx, integrateEpic, atBase, 1, "failed", "nothing composed", nil, integrateEndedAt(),
	); err != nil {
		t.Fatalf("EndBatch at the stored base: %v", err)
	}
	if row := integrateEpicRow(t, s); row.StoredBase != integrationBase || row.Candidate != "" {
		t.Fatalf("stored base and candidate after a failed batch at the stored base = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, integrationBase, "")
	}

	if err := s.PrepareNode(ctx, integrateEpic, moved, 1, "rev-3", "refs-2"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.EndBatch(ctx, integrateEpic, moved, 1, "failed", "red", nil, integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch at another revision: %v", err)
	}
	if row := integrateEpicRow(t, s); row.StoredBase != integrationBase || row.Candidate != "rev-3" {
		t.Errorf("stored base and candidate after a failed batch at another revision = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, integrationBase, "rev-3")
	}
	if got := integratePending(t, s); got != pendingBefore {
		t.Errorf("pending triggers after failed batches with no trigger = %d, want %d", got, pendingBefore)
	}
}

// TestEndBatchAtAnotherAttemptChangesNothing checks that EndBatch at an attempt the run is not at ends nothing, moves
// no base and adds no trigger.
func TestEndBatchAtAnotherAttemptChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	if err := s.PrepareNode(ctx, integrateEpic, path, 1, "rev-4", "refs-4"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	pendingBefore := integratePending(t, s)

	trigger := NodeTrigger{ID: "workflow:S1", Cause: "workflow node " + path + " passed: "}
	if err := s.EndBatch(ctx, integrateEpic, path, 2, "passed", "wrong attempt", &trigger,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch at another attempt: %v", err)
	}
	requireIntegrateRun(t, s, path, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate",
		Attempt: 1, Revision: "rev-4", Refs: "refs-4", State: NodeRunning, Started: integrateStartedAt()})
	if row := integrateEpicRow(t, s); row.StoredBase != integrationBase || row.Candidate != "" {
		t.Errorf("stored base and candidate after EndBatch at another attempt = %q and %q, want %q and %q",
			row.StoredBase, row.Candidate, integrationBase, "")
	}
	if _, _, ok := integrateTrigger(t, s, trigger.ID); ok {
		t.Errorf("trigger %s stored by EndBatch at another attempt, want none", trigger.ID)
	}
	if got := integratePending(t, s); got != pendingBefore {
		t.Errorf("pending triggers after EndBatch at another attempt = %d, want %d", got, pendingBefore)
	}
}

// TestEndBatchKeepsStoredTriggerAsItIs checks that a batch whose trigger id is already stored, here claimed by a turn,
// leaves that trigger as it was stored.
func TestEndBatchKeepsStoredTriggerAsItIs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: "plan/S1/integrate", ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: "plan/S2/integrate", ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	if err := s.PrepareNode(ctx, integrateEpic, "plan/S1/integrate", 1, "rev-5", "refs-5"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	shared := NodeTrigger{ID: "workflow:shared", Cause: "first cause"}
	if err := s.EndBatch(ctx, integrateEpic, "plan/S1/integrate", 1, "failed", "red", &shared,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}
	claimIntegrateTrigger(t, s, shared.ID, 6)

	if err := s.PrepareNode(ctx, integrateEpic, "plan/S2/integrate", 1, "rev-6", "refs-6"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	again := NodeTrigger{ID: shared.ID, Cause: "second cause"}
	if err := s.EndBatch(ctx, integrateEpic, "plan/S2/integrate", 1, "failed", "red again", &again,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch of another run with the stored trigger: %v", err)
	}
	if state, cause, ok := integrateTrigger(t, s, shared.ID); !ok || state != "claimed" || cause != shared.Cause {
		t.Errorf("trigger %s after a second batch = %q %q stored %t, want claimed %q",
			shared.ID, state, cause, ok, shared.Cause)
	}
}

// TestParkEpicWakingParksOnceWithTellAndTrigger checks that ParkEpicWaking parks the epic with its reason, adds the
// reason as one tell and adds its trigger pending. A repeat with the trigger's id, past the trigger's claim, parks and
// tells nothing.
func TestParkEpicWakingParksOnceWithTellAndTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)

	const reason = "epic not built: R1 has no red at the epic's base"
	trigger := NodeTrigger{ID: "workflow:unbuilt-1", Cause: "unbuilt after turn 1"}
	if err := s.ParkEpicWaking(ctx, integrateEpic, reason, trigger, integrateParkedAt()); err != nil {
		t.Fatalf("ParkEpicWaking: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Parked != reason {
		t.Errorf("parked of epic %s after ParkEpicWaking = %q, want %q", integrateEpic, row.Parked, reason)
	}
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after ParkEpicWaking = %q, want one tell %q", integrateEpic, got, reason)
	}
	if state, cause, ok := integrateTrigger(t, s, trigger.ID); !ok || state != "pending" || cause != trigger.Cause {
		t.Fatalf("trigger %s after ParkEpicWaking = %q %q stored %t, want pending %q",
			trigger.ID, state, cause, ok, trigger.Cause)
	}

	claimIntegrateTrigger(t, s, trigger.ID, 7)
	again := NodeTrigger{ID: trigger.ID, Cause: "a second cause"}
	if err := s.ParkEpicWaking(ctx, integrateEpic, "second reason", again, integrateEndedAt()); err != nil {
		t.Fatalf("repeat ParkEpicWaking: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Parked != reason {
		t.Errorf("parked of epic %s after a repeat = %q, want %q", integrateEpic, row.Parked, reason)
	}
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after a repeat = %q, want one tell %q", integrateEpic, got, reason)
	}
	if state, cause, _ := integrateTrigger(t, s, trigger.ID); state != "claimed" || cause != trigger.Cause {
		t.Errorf("trigger %s after a repeat = %q %q, want claimed %q", trigger.ID, state, cause, trigger.Cause)
	}
}

// TestEndPlanBuiltEndsRunsAndSetsBuiltOnce checks that EndPlanBuilt ends each named run that is running with its
// outcome and output and sets the epic's phase to built. A repeat changes no run.
func TestEndPlanBuiltEndsRunsAndSetsBuiltOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1},
		NodeRun{Path: "plan/S1/integrate", ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
	)

	ends := []NodeEnd{
		{Path: "plan", Outcome: "passed", Output: "built"},
		{Path: "plan/S1/integrate", Outcome: "passed"},
	}
	if err := s.EndPlanBuilt(ctx, integrateEpic, ends, integrateEndedAt()); err != nil {
		t.Fatalf("EndPlanBuilt: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "built" {
		t.Errorf("phase of epic %s after EndPlanBuilt = %q, want built", integrateEpic, row.Phase)
	}
	planEnded := NodeRun{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1, Outcome: "passed",
		Output: "built", State: NodeEnded, Started: integrateStartedAt(), Ended: integrateEndedAt()}
	requireIntegrateRun(t, s, "plan", planEnded)
	requireIntegrateRun(t, s, "plan/S1/integrate", NodeRun{Path: "plan/S1/integrate", ID: "id-s1",
		Node: "integrate", Kind: "integrate", Attempt: 1, Outcome: "passed", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateEndedAt()})

	repeat := []NodeEnd{{Path: "plan", Outcome: "failed", Output: "again"}}
	if err := s.EndPlanBuilt(ctx, integrateEpic, repeat, integrateParkedAt()); err != nil {
		t.Fatalf("repeat EndPlanBuilt: %v", err)
	}
	requireIntegrateRun(t, s, "plan", planEnded)
}

// TestEndPlanBuiltNeedsFirstEndRunning checks that EndPlanBuilt whose first end's run has already ended changes
// nothing: no run it names is ended and the epic's phase stays as it was.
func TestEndPlanBuiltNeedsFirstEndRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: "plan/S0/integrate", ID: "id-s0", Node: "integrate", Kind: "integrate", Attempt: 1},
		NodeRun{Path: "plan/S1/integrate", ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
	)
	if err := s.EndNodes(ctx, integrateEpic, []NodeEnd{{Path: "plan/S0/integrate", Outcome: "passed"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	ends := []NodeEnd{
		{Path: "plan/S0/integrate", Outcome: "failed"},
		{Path: "plan/S1/integrate", Outcome: "passed"},
	}
	if err := s.EndPlanBuilt(ctx, integrateEpic, ends, integrateParkedAt()); err != nil {
		t.Fatalf("EndPlanBuilt: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "accepted" {
		t.Errorf("phase of epic %s after EndPlanBuilt with an ended first run = %q, want accepted", integrateEpic,
			row.Phase)
	}
	requireIntegrateRun(t, s, "plan/S0/integrate", NodeRun{Path: "plan/S0/integrate", ID: "id-s0",
		Node: "integrate", Kind: "integrate", Attempt: 1, Outcome: "passed", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateEndedAt()})
	requireIntegrateRun(t, s, "plan/S1/integrate", NodeRun{Path: "plan/S1/integrate", ID: "id-s1",
		Node: "integrate", Kind: "integrate", Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
}

// TestEndPlanBuiltInCorrectingKeepsPhase checks that EndPlanBuilt of a plan that ends while its epic is correcting ends
// the plan run and the runs under it, and leaves the epic's phase correcting.
func TestEndPlanBuiltInCorrectingKeepsPhase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	if err := sqlcgen.New(s.db).SetEpicPhase(ctx, sqlcgen.SetEpicPhaseParams{
		Phase: "correcting", ID: integrateEpic,
	}); err != nil {
		t.Fatalf("set phase of epic %s: %v", integrateEpic, err)
	}
	startIntegrateRuns(t, s,
		NodeRun{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1},
		NodeRun{Path: "plan/S1/integrate", ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1},
	)

	ends := []NodeEnd{
		{Path: "plan", Outcome: "passed", Output: "built"},
		{Path: "plan/S1/integrate", Outcome: "passed"},
	}
	if err := s.EndPlanBuilt(ctx, integrateEpic, ends, integrateEndedAt()); err != nil {
		t.Fatalf("EndPlanBuilt: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "correcting" {
		t.Errorf("phase of epic %s after EndPlanBuilt = %q, want correcting", integrateEpic, row.Phase)
	}
	requireIntegrateRun(t, s, "plan", NodeRun{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1,
		Outcome: "passed", Output: "built", State: NodeEnded, Started: integrateStartedAt(), Ended: integrateEndedAt()})
	requireIntegrateRun(t, s, "plan/S1/integrate", NodeRun{Path: "plan/S1/integrate", ID: "id-s1",
		Node: "integrate", Kind: "integrate", Attempt: 1, Outcome: "passed", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateEndedAt()})
}

// TestParkNodeMetAtBaseParksRunEpicAndTellOnce checks that ParkNodeMetAtBase parks the running run at its attempt and
// parks the epic with its reason as one tell. R4: the park used to set the phase to met_at_base and park for good. It now
// opens a question the owner answers, so the phase stays until the owner ends the epic. A run at another attempt changes
// nothing, and a repeat changes nothing.
func TestParkNodeMetAtBaseParksRunEpicAndTellOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1})

	const reason = "workflow node red: R1 met at the base; you can retry, drop or end"
	if err := s.ParkNodeMetAtBase(ctx, integrateEpic, "red", 2, "failed", "wrong attempt", reason, []string{"R1"},
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase at another attempt: %v", err)
	}
	requireIntegrateRun(t, s, "red", NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence",
		Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
	if row := integrateEpicRow(t, s); row.Phase != "accepted" || row.Parked != "" {
		t.Errorf("phase and parked after ParkNodeMetAtBase at another attempt = %q and %q, want accepted and empty",
			row.Phase, row.Parked)
	}

	if err := s.ParkNodeMetAtBase(ctx, integrateEpic, "red", 1, "failed", "R1 met", reason, []string{"R1"},
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
	parked := NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1, Outcome: "failed",
		Output: "R1 met", State: NodeParked, Started: integrateStartedAt()}
	requireIntegrateRun(t, s, "red", parked)
	if row := integrateEpicRow(t, s); row.Parked != reason || row.Phase != "accepted" {
		t.Errorf("parked and phase after ParkNodeMetAtBase = %q and %q, want %q and accepted", row.Parked, row.Phase,
			reason)
	}
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after ParkNodeMetAtBase = %q, want one tell %q", integrateEpic, got, reason)
	}

	if err := s.ParkNodeMetAtBase(ctx, integrateEpic, "red", 1, "passed", "again", "second reason", []string{"R1"},
		integrateEndedAt()); err != nil {
		t.Fatalf("repeat ParkNodeMetAtBase: %v", err)
	}
	requireIntegrateRun(t, s, "red", parked)
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after a repeat ParkNodeMetAtBase = %q, want one tell %q", integrateEpic, got, reason)
	}
}

// TestRetryRefusedForEpicEndedAtBase checks that RetryEpic and RetryNode on an epic in phase met_at_base are
// RefusalErrors that name the park reason, and leave the epic and the parked run as the owner's end left them. R4: the
// phase is set by the owner's end answer to the park question, which the park no longer does.
func TestRetryRefusedForEpicEndedAtBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1})

	const reason = "workflow node red: R1 met at the base; you can retry, drop or end"
	if err := s.ParkNodeMetAtBase(ctx, integrateEpic, "red", 1, "failed", "R1 met", reason, []string{"R1"},
		integrateParkedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, integrateEpic, "D3", "", 1, ChoiceEnd, integrateParkedAt()); err != nil {
		t.Fatalf("AnswerQuestion end: %v", err)
	}

	want := "epic " + integrateEpic + " is parked for good: " + reason
	requireRefusal(t, s.RetryEpic(ctx, integrateEpic), want)
	requireRefusal(t, s.RetryNode(ctx, integrateEpic, "red"), want)

	requireIntegrateRun(t, s, "red", NodeRun{Path: "red", ID: "id-red", Node: "red", Kind: "evidence",
		Attempt: 1, Outcome: "failed", Output: "R1 met", State: NodeParked, Started: integrateStartedAt()})
	if row := integrateEpicRow(t, s); row.Parked != reason || row.Phase != "met_at_base" {
		t.Errorf("parked and phase after the refused retries = %q and %q, want %q and met_at_base", row.Parked,
			row.Phase, reason)
	}
}
