package server

import (
	"slices"
	"strings"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The places of a task's lifecycle, as EpicBuildTask.place and EpicTask.place name them: planned before its first
// unit, then its five rings outside in, then integrated.
const (
	placePlanned    = "planned"
	placeRed        = "red"
	placeImplement  = "implement"
	placeGreen      = "green"
	placeReview     = "review"
	placeIntegrate  = "integrate"
	placeIntegrated = "integrated"
)

// The tones of a task at its place, as EpicBuildTask.tone and EpicTask.tone name them.
const (
	tonePlan    = "plan"
	toneRun     = "run"
	toneMine    = "mine"
	toneLimited = "limited"
	toneBad     = "bad"
	toneAcc     = "acc"
	toneOk      = "ok"
)

// The statuses a task's lifecycle reads: a build task is open until it is done or a gap, and a record task's status is
// free text, of which these words, compared without case, place it.
const (
	statusOpen       = "open"
	statusDone       = "done"
	statusIntegrated = "integrated"
	statusGap        = "gap"
	statusGated      = "gated"
	statusDispatched = "dispatched"
)

// evidenceLabelPrefix starts the task label of an evidence run's judge step, which names the run's id after it.
const evidenceLabelPrefix = "evidence-"

// lifecycle is where a task stands: its place and its tone there.
type lifecycle struct {
	place, tone string
}

// epicLifecycles sets the place and tone of each task of resp from labeled, the epic's labeled steps oldest first: its
// record tasks from their steps, and its build tasks from stored, the stored epic they were read from, which is nil for
// a record epic.
func epicLifecycles(resp *orreryv1.EpicsGetResponse, stored *enginev1.StoredEpic, labeled []*enginev1.Step) {
	epicRecordLifecycles(resp.GetTasks(), labeled)
	epicBuildLifecycles(resp.GetBuildTasks(), stored.GetTasks(), labeled, store.EndedPhase(stored.GetPhase()),
		stored.GetPaused() || stored.GetParked() != "")
}

// epicBuildLifecycles sets the place and tone of each build task in tasks from its stored task of the same index in
// stored and from steps, the epic's labeled steps oldest first. ended is whether the epic is in a phase its builds no
// longer run in, and held whether it is paused or parked.
func epicBuildLifecycles(
	tasks []*orreryv1.EpicBuildTask,
	stored []*enginev1.EpicTask,
	steps []*enginev1.Step,
	ended, held bool,
) {
	for i, task := range tasks {
		life := buildLifecycle(stored[i], taskSteps(steps, buildTaskLabels(stored[i])), ended, held)
		task.Place, task.Tone = life.place, life.tone
	}
}

// buildTaskLabels returns the task labels of task's steps: its id, and the evidence labels of the red and green runs
// its build names.
func buildTaskLabels(task *enginev1.EpicTask) []string {
	labels := []string{task.GetId()}
	for _, run := range []*enginev1.EvidenceRun{task.GetRed(), task.GetGreen()} {
		if run.GetId() != "" {
			labels = append(labels, evidenceLabelPrefix+run.GetId())
		}
	}
	return labels
}

// taskSteps returns the steps of steps whose task label is one of labels, in order.
func taskSteps(steps []*enginev1.Step, labels []string) []*enginev1.Step {
	var out []*enginev1.Step
	for _, step := range steps {
		if slices.Contains(labels, step.GetTask()) {
			out = append(out, step)
		}
	}
	return out
}

// buildLifecycle returns where a build task stands. A done task is integrated once its integrate run passed with it,
// and accepted at integrate until then. A gap is bad at the ring of the stage it ended on. An open task with no stage
// is planned, and any other is at the ring of its stage, in the tone openTone gives it.
func buildLifecycle(task *enginev1.EpicTask, steps []*enginev1.Step, ended, held bool) lifecycle {
	switch task.GetStatus() {
	case statusDone:
		if task.GetIntegrated() {
			return lifecycle{placeIntegrated, toneOk}
		}
		return lifecycle{placeIntegrate, toneAcc}
	case statusGap:
		return lifecycle{stagePlaceOr(task.GetStage(), placeImplement), toneBad}
	}
	place := stagePlaceOr(task.GetStage(), placePlanned)
	if place == placePlanned {
		return lifecycle{placePlanned, tonePlan}
	}
	return lifecycle{place, openTone(task, steps, ended, held)}
}

// stagePlaceOr returns the ring a build stage places a task in: red, implement for implement and orchestrate, green,
// and review for review, verify and design, the design judge's check after the last review gate. It returns otherwise
// for a stage that names no ring.
func stagePlaceOr(stage, otherwise string) string {
	switch stage {
	case "red":
		return placeRed
	case "implement", "orchestrate":
		return placeImplement
	case "green":
		return placeGreen
	case "review", "verify", "design":
		return placeReview
	default:
		return otherwise
	}
}

// openTone is the tone of an open task with a stage. In an ended epic no unit will start again, so the task is bad when
// its last unit failed, its latest step having ended other than done or its build carrying a failure, and plan
// otherwise. In a live epic a running step gives its own tone, a held build or a paused or parked epic is mine, and
// any other task is run: its build starts its next unit, so a unit that ended goal_unmet or checks_failed is retried or
// climbs the ladder and is no failure of the task.
func openTone(task *enginev1.EpicTask, steps []*enginev1.Step, ended, held bool) string {
	if ended {
		latest := lastStep(steps)
		if task.GetFailure() != "" || (latest != nil && latest.GetEnd() != epicEndDone) {
			return toneBad
		}
		return tonePlan
	}
	if running := runningStep(steps); running != nil {
		return runningTone(running)
	}
	if task.GetHeld() != "" || held {
		return toneMine
	}
	return toneRun
}

// lastStep is the latest of steps, or nil when there are none.
func lastStep(steps []*enginev1.Step) *enginev1.Step {
	if len(steps) == 0 {
		return nil
	}
	return steps[len(steps)-1]
}

// runningStep is the latest of steps that has not ended, or nil when every one has.
func runningStep(steps []*enginev1.Step) *enginev1.Step {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].GetEnd() == "" {
			return steps[i]
		}
	}
	return nil
}

