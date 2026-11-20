package interpret

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// batchPath is the path of the integrate run in the settle walk of the plan at "plan" under settle point 2.
const batchPath = "plan/S2/integrate"

// integrateKinds returns the kinds of the integrate cases: the plan and build kinds, and batch as the integrate kind.
func integrateKinds(batch kinds.Kind) map[string]kinds.Kind {
	return map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}, "integrate": batch}
}

// batchNode returns the integrate node of a plan body.
func batchNode() workflow.Node {
	return kindNode("integrate", "integrate")
}

// batchRun returns the running integrate run at batchPath at attempt 1, with revision as its revision.
func batchRun(revision string) store.NodeRun {
	run := attempted(running(startOf(batchPath, "integrate", "integrate", 0)), 1)
	run.Revision = revision
	return run
}

// batchTrigger returns the trigger the integrate run at batchPath writes at attempt 1 when it ends with outcome and
// output.
func batchTrigger(outcome, output string) *store.NodeTrigger {
	return &store.NodeTrigger{
		ID:    "workflow:" + store.NodeRunID(decideEpic, batchPath, 0, "integrated attempt 1"),
		Cause: fmt.Sprintf("workflow node %s %s: %s", batchPath, outcome, output),
	}
}

// unbuiltTrigger returns the trigger a plan writes when it would end complete and the epic is not built: its reason is
// the cause.
func unbuiltTrigger(reason string) *store.NodeTrigger {
	return &store.NodeTrigger{
		ID:    "workflow:" + store.NodeRunID(decideEpic, "plan", 0, "unbuilt after turn 2"),
		Cause: reason,
	}
}

// baseEvidence returns the evidence node id, which checks at the epic's base.
func baseEvidence(id string) workflow.Node {
	return workflow.Node{ID: id, Kind: "evidence", Settings: &workflow.Settings{At: "base"}}
}

// itemRequirement returns a Requirement that names an evidence item.
func itemRequirement(id string) store.EpicRequirement {
	return store.EpicRequirement{ID: id, Item: evidence.Item{Conclusion: id + " holds"}}
}

// redRun returns the red evidence run of Requirement id of node run at its attempt, at the epic's base main, with
// verdict, and passed when it passed.
func redRun(node store.NodeRun, id, verdict string, passed bool) store.EvidenceRun {
	return store.EvidenceRun{
		ID: store.NodeWorkID(node.ID, node.Attempt, id), Item: id, Revision: "main", Phase: evidence.PhaseRed,
		State: "ended", Verdict: verdict, Passed: passed,
	}
}

// captureRun returns the passed green capture of Requirement id of the integrate run at its attempt.
func captureRun(run store.NodeRun, id string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: store.NodeWorkID(run.ID, run.Attempt, "capture "+id), Item: id, Phase: evidence.PhaseGreen,
		State: "ended", Verdict: evidence.VerdictHolds, Passed: true,
	}
}

// withEvidence returns rows with requirements and the evidence runs.
func withEvidence(rows kinds.Rows, requirements []store.EpicRequirement, runs ...store.EvidenceRun) kinds.Rows {
	rows.Epic.Requirements = requirements
	rows.Evidence = runs
	return rows
}

