// Package interpret runs an epic's workflow over its node runs. Decide is its pure pass: it returns the actions that
// move the runs on.
package interpret

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// ActionKind names what the interpreter does for an epic's workflow.
type ActionKind string

// The actions Decide returns. ActStart, ActEnd, ActRound, ActPark, ActParkNode, ActBatch, ActParkEpic and ActRebuild
// change the node runs or the epic in the store, and so do ActHold, ActReview, ActClosure and ActConclude for the
// review. ActWork hands a check or release node run's work to the interpreter, and ActRun hands a node run to its kind.
// ActGapRound starts the gap round of a conclusion run, which gives each live gap a planner turn. ActRerun ends a rerun
// of a conclusion run's review, the run that runs a reviewer again, with the verdict and findings it settled.
// ActRebuild starts the ended build run of an open task again, as a send_back that reopens a done task needs.
// ActGapTask ends an open task as a gap, as its build kind does when a dependency of the task is lost, so the task
// never starts.
const (
	ActStart    ActionKind = "start"
	ActEnd      ActionKind = "end"
	ActRound    ActionKind = "round"
	ActPark     ActionKind = "park"
	ActParkNode ActionKind = "park_node"
	ActParkEpic ActionKind = "park_epic"
	ActBatch    ActionKind = "batch"
	ActHold     ActionKind = "hold"
	ActReview   ActionKind = "review"
	ActClosure  ActionKind = "closure"
	ActConclude ActionKind = "conclude"
	ActGapRound ActionKind = "gap_round"
	ActRerun    ActionKind = "rerun"
	ActWork     ActionKind = "work"
	ActRun      ActionKind = "run"
	ActRebuild  ActionKind = "rebuild"
	ActGapTask  ActionKind = "gap_task"
)

// Action is one thing Decide asks for. Start carries the Runs to start and, for a later plan, its Trigger. End carries
// the Ends, a failed end in a plan body its Trigger, and an end that builds the epic Built. Round and Park name the
// loop at Path and its current Round, and Park its Reason. Run names the node run at Path of NodeKind, and for a build
// child its Task. ParkNode parks the node run at Path, whose Attempt ended with Outcome and Output, for Reason. An end
// that carries Tell also tells the owner it, as an evidence node at base does when it found Requirements met at the
// base and ends passed, so the epic goes on. Batch ends the integrate run Run at Path, whose Attempt ended with Outcome
// and Output, with Trigger. ParkEpic parks the epic for Reason, with Trigger. Work carries a check or release node
// run's work: Work is the kind's action, prepare, start, capture, repro, refs, advance, take, integrator, step, judge,
// record, stop, ending, free, replan or resume, and Def, Run, IDs, Items, Tasks and Units are what it is for. An
// advance and a stop carry their Output, an ending its Outcome, an integrator the failure its round mends as its
// Output, an integrate run's advance the Trigger its passed batch writes, a replan the plan run it starts as its Runs,
// and the Trigger that wakes the planner and the tell as its Reason. Hold pauses the epic for the review step IDs[0],
// stopped for Reason. Review ends the review run at Path, whose Attempt ended with Output, Verdicts and Ledger. Closure
// ends the closure run at Path, whose Attempt ended with Outcome and Output, with ReviewRound and Closures. Conclude
// ends the conclusion run at Path, whose Attempt ended with Phase, Output and Question. GapRound starts the gap round
// of the conclusion run at Path: the plan run in Runs, a trigger per live gap in Triggers, and a gap turn per live gap
// in GapTurns. Rerun ends the rerun run at Path, whose Attempt ended passed with Output, Verdicts and Ledger: the one
// verdict and the findings its reviewer's walk settled. Rebuild turns the build run at Path of Task, which ended at
// Attempt, running at its next attempt. GapTask ends the task Task, whose stored build is Build, as a gap for Reason.
type Action struct {
	Kind        ActionKind
	Runs        []store.NodeRun
	Trigger     *store.NodeTrigger
	Triggers    []store.NodeTrigger
	Ends        []store.NodeEnd
	Path        string
	Round       int64
	Reason      string
	NodeKind    string
	Task        int64
	Build       store.TaskBuild
	Attempt     int64
	Outcome     string
	Output      string
	Work        kinds.ActionKind
	Def         workflow.Node
	Run         store.NodeRun
	IDs         []string
	Items       []string
	Tasks       []int64
	Units       []kinds.Unit
	Verdicts    []store.ReviewVerdict
	Ledger      []store.LedgerFinding
	ReviewRound store.ReviewRound
	Closures    []store.Closure
	Phase       string
	Tell        string
	Built       bool
	GapTurns    []store.GapTurn
	Question    store.GapsQuestion
}

