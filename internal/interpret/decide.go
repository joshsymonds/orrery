// Package interpret runs an epic's workflow over its node runs. Decide is its pure pass: it returns the actions that
// move the runs on.
package interpret

import (
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// ActionKind names what the interpreter does for an epic's workflow.
type ActionKind string

// The actions Decide returns. ActStart, ActEnd, ActRound and ActPark change the node runs in the store, and ActRun
// hands a node run to its kind.
const (
	ActStart ActionKind = "start"
	ActEnd   ActionKind = "end"
	ActRound ActionKind = "round"
	ActPark  ActionKind = "park"
	ActRun   ActionKind = "run"
)

// Action is one thing Decide asks for. Start carries the Runs to start and, for a later plan, its Trigger. End carries
// the Ends. Round and Park name the loop at Path and its current Round, and Park its Reason. Run names the node run at
// Path of NodeKind, and for a build child its Task.
type Action struct {
	Kind     ActionKind
	Runs     []store.NodeRun
	Trigger  *store.NodeTrigger
	Ends     []store.NodeEnd
	Path     string
	Round    int64
	Reason   string
	NodeKind string
	Task     int64
}

// The kinds, spent setting, task status and trigger prefix the pass reads.
const (
	kindPlan      = "plan"
	kindBuild     = "build"
	kindLoop      = "loop"
	spentContinue = "continue"
	taskOpen      = "open"
	triggerPrefix = "workflow:"
)

// Decide returns the actions that move flow's node runs on epic forward, given the epic's rows and the registered
// kinds. The walk takes the top-level nodes in order and stops at the first one that has not ended, so the actions are what
// that node needs. Once every top-level node has ended, Decide returns none.
func Decide(flow workflow.Workflow, rows kinds.Rows, reg map[string]kinds.Kind) []Action {
	acts, _ := newPass(rows, reg).walk(flow.Nodes, scope{})
	return acts
}

// pass is one Decide call: the epic's rows, the registered kinds, and the node runs of the rows by path.
type pass struct {
	rows   kinds.Rows
	reg    map[string]kinds.Kind
	byPath map[string]store.NodeRun
}

// newPass returns the pass over rows and reg.
func newPass(rows kinds.Rows, reg map[string]kinds.Kind) pass {
	byPath := make(map[string]store.NodeRun, len(rows.Runs))
	for _, run := range rows.Runs {
		byPath[run.Path] = run
	}
	return pass{rows: rows, reg: reg, byPath: byPath}
}

// scope is where a list of nodes sits: each node's path starts with prefix, and its node runs are in round.
type scope struct {
	prefix string
	round  int64
}

// walk decides the nodes of one list in order. It returns the actions the list needs and whether every node in it has
// ended. The first node that has not ended decides the walk, so no node after it is looked at.
func (p pass) walk(nodes []workflow.Node, at scope) ([]Action, bool) {
	for _, def := range nodes {
		run, ok := p.byPath[at.prefix+def.ID]
		if !ok {
			return []Action{p.start(def, at)}, false
		}
		if run.State == store.NodeEnded {
			continue
		}
		if run.State == store.NodeParked {
			return nil, false
		}
		return p.running(def, run), false
	}
	return nil, true
}

// start returns the action that starts def, which has no run at its path in its scope. A loop starts at its first
// round. A plan starts with Mark 0 and no trigger when the epic has no plan run yet, and otherwise with the highest
// turn seq and a trigger that asks the planner for a turn.
func (p pass) start(def workflow.Node, at scope) Action {
	path := at.prefix + def.ID
	run := store.NodeRun{
		Path: path, ID: store.NodeRunID(p.rows.Epic.ID, path, at.round, "start"), Node: def.ID, Kind: kindOf(def),
		Round: at.round,
	}
	act := Action{Kind: ActStart}
	switch {
	case def.Loop != nil:
		run.LoopRound = 1
	case def.Kind == kindPlan && p.hasPlanRun():
		run.Mark = p.highestTurn()
		act.Trigger = &store.NodeTrigger{
			ID:    triggerPrefix + store.NodeRunID(p.rows.Epic.ID, path, at.round, "trigger"),
			Cause: "workflow node " + path + " started",
		}
	}
	act.Runs = []store.NodeRun{run}
	return act
}

// kindOf returns the kind a node run of def has: its kind, or loop for a loop.
func kindOf(def workflow.Node) string {
	if def.Loop != nil {
		return kindLoop
	}
	return def.Kind
}

// hasPlanRun reports whether any node run of the epic is a plan's.
func (p pass) hasPlanRun() bool {
	for _, run := range p.rows.Runs {
		if run.Kind == kindPlan {
			return true
		}
	}
	return false
}

// highestTurn returns the highest turn seq of the epic, zero when it has no turn.
func (p pass) highestTurn() int64 {
	var highest int64
	for _, turn := range p.rows.Epic.Turns {
		highest = max(highest, turn.Seq)
	}
	return highest
}

// running decides a node run that has not ended. A loop decides its round. Any other node is decided by its
// registered kind, and a node the kind leaves waiting, or whose kind is not registered, gives no action. A plan that
// runs also fans its tasks out under its first build node.
func (p pass) running(def workflow.Node, run store.NodeRun) []Action {
	if def.Loop != nil {
		return p.loop(def, run)
	}
	kind, ok := p.reg[def.Kind]
	if !ok {
		return nil
	}
	act := kind.Decide(kinds.Node{Def: def, Run: run}, p.rows)
	if act.Kind == kinds.ActEnd {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, act.Outcome)}}
	}
	if act.Kind != kinds.ActRun {
		return nil
	}
	acts := []Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}
	if def.Kind == kindPlan {
		acts = append(acts, p.fanOut(def, run)...)
	}
	return acts
}