// runningTone is the tone of a task whose step runs: mine while the owner holds the step, limited while a usage limit
// holds it, and run otherwise.
func runningTone(step *enginev1.Step) string {
	switch {
	case step.GetHeld():
		return toneMine
	case step.GetLimited():
		return toneLimited
	default:
		return toneRun
	}
}

// epicRecordLifecycles sets the place and tone of each record task in tasks from steps, the epic's labeled steps
// oldest first, of which a task's are those its slug labels.
func epicRecordLifecycles(tasks []*orreryv1.EpicTask, steps []*enginev1.Step) {
	for _, task := range tasks {
		life := recordLifecycle(task.GetStatus(), taskSteps(steps, []string{task.GetSlug()}))
		task.Place, task.Tone = life.place, life.tone
	}
}

// recordLifecycle returns where a record task with status stands, from its steps, by the first rule that holds: a done
// or integrated task is integrated; a gap is bad at its latest step's ring, or at implement with no step; a running
// latest step places it at its ring in its tone; a gated task, or one whose latest step ended done, is accepted at
// integrate; a latest step that ended otherwise leaves it running at its ring, since the task stays open for the next
// attempt; a dispatched task with no step runs at implement; and any other is planned.
func recordLifecycle(status string, steps []*enginev1.Step) lifecycle {
	word := strings.ToLower(status)
	latest := lastStep(steps)
	switch {
	case word == statusDone || word == statusIntegrated:
		return lifecycle{placeIntegrated, toneOk}
	case word == statusGap && latest == nil:
		return lifecycle{placeImplement, toneBad}
	case word == statusGap:
		return lifecycle{recordStepPlace(latest), toneBad}
	case latest != nil && latest.GetEnd() == "":
		return lifecycle{recordStepPlace(latest), runningTone(latest)}
	case word == statusGated || (latest != nil && latest.GetEnd() == epicEndDone):
		return lifecycle{placeIntegrate, toneAcc}
	case latest != nil:
		return lifecycle{recordStepPlace(latest), toneRun}
	case word == statusDispatched:
		return lifecycle{placeImplement, toneRun}
	default:
		return lifecycle{placePlanned, tonePlan}
	}
}

// recordStepPlace is the ring a record task's step places it in, as a record has no build stage: review for a read-only
// step; green for a step running its checks, gate or judge, or one that ended checks_failed or goal_unmet; implement
// for any other.
func recordStepPlace(step *enginev1.Step) string {
	end := step.GetEnd()
	switch {
	case step.GetReadOnly():
		return placeReview
	case end == "" && recordCheckStage(step.GetStage()), end == epicEndChecksFailed, end == epicEndGoalUnmet:
		return placeGreen
	default:
		return placeImplement
	}
}

// recordCheckStage reports whether stage is a loop stage in which a running step checks its work: checks, gate or
// judge.
func recordCheckStage(stage string) bool {
	switch stage {
	case "checks", "gate", "judge":
		return true
	default:
		return false
	}
}
