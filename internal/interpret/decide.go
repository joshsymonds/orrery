// Package interpret runs an epic's workflow over its node runs. Decide is its pure pass: it returns the actions that
// move the runs on.
package interpret

import (
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// ActionKind names what the interpreter does for an epic's workflow.
type ActionKind string

// The actions Decide returns. ActStart, ActEnd, ActRound, ActPark, ActParkNode, ActBatch and ActParkEpic change the node
// runs or the epic in the store, ActWork hands a check node run's work to the interpreter, and ActRun hands a node run
// to its kind.
const (
	ActStart    ActionKind = "start"
	ActEnd      ActionKind = "end"
	ActRound    ActionKind = "round"
	ActPark     ActionKind = "park"
	ActParkNode ActionKind = "park_node"
	ActParkEpic ActionKind = "park_epic"
	ActBatch    ActionKind = "batch"
	ActWork     ActionKind = "work"
	ActRun      ActionKind = "run"
)

// Action is one thing Decide asks for. Start carries the Runs to start and, for a later plan, its Trigger. End carries
// the Ends, a failed end in a plan body its Trigger, and an end that builds the epic Built. Round and Park name the loop
// at Path and its current Round, and Park its Reason. Run names the node run at Path of NodeKind, and for a build child
// its Task. ParkNode parks the node run at Path, whose Attempt ended with Outcome and Output, for Reason, and for good
// when Final. Batch ends the integrate run at Path, whose Attempt ended with Outcome and Output, with Trigger. ParkEpic
// parks the epic for Reason, with Trigger. Work carries a check node run's work: Work is the kind's action, prepare,
// start, capture, refs or advance, and Def, Run, IDs, Items and Tasks are what it is for. An advance carries its Output
// and the Trigger its passed batch writes.
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
	Attempt  int64
	Outcome  string
	Output   string
	Work     kinds.ActionKind
	Def      workflow.Node
	Run      store.NodeRun
	IDs      []string
	Items    []string
	Tasks    []int64
	Final    bool
	Built    bool
}

// The kinds, spent setting, task status, outcome and trigger prefix the pass reads.
const (
	kindPlan      = "plan"
	kindBuild     = "build"
	kindLoop      = "loop"
	spentContinue = "continue"
	taskOpen      = "open"
	outcomePassed = "passed"
	outcomeFailed = "failed"
	triggerPrefix = "workflow:"
	kindIntegrate = "integrate"
)

// place is where a list of nodes sits. A check node that ends failed is decided by its place: at the top level it
// parks, in a loop body it ends the round, and in a plan body it wakes the planner.
type place int

const (
	topLevel place = iota
	loopBody
	planBody
)

