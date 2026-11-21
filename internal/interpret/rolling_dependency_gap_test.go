package interpret

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// dependencyGapBuild is the stored build of the open task that waits on a lost dependency. Guard makes it non-zero,
// so a test sees the build the action carries.
var dependencyGapBuild = store.TaskBuild{Guard: "refused before it started"}

// lostTask returns task seq of turn 1 with status, which a dependency that is lost has.
func lostTask(seq int64, status string) store.EpicTask {
	return store.EpicTask{Seq: seq, Turn: 1, Status: status}
}

// TestRollingIntegrateDependencyGapDecided: the open task T2 has not started and depends on T1, which is lost. Its
// running build child gives a gap task for T2 naming the lost dependency, and no run. An unrelated open task T3 with no
// dependency still gets its run.
func TestRollingIntegrateDependencyGapDecided(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status string
		reason string
	}{
		{name: "failed dependency", status: "failed", reason: "dependency T1 failed"},
		{name: "dependency that ended as a gap", status: store.TaskGap, reason: "dependency T1 ended as a gap"},
	}
	for _, tc := range cases {
		dependent := openTask(2, 1)
		dependent.DependsOn = []int64{1}
		dependent.Build = dependencyGapBuild
		tasks := []store.EpicTask{lostTask(1, tc.status), dependent, openTask(3, 1)}
		runs := []store.NodeRun{
			runningPlan(0),
			running(childOf("plan/build/T2", 0, 2, 1, 2)),
			running(childOf("plan/build/T3", 0, 3, 1, 3)),
		}
		acts := Decide(rollingFlow(), rowsOf([]store.EpicTurn{turnAt(1, "work")}, tasks, runs...),
			integrateKinds(scripted{}))

		var gaps []Action
		for _, act := range acts {
			if act.Kind == ActRun && act.Path == "plan/build/T2" {
				t.Errorf("%s: Decide = %+v, want no run for T2, whose dependency is lost", tc.name, acts)
			}
			if act.Kind == ActGapTask {
				gaps = append(gaps, act)
			}
		}
		want := Action{
			Kind: ActGapTask, Path: "plan/build/T2", Task: 2, Build: dependencyGapBuild, Reason: tc.reason,
		}
		if !reflect.DeepEqual(gaps, []Action{want}) {
			t.Errorf("%s: gap tasks = %+v, want %+v", tc.name, gaps, want)
		}
		if !hasRunAt(acts, "plan/build/T3") {
			t.Errorf("%s: Decide = %+v, want T3, which has no dependency, run", tc.name, acts)
		}
	}
}

// hasRunAt reports whether acts hand a run of the node at path.
func hasRunAt(acts []Action, path string) bool {
	for _, act := range acts {
		if act.Kind == ActRun && act.Path == path {
			return true
		}
	}
	return false
}

// gapRungOf returns the rung of the task's ladder that is rung store.GapRung, and whether it has one.
func gapRungOf(task store.EpicTask) (store.TaskRung, bool) {
	for _, rung := range task.Rungs {
		if rung.Rung == store.GapRung {
			return rung, true
		}
	}
	return store.TaskRung{}, false
}

// TestRollingIntegrateDependencyGapCarried: carrying the gap task of T2, open at attempt 0, over a real store ends T2
// as a gap on rung 6, moved by its dependency and naming it, and adds the pending trigger for the gap. Carrying the
// same action again, after T2 has moved, is no error and writes nothing more.
func TestRollingIntegrateDependencyGapCarried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, planBuildPin())
	plan := runPlan("complete")
	plan.Tasks[1].DependsOn = []string{"first"}
	completeTurn(t, s, plan)
	runner := newRunner(s, &fakePlanner{}, &fakeBuilder{})

	task := epicOf(t, s).Tasks[1]
	if task.Seq != 2 || !reflect.DeepEqual(task.DependsOn, []int64{1}) || task.Build.Attempt != 0 {
		t.Fatalf("task = %+v, want open T2 at attempt 0 depending on T1", task)
	}
	const reason = "dependency T1 failed"
	act := Action{Kind: ActGapTask, Path: "plan/build/T2", Task: task.Seq, Build: task.Build, Reason: reason}
	if err := runner.carry(ctx, epicOf(t, s), act); err != nil {
		t.Fatalf("carry of the gap task: %v", err)
	}

	gapped := epicOf(t, s).Tasks[1]
	if gapped.Status != store.TaskGap {
		t.Errorf("task status = %q, want %q", gapped.Status, store.TaskGap)
	}
	rung, ok := gapRungOf(gapped)
	if !ok {
		t.Fatalf("task rungs = %+v, want rung %d", gapped.Rungs, store.GapRung)
	}
	if rung.MovedBy != store.MovedByDependency || rung.Cause != reason {
		t.Errorf("gap rung moved by %q with cause %q, want moved by %q with cause %q",
			rung.MovedBy, rung.Cause, store.MovedByDependency, reason)
	}
	wantTrigger := "task T2 ended as a gap: " + reason
	if got := dependencyGapTriggers(t, s); !reflect.DeepEqual(got, []string{wantTrigger}) {
		t.Errorf("pending T2-gap triggers = %q, want the one trigger %q", got, wantTrigger)
	}

	if err := runner.carry(ctx, epicOf(t, s), act); err != nil {
		t.Errorf("carry of the same gap task after T2 moved: %v", err)
	}
	if got := dependencyGapTriggers(t, s); !reflect.DeepEqual(got, []string{wantTrigger}) {
		t.Errorf("after the second carry, pending T2-gap triggers = %q, want the one trigger %q", got, wantTrigger)
	}
}

// dependencyGapTriggers returns the causes of the pending triggers of the test epic whose id is T2-gap.
func dependencyGapTriggers(t *testing.T, s *store.Store) []string {
	t.Helper()
	triggers, err := s.TurnTriggers(context.Background(), runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	var causes []string
	for _, trigger := range triggers {
		if trigger.ID == "T2-gap" {
			causes = append(causes, trigger.Cause)
		}
	}
	return causes
}
