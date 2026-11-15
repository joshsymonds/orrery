package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// attempted returns run at attempt.
func attempted(run store.NodeRun, attempt int64) store.NodeRun {
	run.Attempt = attempt
	return run
}

// checkRefine returns the loop of the check cases: two rounds at most, until check reports passed, parked when its
// rounds are spent, and the body given.
func checkRefine(body ...workflow.Node) workflow.Node {
	settings := workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "check", Name: "passed"}, Spent: "park"}
	return loopNode(settings, body...)
}

// planNodeWithChecks returns a plan node whose body is a build node followed by checks.
func planNodeWithChecks(checks ...workflow.Node) workflow.Node {
	return kindNode("plan", "plan", append([]workflow.Node{kindNode("build", "build")}, checks...)...)
}

// planWithChecks returns the flow of one plan node whose body is a build node followed by checks.
func planWithChecks(checks ...workflow.Node) []workflow.Node {
	return []workflow.Node{planNodeWithChecks(checks...)}
}

// planKinds returns the kinds of the plan cases: the plan and build kinds, and gate as the check kind.
func planKinds(gate kinds.Kind) map[string]kinds.Kind {
	return map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}, "gate": gate}
}

func TestDecideCheckWork(t *testing.T) {
	t.Parallel()
	gate := kindNode("a", "gate")
	run := running(startOf("a", "a", "gate", 0))
	decideCases(t, []decideCase{
		{
			name: "prepare gives work that prepares the run",
			flow: []workflow.Node{gate},
			rows: rowsOf(nil, nil, run),
			reg:  map[string]kinds.Kind{"gate": scripted{0: {Kind: kinds.ActPrepare}}},
			want: []Action{{Kind: ActWork, Work: kinds.ActPrepare, Path: "a", NodeKind: "gate", Def: gate, Run: run}},
		},
		{
			name: "start gives work that starts the ids with their items",
			flow: []workflow.Node{gate},
			rows: rowsOf(nil, nil, run),
			reg: map[string]kinds.Kind{"gate": scripted{0: {
				Kind: kinds.ActStart, IDs: []string{"w1", "w2"}, Items: []string{"R1", "R2"},
			}}},
			want: []Action{{
				Kind: ActWork, Work: kinds.ActStart, Path: "a", NodeKind: "gate", Def: gate, Run: run,
				IDs: []string{"w1", "w2"}, Items: []string{"R1", "R2"},
			}},
		},
		{
			name: "refs gives work that checks the refs of the run",
			flow: []workflow.Node{gate},
			rows: rowsOf(nil, nil, run),
			reg:  map[string]kinds.Kind{"gate": scripted{0: {Kind: kinds.ActRefs}}},
			want: []Action{{Kind: ActWork, Work: kinds.ActRefs, Path: "a", NodeKind: "gate", Def: gate, Run: run}},
		},
		{
			name: "waiting check gives no action",
			flow: []workflow.Node{gate},
			rows: rowsOf(nil, nil, run),
			reg:  map[string]kinds.Kind{"gate": scripted{0: {Kind: kinds.ActNone}}},
			want: nil,
		},
	})
}

func TestDecideCheckEnds(t *testing.T) {
	t.Parallel()
	flow := []workflow.Node{kindNode("a", "gate"), kindNode("b", "test")}
	decideCases(t, []decideCase{
		{
			name: "top-level check that ends failed parks its node with attempt, output and reason",
			flow: flow,
			rows: rowsOf(nil, nil, running(attempted(startOf("a", "a", "gate", 0), 2))),
			reg:  map[string]kinds.Kind{"gate": scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "boom"}}},
			want: []Action{{
				Kind: ActParkNode, Path: "a", Attempt: 2, Outcome: "failed", Output: "boom",
				Reason: "workflow node a failed: boom",
			}},
		},
		{
			name: "top-level check that ends passed ends with its output",
			flow: flow,
			rows: rowsOf(nil, nil, running(startOf("a", "a", "gate", 0))),
			reg:  map[string]kinds.Kind{"gate": scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "ok"}}},
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "a", Outcome: "passed", Output: "ok"}}}},
		},
	})
}

func TestDecideLoopChecks(t *testing.T) {
	t.Parallel()
	decideCases(t, []decideCase{
		{
			name: "failed check in a loop body ends with its output and the round ends",
			flow: []workflow.Node{checkRefine(kindNode("check", "gate"))},
			rows: rowsOf(nil, nil,
				running(loopOf(1, 0)),
				running(startOf("refine@1/check", "check", "gate", 1)),
			),
			reg: map[string]kinds.Kind{"gate": scripted{1: {Kind: kinds.ActEnd, Outcome: "failed", Output: "boom"}}},
			want: []Action{{
				Kind: ActEnd, Ends: []store.NodeEnd{{Path: "refine@1/check", Outcome: "failed", Output: "boom"}},
			}},
		},
		{
			name: "failed check ends the round, so the node after it in the body does not start",
			flow: []workflow.Node{checkRefine(kindNode("check", "gate"), kindNode("review", "review"))},
			rows: rowsOf(nil, nil,
				running(loopOf(1, 0)),
				ended(startOf("refine@1/check", "check", "gate", 1), "failed"),
			),
			want: []Action{{Kind: ActRound, Path: "refine", Round: 1}},
		},
	})
}