func TestDecideBatchActions(t *testing.T) {
	t.Parallel()
	oneTurn := []store.EpicTurn{turnAt(1, "work")}
	tasks := []store.EpicTask{doneTask(1, 1), doneTask(2, 1)}
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "prepare gives work that prepares the batch's tasks",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("")),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActPrepare, Tasks: []int64{1, 2}}}),
			want: []Action{plan, {
				Kind: ActWork, Work: kinds.ActPrepare, Tasks: []int64{1, 2}, Path: batchPath, NodeKind: "integrate",
				Def: batch, Run: batchRun(""),
			}},
		},
		{
			name: "capture gives work that captures the Requirements with their items",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg: integrateKinds(scripted{0: {
				Kind: kinds.ActCapture, IDs: []string{"c1", "c2"}, Items: []string{"R1", "R2"},
			}}),
			want: []Action{plan, {
				Kind: ActWork, Work: kinds.ActCapture, IDs: []string{"c1", "c2"}, Items: []string{"R1", "R2"},
				Path: batchPath, NodeKind: "integrate", Def: batch, Run: batchRun("c1"),
			}},
		},
		{
			name: "advance gives work that moves the epic, carrying the trigger its passed end writes",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActAdvance, Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActWork, Work: kinds.ActAdvance, Output: "R1 green", Trigger: batchTrigger("passed", "R1 green"),
				Path: batchPath, NodeKind: "integrate", Def: batch, Run: batchRun("c1"),
			}},
		},
		{
			name: "park of the integrate run parks its node with the park's text as output and reason",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg: integrateKinds(scripted{0: {
				Kind: kinds.ActPark, Output: "integrate plan/S2/integrate: Done gate lint lost at a restart twice",
			}}),
			want: []Action{plan, {
				Kind: ActParkNode, Path: batchPath, Attempt: 1, Outcome: "failed",
				Output: "integrate plan/S2/integrate: Done gate lint lost at a restart twice",
				Reason: "integrate plan/S2/integrate: Done gate lint lost at a restart twice",
			}},
		},
		{
			name: "passed batch ends with its output and the trigger that wakes the planner",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green",
				Trigger: batchTrigger("passed", "R1 green"), Run: batchRun("c1"),
			}},
		},
		{
			name: "failed batch ends with its output and the trigger that wakes the planner",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "Done gate lint failed"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "failed", Output: "Done gate lint failed",
				Trigger: batchTrigger("failed", "Done gate lint failed"), Run: batchRun("c1"),
			}},
		},
		{
			name: "empty batch ends with its output and no trigger",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("")),
			reg: integrateKinds(scripted{0: {
				Kind: kinds.ActEnd, Outcome: "passed", Output: "no accepted task to integrate",
			}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "no accepted task to integrate",
				Run: batchRun(""),
			}},
		},
		{
			name: "waiting integrate run gives no action of its own",
			flow: planWithChecks(batch),
			rows: rowsOf(oneTurn, tasks, runningPlan(0), batchRun("c1")),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
	})
}

func TestDecideBatchHoldsFanOut(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")}
	tasks := []store.EpicTask{doneTask(1, 1), doneTask(2, 1), openTask(3, 2)}
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	started := Action{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 1)}}
	decideCases(t, []decideCase{
		{
			name: "running integrate run holds the start of an open task",
			flow: planWithChecks(batch),
			rows: rowsOf(turns, tasks, runningPlan(0), batchRun("c1")),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
		{
			name: "parked integrate run holds the start of an open task",
			flow: planWithChecks(batch),
			rows: rowsOf(turns, tasks, runningPlan(0), parked(batchRun("c1"))),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
		{
			name: "open task starts once the integrate run has ended",
			flow: planWithChecks(batch),
			rows: rowsOf(turns, tasks, runningPlan(0), ended(batchRun("c1"), "passed")),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan, started},
		},
	})
}

func TestDecideBatchPlanEnds(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	tasks := []store.EpicTask{doneTask(1, 1), doneTask(2, 2)}
	flow := append([]workflow.Node{baseEvidence("red")}, planWithChecks(batchNode())...)
	red := ended(startOf("red", "red", "evidence", 0), "passed")
	reg := integrateKinds(scripted{})
	one := []store.EpicRequirement{itemRequirement("R1")}
	unbuiltR1 := "epic not built: R1 is not green at the stored base s1"
	// complete returns the rows of a plan that completes after turn 1, with stored base s1, the Requirements, the
	// evidence runs and the node runs given after the red run.
	complete := func(
		requirements []store.EpicRequirement, evidenceRuns []store.EvidenceRun, runs ...store.NodeRun,
	) kinds.Rows {
		rows := rowsOf(turns, tasks, append([]store.NodeRun{runningPlan(1), red}, runs...)...)
		rows.Epic.StoredBase = "s1"
		return withEvidence(rows, requirements, evidenceRuns...)
	}
	decideCases(t, []decideCase{
		{
			name: "complete plan holding integrate ends built when every Requirement has its proof",
			flow: flow,
			rows: complete(one, []store.EvidenceRun{
				redRun(red, "R1", evidence.VerdictFails, true),
				captureRun(batchRun("s1"), "R1"),
			}, ended(batchRun("s1"), "passed")),
			reg:  reg,
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, Built: true}},
		},
		{
			name: "complete plan holding integrate parks the epic with the Requirement that is not green",
			flow: flow,
			rows: complete(one, []store.EvidenceRun{redRun(red, "R1", evidence.VerdictFails, true)},
				ended(batchRun(""), "passed"),
			),
			reg: reg,
			want: []Action{{
				Kind: ActParkEpic, Reason: unbuiltR1, Trigger: unbuiltTrigger(unbuiltR1),
			}},
		},
		{
			name: "unbuilt Requirements are joined in contract order",
			flow: flow,
			rows: complete([]store.EpicRequirement{itemRequirement("R1"), {ID: "R2"}}, nil,
				ended(batchRun(""), "passed"),
			),
			reg: reg,
			want: []Action{{
				Kind:   ActParkEpic,
				Reason: "epic not built: R1 is not green at the stored base s1; R2 names no evidence item",
				Trigger: unbuiltTrigger(
					"epic not built: R1 is not green at the stored base s1; R2 names no evidence item",
				),
			}},
		},
		{
			name: "complete plan without an integrate node ends as it did, and is not built",
			flow: []workflow.Node{planNode()},
			rows: rowsOf(turns, tasks, runningPlan(1)),
			reg:  planKinds(scripted{}),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}}},
		},
	})
}

