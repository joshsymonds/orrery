package interpret

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// decideEpic is the epic every case decides.
const decideEpic = "orrery-7a8b9c0/epic-decide"

// scripted is a test-only kind. Its node run in round r gets the action scripted for r, and a round it has no script
// for waits.
type scripted map[int64]kinds.Action

// Decide returns the action scripted for the round of the node run, or none when the round has no script.
func (s scripted) Decide(node kinds.Node, _ kinds.Rows) kinds.Action {
	if act, ok := s[node.Run.Round]; ok {
		return act
	}
	return kinds.Action{Kind: kinds.ActNone}
}

// endIn scripts a kind that ends round i+1 with outcomes[i].
func endIn(outcomes ...string) scripted {
	script := scripted{}
	for i, outcome := range outcomes {
		script[int64(i)+1] = kinds.Action{Kind: kinds.ActEnd, Outcome: outcome}
	}
	return script
}

// refineKinds returns the kinds of the refine loop: work ends every round with no outcome, and review ends round r
// with reviews[r-1].
func refineKinds(reviews ...string) map[string]kinds.Kind {
	return map[string]kinds.Kind{"work": endIn("", ""), "review": endIn(reviews...)}
}

// kindNode returns a kind node with id that holds body.
func kindNode(id, kind string, body ...workflow.Node) workflow.Node {
	return workflow.Node{ID: id, Kind: kind, Body: body}
}

// loopNode returns the loop node refine with its settings and its body. loopOf is a run of it.
func loopNode(loop workflow.Loop, body ...workflow.Node) workflow.Node {
	return workflow.Node{ID: "refine", Loop: &loop, Body: body}
}

// refine returns the loop the loop cases run: two rounds at most, until review reports done, spent as given, and a
// body of work then review.
func refine(spent string) workflow.Node {
	settings := workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "review", Name: "done"}, Spent: spent}
	return loopNode(settings, kindNode("work", "work"), kindNode("review", "review"))
}

// planNode returns a plan node whose body is one build node.
func planNode() workflow.Node {
	return kindNode("plan", "plan", kindNode("build", "build"))
}

// startOf returns the run a start of node of kind at path in round stores. It has no state, which the store sets.
func startOf(path, node, kind string, round int64) store.NodeRun {
	return store.NodeRun{
		Path: path, ID: store.NodeRunID(decideEpic, path, round, "start"), Node: node, Kind: kind, Round: round,
	}
}

// childOf returns the build child run at path of a plan run in round, for task seq, the position-th task of turn.
func childOf(path string, round, task, turn, position int64) store.NodeRun {
	cause := fmt.Sprintf("turn %d task %d", turn, position)
	return store.NodeRun{
		Path: path, ID: store.NodeRunID(decideEpic, path, round, cause), Node: "build", Kind: "build", Task: task,
		Round: round,
	}
}

// loopOf returns the loop run of refine at loop round loopRound with granted rounds granted. It has no state.
func loopOf(loopRound, granted int64) store.NodeRun {
	run := startOf("refine", "refine", "loop", 0)
	run.LoopRound, run.Granted = loopRound, granted
	return run
}

// endedIn returns the ended run of node in round of refine, with outcome.
func endedIn(round int64, node, outcome string) store.NodeRun {
	return ended(startOf(fmt.Sprintf("refine@%d/%s", round, node), node, node, round), outcome)
}

// running returns run in the running state.
func running(run store.NodeRun) store.NodeRun {
	run.State = store.NodeRunning
	return run
}

// ended returns run in the ended state with outcome.
func ended(run store.NodeRun, outcome string) store.NodeRun {
	run.State, run.Outcome = store.NodeEnded, outcome
	return run
}

// parked returns run in the parked state.
func parked(run store.NodeRun) store.NodeRun {
	run.State = store.NodeParked
	return run
}

// withMark returns run with mark as its plan's highest turn seq at start.
func withMark(run store.NodeRun, mark int64) store.NodeRun {
	run.Mark = mark
	return run
}