// Decide returns the actions that move flow's node runs on epic forward, given the epic's rows and the registered
// kinds. The walk takes the top-level nodes in order and stops at the first one that has not ended, so the actions are what
// that node needs. Once every top-level node has ended, Decide returns none.
func Decide(flow workflow.Workflow, rows kinds.Rows, reg map[string]kinds.Kind) []Action {
	acts, _ := newPass(rows, reg).walk(flow.Nodes, scope{where: topLevel})
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

// scope is where a list of nodes sits: each node's path starts with prefix, its node runs are in round, and the list
// is at where.
type scope struct {
	prefix string
	round  int64
	where  place
}

// walk decides the nodes of one list in order. It returns the actions the list needs and whether the list has ended.
// The first node that has not ended decides the walk, so no node after it is looked at. A node that ended failed ends
// the list as well, so a failed check ends the round of its loop.
func (p pass) walk(nodes []workflow.Node, at scope) ([]Action, bool) {
	for _, def := range nodes {
		run, ok := p.byPath[at.prefix+def.ID]
		if !ok {
			return []Action{p.start(def, at)}, false
		}
		switch run.State {
		case store.NodeEnded:
			if run.Outcome == outcomeFailed {
				return nil, true
			}
		case store.NodeParked:
			return nil, false
		default:
			return p.running(def, run, at), false
		}
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

// running decides a node run that has not ended. A loop decides its round, and a plan its settle walks and fan-out. Any
// other node is decided by its registered kind, and a node the kind leaves waiting, or whose kind is not registered,
// gives no action.
func (p pass) running(def workflow.Node, run store.NodeRun, at scope) []Action {
	if def.Loop != nil {
		return p.loop(def, run)
	}
	kind, ok := p.reg[def.Kind]
	if !ok {
		return nil
	}
	act := kind.Decide(kinds.Node{Def: def, Run: run}, p.rows)
	if def.Kind == kindPlan {
		return p.plan(def, run, act)
	}
	switch act.Kind {
	case kinds.ActEnd:
		return p.end(def, run, act, at)
	case kinds.ActPrepare, kinds.ActStart, kinds.ActCapture, kinds.ActRefs, kinds.ActAdvance:
		return []Action{p.work(def, run, act)}
	case kinds.ActPark:
		return []Action{{
			Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: act.Output,
			Reason: act.Output,
		}}
	case kinds.ActRun:
		return []Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}
	default:
		return nil
	}
}

// plan decides a running plan run from the act its kind gives. The plan runs while its kind keeps it running, and it
// ends once its kind ends it and its latest settle walk has ended. Its settle walks and its fan-out are decided with it.
// A plan whose kind leaves it waiting gives no action.
func (p pass) plan(def workflow.Node, run store.NodeRun, act kinds.Action) []Action {
	if act.Kind != kinds.ActEnd && act.Kind != kinds.ActRun {
		return nil
	}
	walks, settled := p.settle(def, run)
	if act.Kind == kinds.ActEnd && settled {
		return p.complete(def, run, act)
	}
	acts := append([]Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}, walks...)
	return append(acts, p.fanOut(def, run)...)
}

// complete decides a plan run whose kind ends it and whose latest settle walk has ended. A plan body that holds an
// integrate node ends built when every Requirement is built. Otherwise it parks the epic, and wakes the planner with a
// trigger whose cause names what is unbuilt. Any other plan ends as it did.
func (p pass) complete(def workflow.Node, run store.NodeRun, act kinds.Action) []Action {
	ends := p.endsUnder(run.Path, act.Outcome, act.Output)
	if !slices.ContainsFunc(def.Body, func(node workflow.Node) bool { return node.Kind == kindIntegrate }) {
		return []Action{{Kind: ActEnd, Ends: ends}}
	}
	missing := kinds.Unbuilt(p.rows)
	if len(missing) == 0 {
		return []Action{{Kind: ActEnd, Ends: ends, Built: true}}
	}
	reason := "epic not built: " + strings.Join(missing, "; ")
	id := store.NodeRunID(p.rows.Epic.ID, run.Path, run.Round, fmt.Sprintf("unbuilt after turn %d", p.highestTurn()))
	trigger := &store.NodeTrigger{ID: triggerPrefix + id, Cause: reason}
	return []Action{{Kind: ActParkEpic, Reason: reason, Trigger: trigger}}
}

// settle decides the settle walks of a running plan run, and reports whether its latest settle walk has ended. A settle
// point is the highest seq among the tasks of turns above the plan's mark. Its walk runs the plan body's nodes that are
// not build nodes, under <plan path>/S<seq>/. The latest walk starts only when no task of the epic is open. Every walk
// already started is decided on each pass, so an older walk finishes while a newer settle point waits for its tasks.
func (p pass) settle(def workflow.Node, run store.NodeRun) ([]Action, bool) {
	latest, ok := p.settlePoint(run.Mark)
	if !ok {
		return nil, true
	}
	nodes := slices.DeleteFunc(slices.Clone(def.Body), func(node workflow.Node) bool {
		return node.Kind == kindBuild
	})
	var acts []Action
	for _, task := range p.rows.Epic.Tasks {
		prefix := settlePrefix(run.Path, task.Seq)
		if task.Turn <= run.Mark || task.Seq == latest || !p.hasRunUnder(prefix) {
			continue
		}
		walked, _ := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
		acts = append(acts, walked...)
	}
	prefix := settlePrefix(run.Path, latest)
	if !p.hasRunUnder(prefix) && p.anyTaskOpen() {
		return acts, false
	}
	walked, done := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
	return append(acts, walked...), done
}

// settlePoint returns the highest seq among the tasks of turns above mark, and whether there is one.
func (p pass) settlePoint(mark int64) (int64, bool) {
	var highest int64
	found := false
	for _, task := range p.rows.Epic.Tasks {
		if task.Turn > mark && (!found || task.Seq > highest) {
			highest, found = task.Seq, true
		}
	}
	return highest, found
}

// settlePrefix returns the prefix of the settle walk at seq of the plan at path.
func settlePrefix(path string, seq int64) string {
	return fmt.Sprintf("%s/S%d/", path, seq)
}

// anyTaskOpen reports whether any task of the epic is open.
func (p pass) anyTaskOpen() bool {
	return slices.ContainsFunc(p.rows.Epic.Tasks, func(task store.EpicTask) bool { return task.Status == taskOpen })
}

// hasRunUnder reports whether a node run's path starts with prefix.
func (p pass) hasRunUnder(prefix string) bool {
	for _, run := range p.rows.Runs {
		if strings.HasPrefix(run.Path, prefix) {
			return true
		}
	}
	return false
}

// end decides a node run its kind ends. An integrate run's end is its batch. A failed end is decided by where the run
// sits. Any other end ends the run with its outcome and output, and the running runs under it.
func (p pass) end(def workflow.Node, run store.NodeRun, act kinds.Action, at scope) []Action {
	if def.Kind == kindIntegrate {
		return []Action{p.batch(run, act)}
	}
	if act.Outcome == outcomeFailed {
		return p.failed(def, run, act.Output, at)
	}
	return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, act.Outcome, act.Output)}}
}

