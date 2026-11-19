// Package interpret runs an epic's workflow over its node runs. Decide is its pure pass: it returns the actions that
// move the runs on.
package interpret

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// ActionKind names what the interpreter does for an epic's workflow.
type ActionKind string

// The actions Decide returns. ActStart, ActEnd, ActRound, ActPark, ActParkNode, ActBatch, ActSync and ActParkEpic
// change the node runs or the epic in the store, and so do ActHold, ActReview, ActClosure and ActConclude for the
// review. ActWork hands a check, sync or release node run's work to the interpreter, and ActRun hands a node run to its
// kind. ActReadOrigin reads origin's main for a sync and records the read on the epic.
const (
	ActStart      ActionKind = "start"
	ActEnd        ActionKind = "end"
	ActRound      ActionKind = "round"
	ActPark       ActionKind = "park"
	ActParkNode   ActionKind = "park_node"
	ActParkEpic   ActionKind = "park_epic"
	ActBatch      ActionKind = "batch"
	ActSync       ActionKind = "sync"
	ActReadOrigin ActionKind = "read_origin"
	ActHold       ActionKind = "hold"
	ActReview     ActionKind = "review"
	ActClosure    ActionKind = "closure"
	ActConclude   ActionKind = "conclude"
	ActWork       ActionKind = "work"
	ActRun        ActionKind = "run"
)