// runningPlan returns the running run of the plan at path "plan" that started after turn mark.
func runningPlan(mark int64) store.NodeRun {
	return withMark(running(startOf("plan", "plan", "plan", 0)), mark)
}

// openTask returns an open task seq planned in turn turn.
func openTask(seq, turn int64) store.EpicTask {
	return store.EpicTask{Seq: seq, Turn: turn, Status: "open"}
}

// doneTask returns a done task seq planned in turn turn.
func doneTask(seq, turn int64) store.EpicTask {
	return store.EpicTask{Seq: seq, Turn: turn, Status: "done"}
}

// turnAt returns a turn seq that succeeded with outcome.
func turnAt(seq int64, outcome string) store.EpicTurn {
	return store.EpicTurn{Seq: seq, State: "succeeded", Outcome: outcome}
}

// rowsOf returns the rows of decideEpic, which is accepted on base main, with turns, tasks and node runs.
func rowsOf(turns []store.EpicTurn, tasks []store.EpicTask, runs ...store.NodeRun) kinds.Rows {
	epic := store.EpicRow{ID: decideEpic, Base: "main", Phase: "accepted", Turns: turns, Tasks: tasks}
	return kinds.Rows{Epic: epic, Runs: runs}
}

// endedClean returns rows with the epic ended clean, so its build children wait.
func endedClean(rows kinds.Rows) kinds.Rows {
	rows.Epic.Phase = "clean"
	return rows
}

// decideCase is one case: the flow, the rows and the kinds Decide reads, and the actions it must return.
type decideCase struct {
	name string
	flow []workflow.Node
	rows kinds.Rows
	reg  map[string]kinds.Kind
	want []Action
}

// decideCases runs each case as a parallel subtest that checks Decide over the case's flow, rows and kinds.
func decideCases(t *testing.T, cases []decideCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Decide(workflow.Workflow{Name: "decide", Nodes: tc.flow}, tc.rows, tc.reg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecideStarts(t *testing.T) {
	t.Parallel()
	decideCases(t, []decideCase{
		{
			name: "first node starts when no run exists",
			flow: []workflow.Node{kindNode("a", "test")},
			rows: rowsOf(nil, nil),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("a", "a", "test", 0)}}},
		},
		{
			name: "a node waits while the node before it runs",
			flow: []workflow.Node{kindNode("a", "test"), kindNode("b", "test")},
			rows: rowsOf(nil, nil, running(startOf("a", "a", "test", 0))),
			reg:  map[string]kinds.Kind{"test": scripted{}},
			want: nil,
		},
		{
			name: "a node starts once the node before it ends",
			flow: []workflow.Node{kindNode("a", "test"), kindNode("b", "test")},
			rows: rowsOf(nil, nil, ended(startOf("a", "a", "test", 0), "")),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("b", "b", "test", 0)}}},
		},
		{
			name: "built-in plan starts with mark 0 and no trigger despite earlier turns",
			flow: []workflow.Node{planNode()},
			rows: rowsOf([]store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}, nil),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("plan", "plan", "plan", 0)}}},
		},
		{
			name: "later plan starts with the highest turn seq and its trigger",
			flow: []workflow.Node{planNode(), kindNode("replan", "plan", kindNode("rebuild", "build"))},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work"), turnAt(3, "complete"), turnAt(2, "work")},
				nil,
				ended(startOf("plan", "plan", "plan", 0), "complete"),
			),
			want: []Action{{
				Kind: ActStart,
				Runs: []store.NodeRun{withMark(startOf("replan", "replan", "plan", 0), 3)},
				Trigger: &store.NodeTrigger{
					ID:    "workflow:" + store.NodeRunID(decideEpic, "replan", 0, "trigger"),
					Cause: "workflow node replan started",
				},
			}},
		},
		{
			name: "loop starts at loop round 1",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{loopOf(1, 0)}}},
		},
	})
}