// failed decides a check node run that ended failed with output, by where it sits. At the top level it parks, so the
// flow holds at it, and an evidence node at base that found a Requirement met parks for good. In a loop body it ends
// with its output, and the round ends. In a plan body it ends with a trigger that wakes the planner. The trigger's id is
// derived from the run's path, round and attempt, so a restart records it once.
func (p pass) failed(def workflow.Node, run store.NodeRun, output string, at scope) []Action {
	reason := fmt.Sprintf("workflow node %s failed: %s", run.Path, output)
	switch at.where {
	case loopBody:
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, outcomeFailed, output)}}
	case planBody:
		id := store.NodeRunID(p.rows.Epic.ID, run.Path, run.Round, fmt.Sprintf("failed attempt %d", run.Attempt))
		trigger := &store.NodeTrigger{ID: triggerPrefix + id, Cause: reason}
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, outcomeFailed, output), Trigger: trigger}}
	default:
		if met := p.metAtBase(def, run); len(met) > 0 {
			return []Action{{
				Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: output,
				Reason: fmt.Sprintf(
					"workflow node %s: %s met at the epic's base %s; parked for good, "+
						"and a corrected contract is a new epic",
					run.Path, strings.Join(met, ", "), p.rows.Epic.Base,
				),
				Final: true,
			}}
		}
		return []Action{{
			Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: output,
			Reason: reason,
		}}
	}
}

// metAtBase returns the Requirements that an evidence node at base found met at the epic's base, in contract order. It
// returns none for any other node run.
func (p pass) metAtBase(def workflow.Node, run store.NodeRun) []string {
	if def.Kind != kindEvidence || def.Settings.At != atBase {
		return nil
	}
	return kinds.MetAtBase(kinds.Node{Def: def, Run: run}, p.rows)
}

// batch decides the end of an integrate run. An empty batch ends with no revision, since it claimed no batch, and it
// writes no trigger. Any other batch ends with a trigger that wakes the planner.
func (p pass) batch(run store.NodeRun, act kinds.Action) Action {
	out := Action{Kind: ActBatch, Path: run.Path, Attempt: run.Attempt, Outcome: act.Outcome, Output: act.Output}
	if run.Revision != "" {
		out.Trigger = p.batchTrigger(run, act.Outcome, act.Output)
	}
	return out
}