// TestDecideEvidenceRedContinuesWithATell checks the end of an evidence node at base whose red runs judged Requirements
// holding at base. Those Requirements are recorded as met at the base with a tell, and the node ends passed, so the
// epic goes on with no park. A node that failed for any other reason parks as it did.
func TestDecideEvidenceRedContinuesWithATell(t *testing.T) {
	t.Parallel()
	node := baseEvidence("red")
	run := attempted(running(startOf("red", "red", "evidence", 0)), 1)
	reg := map[string]kinds.Kind{
		"evidence": scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "R1 red: failed"}},
	}
	one := []store.EpicRequirement{itemRequirement("R1")}
	two := []store.EpicRequirement{itemRequirement("R1"), itemRequirement("R2")}
	const oneTell = "workflow node red: R1 met at the epic's base main, recorded as met there, and the epic goes on"
	const twoTell = "workflow node red: R1, R2 met at the epic's base main, recorded as met there, and the epic goes on"
	decideCases(t, []decideCase{
		{
			name: "top-level evidence at base that fails with a Requirement met ends passed with a tell",
			flow: []workflow.Node{node},
			rows: withEvidence(rowsOf(nil, nil, run), one, redRun(run, "R1", evidence.VerdictHolds, false)),
			reg:  reg,
			want: []Action{{
				Kind: ActEnd, Ends: []store.NodeEnd{{Path: "red", Outcome: "passed", Output: oneTell}}, Tell: oneTell,
			}},
		},
		{
			name: "several Requirements met at base are named in contract order in one tell",
			flow: []workflow.Node{node},
			rows: withEvidence(rowsOf(nil, nil, run), two,
				redRun(run, "R1", evidence.VerdictHolds, false),
				redRun(run, "R2", evidence.VerdictHolds, false),
			),
			reg: reg,
			want: []Action{{
				Kind: ActEnd, Ends: []store.NodeEnd{{Path: "red", Outcome: "passed", Output: twoTell}}, Tell: twoTell,
			}},
		},
		{
			name: "a Requirement met beside a red that gave no verdict parks as a failure",
			flow: []workflow.Node{node},
			rows: withEvidence(rowsOf(nil, nil, run), two,
				redRun(run, "R1", evidence.VerdictHolds, false),
				redRun(run, "R2", "", false),
			),
			reg: reg,
			want: []Action{{
				Kind: ActParkNode, Path: "red", Attempt: 1, Outcome: "failed", Output: "R1 red: failed",
				Reason: "workflow node red failed: R1 red: failed",
			}},
		},
		{
			name: "top-level evidence at base that fails with no Requirement met parks as it did",
			flow: []workflow.Node{node},
			rows: withEvidence(rowsOf(nil, nil, run), one, redRun(run, "R1", "", false)),
			reg:  reg,
			want: []Action{{
				Kind: ActParkNode, Path: "red", Attempt: 1, Outcome: "failed", Output: "R1 red: failed",
				Reason: "workflow node red failed: R1 red: failed",
			}},
		},
	})
}

// carryLog is the store the integrate actions are carried to in the tests. It logs each write it is given and stores
// nothing. The methods it does not override are never called.
type carryLog struct {
	Store
	writes []string
}

