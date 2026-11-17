// Package kinds holds the workflow node kinds the interpreter runs. A kind decides, from its node run and the rows of
// the epic the run is on, whether the run goes on, ends, or waits. Each Decide reads its inputs only, so the same inputs
// give the same action.
package kinds

import (
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
// for a review step its owner stopped.
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
)

// Action is a kind's decision for its node run. Outcome is the outcome an end reports and empty when it reports none,
// and Output the output an end reports. IDs are the ids of the work a start starts and Items the Requirement each of
// them is for, in the same order. Tasks are the seqs of the tasks a prepare claims for its batch, in seq order. Units
// are the steps a start of a review or closure run starts, in the order of IDs. Verdicts, Ledger, Round, Closures and
// Phase are what a review, closure or conclusion ends with. Revision, when set, is the revision the work runs at in
// place of the run's own: a release run walks the Done gates and captures of a merge the release integrator committed,
// and records it as the candidate, at that commit. Reason, on a sync run's advance, is why the epic is no longer at the
// quiet point the sync started at, and empty while it is. Tell, on a sync run's end, reports whether its output is
// told to the owner.
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
}

// Kind decides what happens next to a node run.
type Kind interface {
	Decide(node Node, rows Rows) Action
}

// Plan is the plan kind. Its run ends complete once the epic's last turn, above the turn the run started after, has
// succeeded with outcome complete, and no turn runs, no task is open and no trigger is pending.
type Plan struct{}

// Build is the build kind. Its run is one planned task: it runs while the task is open on an epic with a base whose phase
// is not ended, and ends once the task is no longer open.
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
)

// Decide returns end complete when planComplete holds, and run otherwise.
func (Plan) Decide(node Node, rows Rows) Action {
	if planComplete(node.Run.Mark, rows) {
		return Action{Kind: ActEnd, Outcome: outcomeComplete}
	}
	return Action{Kind: ActRun}
}

// planComplete reports whether a plan run started after turn mark may end complete. The epic's highest-seq turn must be
// above mark, succeeded and complete. No turn may be running, no task open and no trigger pending. No evidence amendment
// may be under way either, neither an amend_evidence action still pending nor an amendment checking or capturing, since
// its end decides what the plan has built and wakes the planner.
func planComplete(mark int64, rows Rows) bool {
	if rows.Pending != 0 || anyTaskOpen(rows.Epic.Tasks) || amending(rows) {
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

// Decide returns end with no outcome when the run's task is not open on the epic. Otherwise it returns run when the
// epic has a base and its phase is not ended, and none when it does not.
func (Build) Decide(node Node, rows Rows) Action {
	if !isOpenTask(rows.Epic.Tasks, node.Run.Task) {
		return Action{Kind: ActEnd}
	}
	if rows.Epic.Base != "" && !Ended(rows.Epic.Phase) {
		return Action{Kind: ActRun}
	}
	return Action{Kind: ActNone}
}

// isOpenTask reports whether the task with seq among tasks is open.
func isOpenTask(tasks []store.EpicTask, seq int64) bool {
	for i := range tasks {
		if tasks[i].Seq == seq && tasks[i].Status == taskOpen {
			return true
		}
	}
	return false
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