// The kinds, spent setting, task status, outcome and trigger prefix the pass reads.
const (
	kindPlan      = "plan"
	kindBuild     = "build"
	kindLoop      = "loop"
	spentContinue = "continue"
	taskOpen      = "open"
	taskDone      = "done"
	outcomePassed = "passed"
	outcomeFailed = "failed"
	triggerPrefix = "workflow:"
	kindIntegrate = "integrate"
	kindRelease   = "release"
)

// The kinds of the end-of-epic review nodes. Each ends with an action of its own.
const (
	reviewNodeKind   = "review"
	closureNodeKind  = "closure"
	concludeNodeKind = "conclude"
)

// The phase an epic is in while its review's ledger is corrected, and the prefix of the trigger a plan that starts then
// writes in place of the started one.
const (
	ledgerPhase         = "correcting"
	ledgerTriggerPrefix = "ledger:"
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
// kinds. An orphaned run, one running under a run that has ended (orphans), is decided first, and while one runs the
// walk waits. Otherwise the walk takes the top-level nodes in order and stops at the first one that has not ended, so
// the actions are what that node needs. Once every top-level node has ended, Decide returns none.
func Decide(flow workflow.Workflow, rows kinds.Rows, reg map[string]kinds.Kind) []Action {
	walk := newPass(rows, reg, flow)
	if acts, orphaned := walk.orphans(); orphaned {
		return acts
	}
	acts, _ := walk.walk(flow.Nodes, scope{where: topLevel})
	return acts
}

// Rests reports whether Decide gives flow no action on any rows that hold rows.Epic's phase and rows.Runs, read from
// the runs' Path, State, Outcome and Attempt alone: no run is orphaned, and the walk of the top-level nodes stops at a
// node that ended failed, at a parked node, or at the end of the list, or else at a running node that its kind gives no
// action whatever the epic's other rows hold (idle). An epic at rest has nothing to decide until one of its node runs or
// its phase changes.
func Rests(flow workflow.Workflow, rows kinds.Rows, reg map[string]kinds.Kind) bool {
	walk := newPass(rows, reg, flow)
	if len(walk.orphaned()) > 0 {
		return false
	}
	def, run, halt := walk.frontier(flow.Nodes, scope{where: topLevel})
	switch halt {
	case haltEnded, haltParked:
		return true
	case haltRunning:
		return walk.idle(def, run)
	default:
		return false
	}
}

// pass is one Decide call: the epic's rows, the registered kinds, the node runs of the rows by path, and the workflow
// being walked, whose plan a release's replan runs (replanPlan).
type pass struct {
	rows   kinds.Rows
	reg    map[string]kinds.Kind
	byPath map[string]store.NodeRun
	flow   workflow.Workflow
}

// newPass returns the pass over rows and reg, walking flow.
func newPass(rows kinds.Rows, reg map[string]kinds.Kind, flow workflow.Workflow) pass {
	byPath := make(map[string]store.NodeRun, len(rows.Runs))
	for _, run := range rows.Runs {
		byPath[run.Path] = run
	}
	return pass{rows: rows, reg: reg, byPath: byPath, flow: flow}
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
	def, run, halt := p.frontier(nodes, at)
	switch halt {
	case haltUnstarted:
		return []Action{p.start(def, at)}, false
	case haltParked:
		return nil, false
	case haltRunning:
		return p.running(def, run, at), false
	default:
		return nil, true
	}
}

// halt is where the walk of a list of nodes stops: at the list's end or a node that ended failed, which ends the list,
// at a parked node, at a node with no run, or at a running node.
type halt int

const (
	haltEnded halt = iota
	haltParked
	haltUnstarted
	haltRunning
)

// frontier returns the node of nodes, in scope at, that the walk stops at, its run, and why it stops there: the first
// node with no run, or whose run is running or parked, or that ended failed. A list whose every node ended stops at
// its end, with no node.
func (p pass) frontier(nodes []workflow.Node, at scope) (workflow.Node, store.NodeRun, halt) {
	for _, def := range nodes {
		run, ok := p.byPath[at.prefix+def.ID]
		if !ok {
			return def, store.NodeRun{}, haltUnstarted
		}
		switch run.State {
		case store.NodeEnded:
			if run.Outcome == outcomeFailed {
				return def, run, haltEnded
			}
		case store.NodeParked:
			return def, run, haltParked
		default:
			return def, run, haltRunning
		}
	}
	return workflow.Node{}, store.NodeRun{}, haltEnded
}

// start returns the action that starts def, which has no run at its path in its scope. A loop starts at its first
// round. A plan starts with Mark 0 and no trigger when the epic has no plan run yet, and otherwise with the highest
// turn seq and the trigger that asks the planner for a turn.
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
		act.Trigger = p.planTrigger(path, at.round)
	}
	act.Runs = []store.NodeRun{run}
	return act
}

