package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// workEpic and workOther are the epics the work write tests write to. workOther is a second epic whose rows they must
// not touch.
const (
	workEpic  = "orrery-9c0d1e2/epic-work"
	workOther = "orrery-9c0d1e2/epic-work-other"
)

// openWorkStore opens a migrated store with workEpic and workOther created, and returns it with a raw handle to the
// same file.
func openWorkStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	s, raw := openNodeStore(t)
	for _, id := range []string{workEpic, workOther} {
		if err := s.CreateEpic(context.Background(), loopEpicRow(id)); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
	}
	return s, raw
}

// requireRefusalError fails unless err is a RefusalError whose text is want.
func requireRefusalError(t *testing.T, err error, want string) {
	t.Helper()
	var refusal store.RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a RefusalError %q", err, want)
	}
	if string(refusal) != want {
		t.Errorf("RefusalError = %q, want %q", string(refusal), want)
	}
}

// TestNodeWorkIDMatchesFormAndDiffers checks that NodeWorkID is the first 32 hex digits of the sha256 of the work
// namespace, run id, attempt and part joined with NUL, and that changing any one of those parts changes the id. The
// expected value is computed outside the package with bash printf and sha256sum.
func TestNodeWorkIDMatchesFormAndDiffers(t *testing.T) {
	t.Parallel()
	const (
		runID   = "id-check"
		attempt = int64(2)
		part    = "check"
		want    = "f184a2545f1589137f5fd99187592081"
	)
	if got := store.NodeWorkID(runID, attempt, part); got != want {
		t.Errorf("NodeWorkID = %q, want %q", got, want)
	}

	differs := map[string]string{
		"run id":  store.NodeWorkID("id-other", attempt, part),
		"attempt": store.NodeWorkID(runID, 3, part),
		"part":    store.NodeWorkID(runID, attempt, "step"),
	}
	for name, got := range differs {
		if got == want {
			t.Errorf("NodeWorkID with another %s = %q, the same as the base id", name, got)
		}
	}
}

// TestPrepareNodeStoresRevisionAndRefsOnceAtItsAttempt checks that PrepareNode stores the revision and refs of the
// running run at its attempt, and that a repeat, a run at another attempt and an ended run change nothing.
func TestPrepareNodeStoresRevisionAndRefsOnceAtItsAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openWorkStore(t)

	runs := []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 2},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 2},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, workEpic, []store.NodeEnd{{Path: "done", Outcome: "passed"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	if err := s.PrepareNode(ctx, workEpic, "check", 2, "rev-1", "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.PrepareNode(ctx, workEpic, "check", 2, "rev-2", "refs-2"); err != nil {
		t.Fatalf("repeat PrepareNode: %v", err)
	}
	if err := s.PrepareNode(ctx, workEpic, "check", 1, "rev-3", "refs-3"); err != nil {
		t.Fatalf("PrepareNode at another attempt: %v", err)
	}
	if err := s.PrepareNode(ctx, workEpic, "done", 2, "rev-4", "refs-4"); err != nil {
		t.Fatalf("PrepareNode of an ended run: %v", err)
	}

	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 2, Revision: "rev-1", Refs: "refs-1",
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 2, Outcome: "passed",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
	})
}

// TestRecordNodeResultStoresResultOnceAtItsAttempt checks that RecordNodeResult stores the result of the running run at
// its attempt, and that a repeat, a run at another attempt and an ended run change nothing.
func TestRecordNodeResultStoresResultOnceAtItsAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openWorkStore(t)

	runs := []store.NodeRun{
		{Path: "owned", ID: "id-owned", Node: "owned", Kind: "owned", Attempt: 1},
		{Path: "done", ID: "id-done", Node: "done", Kind: "owned", Attempt: 1},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, workEpic, []store.NodeEnd{{Path: "done", Outcome: "passed"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	if err := s.RecordNodeResult(ctx, workEpic, "owned", 1, `["plan.go"]`); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	if err := s.RecordNodeResult(ctx, workEpic, "owned", 1, `["other.go"]`); err != nil {
		t.Fatalf("repeat RecordNodeResult: %v", err)
	}
	if err := s.RecordNodeResult(ctx, workEpic, "owned", 2, `["third.go"]`); err != nil {
		t.Fatalf("RecordNodeResult at another attempt: %v", err)
	}
	if err := s.RecordNodeResult(ctx, workEpic, "done", 1, `["ended.go"]`); err != nil {
		t.Fatalf("RecordNodeResult of an ended run: %v", err)
	}

	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "done", ID: "id-done", Node: "done", Kind: "owned", Attempt: 1, Outcome: "passed",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
		{Path: "owned", ID: "id-owned", Node: "owned", Kind: "owned", Attempt: 1, Result: `["plan.go"]`,
			State: store.NodeRunning, Started: nodeStartedAt()},
	})
}

