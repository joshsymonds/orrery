package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

var (
	_ Kind = Plan{}
	_ Kind = Build{}
)

// planRows returns the rows of an epic with turns, tasks and pending triggers.
func planRows(turns []store.EpicTurn, tasks []store.EpicTask, pending int) Rows {
	return Rows{Epic: store.EpicRow{Turns: turns, Tasks: tasks}, Pending: pending}
}

// planNode returns a plan node whose run started with Mark mark.
func planNode(mark int64) Node {
	return Node{Run: store.NodeRun{Kind: "plan", Mark: mark}}
}

func TestPlanDecide(t *testing.T) {
	t.Parallel()
	done := []store.EpicTask{{Seq: 1, Status: "done"}}
	cases := []struct {
		name string
		node Node
		rows Rows
		want Action
	}{
		{
			"complete turn above mark with nothing open ends complete",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "complete"}}, done, 0),
			Action{Kind: ActEnd, Outcome: "complete"},
		},
		{
			"first plan with mark 0 ends on its first complete turn",
			planNode(0),
			planRows([]store.EpicTurn{{Seq: 1, State: "succeeded", Outcome: "complete"}}, nil, 0),
			Action{Kind: ActEnd, Outcome: "complete"},
		},
		{
			"highest seq decides, not the turn listed last",
			planNode(2),
			planRows([]store.EpicTurn{
				{Seq: 3, State: "succeeded", Outcome: "complete"},
				{Seq: 4, State: "succeeded", Outcome: "work"},
			}, nil, 0),
			Action{Kind: ActRun},
		},
		{"no turn gives run", planNode(0), planRows(nil, nil, 0), Action{Kind: ActRun}},
		{
			"complete turn at mark gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 2, State: "succeeded", Outcome: "complete"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"complete turn below mark gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 1, State: "succeeded", Outcome: "complete"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"running highest turn gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "running"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"running earlier turn gives run",
			planNode(1),
			planRows([]store.EpicTurn{
				{Seq: 2, State: "running"},
				{Seq: 3, State: "succeeded", Outcome: "complete"},
			}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"failed highest turn gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "failed", Outcome: "complete"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"work outcome gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "work"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"wait outcome gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "wait"}}, nil, 0),
			Action{Kind: ActRun},
		},
		{
			"open task gives run",
			planNode(2),
			planRows(
				[]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "complete"}},
				[]store.EpicTask{{Seq: 4, Status: "open"}},
				0,
			),
			Action{Kind: ActRun},
		},
		{
			"open task from an earlier turn gives run",
			planNode(2),
			planRows(
				[]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "complete"}},
				[]store.EpicTask{{Seq: 1, Turn: 1, Status: "open"}},
				0,
			),
			Action{Kind: ActRun},
		},
		{
			"pending trigger gives run",
			planNode(2),
			planRows([]store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "complete"}}, done, 1),
			Action{Kind: ActRun},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Plan{}.Decide(tc.node, tc.rows)
			if got != tc.want {
				t.Errorf("Plan.Decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// buildRows returns the rows of an epic with base, phase and tasks.
func buildRows(base, phase string, tasks []store.EpicTask) Rows {
	return Rows{Epic: store.EpicRow{Base: base, Phase: phase, Tasks: tasks}}
}

func TestBuildDecide(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{Kind: "build", Task: 2}}
	cases := []struct {
		name string
		node Node
		rows Rows
		want Action
	}{
		{
			"absent task ends with no outcome",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 1, Status: "open"}}),
			Action{Kind: ActEnd},
		},
		{
			"done task ends with no outcome",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 2, Status: "done"}}),
			Action{Kind: ActEnd},
		},
		{
			"failed task ends with no outcome",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 2, Status: "failed"}}),
			Action{Kind: ActEnd},
		},
		{
			"open task on accepted epic with base runs",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 2, Status: "open"}}),
			Action{Kind: ActRun},
		},
		{
			"open run task runs beside another open task",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 1, Status: "open"}, {Seq: 2, Status: "open"}}),
			Action{Kind: ActRun},
		},
		{
			"done run task ends even when another task is open",
			node,
			buildRows("main", "accepted", []store.EpicTask{{Seq: 1, Status: "open"}, {Seq: 2, Status: "done"}}),
			Action{Kind: ActEnd},
		},
		{
			"open task on accepted epic with no base gives none",
			node,
			buildRows("", "accepted", []store.EpicTask{{Seq: 2, Status: "open"}}),
			Action{Kind: ActNone},
		},
		{
			"open task on epic not accepted gives none",
			node,
			buildRows("main", "deciding", []store.EpicTask{{Seq: 2, Status: "open"}}),
			Action{Kind: ActNone},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Build{}.Decide(tc.node, tc.rows)
			if got != tc.want {
				t.Errorf("Build.Decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
