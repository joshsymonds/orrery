package build

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// endGoalUnmet is the end of an implementer step whose judge found the goal unmet on its second verdict. Its return is
// kept, and the implementer's commit is read from it.
const endGoalUnmet = "goal_unmet"

// statusNeedsContext is the status of an implementer return that asks for the files it needs beyond its owned list.
const statusNeedsContext = "needs_context"

// needsFilesKey is the key of the paths an implementer's return names as needed beyond its owned list.
const needsFilesKey = "needs_files"

// gateChangesKey is the key of the gate-needed changes an implementer's return declares.
const gateChangesKey = "gate_changes"

// implementerReturn is the part of the implementer's kept return Decide reads: its summary, the commit it made, the
// files it names as needed beyond its owned list, which parseImplementerReturn reads from needsFilesKey, and the
// gate-needed changes it declares, which parseImplementerReturn reads from gateChangesKey.
type implementerReturn struct {
	Summary     string             `json:"summary"`
	Commit      string             `json:"commit"`
	NeedsFiles  []string           `json:"-"`
	GateChanges []store.GateChange `json:"-"`
}

// Decide returns the next action for an open task from its view. A stopped or parked build waits until the owner
// releases it, a waiting build until the tasks it waits for are integrated (decideWait), and an asked build until its
// question is answered (decideAsked). A task on ApproachRung waits for its change of approach before its first unit
// (approachAwaited), and a task whose change of approach is checking moves to red before its implementer (approachDue).
// Every other build is decided by its stage (decideStage).
func Decide(view View) Action {
	build := view.Task.Build
	switch build.Held {
	case HeldWaiting:
		return decideWait(view)
	case HeldAsked:
		return decideAsked(view)
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
	if approachAwaited(view) {
		return Action{Kind: ActNone}
	}
	if approachDue(view) {
		build.Stage = StageRed
		build.Step = ""
		build.NoVerdict = 0
		return Action{Kind: ActMove, Next: build}
	}
	return decideStage(view)
}

// decideStage returns the action for a build that no hold or approach move decides, by its stage. A fresh task starts at
// red, or at implement when it is closure-kind, since a closure-kind task runs no red and no green.
func decideStage(view View) Action {
	switch view.Task.Build.Stage {
	case "":
		return decideFresh(view)
	case StageRed:
		return decideRed(view)
	case StageImplement:
		return decideImplement(view)
	case StageOrchestrate:
		return decideOrchestrate(view)
	case StageGreen:
		return decideGreen(view)
	case StageReview:
		return decideReview(view)
	case StageVerify:
		return decideVerify(view)
	case StageDesign:
		return decideDesign(view)
	}
	return Action{Kind: ActNone}
}

// DecideStartError returns the action for a unit whose start the engine refused with err. A red unit climbs the task,
// a green unit spends its attempt as a capture that failed does, an orchestrate unit settles its request as it was
// settled before the orchestrator (requestFallback), and any other unit climbs the task.
func DecideStartError(view View, err error) Action {
	switch view.Task.Build.Stage {
	case StageRed:
		return Action{Kind: ActFail, Reason: fmt.Sprintf("red not captured: %v", err)}
	case StageGreen:
		return spend(view, fmt.Sprintf("green not captured: %v", err))
	case StageOrchestrate:
		return withNote(requestFallback(view), fmt.Sprintf("orchestrate did not start: %v", err))
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
// exists. A closure-kind task runs no red and no green, so it starts at implement, as attempt 1. Its base is the epic's
// candidate when the epic has one (a red batch's fixes start from it), and otherwise the branch tip.
func decideFresh(view View) Action {
	if view.Paused || view.Parked || view.Branch == "" {
		return Action{Kind: ActNone}
	}
	build := view.Task.Build
	build.Stage = StageRed
	if view.Task.Evidence.Kind == evidence.KindClosure {
		build.Stage = StageImplement
		build.Attempt = 1
	}
	build.Base = view.Branch
	if view.Candidate != "" {
		build.Base = view.Candidate
	}
	return Action{Kind: ActMove, Next: build}
}

// decideRed returns the action for the red unit: its evidence run at the base, once it has ended. A red run that
// exited store.EnvironmentExit reruns or parks the unit as environmentRerun decides. Any other red run is judged for
// its note (redOutcome): red is not a gate, so a run whose conclusion holds at the base, that was not captured, or
// whose judge gave no verdict twice, passes into implement with its outcome recorded as a note.
func decideRed(view View) Action {
	build := view.Task.Build
	run := view.Run
	if run == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartRun, Phase: StageRed, Revision: build.Base,
			ID: RunID(unitScope(view.Epic, view.Task), view.Task.Seq, StageRed, build.Attempt, build.Rerun),
		}
	}
	if run.Ended.IsZero() {
		return Action{Kind: ActNone}
	}
	if run.NotCaptured == store.NotCapturedEnvironment {
		return environmentRerun(view, StageRed+" capture")
	}
	if run.NotCaptured != "" {
		return redOutcome(view, run, "not captured: "+run.NotCaptured)
	}
	if run.Verdict == "" {
		if action, again := silentRerun(view); again {
			return action
		}
		return redOutcome(view, run, "the judge gave no verdict twice: "+run.JudgeReason)
	}
	if !run.Passed {
		return redOutcome(view, run, fmt.Sprintf("the conclusion holds at the base (exit %d)", run.Exit))
	}
	return redOutcome(view, run, "")
}

// redOutcome returns the action for a red run that ended, for outcome: what the run gave, as a note's text, or empty
// when red holds at the base and there is nothing to note. The build goes on to implement with the run kept as its red
// run, and the note recorded with the move. A checking version applies whatever red gave (settleVersion). Its note
// lands first, on a move that keeps the build at red, so the settle follows on the next pass once the note is kept.
func redOutcome(view View, run *store.EvidenceRun, outcome string) Action {
	note := ""
	if outcome != "" {
		note = fmt.Sprintf("%s red: %s", taskLabel(view.Task.Seq), outcome)
	}
	if version, ok := view.Task.Checking(); ok {
		if note != "" && !slices.Contains(view.Task.Notes, note) {
			return noted(view, view.Task.Build, note)
		}
		return settleVersion(view, version, run.ID)
	}
	build := view.Task.Build
	build.Stage = StageImplement
	build.Attempt = max(build.Attempt, 1)
	build.Rerun = 0
	build.NoVerdict = 0
	build.RedRun = run.ID
	if note == "" {
		return Action{Kind: ActMove, Next: build}
	}
	return noted(view, build, note)
}

// settleVersion returns the action that applies the checking version and moves the build to implement, with its attempt,
// its rerun and its refs kept, its verdict counted from zero and no step, and redRun as its red run.
func settleVersion(view View, version store.TaskVersion, redRun string) Action {
	build := view.Task.Build
	build.Stage = StageImplement
	build.NoVerdict = 0
	build.Step = ""
	build.RedRun = redRun
	settle := store.TaskVersionSettle{Version: version.Version, State: store.VersionApplied, RedRun: redRun}
	return Action{Kind: ActSettleVersion, Next: build, Settle: &settle}
}

// silentSpend returns the action for an implementer step whose return gave no verdict for reason: its first no verdict
// reruns the unit, and its second spends the attempt.
func silentSpend(view View, reason string) Action {
	if action, again := silentRerun(view); again {
		return action
	}
	return spend(view, fmt.Sprintf("%s implement gave no verdict twice: %s", taskLabel(view.Task.Seq), reason))
}

// decideImplement returns the action for the implement unit. Without a step, the refs are snapshotted and then the
// implementer starts. An ended step first checks the ref moves it made, and then its end: a finished return guards its
// commit before the build moves to green there, or to review for a closure-kind task, which runs no green. Any other
// end spends the attempt, pauses or reruns the unit. A
// returned end that grantOrFail cannot grant, and a checks_failed end whose only failed check is owned, are requests:
// the orchestrator decides them (request), with what the build did before it as the fallback.
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
			ID: StepID(
				unitScope(view.Epic, view.Task), view.Task.Seq, StageImplement, build.Attempt, build.Rerun, 0, 0,
			),
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
			return silentSpend(view, endText(step))
		}
		return Action{
			Kind: ActGuard, Next: finishedBuild(view, ret),
			GateChanges: mergeGateChanges(view.Task.GateChanges, ret.GateChanges),
		}
	case endReturned:
		return decideReturned(view, step)
	case endChecksFailed:
		if onlyOwnedFailed(step) {
			return request(view, spend(view, endText(step)))
		}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	}
	return spend(view, endText(step))
}