// TestCheckNodeRefsOnlyAddsTellsOnce checks that CheckNodeRefs leaves the running run running and its epic unparked,
// adds its tells and records that the refs were checked, and that a repeat, a call at another attempt and a call on an
// ended run add no tells.
func TestCheckNodeRefsOnlyAddsTellsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, workEpic, []store.NodeEnd{{Path: "done", Outcome: "passed"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	if err := s.CheckNodeRefs(ctx, workEpic, "check", 1, []string{"moved a"}, loopParkedAt()); err != nil {
		t.Fatalf("CheckNodeRefs: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, workEpic, "check", 1, []string{"moved b"}, loopParkedAt()); err != nil {
		t.Fatalf("repeat CheckNodeRefs: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, workEpic, "check", 2, []string{"moved c"}, loopParkedAt()); err != nil {
		t.Fatalf("CheckNodeRefs at another attempt: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, workEpic, "done", 1, []string{"moved d"}, loopParkedAt()); err != nil {
		t.Fatalf("CheckNodeRefs of an ended run: %v", err)
	}

	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1, RefsChecked: true,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1, Outcome: "passed",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
	})
	if parked := parkedOf(t, raw, workEpic); parked != "" {
		t.Errorf("parked of epic %s after CheckNodeRefs = %q, want empty", workEpic, parked)
	}
	if got := tellsOf(t, raw, workEpic); !reflect.DeepEqual(got, []string{"moved a"}) {
		t.Errorf("tells of epic %s after CheckNodeRefs = %q, want one tell %q", workEpic, got, "moved a")
	}
}

// TestParkNodeParksRunAndEpicWithOneTell checks that ParkNode parks the running run at its attempt with its outcome
// and output, parks its epic with the reason and adds the reason as one tell, and that a call at another attempt, a
// repeat and a call on an ended run change nothing.
func TestParkNodeParksRunAndEpicWithOneTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, workEpic, []store.NodeEnd{{Path: "done", Outcome: "passed"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	const reason = "workflow node check failed: gate output"
	if err := s.ParkNode(
		ctx,
		workEpic,
		"check",
		2,
		"failed",
		"wrong attempt",
		store.ParkClassBranch,
		reason,
		loopParkedAt(),
	); err != nil {
		t.Fatalf("ParkNode at another attempt: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workEpic,
		"check",
		1,
		"failed",
		"gate output",
		store.ParkClassBranch,
		reason,
		loopParkedAt(),
	); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workEpic,
		"check",
		1,
		"passed",
		"second",
		store.ParkClassBranch,
		"second reason",
		nodeEndedAt(),
	); err != nil {
		t.Fatalf("repeat ParkNode: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workEpic,
		"done",
		1,
		"failed",
		"ended",
		store.ParkClassBranch,
		"ended reason",
		nodeEndedAt(),
	); err != nil {
		t.Fatalf("ParkNode of an ended run: %v", err)
	}

	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1, Outcome: "failed",
			Output: "gate output", State: store.NodeParked, Started: nodeStartedAt()},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1, Outcome: "passed",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
	})
	if parked := parkedOf(t, raw, workEpic); parked != reason {
		t.Errorf("parked of epic %s after ParkNode = %q, want %q", workEpic, parked, reason)
	}
	if got := tellsOf(t, raw, workEpic); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after ParkNode = %q, want one tell %q", workEpic, got, reason)
	}
	if parked := parkedOf(t, raw, workOther); parked != "" {
		t.Errorf("parked of epic %s after ParkNode of %s = %q, want empty", workOther, workEpic, parked)
	}
}

