package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestEpicFlowReadsEpicRunsAndPendingOfOneState checks that EpicFlow returns the epic Epic returns, the node runs
// NodeRuns returns, and the pending triggers of its epic only, as each trigger is claimed or consumed.
func TestEpicFlowReadsEpicRunsAndPendingOfOneState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow"
	const other = "orrery-1a2b3c4/epic-flow-other"
	createEpic(t, s, fullEpic(id))
	createEpic(t, s, fullEpic(other))

	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"}}
	first := NodeTrigger{ID: "workflow:id-one", Cause: "workflow node one started"}
	second := NodeTrigger{ID: "workflow:id-two", Cause: "workflow node two started"}
	third := NodeTrigger{ID: "workflow:id-three", Cause: "workflow node three started"}
	if err := s.StartNodes(ctx, id, runs, &first, epicCreated()); err != nil {
		t.Fatalf("StartNodes of first trigger: %v", err)
	}
	if err := s.StartNodes(ctx, id, nil, &second, epicCreated()); err != nil {
		t.Fatalf("StartNodes of second trigger: %v", err)
	}
	if err := s.StartNodes(ctx, other, nil, &third, epicCreated()); err != nil {
		t.Fatalf("StartNodes of other epic's trigger: %v", err)
	}

	checkFlow := func(want int) {
		t.Helper()
		flow, err := s.EpicFlow(ctx, id)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		epic, err := s.Epic(ctx, id)
		if err != nil {
			t.Fatalf("Epic: %v", err)
		}
		nodes, err := s.NodeRuns(ctx, id)
		if err != nil {
			t.Fatalf("NodeRuns: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("NodeRuns = %+v, want the plan run", nodes)
		}
		if !reflect.DeepEqual(flow.Epic, epic) {
			t.Errorf("EpicFlow epic = %+v, want the epic Epic returns %+v", flow.Epic, epic)
		}
		if !reflect.DeepEqual(flow.Runs, nodes) {
			t.Errorf("EpicFlow runs = %+v, want the runs NodeRuns returns %+v", flow.Runs, nodes)
		}
		if flow.Pending != want {
			t.Errorf("EpicFlow pending = %d, want %d", flow.Pending, want)
		}
	}

	// CreateEpic leaves its own trigger pending, so the epic counts it with the two started above, and never the other
	// epic's trigger.
	checkFlow(3)
	if _, err := s.fixtureExec(
		ctx, `UPDATE epic_triggers SET state = 'claimed', turn = 3 WHERE epic = ? AND id = ?`, id, first.ID,
	); err != nil {
		t.Fatalf("claim first trigger: %v", err)
	}
	checkFlow(2)
	if _, err := s.fixtureExec(
		ctx, `UPDATE epic_triggers SET state = 'consumed', turn = 3 WHERE epic = ? AND id = ?`, id, second.ID,
	); err != nil {
		t.Fatalf("consume second trigger: %v", err)
	}
	checkFlow(1)
}

// TestEpicFlowOfUnknownEpicIsErrNoEpic checks that EpicFlow of an id no epic has returns an error matching ErrNoEpic.
func TestEpicFlowOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	if _, err := s.EpicFlow(context.Background(), "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("EpicFlow of unknown id err = %v, want ErrNoEpic", err)
	}
}

// evidenceRunIDs returns the ids of runs in order.
func evidenceRunIDs(runs []EvidenceRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}