// finishedBuild returns the build a finished implementer's return moves to, to be guarded at its commit, or at the head
// when it made none. It moves to green, except for a closure-kind task, which runs no green: its implementer's step has
// passed the task's command gate, so it moves from its gate to review at the first gate.
func finishedBuild(view View, ret implementerReturn) store.TaskBuild {
	build := view.Task.Build
	build.Stage = StageGreen
	if view.Task.Evidence.Kind == evidence.KindClosure {
		build.Stage = StageReview
		build.GatePos = 0
		build.FindingPos = 0
	}
	build.Revision = view.Head
	if ret.Commit != "" {
		build.Revision = ret.Commit
	}
	build.Rerun = 0
	build.NoVerdict = 0
	build.Step = ""
	return build
}

// mergeGateChanges returns the gate-needed changes a task declared before, with declared merged in: a declared file the
// task had not declared is added after them, in declared order, and one it had declared keeps its place and takes the
// new reason. A declared change with a blank path or reason declares nothing. It returns nil when declared adds or
// changes nothing, so the guard's write stores the build alone.
func mergeGateChanges(before, declared []store.GateChange) []store.GateChange {
	merged := slices.Clone(before)
	changed := false
	for _, change := range declared {
		change.Path, change.Reason = strings.TrimSpace(change.Path), strings.TrimSpace(change.Reason)
		if change.Path == "" || change.Reason == "" {
			continue
		}
		i := slices.IndexFunc(merged, func(held store.GateChange) bool { return held.Path == change.Path })
		switch {
		case i < 0:
			merged = append(merged, change)
			changed = true
		case merged[i].Reason != change.Reason:
			merged[i].Reason = change.Reason
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return merged
}

// decideReturned returns the action for an implementer step that ended returned. A return that does not parse gives no
// verdict. A needs_context return that grantOrFail grants is granted. Any other return is a request (request), with the
// failure returnedFail gives as its fallback.
func decideReturned(view View, step *store.StepRow) Action {
	ret, ok := parseImplementerReturn(step.Result)
	if !ok {
		return silentSpend(view, endText(step))
	}
	fail := returnedFail(step, ret)
	if step.Status == statusNeedsContext {
		if grant := grantOrFail(view, fail); grant.Kind == ActGrant {
			return grant
		}
	}
	return request(view, fail)
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

// parseImplementerReturn decodes the implementer's kept return, the paths under needsFilesKey and the changes under
// gateChangesKey when it has them. It reports false when the return does not decode, when needsFilesKey is not a list
// of paths, or when gateChangesKey is not a list of changes.
func parseImplementerReturn(result string) (implementerReturn, bool) {
	var ret implementerReturn
	if err := json.Unmarshal([]byte(result), &ret); err != nil {
		return ret, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &fields); err != nil {
		return ret, false
	}
	if raw, ok := fields[needsFilesKey]; ok {
		if err := json.Unmarshal(raw, &ret.NeedsFiles); err != nil {
			return implementerReturn{}, false
		}
	}
	if raw, ok := fields[gateChangesKey]; ok {
		if err := json.Unmarshal(raw, &ret.GateChanges); err != nil {
			return implementerReturn{}, false
		}
	}
	return ret, true
}

// grantLimit is how many reruns a build may have reached and still be granted files: a grant is a rerun, so a task is
// granted files at most this many times, fewer when other reruns came first.
const grantLimit = 3

// grantOrFail returns the action for an implementer that returned needs_context naming the files fail carries. When
// it names a file the task does not own, the build has reruns left under grantLimit, and every such file passes
// grantRefusal and is not one of the task's refused files, the task is granted those files and its implementer runs
// again in the same worktree, with a note naming them. A file another task holds, open or accepted and not yet
// integrated, is granted like a free file. When a file fails either test, no file is granted by rule and the request
// goes to the orchestrator, which decides it. Otherwise fail is returned, and the task climbs with its failure.
func grantOrFail(view View, fail Action) Action {
	var grant []string
	for _, file := range fail.NeedsFiles {
		if slices.Contains(view.Task.Files, file) || slices.Contains(grant, file) {
			continue
		}
		if grantRefusal(file, view.Task, nil) != "" || slices.Contains(view.Task.Refused, file) {
			return fail
		}
		grant = append(grant, file)
	}
	if len(grant) == 0 || view.Task.Build.Rerun >= grantLimit {
		return fail
	}
	next := view.Task.Build
	next.Rerun++
	next.NoVerdict = 0
	next.Step = ""
	next.Failure = "you returned needs_context for files outside your list, and these are now yours: " +
		strings.Join(grant, ", ") + ". Your earlier work is in this worktree; continue from it."
	return Action{Kind: ActGrant, Next: next, NeedsFiles: grant, Reason: fail.Reason}
}
