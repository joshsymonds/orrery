package build

import (
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// endGoalUnmet is the end of an implementer step whose judge found the goal unmet on its second verdict. Its return is
// kept, and the implementer's commit is read from it.
const endGoalUnmet = "goal_unmet"

// implementerReturn is the part of the implementer's kept return Decide reads: its summary and the commit it made.
type implementerReturn struct {
	Summary string `json:"summary"`
	Commit  string `json:"commit"`
}

// Decide returns the next action for an open task from its view. A held build waits until the owner releases it. A
// fresh task starts at red, and every other build is decided by its stage.
func Decide(view View) Action {
	build := view.Task.Build
	switch build.Held {
	case HeldStopped:
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return releaseHold(build)
	case HeldParked:
		if view.Parked {
			return Action{Kind: ActNone}
		}
		build.NoVerdict = 0
		return releaseHold(build)
	}
	switch build.Stage {
	case "":
		return decideFresh(view)
	case StageRed:
		return decideRed(view)
	case StageImplement:
		return decideImplement(view)
	case StageGreen:
		return decideGreen(view)
	case StageReview:
		return decideReview(view)
	case StageVerify:
		return decideVerify(view)
	}
	return Action{Kind: ActNone}
}

// DecideStartError returns the action for a unit whose start the engine refused with err. A red unit fails, a green
// unit spends its attempt as a capture that failed does, and any other unit fails.
func DecideStartError(view View, err error) Action {
	switch view.Task.Build.Stage {
	case StageRed:
		return Action{Kind: ActFail, Reason: fmt.Sprintf("red not captured: %v", err)}
	case StageGreen:
		return spend(view, fmt.Sprintf("green not captured: %v", err))
	}
	return Action{Kind: ActFail, Reason: fmt.Sprintf("%s did not start: %v", view.Task.Build.Stage, err)}
}

// releaseHold returns the action that releases a held build: it runs its unit again, with no held state.
func releaseHold(build store.TaskBuild) Action {
	build.Held = ""
	build.Rerun++
	return Action{Kind: ActMove, Next: build}
}

// decideFresh returns the action for a task with no stage: it starts at red, once the epic is running and the branch
// exists. Its base is the epic's candidate when the epic has one (a red batch's fixes start from it), and otherwise the
// branch tip.
func decideFresh(view View) Action {
	if view.Paused || view.Parked || view.Branch == "" {
		return Action{Kind: ActNone}
	}
	build := view.Task.Build
	build.Stage = StageRed
	build.Base = view.Branch
	if view.Candidate != "" {
		build.Base = view.Candidate
	}
	return Action{Kind: ActMove, Next: build}
}

// decideRed returns the action for the red unit: its evidence run at the base, once it has ended. A red run that
// fails at the base passes into implement with the run kept. One whose conclusion holds at the base fails the task.
func decideRed(view View) Action {
	build := view.Task.Build
	run := view.Run
	if run == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartRun, Phase: StageRed, Revision: build.Base,
			ID: RunID(view.Epic, view.Task.Seq, StageRed, 0, build.Rerun),
		}
	}
	if run.Ended.IsZero() {
		return Action{Kind: ActNone}
	}
	if run.NotCaptured != "" {
		return Action{Kind: ActFail, Reason: "red not captured: " + run.NotCaptured}
	}
	if run.Verdict == "" {
		return noVerdict(view, StageRed, run.JudgeReason)
	}
	if !run.Passed {
		return Action{Kind: ActFail, Reason: "red: the conclusion holds at the base"}
	}
	build.Stage = StageImplement
	build.Attempt = 1
	build.Rerun = 0
	build.NoVerdict = 0
	build.RedRun = run.ID
	return Action{Kind: ActMove, Next: build}
}

// decideImplement returns the action for the implement unit. Without a step, the refs are snapshotted and then the
// implementer starts. An ended step first checks the ref moves it made, and then its end: a finished return moves the
// build to green at its commit, and any other end spends the attempt, pauses or reruns the unit.
func decideImplement(view View) Action {
	build := view.Task.Build
	step := view.Step
	if step == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		if build.Refs == "" {
			return Action{Kind: ActSnapshot}
		}
		return Action{
			Kind: ActStartStep, Unit: StageImplement,
			ID: StepID(view.Epic, view.Task.Seq, StageImplement, build.Attempt, build.Rerun, 0, 0),
		}
	}
	if step.End == "" {
		return Action{Kind: ActNone}
	}
	if build.Refs != "" {
		reason, tells := implementerMoves(view)
		build.Refs = ""
		return Action{Kind: ActRefs, Next: build, Reason: reason, Tells: tells}
	}
	switch step.End {
	case endDone, endGoalUnmet:
		ret, ok := parseImplementerReturn(step.Result)
		if !ok {
			return noVerdict(view, StageImplement, endText(step))
		}
		build.Stage = StageGreen
		build.Revision = view.Head
		if ret.Commit != "" {
			build.Revision = ret.Commit
		}
		build.Rerun = 0
		build.NoVerdict = 0
		build.Step = ""
		return Action{Kind: ActMove, Next: build}
	case endReturned:
		ret, ok := parseImplementerReturn(step.Result)
		if !ok {
			return noVerdict(view, StageImplement, endText(step))
		}
		return Action{Kind: ActFail, Reason: fmt.Sprintf("returned %s: %s", step.Status, ret.Summary)}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	}
	return spend(view, endText(step))
}

// implementerMoves returns what the implementer's ref moves do. Reason is the text of the first move that parks, empty
// when none does. Tells is the text of each move that does not park, in order.
func implementerMoves(view View) (string, []string) {
	label := taskLabel(view.Task.Seq)
	reason := ""
	var tells []string
	for _, move := range view.RefMoves {
		text := fmt.Sprintf("%s implementer moved %s (%s to %s)", label, move.Ref, move.From, move.To)
		switch {
		case !move.Parks:
			tells = append(tells, text)
		case reason == "":
			reason = text
		}
	}
	return reason, tells
}

// parseImplementerReturn decodes the implementer's kept return. It reports false when the return does not decode.
func parseImplementerReturn(result string) (implementerReturn, bool) {
	var ret implementerReturn
	err := json.Unmarshal([]byte(result), &ret)
	return ret, err == nil
}
