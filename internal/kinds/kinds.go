// Package kinds holds the workflow node kinds the interpreter runs. A kind decides, from its node run and the rows of
// the epic the run is on, whether the run goes on, ends, or waits. Each Decide reads its inputs only, so the same inputs
// give the same action.
package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// Node is a workflow node on an epic: its definition and the run the interpreter started for it.
type Node struct {
	Def workflow.Node
	Run store.NodeRun
}

// Rows is what a kind reads of an epic: the epic with its turns, tasks and Requirements, its node runs, its node
// checks, its evidence runs and its agent steps, how many of its triggers and amend_evidence actions are pending, how
// many of the actions its planner returned are pending, its end-of-epic review, the epic-stage reviewers enabled for it
// in merged order, its release runs, and the epic that holds the release slot of its repository, which is "" when the
// slot is free. Now is the time of the runner's clock the rows are decided at, which the age of the epic's last read of
// origin's main is measured to.
type Rows struct {
	Epic     store.EpicRow
	Runs     []store.NodeRun
	Checks   []store.NodeCheck
	Evidence []store.EvidenceRun
	Steps    []store.StepRow
	Pending  int
	// AmendActions counts the epic's amend_evidence actions that are still pending, and Actions every planner action
	// of the epic that is still pending, those among it.
	AmendActions int
	Actions      int
	// GapTurns holds the planner turns each gap was given, and Unresolvable the gaps the latest succeeded turn declared
	// it cannot resolve without the owner, as store.EpicFlow reads them. Reruns holds the reruns rerun_review actions
	// asked for, in turn then position order, as store.EpicFlow reads them.
	GapTurns     []store.GapTurn
	Unresolvable []store.GapDeclaration
	Reruns       []store.ReviewRerun
	Review       store.Review
	Reviewers    []Reviewer
	Releases     []store.ReleaseRun
	Slot         string
	Now          time.Time
}

// Reviewer is an enabled epic-stage reviewer: its name and whether its findings are verified.
type Reviewer struct {
	Name   string
	Verify bool
}

// Unit is one step a review or closure run starts: its work id, its part, and the reviewer or finding it is for.
type Unit struct {
	ID, Part, Reviewer string
	Finding            store.LedgerFinding
}

// ActionKind names what a kind does with its node run.
type ActionKind string

// The actions a kind returns for its node run. ActNone leaves the run running. ActPrepare records the revision the
// run's work runs at, ActStart starts work and ActRefs checks the refs the run was prepared with. ActCapture starts
// Requirement captures, and ActRepro starts the reproductions of Requirements, with IDs and Items as for ActCapture.
// ActAdvance moves the epic's branch to the run's candidate, and ActPark parks the run. ActHold pauses the epic once
// for a review step its owner stopped. ActGap gives a build that waits for a lost dependency, whose Reason names it.
const (
	ActNone    ActionKind = "none"
	ActRun     ActionKind = "run"
	ActEnd     ActionKind = "end"
	ActPrepare ActionKind = "prepare"
	ActStart   ActionKind = "start"
	ActRefs    ActionKind = "refs"
	ActCapture ActionKind = "capture"
	ActRepro   ActionKind = "repro"
	ActAdvance ActionKind = "advance"
	ActPark    ActionKind = "park"
	ActHold    ActionKind = "hold"
	ActGap     ActionKind = "gap"
)

// Action is a kind's decision for its node run. Outcome is the outcome an end reports and empty when it reports none,
// and Output the output an end reports. IDs are the ids of the work a start starts and Items the Requirement each of
// them is for, in the same order. Tasks are the seqs of the tasks a prepare claims for its batch, in seq order. Units
// are the steps a start of a review or closure run starts, in the order of IDs. Verdicts, Ledger, Round, Closures and
// Phase are what a review, closure or conclusion ends with. Gaps are the live gaps a conclusion's gap round gives
// each a planner turn, and Question is the gaps question an ending with gaps opens. Revision, when set, is the revision
// the work runs at in place of the run's own: a release run walks the Done gates and captures of a merge the release
// integrator committed, and records it as the candidate, at that commit. Reason, on a sync run's advance, is why the
// epic is no longer at the quiet point the sync started at, and empty while it is. Tell, on a sync run's end, reports
// whether its output is told to the owner.
type Action struct {
	Kind     ActionKind
	Outcome  string
	Output   string
	IDs      []string
	Items    []string
	Tasks    []int64
	Units    []Unit
	Verdicts []store.ReviewVerdict
	Ledger   []store.LedgerFinding
	Round    store.ReviewRound
	Closures []store.Closure
	Phase    string
	Revision string
	Reason   string
	Tell     bool
	Gaps     []Gap
	Question store.GapsQuestion
}

