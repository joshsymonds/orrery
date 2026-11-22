package server

import (
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The states a Requirement shows. The first that holds decides it, in this order: proven, failed, held, in_flight, and
// not_reached when none does.
const (
	requirementProven     = "proven"
	requirementFailed     = "failed"
	requirementHeld       = "held"
	requirementInFlight   = "in_flight"
	requirementNotReached = "not_reached"
)

// The centers an epic shows. The first that holds decides it, in this order: asks, paused, releasing, shipped,
// ended_short, and running when none does.
const (
	centerAsks       = "asks"
	centerPaused     = "paused"
	centerReleasing  = "releasing"
	centerShipped    = "shipped"
	centerEndedShort = "ended_short"
	centerRunning    = "running"
)

// The spellings the derivation reads by name: an ended run, a task's status, a failed release step, the ending that
// asks the owner to release or close, and the phases an epic ends in besides those.
const (
	runStateEnded        = "ended"
	taskStatusDone       = "done"
	taskStatusFailed     = "failed"
	taskStatusIntegrated = "integrated"
	releaseStepFailed    = "failed"
	phaseEndedWithGaps   = "ended_with_gaps"
	epicPhaseMetAtBase   = "met_at_base"
)

// markTask is a task as the Requirement states and the center read it: the label its labeled steps carry, its status,
// its stage and the Requirements it names.
type markTask struct {
	label        string
	status       string
	stage        string
	requirements []string
}

// orreryMarkTasks returns each task of an orreryd epic as a markTask, in order. A build task's steps carry its id.
func orreryMarkTasks(tasks []*enginev1.EpicTask) []markTask {
	out := make([]markTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, markTask{
			label:        task.GetId(),
			status:       task.GetStatus(),
			stage:        task.GetStage(),
			requirements: task.GetRequirements(),
		})
	}
	return out
}

// recordMarkTasks returns each task of a record epic as a markTask, in order. A record task's steps carry its slug, and
// it has no stage.
func recordMarkTasks(tasks []taskRecord) []markTask {
	out := make([]markTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, markTask{label: task.Slug, status: task.Status, requirements: task.Requirements})
	}
	return out
}

// started reports whether the task has started: its build has a stage, its status is one a started task holds, or one
// of its labeled steps exists.
func (t markTask) started(steps []*orreryv1.EpicStep) bool {
	if t.stage != "" || taskStatusStarted(t.status) {
		return true
	}
	return slices.ContainsFunc(steps, func(step *orreryv1.EpicStep) bool {
		return step.GetTask() == t.label
	})
}

// heldIn reports whether a running step of the task is held: a labeled step of the task that has no end and is held.
func (t markTask) heldIn(steps []*orreryv1.EpicStep) bool {
	return slices.ContainsFunc(steps, func(step *orreryv1.EpicStep) bool {
		return step.GetTask() == t.label && step.GetEnd() == "" && step.GetHeld()
	})
}

// failed reports whether the task's build failed.
func (t markTask) failed() bool {
	return t.status == taskStatusFailed
}

// integrated reports whether the task reads as integrated: its status is done or integrated, compared without case, as
// the client reads a task's status.
func (t markTask) integrated() bool {
	switch strings.ToLower(t.status) {
	case taskStatusDone, taskStatusIntegrated:
		return true
	}
	return false
}

// taskStatusStarted reports whether a status is one a started task holds, compared without case. A task whose status is
// open or pending has not started.
func taskStatusStarted(status string) bool {
	switch strings.ToLower(status) {
	case "dispatched", "gated", "gap", taskStatusFailed, taskStatusDone, taskStatusIntegrated:
		return true
	}
	return false
}

// tasksNaming returns the tasks that name the Requirement id, in order.
func tasksNaming(id string, tasks []markTask) []markTask {
	var out []markTask
	for _, task := range tasks {
		if slices.Contains(task.requirements, id) {
			out = append(out, task)
		}
	}
	return out
}

// greenPassed reports whether a green run of the Requirement id passed.
func greenPassed(id string, runs []*enginev1.EvidenceRun) bool {
	return slices.ContainsFunc(runs, func(run *enginev1.EvidenceRun) bool {
		return run.GetItem() == id && run.GetPhase() == evidence.PhaseGreen && run.GetPassed()
	})
}

// latestGreenFailed reports whether the newest green run of the Requirement id has ended without passing. A run that is
// still running is not a failure. Runs created in the same second are ordered by their listing, the later one newer.
func latestGreenFailed(id string, runs []*enginev1.EvidenceRun) bool {
	var latest *enginev1.EvidenceRun
	for _, run := range runs {
		if run.GetItem() != id || run.GetPhase() != evidence.PhaseGreen {
			continue
		}
		if latest == nil || run.GetCreated() >= latest.GetCreated() {
			latest = run
		}
	}
	return latest != nil && latest.GetState() == runStateEnded && !latest.GetPassed()
}

// runningRun reports whether a run of the Requirement id has not ended.
func runningRun(id string, runs []*enginev1.EvidenceRun) bool {
	return slices.ContainsFunc(runs, func(run *enginev1.EvidenceRun) bool {
		return run.GetItem() == id && run.GetState() != runStateEnded
	})
}