// loop decides a running loop by its current round. The round's body walks first, and the round ends once every node
// of the body has ended. The loop then ends when its until node ended with the until outcome, moves to the next round
// while rounds remain, and otherwise ends when spent is continue and parks when it is park.
func (p pass) loop(def workflow.Node, run store.NodeRun) []Action {
	body := scope{prefix: fmt.Sprintf("%s@%d/", run.Path, run.LoopRound), round: run.LoopRound}
	acts, done := p.walk(def.Body, body)
	if !done {
		return acts
	}
	until := def.Loop.Until
	if p.byPath[body.prefix+until.Node].Outcome == until.Name {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, "")}}
	}
	rounds := int64(def.Loop.Max) + run.Granted
	if run.LoopRound < rounds {
		return []Action{{Kind: ActRound, Path: run.Path, Round: run.LoopRound}}
	}
	if def.Loop.Spent == spentContinue {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, "")}}
	}
	reason := fmt.Sprintf("loop %s spent its %d rounds without %s.%s", def.ID, rounds, until.Node, until.Name)
	return []Action{{Kind: ActPark, Path: run.Path, Round: run.LoopRound, Reason: reason}}
}

// endsUnder returns the end of the run at path with outcome, followed by an end with no outcome for each running run
// under it.
func (p pass) endsUnder(path, outcome string) []store.NodeEnd {
	ends := []store.NodeEnd{{Path: path, Outcome: outcome}}
	for _, run := range p.rows.Runs {
		if run.State == store.NodeRunning && within(run.Path, path) {
			ends = append(ends, store.NodeEnd{Path: run.Path})
		}
	}
	return ends
}

// within reports whether child sits under the node at path: a child of its plan, or a node of one of its loop's rounds.
func within(child, path string) bool {
	return strings.HasPrefix(child, path+"/") || strings.HasPrefix(child, path+"@")
}

// fanOut returns the fan-out of a running plan run under its first build node. It starts each open task above the
// plan's mark that has no child run, then decides the running children: a run for each that runs and one end for those
// that end.
func (p pass) fanOut(plan workflow.Node, run store.NodeRun) []Action {
	build, ok := firstBuild(plan.Body)
	if !ok {
		return nil
	}
	prefix := run.Path + "/" + build.ID + "/"
	acts := p.startTasks(run, build, prefix)
	return append(acts, p.decideChildren(build, prefix)...)
}

// firstBuild returns the first build node of a plan body, the node its tasks fan out under.
func firstBuild(body []workflow.Node) (workflow.Node, bool) {
	for _, node := range body {
		if node.Kind == kindBuild {
			return node, true
		}
	}
	return workflow.Node{}, false
}

// startTasks returns a start, with its child run, for each open task of a turn above the plan run's mark that has no
// child run under prefix.
func (p pass) startTasks(run store.NodeRun, build workflow.Node, prefix string) []Action {
	var acts []Action
	for _, task := range p.rows.Epic.Tasks {
		if task.Status != taskOpen || task.Turn <= run.Mark {
			continue
		}
		path := fmt.Sprintf("%sT%d", prefix, task.Seq)
		if _, has := p.byPath[path]; has {
			continue
		}
		child := store.NodeRun{
			Path: path, ID: store.NodeRunID(p.rows.Epic.ID, path, run.Round, p.causeOf(task)), Node: build.ID,
			Kind: kindBuild, Task: task.Seq, Round: run.Round,
		}
		acts = append(acts, Action{Kind: ActStart, Runs: []store.NodeRun{child}})
	}
	return acts
}

// causeOf returns the cause a child run of task carries: the task's turn and its 1-based position among the tasks of
// that turn in seq order.
func (p pass) causeOf(task store.EpicTask) string {
	var earlier int64
	for _, other := range p.rows.Epic.Tasks {
		if other.Turn == task.Turn && other.Seq < task.Seq {
			earlier++
		}
	}
	return fmt.Sprintf("turn %d task %d", task.Turn, earlier+1)
}

// decideChildren decides each running child run under prefix with the build kind. A child that runs gives a run for
// it, and the children that end give one end with no outcome. A child whose kind is not registered waits.
func (p pass) decideChildren(build workflow.Node, prefix string) []Action {
	kind, ok := p.reg[build.Kind]
	if !ok {
		return nil
	}
	var acts []Action
	var ends []store.NodeEnd
	for _, child := range p.rows.Runs {
		if child.State != store.NodeRunning || !strings.HasPrefix(child.Path, prefix) {
			continue
		}
		act := kind.Decide(kinds.Node{Def: build, Run: child}, p.rows)
		if act.Kind == kinds.ActRun {
			acts = append(acts, Action{Kind: ActRun, Path: child.Path, NodeKind: build.Kind, Task: child.Task})
		}
		if act.Kind == kinds.ActEnd {
			ends = append(ends, store.NodeEnd{Path: child.Path})
		}
	}
	if len(ends) > 0 {
		acts = append(acts, Action{Kind: ActEnd, Ends: ends})
	}
	return acts
}