// TestEpicFlowKeepsOnlyRunsCurrentWithTheirRequirementItem checks that EpicFlow lists a Requirement run while its kept
// spec equals the item its Requirement stores, drops it once an UPDATE changes that item by SQL, and keeps the run of
// another Requirement. EvidenceRuns still lists the dropped run as history.
func TestEpicFlowKeepsOnlyRunsCurrentWithTheirRequirementItem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-runs"
	row := evidenceEpic(id)
	createEpic(t, s, row)
	items := make(map[string]evidence.Item)
	for _, requirement := range row.Requirements {
		items[requirement.ID] = requirement.Item
	}
	for _, requirement := range []struct{ run, item string }{{"run-r1", "R1"}, {"run-r2", "R2"}} {
		if err := s.AddEvidenceRun(ctx, EvidenceRun{
			ID: requirement.run, Epic: id, Item: requirement.item, Revision: "abc1234", Phase: "red",
			Source: "captured", Spec: items[requirement.item], Created: runCreatedAt(),
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", requirement.run, err)
		}
	}

	checkFlow := func(want ...string) {
		t.Helper()
		flow, err := s.EpicFlow(ctx, id)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		if got := evidenceRunIDs(flow.Evidence); !reflect.DeepEqual(got, want) {
			t.Fatalf("EpicFlow evidence runs = %v, want %v", got, want)
		}
	}
	checkFlow("run-r1", "run-r2")

	changed := items["R1"]
	changed.Conclusion = "The store changed"
	raw, err := encodeItem(changed)
	if err != nil {
		t.Fatalf("encode changed item: %v", err)
	}
	if _, err := s.fixtureExec(
		ctx, `UPDATE epic_requirements SET item = ? WHERE epic = ? AND id = 'R1'`, raw, id,
	); err != nil {
		t.Fatalf("change item of R1: %v", err)
	}
	checkFlow("run-r2")

	history, err := s.EvidenceRuns(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if got, want := evidenceRunIDs(history), []string{"run-r1", "run-r2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("EvidenceRuns = %v, want %v: history keeps the run whose item changed", got, want)
	}
}

// TestTaskRunReadsAsRedAndGreenOnlyWhileCurrentWithTheTaskEvidence checks that a task's red and green runs read back
// through Epic while their kept spec equals the task's evidence, and read nil once an UPDATE changes that evidence by
// SQL. Build keeps the run ids the build named.
func TestTaskRunReadsAsRedAndGreenOnlyWhileCurrentWithTheTaskEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	spec := plannedTurn().Tasks[0].Evidence
	for _, run := range []struct{ id, phase string }{{"red-run", "red"}, {"green-run", "green"}} {
		if err := s.AddEvidenceRun(ctx, EvidenceRun{
			ID: run.id, Epic: buildEpic, Item: "T1", Revision: "abc1234", Phase: run.phase, Source: "captured",
			Spec: spec, Created: runCreatedAt(),
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", run.id, err)
		}
	}
	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{Stage: "implement", Base: buildBase, RedRun: "red-run", GreenRun: "green-run"}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, from, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	task := buildTaskOf(t, s, 1)
	if task.Red == nil || task.Red.ID != "red-run" || task.Green == nil || task.Green.ID != "green-run" {
		t.Fatalf("red = %v, green = %v; want the red-run and green-run runs", task.Red, task.Green)
	}

	changed := spec
	changed.Conclusion = "the store changed"
	raw, err := encodeItem(changed)
	if err != nil {
		t.Fatalf("encode changed evidence: %v", err)
	}
	if _, err := s.fixtureExec(
		ctx, `UPDATE epic_tasks SET evidence = ? WHERE epic = ? AND seq = 1`, raw, buildEpic,
	); err != nil {
		t.Fatalf("change evidence of task 1: %v", err)
	}
	task = buildTaskOf(t, s, 1)
	if task.Red != nil || task.Green != nil {
		t.Errorf("red = %v, green = %v after the evidence changed; want nil and nil", task.Red, task.Green)
	}
	if task.Build.RedRun != "red-run" || task.Build.GreenRun != "green-run" {
		t.Errorf("build runs = %q and %q; want red-run and green-run kept", task.Build.RedRun, task.Build.GreenRun)
	}
}

// TestEpicFlowReadsReviewAndLabeledStepsOfItsReviewRuns checks that EpicFlow returns the review EpicReview returns, and
// after the agent steps exactly the steps labeled with its epic and the path of one of its review or closure runs, each
// once, in the order of its runs.
func TestEpicFlowReadsReviewAndLabeledStepsOfItsReviewRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-review"
	const other = "orrery-1a2b3c4/epic-flow-review-other"
	createEpic(t, s, fullEpic(id))

	const agentRunID = "id-agent"
	runs := []NodeRun{
		{Path: "agent", ID: agentRunID, Node: "agent", Kind: "agent", Attempt: 1},
		{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1},
		{Path: "correct@1/closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1},
	}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.FreezeReview(ctx, id, "review", 1, "rev-frozen", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	ops := []EngineOp{
		stepAddOf(NodeWorkID(agentRunID, 1, "step")),
		labelledStepOf("step-agent-path", id, "agent"),
		labelledStepOf("step-review-a", id, "review"),
		labelledStepOf("step-review-b", id, "review"),
		labelledStepOf("step-closure", id, "correct@1/closure"),
		labelledStepOf("step-other-epic", other, "review"),
		labelledStepOf("step-no-run", id, "plan"),
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	review, err := s.EpicReview(ctx, id)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if flow.Review.Frozen != "rev-frozen" || !reflect.DeepEqual(flow.Review, review) {
		t.Errorf("EpicFlow review = %+v, want the review EpicReview returns %+v", flow.Review, review)
	}
	want := []string{NodeWorkID(agentRunID, 1, "step"), "step-closure", "step-review-a", "step-review-b"}
	if got := stepIDsOf(flow.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("EpicFlow steps = %v, want %v", got, want)
	}
	for _, row := range flow.Steps {
		got, err := s.Step(ctx, row.ID)
		if err != nil {
			t.Fatalf("Step %s: %v", row.ID, err)
		}
		if !reflect.DeepEqual(row, got) {
			t.Errorf("EpicFlow step %s = %+v, want the row Step returns %+v", row.ID, row, got)
		}
	}
}

// TestEpicFlowReadsLabeledStepsInTheOrderOfItsRuns checks that EpicFlow, which reads every review and closure run's
// labeled steps in one query, returns them grouped in the order of the runs, each run's in the order they started,
// even where the steps of a later run sort before those of an earlier one.
func TestEpicFlowReadsLabeledStepsInTheOrderOfItsRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-run-order"
	createEpic(t, s, fullEpic(id))
	runs := []NodeRun{
		{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1},
		{Path: "correct@1/closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1},
	}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	ops := []EngineOp{
		labelledStepOf("step-a-review", id, "review"),
		labelledStepOf("step-b-closure", id, "correct@1/closure"),
		labelledStepOf("step-c-review", id, "review"),
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	// The runs read in path order, so the closure's steps come first, then the review's.
	want := []string{"step-b-closure", "step-a-review", "step-c-review"}
	if got := stepIDsOf(flow.Steps); !reflect.DeepEqual(got, want) {
		t.Errorf("EpicFlow steps = %v, want %v", got, want)
	}
}

// TestEpicFlowWithoutReviewHasNoLabeledSteps checks that EpicFlow of an epic whose review has not started returns the
// zero Review, and no labeled step even when it is labeled with the path of a run that is not a review or closure.
func TestEpicFlowWithoutReviewHasNoLabeledSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-no-review"
	createEpic(t, s, fullEpic(id))
	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1}}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("step-plan", id, "plan")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Review, Review{}) {
		t.Errorf("EpicFlow review = %+v, want the zero Review", flow.Review)
	}
	if len(flow.Steps) != 0 {
		t.Errorf("EpicFlow steps = %v, want none", stepIDsOf(flow.Steps))
	}
}