// TestNodeOutputOverMaxIsCutOnARuneStart checks that the output EndNodesWaking and ParkNode store is cut to
// NodeOutputMax bytes at a rune start: a multi-byte rune that would straddle the limit is dropped whole, and an output
// that ends exactly at the limit is kept.
func TestNodeOutputOverMaxIsCutOnARuneStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openWorkStore(t)

	straddling := strings.Repeat("a", store.NodeOutputMax-1) + "é"
	exact := strings.Repeat("a", store.NodeOutputMax) + "é"
	runs := []store.NodeRun{
		{Path: "straddle", ID: "id-straddle", Node: "straddle", Kind: "gate", Attempt: 1},
		{Path: "exact", ID: "id-exact", Node: "exact", Kind: "gate", Attempt: 1},
		{Path: "parked", ID: "id-parked", Node: "parked", Kind: "gate", Attempt: 1},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	trigger := store.NodeTrigger{ID: "workflow:straddle", Cause: "workflow node straddle failed"}
	ends := []store.NodeEnd{
		{Path: "straddle", Outcome: "passed", Output: straddling},
		{Path: "exact", Outcome: "passed", Output: exact},
	}
	if err := s.EndNodesWaking(ctx, workEpic, ends, trigger, nodeEndedAt()); err != nil {
		t.Fatalf("EndNodesWaking: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workEpic,
		"parked",
		1,
		"failed",
		straddling,
		store.ParkClassNode,
		"parked reason",
		nodeEndedAt(),
	); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}

	cut := strings.Repeat("a", store.NodeOutputMax-1)
	kept := strings.Repeat("a", store.NodeOutputMax)
	got, err := s.NodeRuns(ctx, workEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	outputs := map[string]string{}
	for _, run := range got {
		outputs[run.Path] = run.Output
	}
	want := map[string]string{"straddle": cut, "exact": kept, "parked": cut}
	if !reflect.DeepEqual(outputs, want) {
		t.Errorf("outputs of epic %s = %v, want %v", workEpic, outputs, want)
	}
}

// TestEndNodesWakingEndsRunsAndAddsTriggerOnce checks that EndNodesWaking ends each running run with its outcome,
// output and end second and adds the trigger pending, and that a repeat leaves the runs and the trigger as they were,
// even once a turn has claimed the trigger.
func TestEndNodesWakingEndsRunsAndAddsTriggerOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1}}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	// CreateEpic stores its own pending trigger, so the pending count is counted before and after the write.
	pendingBefore := pendingTriggersOf(t, raw, workEpic)
	trigger := store.NodeTrigger{ID: "workflow:plan", Cause: "workflow node plan failed: first"}
	ends := []store.NodeEnd{{Path: "plan", Outcome: "failed", Output: "first"}}
	if err := s.EndNodesWaking(ctx, workEpic, ends, trigger, nodeEndedAt()); err != nil {
		t.Fatalf("EndNodesWaking: %v", err)
	}
	if got := pendingTriggersOf(t, raw, workEpic); got != pendingBefore+1 {
		t.Fatalf("pending triggers after EndNodesWaking = %d, want %d", got, pendingBefore+1)
	}
	row := triggerRowOf(t, raw, workEpic, trigger.ID)
	if row.state != "pending" || row.turn != 0 || row.cause != trigger.Cause || row.created != nodeEndedAt().Unix() {
		t.Errorf("trigger after EndNodesWaking = %+v, want pending turn 0 cause %q created %d",
			row, trigger.Cause, nodeEndedAt().Unix())
	}

	setTriggerState(t, raw, workEpic, trigger.ID, "claimed", 5)
	again := store.NodeTrigger{ID: trigger.ID, Cause: "a different cause"}
	repeat := []store.NodeEnd{{Path: "plan", Outcome: "passed", Output: "second"}}
	if err := s.EndNodesWaking(ctx, workEpic, repeat, again, time.Unix(1700001200, 0)); err != nil {
		t.Fatalf("repeat EndNodesWaking: %v", err)
	}

	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1, Outcome: "failed", Output: "first",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
	})
	row = triggerRowOf(t, raw, workEpic, trigger.ID)
	if row.state != "claimed" || row.turn != 5 || row.cause != trigger.Cause {
		t.Errorf("trigger after a repeat past a claim = %+v, want claimed by turn 5 cause %q", row, trigger.Cause)
	}
}