// planTrigger returns the trigger that asks the planner for a turn when a plan starts at path in round. While the
// epic is correcting, it is the ledger trigger, which wakes the planner with the round's open findings. Its id is
// derived from the path and round, so a restart writes it once. Otherwise it names the start.
func (p pass) planTrigger(path string, round int64) *store.NodeTrigger {
	if p.rows.Epic.Phase == ledgerPhase {
		return &store.NodeTrigger{
			ID:    ledgerTriggerPrefix + store.NodeRunID(p.rows.Epic.ID, path, round, "ledger"),
			Cause: kinds.LedgerCause(p.rows.Review, round),
		}
	}
	return &store.NodeTrigger{
		ID:    triggerPrefix + store.NodeRunID(p.rows.Epic.ID, path, round, "trigger"),
		Cause: "workflow node " + path + " started",
	}
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

// idle reports whether running gives def's run, which is running, no action whatever the epic's rows hold beyond its
// phase and node runs: def is no loop, and its kind is not registered, or its kind is idle in the epic's phase
// (kinds.Idler) and, for a release, no replan plan of the run's attempt runs (replanRun).
func (p pass) idle(def workflow.Node, run store.NodeRun) bool {
	if def.Loop != nil {
		return false
	}
	kind, ok := p.reg[def.Kind]
	if !ok {
		return true
	}
	if def.Kind == kindRelease {
		if _, _, replanning := p.replanRun(run); replanning {
			return false
		}
	}
	idler, ok := kind.(kinds.Idler)
	return ok && idler.Idle(p.rows.Epic.Phase)
}

// planning decides a running release or conclusion whose plan runs: a release whose replan plan runs (replanning), and
// a conclusion before its kind decides (concluding). It reports false for any other node, and for a release or
// conclusion that its kind decides.
func (p pass) planning(def workflow.Node, run store.NodeRun) ([]Action, bool) {
	switch def.Kind {
	case kindRelease:
		return p.replanning(run)
	case concludeNodeKind:
		return p.concluding(run)
	}
	return nil, false
}

// running decides a node run that has not ended. A loop decides its round, and a plan its settle walks and fan-out. A
// release or conclusion whose plan runs decides that plan (planning). An idle node gives no action. Any other node is
// decided by its registered kind, and a node the kind leaves waiting gives no action.
func (p pass) running(def workflow.Node, run store.NodeRun, at scope) []Action {
	if def.Loop != nil {
		return p.loop(def, run)
	}
	kind := p.reg[def.Kind]
	if acts, planning := p.planning(def, run); planning {
		return acts
	}
	if p.idle(def, run) {
		return nil
	}
	act := kind.Decide(kinds.Node{Def: def, Run: run}, p.rows)
	if def.Kind == kindPlan {
		return p.plan(def, run, act)
	}
	switch act.Kind {
	case kinds.ActEnd:
		return p.end(def, run, act, at)
	case kinds.ActPrepare, kinds.ActStart, kinds.ActCapture, kinds.ActRepro, kinds.ActRefs, kinds.ActAdvance,
		kinds.ActTake, kinds.ActIntegrator, kinds.ActStep, kinds.ActJudge, kinds.ActRecord, kinds.ActStop,
		kinds.ActEnding, kinds.ActFree, kinds.ActResume, kinds.ActTaskIntegrator:
		return []Action{p.work(def, run, act)}
	case kinds.ActReplan:
		return []Action{p.replan(def, run, act)}
	case kinds.ActPark:
		return []Action{{
			Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: act.Output,
			Reason: act.Output,
		}}
	case kinds.ActHold:
		return []Action{{Kind: ActHold, Path: run.Path, IDs: act.IDs, Reason: act.Output}}
	case kinds.ActGapRound:
		return []Action{p.gapRound(run, act)}
	case kinds.ActRun:
		return []Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}
	default:
		return nil
	}
}