func TestDecidePlan(t *testing.T) {
	t.Parallel()
	decideCases(t, []decideCase{
		{
			name: "running plan runs and starts a child for each open task",
			flow: []workflow.Node{planNode()},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")},
				[]store.EpicTask{openTask(1, 1), openTask(2, 1), openTask(3, 2)},
				runningPlan(0),
			),
			reg: map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T1", 0, 1, 1, 1)}},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T2", 0, 2, 1, 2)}},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 1)}},
			},
		},
		{
			name: "running plan runs its open children and ends the child whose task left open",
			flow: []workflow.Node{planNode()},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work")},
				[]store.EpicTask{openTask(1, 1), doneTask(2, 1)},
				runningPlan(0),
				running(childOf("plan/build/T1", 0, 1, 1, 1)),
				running(childOf("plan/build/T2", 0, 2, 1, 2)),
			),
			reg: map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActRun, Path: "plan/build/T1", NodeKind: "build", Task: 1},
				{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan/build/T2"}}},
			},
		},
		{
			name: "open children wait once the epic has ended",
			flow: []workflow.Node{planNode()},
			rows: endedClean(rowsOf(
				[]store.EpicTurn{turnAt(1, "work")},
				[]store.EpicTask{openTask(1, 1)},
				runningPlan(0),
				running(childOf("plan/build/T1", 0, 1, 1, 1)),
			)),
			reg:  map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
		{
			name: "child position is the place in its turn and tasks at or below the mark get no child",
			flow: []workflow.Node{planNode()},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")},
				[]store.EpicTask{openTask(1, 1), openTask(2, 2), openTask(3, 2)},
				runningPlan(1),
			),
			reg: map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T2", 0, 2, 2, 1)}},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 2)}},
			},
		},
		{
			name: "complete plan ends with its running children",
			flow: []workflow.Node{planNode()},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")},
				[]store.EpicTask{doneTask(1, 1), doneTask(2, 2)},
				runningPlan(1),
				running(childOf("plan/build/T1", 0, 1, 1, 1)),
			),
			reg: map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{
				{Path: "plan", Outcome: "complete"},
				{Path: "plan/build/T1"},
			}}},
		},
		{
			name: "plan in a loop body fans out under its round's path",
			flow: []workflow.Node{
				loopNode(workflow.Loop{Max: 1, Until: workflow.Outcome{Node: "plan", Name: "complete"}, Spent: "park"},
					planNode()),
			},
			rows: rowsOf(
				[]store.EpicTurn{turnAt(1, "work")},
				[]store.EpicTask{openTask(1, 1)},
				running(loopOf(1, 0)),
				running(startOf("refine@1/plan", "plan", "plan", 1)),
			),
			reg: map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}},
			want: []Action{
				{Kind: ActRun, Path: "refine@1/plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("refine@1/plan/build/T1", 1, 1, 1, 1)}},
			},
		},
	})
}