// TestRetryNodeTurnsParkedRunRunningAtNextAttempt checks that RetryNode turns the parked run of its node running at
// the next attempt, with its outcome, output, revision, refs, refs checked, result and end cleared, and unparks its
// epic. A running run of another node, and the same node's parked run in another epic, stay as they were. A repeat is
// a RefusalError and changes nothing.
func TestRetryNodeTurnsParkedRunRunningAtNextAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1},
	}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, workEpic, []store.NodeEnd{{Path: "done", Outcome: "passed"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if err := s.StartNodes(
		ctx, workOther, []store.NodeRun{{Path: "check", ID: "id-other-check", Node: "check", Kind: "gate", Attempt: 1}},
		nil, nodeStartedAt(),
	); err != nil {
		t.Fatalf("StartNodes of %s: %v", workOther, err)
	}

	const reason = "workflow node check failed: boom"
	if err := s.PrepareNode(ctx, workEpic, "check", 1, "rev-1", "refs-1"); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.RecordNodeResult(ctx, workEpic, "check", 1, `["plan.go"]`); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, workEpic, "check", 1, nil, loopParkedAt()); err != nil {
		t.Fatalf("CheckNodeRefs: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workEpic,
		"check",
		1,
		"failed",
		"boom",
		store.ParkClassNode,
		reason,
		loopParkedAt(),
	); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
	if err := s.ParkNode(
		ctx,
		workOther,
		"check",
		1,
		"failed",
		"other",
		store.ParkClassNode,
		"other reason",
		loopParkedAt(),
	); err != nil {
		t.Fatalf("ParkNode of %s: %v", workOther, err)
	}

	if err := s.RetryNode(
		ctx,
		workEpic,
		"check",
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("RetryNode: %v", err)
	}
	if parked := parkedOf(t, raw, workEpic); parked != "" {
		t.Errorf("parked of epic %s after RetryNode = %q, want empty", workEpic, parked)
	}
	want := []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 2,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "done", ID: "id-done", Node: "done", Kind: "gate", Attempt: 1, Outcome: "passed",
			State: store.NodeEnded, Started: nodeStartedAt(), Ended: nodeEndedAt()},
	}
	requireNodeRuns(t, s, workEpic, want)
	requireNodeRuns(t, s, workOther, []store.NodeRun{
		{Path: "check", ID: "id-other-check", Node: "check", Kind: "gate", Attempt: 1, Outcome: "failed",
			Output: "other", State: store.NodeParked, Started: nodeStartedAt()},
	})
	if parked := parkedOf(t, raw, workOther); parked != "other reason" {
		t.Errorf("parked of epic %s after RetryNode of %s = %q, want %q", workOther, workEpic, parked, "other reason")
	}

	requireRefusalError(
		t,
		s.RetryNode(ctx, workEpic, "check", store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0)),
		"node check of epic "+workEpic+" is not parked",
	)
	requireNodeRuns(t, s, workEpic, want)
}

// TestRetryNodeWithoutParkedRunIsRefused checks that RetryNode of a node with no parked run, whether the run is running
// or the node is unknown, is a RefusalError and changes neither the runs nor the epic's park.
func TestRetryNodeWithoutParkedRunIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1}}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	for _, node := range []string{"check", "missing"} {
		err := s.RetryNode(ctx, workEpic, node, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
		requireRefusalError(t, err, "node "+node+" of epic "+workEpic+" is not parked")
	}
	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1,
			State: store.NodeRunning, Started: nodeStartedAt()},
	})
	if parked := parkedOf(t, raw, workEpic); parked != "" {
		t.Errorf("parked of epic %s after refused RetryNode = %q, want empty", workEpic, parked)
	}
}

// TestRetryNodeNamingAParkedLoopIsRefused checks that a parked loop is not retried as a node: the loop and the epic's
// park stay as ParkLoop left them, since a loop takes its extra round from RetryEpic.
func TestRetryNodeNamingAParkedLoopIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)

	runs := []store.NodeRun{{Path: "r", ID: "id-r", Node: "r", Kind: "loop", LoopRound: 1}}
	if err := s.StartNodes(ctx, workEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	const reason = "loop r spent its 1 rounds without check.passed"
	if err := s.ParkLoop(ctx, workEpic, "r", 1, reason, loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop: %v", err)
	}

	err := s.RetryNode(ctx, workEpic, "r", store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
	requireRefusalError(t, err, "node r of epic "+workEpic+" is not parked")
	requireNodeRuns(t, s, workEpic, []store.NodeRun{
		{Path: "r", ID: "id-r", Node: "r", Kind: "loop", LoopRound: 1, State: store.NodeParked,
			Started: nodeStartedAt()},
	})
	if parked := parkedOf(t, raw, workEpic); parked != reason {
		t.Errorf("parked of epic %s after refused RetryNode = %q, want %q", workEpic, parked, reason)
	}
}