// EndBatch logs the end of a batch.
func (c *carryLog) EndBatch(
	_ context.Context,
	epic string,
	end store.BatchEnd,
	trigger *store.NodeTrigger,
	_ time.Time,
) error {
	c.writes = append(
		c.writes,
		fmt.Sprintf("EndBatch %s %s %d %s %q %+v", epic, end.Path, end.Attempt, end.Outcome, end.Output, trigger),
	)
	return nil
}

// ParkEpicWaking logs the park of an epic with its trigger.
func (c *carryLog) ParkEpicWaking(
	_ context.Context,
	epic, reason string,
	trigger store.NodeTrigger,
	_ time.Time,
) error {
	c.writes = append(c.writes, fmt.Sprintf("ParkEpicWaking %s %q %+v", epic, reason, trigger))
	return nil
}

// ParkNode logs the park of a node run and its class.
func (c *carryLog) ParkNode(
	_ context.Context, epic, path string, attempt int64, outcome, output string, class store.ParkClass, reason string,
	_ time.Time,
) error {
	c.writes = append(
		c.writes,
		fmt.Sprintf("ParkNode %s %s %d %s %q %s %q", epic, path, attempt, outcome, output, class, reason),
	)
	return nil
}

// EndNodesTelling logs the ends of node runs that also tell the owner.
func (c *carryLog) EndNodesTelling(
	_ context.Context,
	epic string,
	ends []store.NodeEnd,
	tell string,
	_ time.Time,
) error {
	c.writes = append(c.writes, fmt.Sprintf("EndNodesTelling %s %+v %q", epic, ends, tell))
	return nil
}

// EndPlanBuilt logs the ends of a plan that built the epic.
func (c *carryLog) EndPlanBuilt(_ context.Context, epic string, ends []store.NodeEnd, _ time.Time) error {
	c.writes = append(c.writes, fmt.Sprintf("EndPlanBuilt %s %+v", epic, ends))
	return nil
}

// TestRunnerCarriesBatchActions checks that the runner writes each integrate action with the store method E7 names.
func TestRunnerCarriesBatchActions(t *testing.T) {
	t.Parallel()
	recorded := &carryLog{}
	runner := New(Config{Store: recorded})
	epic := store.EpicRow{ID: decideEpic}
	passed := batchTrigger("passed", "R1 green")
	unbuilt := unbuiltTrigger("epic not built: R1 names no evidence item")
	acts := []Action{
		{Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green", Trigger: passed},
		{Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "no accepted task to integrate"},
		{Kind: ActParkEpic, Reason: unbuilt.Cause, Trigger: unbuilt},
		{
			Kind: ActEnd,
			Ends: []store.NodeEnd{{Path: "red", Outcome: "passed", Output: "R1 met"}},
			Tell: "R1 met",
		},
		{Kind: ActParkNode, Path: "red", Attempt: 2, Outcome: "failed", Output: "R1 red: failed", Reason: "plain"},
		{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, Built: true},
	}
	for _, act := range acts {
		if err := runner.carry(context.Background(), epic, act); err != nil {
			t.Fatalf("carry %s: %v", act.Kind, err)
		}
	}
	want := []string{
		fmt.Sprintf("EndBatch %s %s 1 passed %q %+v", decideEpic, batchPath, "R1 green", passed),
		fmt.Sprintf("EndBatch %s %s 1 passed %q <nil>", decideEpic, batchPath, "no accepted task to integrate"),
		fmt.Sprintf("ParkEpicWaking %s %q %+v", decideEpic, unbuilt.Cause, *unbuilt),
		fmt.Sprintf(
			"EndNodesTelling %s %+v %q",
			decideEpic,
			[]store.NodeEnd{{Path: "red", Outcome: "passed", Output: "R1 met"}},
			"R1 met",
		),
		fmt.Sprintf("ParkNode %s red 2 failed %q %s %q", decideEpic, "R1 red: failed", store.ParkClassNode, "plain"),
		fmt.Sprintf("EndPlanBuilt %s %+v", decideEpic, []store.NodeEnd{{Path: "plan", Outcome: "complete"}}),
	}
	if !reflect.DeepEqual(recorded.writes, want) {
		t.Errorf("carried writes = %q, want %q", recorded.writes, want)
	}
}