// Kind decides what happens next to a node run.
type Kind interface {
	Decide(node Node, rows Rows) Action
}

// Idler is a Kind whose running node runs wait in some phases of their epic: Idle reports whether Decide gives a run
// of an epic in phase no action, whatever the epic's other rows hold. The interpreter does not read such an epic's rows
// while its phase stays one its waiting run is idle in.
type Idler interface {
	Kind
	Idle(phase string) bool
}

// Release waits in the phases it gives no action in.
var _ Idler = Release{}

// Plan is the plan kind. Its run ends complete once the epic's last turn, above the turn the run started after, has
// succeeded with outcome complete, and no turn runs, no task is open, no trigger is pending and no planner action is
// pending.
type Plan struct{}

// Build is the build kind. Its run is one planned task: it runs while the task is open on an epic with a base whose phase
// is not ended, and ends once the task is no longer open. A task that has not started, one whose build has no stage yet,
// waits for the tasks it depends on: it gives none while one is pending, and gives a gap naming the first lost one, so
// it runs only once each of them has been integrated into a batch.
type Build struct{}

// The states, statuses, outcomes and phases a kind reads, as the store and the epic write them.
const (
	turnRunning        = "running"
	turnSucceeded      = "succeeded"
	taskOpen           = "open"
	taskDone           = "done"
	outcomeComplete    = "complete"
	outcomePassed      = "passed"
	outcomeFailed      = "failed"
	runEnded           = "ended"
	stepDone           = "done"
	phaseMetAtBase     = "met_at_base"
	phaseClean         = "clean"
	phaseEndedWithGaps = "ended_with_gaps"
	phaseReleased      = "released"
	phaseDone          = "done"
	phaseStopped       = "stopped"
	// taskFailed is the status of a task that failed, which a build waiting on it reads as lost.
	taskFailed = "failed"
	// taskGap is the status of a task that ended its ladder as a gap and still holds the Requirements it claims.
	taskGap = "gap"
)

// Decide returns end complete when planComplete holds, and run otherwise.
func (Plan) Decide(node Node, rows Rows) Action {
	if planComplete(node.Run.Mark, rows) {
		return Action{Kind: ActEnd, Outcome: outcomeComplete}
	}
	return Action{Kind: ActRun}
}

// planComplete reports whether a plan run started after turn mark may end complete. The epic's highest-seq turn must be
// above mark, succeeded and complete. No turn may be running, no task open, no trigger pending and no planner action
// pending: a turn may return complete beside an action, such as a retry_node that reruns the plan's failed batch, and
// the plan must not end before that action is carried out, or the run it reruns would run under an ended plan. No
// evidence amendment may be under way either, neither an amend_evidence action still pending nor an amendment checking
// or capturing, since its end decides what the plan has built and wakes the planner.
func planComplete(mark int64, rows Rows) bool {
	if rows.Pending != 0 || rows.Actions != 0 || anyTaskOpen(rows.Epic.Tasks) || amending(rows) {
		return false
	}
	var last store.EpicTurn
	found := false
	for i := range rows.Epic.Turns {
		turn := &rows.Epic.Turns[i]
		if turn.State == turnRunning {
			return false
		}
		if !found || turn.Seq > last.Seq {
			last, found = *turn, true
		}
	}
	return found && last.Seq > mark && last.State == turnSucceeded && last.Outcome == outcomeComplete
}

// amending reports whether an evidence amendment of the epic is under way: an amend_evidence action still pending, or an
// amendment checking or capturing.
func amending(rows Rows) bool {
	if rows.AmendActions > 0 {
		return true
	}
	return slices.ContainsFunc(rows.Epic.Amendments, amendmentUnderWay)
}

// amendmentUnderWay reports whether amended is under way: checking its red, or capturing its green.
func amendmentUnderWay(amended store.EvidenceAmendment) bool {
	return amended.State == store.AmendmentChecking || amended.State == store.AmendmentCapturing
}