// Action is one thing Decide asks for. Start carries the Runs to start and, for a later plan, its Trigger. End carries
// the Ends, a failed end in a plan body its Trigger, and an end that builds the epic Built. Round and Park name the
// loop at Path and its current Round, and Park its Reason. Run names the node run at Path of NodeKind, and for a build
// child its Task. ParkNode parks the node run at Path, whose Attempt ended with Outcome and Output, for Reason. When
// Met names Requirements, the evidence node at base found them holding at the base, and the park's question offers them
// as met. Batch ends the integrate run at Path, whose Attempt ended with Outcome and Output, with Trigger. Sync ends
// the sync run at Path, whose Attempt ended with Outcome and Output, and tells Reason when it is set. ParkEpic parks
// the epic for Reason, with Trigger. Work carries a check, sync or release node run's work: Work is the kind's action,
// prepare, start, capture, repro, refs, advance, take, integrator, step, judge, record, stop, ending, free, replan or
// resume, and Def, Run, IDs, Items, Tasks and Units are what it is for. An advance and a stop carry their Output, an
// ending its Outcome, an integrator the failure its round mends as its Output, an integrate run's advance the Trigger its
// passed batch writes, a replan the plan run it starts as its Runs, the Trigger that wakes the planner and the tell as
// its Reason, and a sync run's advance as its Reason why the epic is no longer at the quiet point the sync started at. Hold
// pauses the epic for the review step IDs[0], stopped for Reason. Review ends the review run at Path, whose Attempt
// ended with Output, Verdicts and Ledger. Closure ends the closure run at Path, whose Attempt ended with Outcome and
// Output, with ReviewRound and Closures. Conclude ends the conclusion run at Path, whose Attempt ended with Phase and
// Output. ReadOrigin carries nothing: it reads origin's main for the epic it was decided for.
type Action struct {
	Kind        ActionKind
	Runs        []store.NodeRun
	Trigger     *store.NodeTrigger
	Ends        []store.NodeEnd
	Path        string
	Round       int64
	Reason      string
	NodeKind    string
	Task        int64
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
	Met         []string
	Built       bool
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

// The kind of a sync run, the node id it is stored under, and the cause its id is derived from. A sync run is no node
// of the workflow: the interpreter adds it under a plan run (syncMain). A free turn, which a turn the planner could not
// start or that a restart cut ends as, is no turn a sync is keyed by.
const (
	kindSync  = "sync"
	syncNode  = "sync"
	syncCause = "sync"
	turnFree  = "free"
)

// syncInterval is how long an epic's read of origin's main stands for the stored base it was read for. Origin's main is
// read for a sync at most once in that time for one stored base, a read that failed among it, so origin that cannot be
// reached is tried again only once it has passed. Its age is measured by the runner's clock against the read's stored
// time, so a restart keeps it.
const syncInterval = 5 * time.Minute

// What an epic's read of origin's main found of origin's commit, as the epic's last read stores it. A read finds the
// repository with no remote origin (originNone), fails (originFailed), finds origin's commit in the stored base
// (originHeld), finds it does not hold the epic's change base (originApart), or finds it ahead of the stored base
// (originAhead), which a sync starts from. The prepare of a sync stores its own read, and one that took origin's commit
// for a merge stores originTaken, so the read it started from starts no second sync.
const (
	originNone   = "none"
	originFailed = "failed"
	originHeld   = "held"
	originApart  = "apart"
	originAhead  = "ahead"
	originTaken  = "taken"
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
// kinds. The walk takes the top-level nodes in order and stops at the first one that has not ended, so the actions are what
// that node needs. Once every top-level node has ended, Decide returns none.
func Decide(flow workflow.Workflow, rows kinds.Rows, reg map[string]kinds.Kind) []Action {
	acts, _ := newPass(rows, reg, flow).walk(flow.Nodes, scope{where: topLevel})
	return acts
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

// running decides a node run that has not ended. A loop decides its round, and a plan its settle walks and fan-out. A
// release whose replan plan runs decides that plan (replanning). Any other node is decided by its registered kind, and a
// node the kind leaves waiting, or whose kind is not registered, gives no action.
func (p pass) running(def workflow.Node, run store.NodeRun, at scope) []Action {
	if def.Loop != nil {
		return p.loop(def, run)
	}
	kind, ok := p.reg[def.Kind]
	if !ok {
		return nil
	}
	if def.Kind == kindRelease {
		if acts, replanning := p.replanning(run); replanning {
			return acts
		}
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
		kinds.ActEnding, kinds.ActFree, kinds.ActResume:
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
	case kinds.ActRun:
		return []Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}
	default:
		return nil
	}
}

// plan decides a running plan run from the act its kind gives. The plan runs while its kind keeps it running, and it
// ends once its kind ends it, its latest settle walk has ended and no sync of origin's main holds it. Its settle walks,
// its sync and its fan-out are decided with it. While a sync holds the plan, the plan gets no run, so the planner gets
// no turn. The planner's pass still carries out the epic's pending actions, which wait on no turn, so the hold never
// waits on the planner. A plan whose kind leaves it waiting gives no action.
func (p pass) plan(def workflow.Node, run store.NodeRun, act kinds.Action) []Action {
	if act.Kind != kinds.ActEnd && act.Kind != kinds.ActRun {
		return nil
	}
	walks, settled := p.settle(def, run)
	synced, holds := p.syncMain(def, run, settled)
	if act.Kind == kinds.ActEnd && settled && !holds {
		return p.complete(def, run, act)
	}
	acts := walks
	if !holds {
		acts = append([]Action{{Kind: ActRun, Path: run.Path, NodeKind: def.Kind, Task: run.Task}}, acts...)
	}
	acts = append(acts, synced...)
	return append(acts, p.fanOut(def, run)...)
}

// syncMain decides the sync of origin's main under a running plan run, and reports whether the sync holds the plan. A
// running sync run under the plan is decided by the sync kind, and holds the plan until it ends. Otherwise, once the
// plan's walks have settled and the epic is at a quiet point (quiet), a sync is due unless a run is stored at <plan
// path>/M<turn>, where <turn> is the epic's highest turn that is not free (syncTurn): one sync is tried for each such
// turn. A due sync reads origin's main first when the epic's last read is stale (originStale), which holds the plan
// while it reads. A sync run starts at that path only when the last read found origin's commit ahead of the stored base
// and no earlier sync tried that commit (kinds.SyncTriedBy), and it holds the plan too. Any other read starts nothing,
// so a turn whose origin's main has not moved stores no run. An interpreter that registers no sync kind starts none.
func (p pass) syncMain(def workflow.Node, run store.NodeRun, settled bool) ([]Action, bool) {
	kind, ok := p.reg[kindSync]
	if !ok {
		return nil, false
	}
	if open, running := p.runningSync(run.Path); running {
		return p.decideSync(kind, open), true
	}
	if !settled || !p.quiet(def, run) {
		return nil, false
	}
	path := syncPath(run.Path, p.syncTurn())
	if _, stored := p.byPath[path]; stored {
		return nil, false
	}
	if p.originStale() {
		return []Action{{Kind: ActReadOrigin}}, true
	}
	read := p.rows.Epic.OriginRead
	if read.State != originAhead || kinds.SyncTriedBy(p.rows, read.Commit) != "" {
		return nil, false
	}
	child := store.NodeRun{
		Path: path, ID: store.NodeRunID(p.rows.Epic.ID, path, run.Round, syncCause), Node: syncNode, Kind: kindSync,
		Round: run.Round,
	}
	return []Action{{Kind: ActStart, Runs: []store.NodeRun{child}}}, true
}

// syncTurn returns the highest seq among the epic's turns that are not free, zero when it has none. A free turn ends a
// turn the planner could not start, such as one a full step cap refused, or one a restart cut, and the planner claims
// another at its next pass, so a key of the highest seq would start a sync for each of them.
func (p pass) syncTurn() int64 {
	var highest int64
	for _, turn := range p.rows.Epic.Turns {
		if turn.State != turnFree {
			highest = max(highest, turn.Seq)
		}
	}
	return highest
}

// originStale reports whether the epic's last read of origin's main no longer stands: it was read for another stored
// base, or syncInterval has passed since it was read, by the rows' clock. A read stored later than that clock, which a
// clock set back leaves, is stale too, so the next read stores the clock's time.
func (p pass) originStale() bool {
	read := p.rows.Epic.OriginRead
	age := p.rows.Now.Sub(read.At)
	return read.Base != p.rows.Epic.StoredBase || age < 0 || age >= syncInterval
}

// syncPath returns the path of the sync run of the plan at path for turn.
func syncPath(path string, turn int64) string {
	return fmt.Sprintf("%s/M%d", path, turn)
}

// runningSync returns the running sync run under the plan at path, and whether there is one.
func (p pass) runningSync(path string) (store.NodeRun, bool) {
	for _, run := range p.rows.Runs {
		if run.Kind == kindSync && run.State == store.NodeRunning && within(run.Path, path) {
			return run, true
		}
	}
	return store.NodeRun{}, false
}

// decideSync decides a running sync run with kind. An end ends the run, telling its output when the kind tells it. The
// prepare, the checks at the merge and the advance are its work.
func (p pass) decideSync(kind kinds.Kind, run store.NodeRun) []Action {
	def := workflow.Node{ID: syncNode, Kind: kindSync}
	act := kind.Decide(kinds.Node{Def: def, Run: run}, p.rows)
	switch act.Kind {
	case kinds.ActEnd:
		end := Action{Kind: ActSync, Path: run.Path, Attempt: run.Attempt, Outcome: act.Outcome, Output: act.Output}
		if act.Tell {
			end.Reason = syncTell(run.Path, act.Outcome, act.Output)
		}
		return []Action{end}
	case kinds.ActPrepare, kinds.ActStart, kinds.ActCapture, kinds.ActRepro, kinds.ActAdvance:
		return []Action{p.work(def, run, act)}
	default:
		return nil
	}
}

// syncTell returns the tell of the sync run at path that ended with outcome and output.
func syncTell(path, outcome, output string) string {
	return fmt.Sprintf("workflow node %s %s: %s", path, outcome, output)
}

// quiet reports whether the epic is at a quiet point of the plan run of def: the plan's body holds an integrate node, a
// batch under the plan run has passed and moved the stored base, the epic has not ended and is not closed
// (kinds.Ended), it is neither paused nor parked, whatever the class of its park, its review has not frozen, it does
// not hold its repository's release slot, and its work is at rest (kinds.Busy), which no pending planner action leaves
// it. The review compares revisions against a frozen one, so no sync runs once it froze.
func (p pass) quiet(def workflow.Node, run store.NodeRun) bool {
	epic := p.rows.Epic
	switch {
	case !slices.ContainsFunc(def.Body, func(node workflow.Node) bool { return node.Kind == kindIntegrate }),
		kinds.Ended(epic.Phase), epic.Paused, epic.Parked != "", p.rows.Review.Frozen != "", p.rows.Slot == epic.ID,
		kinds.Busy(p.rows) != "":
		return false
	}
	return slices.ContainsFunc(p.rows.Runs, func(batch store.NodeRun) bool {
		return batch.Kind == kindIntegrate && within(batch.Path, run.Path) && batch.Outcome == outcomePassed &&
			batch.Revision != ""
	})
}

// complete decides a plan run whose kind ends it and whose latest settle walk has ended. A plan body that holds an
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

// settle decides the settle walks of a running plan run, and reports whether its latest settle walk, and every late
// walk, has ended. A settle point is the highest seq among the tasks of turns above the plan's mark. Its walk runs the
// plan body's nodes that are not build nodes, under <plan path>/S<seq>/. Every walk already started, settle or late, is
// decided on each pass, so an older walk finishes while a newer settle point waits for its tasks. The latest walk starts
// only when no task of the epic is building, open and not waiting on other tasks (anyTaskBuilding), and no late walk
// runs, so a late walk's batch never races a newer one onto the branch. Once the latest walk has ended, the next late
// walk may start (nextLateWalk).
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
	late, lateEnded := p.lateWalks(nodes, run)
	acts = append(acts, late...)
	prefix := settlePrefix(run.Path, latest)
	if !p.hasRunUnder(prefix) && (p.anyTaskBuilding() || !lateEnded) {
		return acts, false
	}
	walked, done := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
	acts = append(acts, walked...)
	if !done || !lateEnded {
		return acts, false
	}
	next, settled := p.nextLateWalk(nodes, run, prefix)
	return append(acts, next...), settled
}

// lateWalks decides each late walk of a plan run that has started, and reports whether all of them have ended. A late
// walk integrates a task that was waiting on other tasks when the latest settle walk started, and so was accepted after
// that walk's integrate run claimed its batch. It runs the nodes under <plan path>/L<seq>/, where <seq> is the seq of
// that task.
func (p pass) lateWalks(nodes []workflow.Node, run store.NodeRun) ([]Action, bool) {
	var acts []Action
	ended := true
	for _, task := range p.rows.Epic.Tasks {
		prefix := latePrefix(run.Path, task.Seq)
		if task.Turn <= run.Mark || !p.hasRunUnder(prefix) {
			continue
		}
		walked, done := p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
		acts = append(acts, walked...)
		ended = ended && done
	}
	return acts, ended
}

// nextLateWalk starts the next late walk of a plan run whose latest settle walk, under latest, and every late walk have
// ended, and reports whether none is left to run. The next late walk is that of the lowest seq among the tasks of turns
// above the plan's mark that waited, are done and are claimed by no batch (lateTask). It starts once the latest walk's
// integrate node has run, when no task of the epic is building. A late walk that ended without its task claimed starts no
// other, so its trigger brings the planner first. A plan body with no integrate node has no late walk.
func (p pass) nextLateWalk(nodes []workflow.Node, run store.NodeRun, latest string) ([]Action, bool) {
	i := slices.IndexFunc(nodes, func(node workflow.Node) bool { return node.Kind == kindIntegrate })
	if i < 0 {
		return nil, true
	}
	if _, ran := p.byPath[latest+nodes[i].ID]; !ran {
		return nil, true
	}
	seq, found := p.lateTask(run.Mark)
	prefix := latePrefix(run.Path, seq)
	if !found || p.hasRunUnder(prefix) {
		return nil, true
	}
	if p.anyTaskBuilding() {
		return nil, false
	}
	return p.walk(nodes, scope{prefix: prefix, round: run.Round, where: planBody})
}

// lateTask returns the lowest seq among the tasks of turns above mark that waited on other tasks, are done and are
// claimed by no batch, and whether there is one.
func (p pass) lateTask(mark int64) (int64, bool) {
	for _, task := range p.rows.Epic.Tasks {
		if task.Turn > mark && task.Status == taskDone && task.Batch == "" && task.Build.After != "" {
			return task.Seq, true
		}
	}
	return 0, false
}

// latePrefix returns the prefix of the late walk at seq of the plan at path.
func latePrefix(path string, seq int64) string {
	return fmt.Sprintf("%s/L%d/", path, seq)
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

// anyTaskBuilding reports whether any task of the epic is open and not waiting on other tasks (build.Waiting). A
// waiting task's build moves only once a batch integrates the tasks it waits for, so a settle walk does not wait for it.
func (p pass) anyTaskBuilding() bool {
	return slices.ContainsFunc(p.rows.Epic.Tasks, func(task store.EpicTask) bool {
		return task.Status == taskOpen && !build.Waiting(task)
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
		return []Action{{Kind: ActConclude, Path: run.Path, Attempt: run.Attempt, Phase: act.Phase, Output: act.Output}}
	}
	if act.Outcome == outcomeFailed {
		return p.failed(def, run, act.Output, at)
	}
	return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, act.Outcome, act.Output)}}
}