// batchTrigger returns the trigger an integrate run writes when it ends with outcome and output. Its id is derived from
// the run's path, round and attempt, so a restart records it once.
func (p pass) batchTrigger(run store.NodeRun, outcome, output string) *store.NodeTrigger {
	id := store.NodeRunID(p.rows.Epic.ID, run.Path, run.Round, fmt.Sprintf("integrated attempt %d", run.Attempt))
	return &store.NodeTrigger{
		ID:    triggerPrefix + id,
		Cause: fmt.Sprintf("workflow node %s %s: %s", run.Path, outcome, output),
	}
}

// work returns the work action of a check node run: the kind's prepare, start, capture, refs or advance, and the ids,
// items and tasks the work is for. An advance also carries its output and the trigger its passed batch writes.
func (p pass) work(def workflow.Node, run store.NodeRun, act kinds.Action) Action {
	out := Action{
		Kind: ActWork, Work: act.Kind, Path: run.Path, NodeKind: def.Kind, Def: def, Run: run, IDs: act.IDs,
		Items: act.Items, Tasks: act.Tasks,
	}
	if act.Kind == kinds.ActAdvance {
		out.Output = act.Output
		out.Trigger = p.batchTrigger(run, outcomePassed, act.Output)
	}
	return out
}

// loop decides a running loop by its current round. The round's body walks first, and the round ends once every node
// of the body has ended. The loop then ends when its until node ended with the until outcome, moves to the next round
// while rounds remain, and otherwise ends when spent is continue and parks when it is park.
func (p pass) loop(def workflow.Node, run store.NodeRun) []Action {
	body := scope{prefix: fmt.Sprintf("%s@%d/", run.Path, run.LoopRound), round: run.LoopRound, where: loopBody}
	acts, done := p.walk(def.Body, body)
	if !done {
		return acts
	}
	until := def.Loop.Until
	if p.byPath[body.prefix+until.Node].Outcome == until.Name {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, "", "")}}
	}
	rounds := int64(def.Loop.Max) + run.Granted
	if run.LoopRound < rounds {
		return []Action{{Kind: ActRound, Path: run.Path, Round: run.LoopRound}}
	}
	if def.Loop.Spent == spentContinue {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, "", "")}}
	}
	reason := fmt.Sprintf("loop %s spent its %d rounds without %s.%s", def.ID, rounds, until.Node, until.Name)
	return []Action{{Kind: ActPark, Path: run.Path, Round: run.LoopRound, Reason: reason}}
}

// endsUnder returns the end of the run at path with outcome and output, followed by an end with no outcome for each
// running run under it.
func (p pass) endsUnder(path, outcome, output string) []store.NodeEnd {
	ends := []store.NodeEnd{{Path: path, Outcome: outcome, Output: output}}
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
// plan's mark that has no child run, unless a batch holds the fan-out, then decides the running children: a run for
// each that runs and one end for those that end.
func (p pass) fanOut(plan workflow.Node, run store.NodeRun) []Action {
	build, ok := firstBuild(plan.Body)
	if !ok {
		return nil
	}
	prefix := run.Path + "/" + build.ID + "/"
	var acts []Action
	if !p.batchOpen(run.Path) {
		acts = p.startTasks(run, build, prefix)
	}
	return append(acts, p.decideChildren(build, prefix)...)
}

// batchOpen reports whether an integrate run under the plan at path is running or parked. While one is, the plan starts
// no task, so no task starts until its batch is green or red.
func (p pass) batchOpen(path string) bool {
	return slices.ContainsFunc(p.rows.Runs, func(run store.NodeRun) bool {
		return run.Kind == kindIntegrate && within(run.Path, path) &&
			(run.State == store.NodeRunning || run.State == store.NodeParked)
	})
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