// plan decides a running plan run from the act its kind gives. The plan runs while its kind keeps it running, and it
// ends once its kind ends it, its walks have settled (settle) and no run under it other than a build child runs
// (bodyRunning). Its walks and its fan-out are decided with it. A plan whose kind leaves it waiting gives no action.
func (p pass) plan(def workflow.Node, run store.NodeRun, act kinds.Action) []Action {
	if act.Kind != kinds.ActEnd && act.Kind != kinds.ActRun {
		return nil
	}
	walks, settled := p.settle(def, run)
	if act.Kind == kinds.ActEnd && settled && !p.bodyRunning(run.Path) {
		return p.complete(def, run, act)
	}
	acts := append([]Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}, walks...)
	return append(acts, p.fanOut(def, run)...)
}

// bodyRunning reports whether a run under the plan run at path other than a build child is running: a node of a walk,
// such as an integrate run under an ended walk that an owner's retry set running again. The plan does not end while one
// runs, so that run ends under its plan, never cut by the plan's end. A build child's run follows its task, which an
// open task already holds the plan for, and the plan's end ends it.
func (p pass) bodyRunning(path string) bool {
	return slices.ContainsFunc(p.rows.Runs, func(run store.NodeRun) bool {
		return run.State == store.NodeRunning && run.Kind != kindBuild && within(run.Path, path)
	})
}

// complete decides a plan run whose kind ends it and whose walks have settled. A plan body that holds an
// integrate node ends built when every Requirement is built or held by a task that ended as a gap, so a plan whose only
// unbuilt Requirements are gapped ends built and does not park. Otherwise it parks the epic, and wakes the planner with
// a trigger whose cause names what is unbuilt. Any other plan ends as it did.
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