// failed decides a check node run that ended failed with output, by where it sits. At the top level it parks, so the
// flow holds at it, and an evidence node at base that found a Requirement met parks with the question that offers the
// Requirements as met. In a loop body it ends with its output, and the round ends. In a plan body it ends with a trigger
// that wakes the planner. The trigger's id is derived from the run's path, round and attempt, so a restart records it
// once.
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
				Reason: metAtBaseReason(run.Path, met, p.rows.Epic.Base), Met: met,
			}}
		}
		return []Action{{
			Kind: ActParkNode, Path: run.Path, Attempt: run.Attempt, Outcome: outcomeFailed, Output: output,
			Reason: reason,
		}}
	}
}

// metAtBaseReason returns the reason an evidence node at base parks with when its red runs judged the Requirements met
// holding at base. It names the node, the Requirements and the base, and offers the three choices the park's question
// declares.
func metAtBaseReason(path string, met []string, base string) string {
	ids := strings.Join(met, ", ")
	return fmt.Sprintf(
		"workflow node %s: %s met at the epic's base %s; you can retry (run the red check of %s again with a fresh "+
			"judge), drop (drop %s as already met, recorded as your decision, and go on without their red) or end "+
			"(end the epic)",
		path, ids, base, ids, ids,
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

// work returns the work action of a check, sync or release node run: the kind's action, and the ids, items, tasks,
// units, output, outcome and reason the work is for. An integrate run's advance also carries the trigger its passed batch
// writes. An action that names a revision runs at it: the run it carries holds that revision in place of its own.
func (p pass) work(def workflow.Node, run store.NodeRun, act kinds.Action) Action {
	if act.Revision != "" {
		run.Revision = act.Revision
	}
	out := Action{
		Kind: ActWork, Work: act.Kind, Path: run.Path, NodeKind: def.Kind, Def: def, Run: run, IDs: act.IDs,
		Items: act.Items, Tasks: act.Tasks, Units: act.Units, Outcome: act.Outcome, Output: act.Output,
		Reason: act.Reason,
	}
	if act.Kind == kinds.ActAdvance && def.Kind == kindIntegrate {
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