func TestDecideEnded(t *testing.T) {
	t.Parallel()
	decideCases(t, []decideCase{
		{
			name: "every top-level node ended gives no action",
			flow: []workflow.Node{kindNode("a", "test"), planNode()},
			rows: rowsOf(nil, nil,
				ended(startOf("a", "a", "test", 0), ""),
				ended(startOf("plan", "plan", "plan", 0), "complete"),
			),
			reg:  map[string]kinds.Kind{"test": scripted{}, "plan": kinds.Plan{}, "build": kinds.Build{}},
			want: nil,
		},
		{
			name: "parked loop gives no action and holds the node after it",
			flow: []workflow.Node{refine("park"), kindNode("b", "test")},
			rows: rowsOf(nil, nil, parked(loopOf(2, 0))),
			reg:  refineKinds("again", "done"),
			want: nil,
		},
		{
			name: "running node with no registered kind waits",
			flow: []workflow.Node{kindNode("a", "missing")},
			rows: rowsOf(nil, nil, running(startOf("a", "a", "missing", 0))),
			want: nil,
		},
		{
			name: "kind that ends its running node ends it with its outcome and holds the node after it",
			flow: []workflow.Node{kindNode("a", "test"), kindNode("b", "test")},
			rows: rowsOf(nil, nil, running(startOf("a", "a", "test", 0))),
			reg:  map[string]kinds.Kind{"test": scripted{0: {Kind: kinds.ActEnd, Outcome: "x"}}},
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "a", Outcome: "x"}}}},
		},
		{
			name: "kind that runs its running node gives a run for it",
			flow: []workflow.Node{kindNode("a", "test")},
			rows: rowsOf(nil, nil, running(startOf("a", "a", "test", 0))),
			reg:  map[string]kinds.Kind{"test": scripted{0: {Kind: kinds.ActRun}}},
			want: []Action{{Kind: ActRun, Path: "a", NodeKind: "test"}},
		},
	})
}

func TestDecideLoop(t *testing.T) {
	t.Parallel()
	decideCases(t, []decideCase{
		{
			name: "round whose until node reports the outcome ends the loop",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(2, 0)), endedIn(2, "work", ""), endedIn(2, "review", "done")),
			reg:  refineKinds("again", "done"),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "refine"}}}},
		},
		{
			name: "round without the until outcome moves to the next round",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(1, 0)), endedIn(1, "work", ""), endedIn(1, "review", "again")),
			reg:  refineKinds("again", "done"),
			want: []Action{{Kind: ActRound, Path: "refine", Round: 1}},
		},
		{
			name: "round past max without the outcome parks with the reason",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(2, 0)), endedIn(2, "work", ""), endedIn(2, "review", "again")),
			reg:  refineKinds("again", "again"),
			want: []Action{{
				Kind: ActPark, Path: "refine", Round: 2,
				Reason: "loop refine spent its 2 rounds without review.done",
			}},
		},
		{
			name: "granted round extends the rounds past max",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(2, 1)), endedIn(2, "work", ""), endedIn(2, "review", "again")),
			reg:  refineKinds("again", "again"),
			want: []Action{{Kind: ActRound, Path: "refine", Round: 2}},
		},
		{
			name: "granted rounds spent park with the total rounds in the reason",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(3, 1)), endedIn(3, "work", ""), endedIn(3, "review", "again")),
			reg:  refineKinds("again", "again", "again"),
			want: []Action{{
				Kind: ActPark, Path: "refine", Round: 3,
				Reason: "loop refine spent its 3 rounds without review.done",
			}},
		},
		{
			name: "spent continue ends the loop with no outcome",
			flow: []workflow.Node{refine("continue")},
			rows: rowsOf(nil, nil, running(loopOf(2, 0)), endedIn(2, "work", ""), endedIn(2, "review", "again")),
			reg:  refineKinds("again", "again"),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "refine"}}}},
		},
		{
			name: "new round starts its first body node",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(2, 0)), endedIn(1, "work", ""), endedIn(1, "review", "again")),
			reg:  refineKinds("again", "done"),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("refine@2/work", "work", "work", 2)}}},
		},
		{
			name: "an until outcome from an earlier round does not end the loop",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil, running(loopOf(2, 0)), endedIn(1, "work", ""), endedIn(1, "review", "done")),
			reg:  refineKinds("done", "again"),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("refine@2/work", "work", "work", 2)}}},
		},
		{
			name: "loop waits while a body node of the round runs, even with the outcome reported",
			flow: []workflow.Node{refine("park")},
			rows: rowsOf(nil, nil,
				running(loopOf(2, 0)),
				running(startOf("refine@2/work", "work", "work", 2)),
				endedIn(2, "review", "done"),
			),
			reg:  map[string]kinds.Kind{"work": endIn(""), "review": endIn("again", "done")},
			want: nil,
		},
	})
}