// settle decides the walks of a running plan run, and reports whether the plan has settled: no walk is unfinished and
// no done task of the plan is unclaimed. A walk runs the plan body's nodes that are not build nodes, under its prefix:
// the settle walk of a task (settlePrefix), or its late walk (latePrefix) at each of its reopen generations. Every walk
// already started is decided on each pass, so each one ends. A new walk starts only when no walk of the plan is
// unfinished, no integrate run under the plan is running or parked (integrating), and some done task of the plan
// (kinds.PlanTask) is claimed by no batch (unclaimedTask). Whether that task is open or building does not matter. The
// new walk runs under the settle walk of that task, or under its late walk when the settle walk already has a run, and
// a walk never starts under a prefix that already has a run. Its integrate node takes every done task no batch has
// claimed, so one batch at a time integrates whatever is done. A plan body with no integrate node has no batch to claim
// a task, so it has no late walk, and it settles once its walks have ended.
func (p pass) settle(def workflow.Node, run store.NodeRun) ([]Action, bool) {
	nodes := slices.DeleteFunc(slices.Clone(def.Body), func(node workflow.Node) bool {
		return node.Kind == kindBuild
	})
	integrates := slices.ContainsFunc(def.Body, func(node workflow.Node) bool { return node.Kind == kindIntegrate })
	var acts []Action
	ended := true
	for _, task := range p.rows.Epic.Tasks {
		if !kinds.PlanTask(task, run, p.rows.Runs) {
			continue
		}
		prefixes := []string{settlePrefix(run.Path, task.Seq)}
		for generation := int64(0); generation <= task.Generation; generation++ {
			prefixes = append(prefixes, latePrefix(run.Path, task.Seq, generation))
		}
		for _, prefix := range prefixes {
			if !p.hasRunUnder(prefix) {
				continue
			}
			walked, done := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
			acts = append(acts, walked...)
			ended = ended && done
		}
	}
	task, unclaimed := p.unclaimedTask(run)
	if unclaimed && ended && !p.integrating(run.Path) {
		prefix := settlePrefix(run.Path, task.Seq)
		if integrates && p.hasRunUnder(prefix) {
			prefix = latePrefix(run.Path, task.Seq, task.Generation)
		}
		if !p.hasRunUnder(prefix) {
			walked, done := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
			acts = append(acts, walked...)
			ended = done
		}
	}
	return acts, ended && (!unclaimed || !integrates)
}

// latePrefix returns the prefix of the late walk of task seq at reopen generation generation of the plan at path:
// <plan path>/L<seq>/ at generation 0, and <plan path>/L<seq>g<generation>/ after a send_back, so each reopen of the
// task walks under its own prefix and never behind a walk an earlier generation ran.
func latePrefix(path string, seq, generation int64) string {
	if generation == 0 {
		return fmt.Sprintf("%s/L%d/", path, seq)
	}
	return fmt.Sprintf("%s/L%dg%d/", path, seq, generation)
}

// settlePrefix returns the prefix of the settle walk at seq of the plan at path, <plan path>/S<seq>/: the walk of the
// task of that seq, which starts once the task is done and no batch claims it.
func settlePrefix(path string, seq int64) string {
	return fmt.Sprintf("%s/S%d/", path, seq)
}

// unclaimedTask returns the done task of the plan run with the highest seq that no batch claims (kinds.PlanTask), and
// whether there is one. A task that a send_back reopened has no claim, and neither has a task done since the last batch
// claimed its tasks.
func (p pass) unclaimedTask(run store.NodeRun) (store.EpicTask, bool) {
	var found store.EpicTask
	ok := false
	for _, task := range p.rows.Epic.Tasks {
		if kinds.PlanTask(task, run, p.rows.Runs) && task.Status == taskDone && task.Batch == "" &&
			(!ok || task.Seq > found.Seq) {
			found, ok = task, true
		}
	}
	return found, ok
}

// integrating reports whether an integrate run under the plan at path is running or parked. While one is, no new walk
// of the plan starts, so one batch runs at a time.
func (p pass) integrating(path string) bool {
	return slices.ContainsFunc(p.rows.Runs, func(run store.NodeRun) bool {
		return run.Kind == kindIntegrate && within(run.Path, path) &&
			(run.State == store.NodeRunning || run.State == store.NodeParked)
	})
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

// end decides a node run its kind ends. An integrate run's end is its batch. A review, closure or conclusion ends with
// its own action, whatever its outcome and wherever it sits. A failed end is decided by where the run sits. Any other
// end ends the run with its outcome and output, and the running runs under it.
func (p pass) end(def workflow.Node, run store.NodeRun, act kinds.Action, at scope) []Action {
	switch def.Kind {
	case kindIntegrate:
		return []Action{p.batch(run, act)}
	case reviewNodeKind:
		return []Action{{
			Kind: ActReview, Path: run.Path, Attempt: run.Attempt, Output: act.Output, Verdicts: act.Verdicts,
			Ledger: act.Ledger,
		}}
	case closureNodeKind:
		return []Action{{
			Kind: ActClosure, Path: run.Path, Attempt: run.Attempt, Outcome: act.Outcome, Output: act.Output,
			ReviewRound: act.Round, Closures: act.Closures,
		}}
	case concludeNodeKind:
		return []Action{{
			Kind: ActConclude, Path: run.Path, Attempt: run.Attempt, Phase: act.Phase, Output: act.Output,
			Question: act.Question,
		}}
	}
	if act.Outcome == outcomeFailed {
		return p.failed(def, run, act.Output, at)
	}
	return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, act.Outcome, act.Output)}}
}