// requirementState returns the state of the Requirement id, the first state that holds: proven when proven is set,
// failed when its latest green run failed or a task naming it failed, held when a task naming it has a held running
// step, in_flight when a task naming it has started or a run of it is running, and not_reached otherwise. The caller
// decides proven: a passed green run for an orreryd epic, and every task naming the Requirement integrated for a
// record epic.
func requirementState(
	id string,
	proven bool,
	tasks []markTask,
	steps []*orreryv1.EpicStep,
	runs []*enginev1.EvidenceRun,
) string {
	naming := tasksNaming(id, tasks)
	switch {
	case proven:
		return requirementProven
	case latestGreenFailed(id, runs) || slices.ContainsFunc(naming, markTask.failed):
		return requirementFailed
	case slices.ContainsFunc(naming, func(task markTask) bool { return task.heldIn(steps) }):
		return requirementHeld
	case runningRun(id, runs) || slices.ContainsFunc(naming, func(task markTask) bool { return task.started(steps) }):
		return requirementInFlight
	}
	return requirementNotReached
}

// orreryRequirements returns the EpicRequirement of each Requirement of an orreryd epic's contract, in contract order,
// each with its state, from the epic's build tasks, its labeled steps and its evidence runs.
func orreryRequirements(
	stored *enginev1.StoredEpic,
	steps []*orreryv1.EpicStep,
	runs []*enginev1.EvidenceRun,
) []*orreryv1.EpicRequirement {
	requirements := epicRequirementMessages(stored.GetContract().GetRequirements(), runs)
	tasks := orreryMarkTasks(stored.GetTasks())
	for _, requirement := range requirements {
		id := requirement.GetId()
		requirement.State = requirementState(id, greenPassed(id, runs), tasks, steps, runs)
	}
	return requirements
}

// recordRequirementMessages returns one EpicRequirement per id the record tasks name, in first-named order, carrying
// its id and state only. A record epic has no evidence runs, and its Requirement is proven when every task naming it is
// integrated.
func recordRequirementMessages(tasks []markTask, steps []*orreryv1.EpicStep) []*orreryv1.EpicRequirement {
	var out []*orreryv1.EpicRequirement
	for _, task := range tasks {
		for _, id := range task.requirements {
			if slices.ContainsFunc(out, func(requirement *orreryv1.EpicRequirement) bool {
				return requirement.GetId() == id
			}) {
				continue
			}
			out = append(out, &orreryv1.EpicRequirement{Id: id})
		}
	}
	for _, requirement := range out {
		id := requirement.GetId()
		naming := tasksNaming(id, tasks)
		proven := !slices.ContainsFunc(naming, func(task markTask) bool { return !task.integrated() })
		requirement.State = requirementState(id, proven, tasks, steps, nil)
	}
	return out
}

// asksOwner reports whether something asks the owner: an open question, a failed release step, a park the owner
// holds, a held running step, or an orreryd epic that ended with gaps, which asks the owner to release it anyway or
// close it.
func asksOwner(resp *orreryv1.EpicsGetResponse, stored *enginev1.StoredEpic) bool {
	epic := resp.GetEpic()
	endedWithGaps := epic.GetSource() == epicSourceOrreryd && epic.GetOutcome() == phaseEndedWithGaps
	return len(resp.GetQuestions()) > 0 ||
		slices.ContainsFunc(resp.GetReleaseSteps(), func(step *orreryv1.EpicReleaseProof) bool {
			return step.GetState() == releaseStepFailed
		}) ||
		(stored.GetParked() != "" && store.ParkClass(stored.GetParkClass()).OwnerHeld()) ||
		slices.ContainsFunc(resp.GetSteps(), func(step *orreryv1.EpicStep) bool {
			return step.GetEnd() == "" && step.GetHeld()
		}) ||
		endedWithGaps
}

// endedPhase reports whether phase is one an epic ends in, closed phases included. A record epic has ended once its
// outcome is set. The list is the store's own, which it keeps unexported, so it is repeated here.
func endedPhase(phase string, record bool) bool {
	if record {
		return phase != ""
	}
	switch phase {
	case epicPhaseMetAtBase, "clean", phaseEndedWithGaps, epicPhaseReleased, epicPhaseDone, "stopped":
		return true
	}
	return store.IsClosedPhase(phase)
}

// epicCentre returns the center of an epic from its answer and its stored epic, which is nil for a record epic. The
// first that holds decides it: asks when asksOwner holds, paused when the epic is paused, releasing when its phase is
// releasing, shipped when it is released (a record's outcome that starts with released, as epicLive reads it),
// ended_short for any other ended phase, and running otherwise.
func epicCentre(resp *orreryv1.EpicsGetResponse, stored *enginev1.StoredEpic) string {
	phase := resp.GetEpic().GetOutcome()
	record := resp.GetEpic().GetSource() == epicSourceRecord
	switch {
	case asksOwner(resp, stored):
		return centerAsks
	case resp.GetPaused():
		return centerPaused
	case phase == store.PhaseReleasing:
		return centerReleasing
	case leadingLetters(phase) == epicPhaseReleased:
		return centerShipped
	case endedPhase(phase, record):
		return centerEndedShort
	}
	return centerRunning
}