// anyTaskOpen reports whether any of tasks is open.
func anyTaskOpen(tasks []store.EpicTask) bool {
	for i := range tasks {
		if tasks[i].Status == taskOpen {
			return true
		}
	}
	return false
}

// Decide returns end with no outcome when the run's task is not open on the epic. An open task that has not started
// and depends on tasks returns gap when one of them is lost, naming the first lost one in DependsOn order, and none
// while one is pending. Otherwise it returns run when the epic has a base and its phase is not ended, and none when it
// does not.
func (Build) Decide(node Node, rows Rows) Action {
	task, open := openTask(rows.Epic.Tasks, node.Run.Task)
	if !open {
		return Action{Kind: ActEnd}
	}
	if task.Build.Stage == "" {
		if action, waiting := awaitDependencies(task.DependsOn, rows); waiting {
			return action
		}
	}
	if rows.Epic.Base != "" && !Ended(rows.Epic.Phase) {
		return Action{Kind: ActRun}
	}
	return Action{Kind: ActNone}
}

// openTask returns the task with seq among tasks, and whether it is open.
func openTask(tasks []store.EpicTask, seq int64) (store.EpicTask, bool) {
	for i := range tasks {
		if tasks[i].Seq == seq && tasks[i].Status == taskOpen {
			return tasks[i], true
		}
	}
	return store.EpicTask{}, false
}

// awaitDependencies decides a build of a task that has not started and depends on the tasks deps names. It returns the
// gap action naming the first lost dependency in deps order, and waiting true. It returns none with waiting true while
// a dependency is pending, and waiting false when every dependency is integrated.
func awaitDependencies(deps []int64, rows Rows) (Action, bool) {
	pending := false
	for _, seq := range deps {
		lost, integrated := dependencyState(seq, rows)
		if lost != "" {
			return Action{Kind: ActGap, Reason: lost}, true
		}
		pending = pending || !integrated
	}
	if pending {
		return Action{Kind: ActNone}, true
	}
	return Action{}, false
}

// dependencyState reports how the task with seq stands for a build that waits on it. lost is the reason it is lost, and
// empty when it is not: it failed, it ended as a gap, its batch's ended integrate run left it out, or that run's result
// does not decode. integrated reports whether an ended integrate run composed it. Neither means it is pending.
func dependencyState(seq int64, rows Rows) (lost string, integrated bool) {
	i := slices.IndexFunc(rows.Epic.Tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		return "", false
	}
	dep := rows.Epic.Tasks[i]
	switch dep.Status {
	case taskFailed:
		return fmt.Sprintf("dependency T%d failed", seq), false
	case taskGap:
		return fmt.Sprintf("dependency T%d ended as a gap", seq), false
	}
	j := slices.IndexFunc(rows.Runs, func(run store.NodeRun) bool {
		return run.Kind == kindIntegrate && run.Path == dep.Batch
	})
	if dep.Batch == "" || j < 0 || rows.Runs[j].State != runEnded {
		return "", false
	}
	composed, err := composedSeqs(rows.Runs[j].Result)
	switch {
	case err != nil:
		return fmt.Sprintf("dependency T%d was claimed by a batch whose result does not decode", seq), false
	case !slices.Contains(composed, seq):
		return fmt.Sprintf("dependency T%d was left out of its batch", seq), false
	}
	return "", true
}

// composedSeqs returns the seqs of the tasks an integrate run's result composed, in the order it composed them. It
// returns an error when the result, or one of its composed entries, does not decode.
func composedSeqs(result string) ([]int64, error) {
	var decoded integrateResult
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		return nil, fmt.Errorf("integrate result does not decode: %w", err)
	}
	seqs := make([]int64, 0, len(decoded.Composed))
	for _, entry := range decoded.Composed {
		var composed struct {
			Task int64 `json:"task"`
		}
		if err := json.Unmarshal(entry, &composed); err != nil {
			return nil, fmt.Errorf("composed entry does not decode: %w", err)
		}
		seqs = append(seqs, composed.Task)
	}
	return seqs, nil
}

// Ended reports whether phase is a phase an epic ends in: the ended phases, and the closed phases a close ends an epic
// in. Every other phase, the empty one included, is not ended.
func Ended(phase string) bool {
	switch phase {
	case phaseMetAtBase, phaseClean, phaseEndedWithGaps, phaseReleased, phaseDone, phaseStopped:
		return true
	}
	return store.IsClosedPhase(phase)
}