// failed decides a check node run that ended failed with output, by where it sits. At the top level an evidence node at
// base whose red runs judged Requirements holding at base ends passed, with a tell naming them, and the epic goes on.
// Any other failure at the top level parks, so the flow holds at it. In a loop body it ends with its output, and the
// round ends. In a plan body it ends with a trigger that wakes the planner. The trigger's id is derived from the run's
// path, round and attempt, so a restart records it once.
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
			tell := metAtBaseTell(run.Path, met, p.rows.Epic.Base)
			return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, outcomePassed, tell), Tell: tell}}
		}
		return []Action{{
			Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: output,
			Reason: reason,
		}}
	}
}

// metAtBaseTell returns the tell an evidence node at base writes when its red runs judged the Requirements met holding
// at base. It names the node, the Requirements and the base, and that they are recorded as met there while the epic
// goes on.
func metAtBaseTell(path string, met []string, base string) string {
	return fmt.Sprintf(
		"workflow node %s: %s met at the epic's base %s, recorded as met there, and the epic goes on",
		path, strings.Join(met, ", "), base,
	)
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
// writes no trigger. A failed batch ends with a trigger that wakes the planner. A passed batch ends with one only when
// it leaves no work behind (leavesWork), so the planner is not woken while a task of the epic is open or unintegrated.
func (p pass) batch(run store.NodeRun, act kinds.Action) Action {
	out := Action{
		Kind: ActBatch, Path: run.Path, Attempt: run.Attempt, Outcome: act.Outcome, Output: act.Output, Run: run,
	}
	if run.Revision != "" && (act.Outcome != outcomePassed || !p.leavesWork(run)) {
		out.Trigger = p.batchTrigger(run, act.Outcome, act.Output)
	}
	return out
}

// leavesWork reports whether a passed integrate run at run's path leaves work behind: a task of the epic is open, or
// unintegrated (see unintegrated). A task that ended as a gap or failed leaves nothing behind.
func (p pass) leavesWork(run store.NodeRun) bool {
	return slices.ContainsFunc(p.rows.Epic.Tasks, func(task store.EpicTask) bool {
		return task.Status == taskOpen || unintegrated(task, run)
	})
}

// unintegrated reports whether task is done and not integrated once the integrate run at run's path passes: no batch
// claims it, or run claims it and run's result does not compose it.
func unintegrated(task store.EpicTask, run store.NodeRun) bool {
	if task.Status != taskDone {
		return false
	}
	return task.Batch == "" || (task.Batch == run.Path && !composedBy(run, task.Seq))
}

// composition is the part of an integrate run's result composedBy reads: the tasks its batch composed into commits.
type composition struct {
	Composed []composedSeq `json:"composed"`
}

// composedSeq is one task a batch composed, by its seq.
type composedSeq struct {
	Task int64 `json:"task"`
}

// composedBy reports whether the result of run composed task seq into a commit, as the integrate kind and the store
// read it. A result that is not a batch result composed none.
func composedBy(run store.NodeRun, seq int64) bool {
	var result composition
	if json.Unmarshal([]byte(run.Result), &result) != nil {
		return false
	}
	return slices.ContainsFunc(result.Composed, func(composed composedSeq) bool { return composed.Task == seq })
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

// work returns the work action of a check or release node run: the kind's action, and the ids, items, tasks,
// units, output, outcome and reason the work is for. An integrate run's advance also carries the trigger its passed
// batch writes, only when the batch leaves no work behind (leavesWork). An action that names a revision runs at it: the
// run it carries holds that revision in place of its own.
func (p pass) work(def workflow.Node, run store.NodeRun, act kinds.Action) Action {
	if act.Revision != "" {
		run.Revision = act.Revision
	}
	out := Action{
		Kind: ActWork, Work: act.Kind, Path: run.Path, NodeKind: def.Kind, Def: def, Run: run, IDs: act.IDs,
		Items: act.Items, Tasks: act.Tasks, Units: act.Units, Outcome: act.Outcome, Output: act.Output,
		Reason: act.Reason,
	}
	if act.Kind == kinds.ActAdvance && def.Kind == kindIntegrate && !p.leavesWork(run) {
		out.Trigger = p.batchTrigger(run, outcomePassed, act.Output)
	}
	return out
}

// loop decides a running loop by its current round. The round's body walks first, and the round ends once every node
// of the body has ended. The loop then ends when its until node ended with the until outcome. A loop that names no max
// moves to the next round while the round's plan planned a task, and otherwise ends with no outcome. A loop with a max
// moves to the next round while rounds remain, and otherwise ends when spent is continue and parks when it is park.
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
	if def.Loop.Max == 0 {
		if p.planned(body.prefix, def.Body) {
			return []Action{{Kind: ActRound, Path: run.Path, Round: run.LoopRound}}
		}
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

// planned reports whether a plan node of a loop body planned a task in the round at prefix: its run holds a task of its
// own (kinds.PlanTask), one of a turn above the mark it started at or a reopened task of an earlier turn it rebuilt. A
// plan node with no run in the round planned none.
func (p pass) planned(prefix string, body []workflow.Node) bool {
	for _, node := range body {
		run, ran := p.byPath[prefix+node.ID]
		if node.Kind != kindPlan || !ran {
			continue
		}
		if slices.ContainsFunc(p.rows.Epic.Tasks, func(task store.EpicTask) bool {
			return kinds.PlanTask(task, run, p.rows.Runs)
		}) {
			return true
		}
	}
	return false
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
// plan's mark that has no child run and rebuilds each whose child run ended, whether or not an integrate run is open,
// then decides the running children: a run for each that runs and one end for those that end.
func (p pass) fanOut(plan workflow.Node, run store.NodeRun) []Action {
	build, ok := firstBuild(plan.Body)
	if !ok {
		return nil
	}
	prefix := run.Path + "/" + build.ID + "/"
	return append(p.startTasks(run, build, prefix), p.decideChildren(build, prefix)...)
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
// child run under prefix, and a rebuild for each such task whose child run ended. A send_back that reopens a done task
// leaves its child run ended, and the run's path is stored, so the task builds again only once its run is rebuilt. An
// open task of a turn at or below the mark that a send_back reopened is started too: the plan run that built it first
// has ended, since no plan ends while a task is open, so the running plan builds it, under its own prefix, and the
// ended child of the earlier plan is kept as it ended. Any other task of those turns was the earlier plan's, and is
// never started again.
func (p pass) startTasks(run store.NodeRun, build workflow.Node, prefix string) []Action {
	var acts []Action
	for _, task := range p.rows.Epic.Tasks {
		if task.Status != taskOpen || (task.Turn <= run.Mark && task.Generation == 0) {
			continue
		}
		path := fmt.Sprintf("%sT%d", prefix, task.Seq)
		if stored, has := p.byPath[path]; has {
			if stored.Kind == kindBuild && stored.State == store.NodeEnded {
				acts = append(acts, Action{Kind: ActRebuild, Path: path, Task: task.Seq, Attempt: stored.Attempt})
			}
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
// it, a child whose task its kind gives a gap for gives a gap task of that task, and the children that end give one end
// with no outcome. A child whose kind is not registered waits.
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
		if act.Kind == kinds.ActGap {
			acts = append(acts, Action{
				Kind: ActGapTask, Path: child.Path, Task: child.Task, Build: p.buildOf(child.Task), Reason: act.Reason,
			})
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

// buildOf returns the stored build of the task with seq among the epic's tasks, and the empty build when there is none.
func (p pass) buildOf(seq int64) store.TaskBuild {
	i := slices.IndexFunc(p.rows.Epic.Tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		return store.TaskBuild{}
	}
	return p.rows.Epic.Tasks[i].Build
}