// TestEpicFlowUnderwayReadsEveryRunningStepAndUnendedEvidenceRun checks that EpicFlow's Underway holds the id of each
// step labeled with its epic whose end is not stored, whatever its task label, in start order then id, and no step that
// ended, no unlabeled step and no step of another epic. It holds the id of each evidence run of the epic that has not
// ended, current or not, and no run that ended. Steps and Evidence keep only what the decisions read: no labeled step of
// a run that is not a review, closure or release, and no run whose item changed since it started.
func TestEpicFlowUnderwayReadsEveryRunningStepAndUnendedEvidenceRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-underway"
	const other = "orrery-1a2b3c4/epic-flow-underway-other"
	row := evidenceEpic(id)
	createEpic(t, s, row)
	createEpic(t, s, fullEpic(other))
	runs := []NodeRun{
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"},
		{Path: "plan/S4/integrate", ID: "id-integrate", Node: "integrate", Kind: "integrate"},
	}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	ops := []EngineOp{
		labelledStepOf("step-a-integrator", id, "plan/S4/integrate"),
		labelledStepOf("step-b-build", id, "T6"),
		labelledStepOf("step-c-conversation", id, ""),
		labelledStepOf("step-d-ended", id, "T4"),
		labelledStepOf("step-e-other-epic", other, "T6"),
		stepAddOf("step-f-unlabeled"),
		StepEnd{ID: "step-d-ended", End: "done", Status: "done"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	items := make(map[string]evidence.Item)
	for _, requirement := range row.Requirements {
		items[requirement.ID] = requirement.Item
	}
	stale := items["R1"]
	stale.Conclusion = "An item R1 no longer holds"
	for _, run := range []struct {
		id, item string
		spec     evidence.Item
	}{{"run-a-current", "R1", items["R1"]}, {"run-b-stale", "R1", stale}, {"run-c-ended", "R2", items["R2"]}} {
		if err := s.AddEvidenceRun(ctx, EvidenceRun{
			ID: run.id, Epic: id, Item: run.item, Revision: "abc1234", Phase: "green", Source: "captured",
			Spec: run.spec, Created: runCreatedAt(),
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", run.id, err)
		}
	}
	if err := s.RecordEvidenceCapture(ctx, "run-c-ended", EvidenceCapture{At: runCapturedAt()}); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	verdict := EvidenceVerdict{Verdict: evidence.VerdictHolds, Passed: true, At: runEndedAt()}
	if err := s.RecordEvidenceVerdict(ctx, "run-c-ended", verdict); err != nil {
		t.Fatalf("RecordEvidenceVerdict: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	want := Underway{
		Steps:    []string{"step-a-integrator", "step-b-build", "step-c-conversation"},
		Evidence: []string{"run-a-current", "run-b-stale"},
	}
	if !reflect.DeepEqual(flow.Underway, want) {
		t.Errorf("Underway = %+v, want %+v", flow.Underway, want)
	}
	if len(flow.Steps) != 0 {
		t.Errorf("EpicFlow steps = %v, want none: no step is labeled with a review, closure or release run",
			stepIDsOf(flow.Steps))
	}
	if got := evidenceRunIDs(flow.Evidence); !reflect.DeepEqual(got, []string{"run-a-current", "run-c-ended"}) {
		t.Errorf("EpicFlow evidence runs = %v, want the current run-a-current and run-c-ended", got)
	}
}