func TestDecideSettleWalks(t *testing.T) {
	t.Parallel()
	waiting := scripted{}
	oneTurn := []store.EpicTurn{turnAt(1, "work")}
	decideCases(t, []decideCase{
		{
			name: "settle checks start in order under the settle point's path once no task is open",
			flow: planWithChecks(kindNode("check", "gate"), kindNode("lint", "gate")),
			rows: rowsOf(oneTurn, []store.EpicTask{doneTask(1, 1), doneTask(2, 1)}, runningPlan(0)),
			reg:  planKinds(waiting),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{startOf("plan/S2/check", "check", "gate", 0)}},
			},
		},
		{
			name: "the settle check after a passed one starts next",
			flow: planWithChecks(kindNode("check", "gate"), kindNode("lint", "gate")),
			rows: rowsOf(oneTurn, []store.EpicTask{doneTask(1, 1), doneTask(2, 1)}, runningPlan(0),
				ended(startOf("plan/S2/check", "check", "gate", 0), "passed"),
			),
			reg: planKinds(waiting),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{startOf("plan/S2/lint", "lint", "gate", 0)}},
			},
		},
		{
			name: "settle checks wait while a task is open",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf(oneTurn, []store.EpicTask{openTask(2, 1)}, runningPlan(0)),
			reg:  planKinds(waiting),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T2", 0, 2, 1, 1)}},
			},
		},
		{
			name: "failed settle check ends with the trigger that wakes the planner",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf(oneTurn, []store.EpicTask{doneTask(1, 1), doneTask(2, 1)}, runningPlan(0),
				attempted(running(startOf("plan/S2/check", "check", "gate", 0)), 1),
			),
			reg: planKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "boom"}}),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{
					Kind: ActEnd,
					Ends: []store.NodeEnd{{Path: "plan/S2/check", Outcome: "failed", Output: "boom"}},
					Trigger: &store.NodeTrigger{
						ID:    "workflow:" + store.NodeRunID(decideEpic, "plan/S2/check", 0, "failed attempt 1"),
						Cause: "workflow node plan/S2/check failed: boom",
					},
				},
			},
		},
		{
			name: "passed settle check wakes nobody",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf(oneTurn, []store.EpicTask{doneTask(1, 1), doneTask(2, 1)}, runningPlan(0),
				ended(startOf("plan/S2/check", "check", "gate", 0), "passed"),
			),
			reg:  planKinds(waiting),
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
	})
}

func TestDecideSettleEnds(t *testing.T) {
	t.Parallel()
	waiting := scripted{}
	oneTurn := []store.EpicTurn{turnAt(1, "work")}
	decideCases(t, []decideCase{
		{
			name: "complete plan waits while its latest settle walk runs",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf([]store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")},
				[]store.EpicTask{doneTask(1, 1), doneTask(2, 2)},
				runningPlan(1),
				running(startOf("plan/S2/check", "check", "gate", 0)),
			),
			reg:  planKinds(waiting),
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
		{
			name: "complete plan ends once its latest settle walk has ended",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf([]store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")},
				[]store.EpicTask{doneTask(1, 1), doneTask(2, 2)},
				runningPlan(1),
				ended(startOf("plan/S2/check", "check", "gate", 0), "passed"),
			),
			reg:  planKinds(waiting),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}}},
		},
		{
			name: "new settle point walks its checks again after new tasks",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf([]store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")},
				[]store.EpicTask{doneTask(1, 1), doneTask(2, 1), doneTask(3, 2)},
				runningPlan(0),
				ended(startOf("plan/S2/check", "check", "gate", 0), "passed"),
			),
			reg: planKinds(waiting),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{startOf("plan/S3/check", "check", "gate", 0)}},
			},
		},
		{
			name: "started settle walk is decided while a newer settle point waits for its open task",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf([]store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")},
				[]store.EpicTask{doneTask(1, 1), doneTask(2, 1), openTask(3, 2)},
				runningPlan(0),
				attempted(running(startOf("plan/S2/check", "check", "gate", 0)), 1),
			),
			reg: planKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "boom"}}),
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{
					Kind: ActEnd,
					Ends: []store.NodeEnd{{Path: "plan/S2/check", Outcome: "failed", Output: "boom"}},
					Trigger: &store.NodeTrigger{
						ID:    "workflow:" + store.NodeRunID(decideEpic, "plan/S2/check", 0, "failed attempt 1"),
						Cause: "workflow node plan/S2/check failed: boom",
					},
				},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 1)}},
			},
		},
		{
			name: "plan in a loop body walks its checks under its round's path",
			flow: []workflow.Node{
				loopNode(workflow.Loop{Max: 1, Until: workflow.Outcome{Node: "plan", Name: "complete"}, Spent: "park"},
					planNodeWithChecks(kindNode("check", "gate"))),
			},
			rows: rowsOf(oneTurn, []store.EpicTask{doneTask(1, 1)},
				running(loopOf(1, 0)),
				running(startOf("refine@1/plan", "plan", "plan", 1)),
			),
			reg: planKinds(waiting),
			want: []Action{
				{Kind: ActRun, Path: "refine@1/plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{startOf("refine@1/plan/S1/check", "check", "gate", 1)}},
			},
		},
	})
}
