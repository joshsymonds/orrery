package build

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The invented values the approach tests use: the number of the version a change of approach stores in the view tests,
// and the reason and deliverable of the change the runner test stores.
const (
	ladderApproachWaitNumber      = 2
	ladderApproachWaitReason      = "the approach names the field it needs"
	ladderApproachWaitDeliverable = "The thing holds through the field."
)

// ladderApproachWaitBuild returns a build of task 3 in stage on attempt, with a red run held and no step, refs or run
// begun.
func ladderApproachWaitBuild(stage string, attempt int64) store.TaskBuild {
	return store.TaskBuild{Stage: stage, Base: decideBranch, Attempt: attempt, RedRun: "red-run"}
}

// ladderApproachWaitView returns the view of open task 3 of decideEpic on build, with versions stored and no step.
func ladderApproachWaitView(build store.TaskBuild, versions ...store.TaskVersion) View {
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen, Build: build, Versions: versions}
	return View{Epic: decideEpic, Task: task}
}

// ladderApproachWaitVersion returns a change of approach in state, as the planner's change_approach stores it.
func ladderApproachWaitVersion(state string) store.TaskVersion {
	return store.TaskVersion{Version: ladderApproachWaitNumber, Source: store.SourceApproach, State: state}
}

// ladderApproachWaitApproaches returns how many versions of task were stored by a change of approach.
func ladderApproachWaitApproaches(task store.EpicTask) int {
	n := 0
	for _, version := range task.Versions {
		if version.Source == store.SourceApproach {
			n++
		}
	}
	return n
}

// TestTaskLadderApproachWaitsForVersion: a build on ApproachRung starts no unit until the planner's change of approach
// is stored. An applied version starts its implementer as on any rung, a checking version moves the build to red at the
// base, and a refused version does not wait again. A build on another rung, and a build in another stage, decides as it
// did before.
func TestTaskLadderApproachWaitsForVersion(t *testing.T) {
	t.Parallel()
	movedRed := ladderApproachWaitBuild(StageImplement, store.ApproachRung)
	movedRed.Stage = StageRed
	review := ladderApproachWaitView(ladderApproachWaitBuild(StageReview, store.ApproachRung))
	review.ReviewGates = []string{"review"}
	reviewStep := StepID(decideEpic, 3, StageReview, store.ApproachRung, 0, 0, 0)
	for _, tc := range []struct {
		name string
		view View
		want Action
	}{
		{
			"implement waits with no version", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.ApproachRung)),
			Action{Kind: ActNone},
		},
		{
			"red waits with no run", ladderApproachWaitView(ladderApproachWaitBuild(StageRed, store.ApproachRung)),
			Action{Kind: ActNone},
		},
		{
			"an applied version starts the snapshot", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.ApproachRung),
				ladderApproachWaitVersion(store.VersionApplied)),
			Action{Kind: ActSnapshot},
		},
		{
			"a checking version moves to red", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.ApproachRung),
				ladderApproachWaitVersion(store.VersionChecking)),
			Action{Kind: ActMove, Next: movedRed},
		},
		{
			"a refused version does not wait again", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.ApproachRung),
				ladderApproachWaitVersion(store.VersionRefused)),
			Action{Kind: ActSnapshot},
		},
		{
			"rung 5 with no version does not wait", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.LastRung)),
			Action{Kind: ActSnapshot},
		},
		{
			"rung 3 with no version does not wait", ladderApproachWaitView(
				ladderApproachWaitBuild(StageImplement, store.OrchestratorRung)),
			Action{Kind: ActSnapshot},
		},
		{
			"a review on rung 4 is not held", review,
			Action{Kind: ActStartStep, Unit: StageReview, ID: reviewStep},
		},
	} {
		if got := Decide(tc.view); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: Decide = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// ladderApproachWaitCheckLastStart fails the test unless the last step f's runner started is id.
func ladderApproachWaitCheckLastStart(f *passFixture, id string) {
	f.t.Helper()
	if len(f.steps.starts) == 0 {
		f.t.Fatalf("no step started, want %s", id)
	}
	if got := f.lastStart().ID; got != id {
		f.t.Errorf("last start %s, want %s", got, id)
	}
}

// ladderApproachWaitCheckRungs fails the test unless task 1's rungs 4 and 5 are approach rungs that ended failed, and
// its rung 6 is a gap rung.
func ladderApproachWaitCheckRungs(f *passFixture) {
	f.t.Helper()
	byRung := map[int64]store.TaskRung{}
	for _, rung := range f.task().Rungs {
		byRung[rung.Rung] = rung
	}
	for _, n := range []int64{store.ApproachRung, store.LastRung} {
		if rung := byRung[n]; rung.Kind != store.RungApproach || rung.Outcome != store.RungFailed {
			f.t.Errorf("rung %d = %+v, want an approach rung that ended failed", n, rung)
		}
	}
	if rung := byRung[store.GapRung]; rung.Kind != store.RungGap {
		f.t.Errorf("rung %d = %+v, want a gap rung", store.GapRung, rung)
	}
}

// TestTaskLadderApproachRunsTwoAttempts: a task that climbs onto ApproachRung starts no step until the planner's change
// of approach is stored. Then its implementer runs on rung 4 under that version, and when that attempt fails the task
// climbs to rung 5 with no wait and no second version. When rung 5 fails too, the task ends as a gap.
func TestTaskLadderApproachRunsTwoAttempts(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: store.OrchestratorRung, Revision: f.base})
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "the orchestrator's attempt failed the gate"})
	ladderClimbCheck(f, store.OrchestratorRung, store.MovedByUnit)

	started := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts); got != started {
		t.Errorf("steps started = %d while awaiting the change of approach, want none", got-started)
	}
	if build := f.task().Build; build.Attempt != store.ApproachRung || build.Stage != StageImplement ||
		build.Step != "" {
		t.Errorf("build %+v while awaiting the change of approach, want implement on rung 4 with no step", build)
	}

	deliverable := ladderApproachWaitDeliverable
	err := f.store.RerunTask(
		f.ctx, passEpic, 1, f.task().Build, f.task().Build,
		store.TaskVersionInput{
			Source: store.SourceApproach, Reason: ladderApproachWaitReason, Deliverable: &deliverable,
		},
		nil,
		store.TaskDecision{ID: "T1-approach", Text: "T1 change of approach: " + ladderApproachWaitReason},
		passClock(),
	)
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	f.pass()
	ladderApproachWaitCheckLastStart(f, passStep(StageImplement, store.ApproachRung, 0))

	f.setStep(passStep(StageImplement, store.ApproachRung, 0), endChecksFailed, "")
	f.pass()
	ladderClimbCheck(f, store.ApproachRung, store.MovedByUnit)
	f.pass()
	ladderApproachWaitCheckLastStart(f, passStep(StageImplement, store.LastRung, 0))
	if n := ladderApproachWaitApproaches(f.task()); n != 1 {
		t.Errorf("change-of-approach versions = %d, want the one stored version", n)
	}

	f.setStep(passStep(StageImplement, store.LastRung, 0), endChecksFailed, "")
	f.pass()
	if status := f.task().Status; status != store.TaskGap {
		t.Errorf("task status = %q, want %q", status, store.TaskGap)
	}
	ladderApproachWaitCheckRungs(f)
}
