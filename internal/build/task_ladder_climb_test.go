package build

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// ladderClimbBefore returns task 1's build as it stands before a failing pass, and fails the test unless its attempt is
// attempt.
func ladderClimbBefore(f *passFixture, attempt int64) store.TaskBuild {
	f.t.Helper()
	build := f.task().Build
	if build.Attempt != attempt {
		f.t.Fatalf("build attempt = %d before the failing pass, want %d", build.Attempt, attempt)
	}
	return build
}

// ladderClimbCheck fails the test unless task 1 of f's epic, read after a pass that climbed it off rung left, has the
// status wantStatus and holds the rung after left: its build is on that rung, rung left ended failed with the task's
// failure as its reason, and the rung after left was moved by movedBy.
func ladderClimbCheck(f *passFixture, left int64, wantStatus, movedBy string) {
	f.t.Helper()
	task := f.task()
	if task.Status != wantStatus || task.Build.Attempt != left+1 {
		f.t.Errorf("task status %q attempt %d, want %q on rung %d",
			task.Status, task.Build.Attempt, wantStatus, left+1)
	}
	var ended, moved bool
	for _, rung := range task.Rungs {
		ended = ended || rung.Rung == left && rung.Outcome == store.RungFailed && rung.Reason == task.Build.Failure
		moved = moved || rung.Rung == left+1 && rung.MovedBy == movedBy
	}
	if !ended {
		f.t.Errorf("rungs = %+v, want rung %d ended failed with reason %q", task.Rungs, left, task.Build.Failure)
	}
	if !moved {
		f.t.Errorf("rungs = %+v, want rung %d moved by %q", task.Rungs, left+1, movedBy)
	}
}

// ladderClimbKeepsWorktrees fails the test unless task 1's directory still exists and git still lists the worktree at
// path, as a climb to a rung before the gap rung leaves them.
func ladderClimbKeepsWorktrees(f *passFixture, path string) {
	f.t.Helper()
	if _, err := os.Stat(TaskDir(f.stateDir, passEpic, 1)); err != nil {
		f.t.Errorf("task directory stat = %v, want it kept", err)
	}
	if !buildListsWorktree(f.t, f.repo, path) {
		f.t.Errorf("git does not list worktree %s, want it kept", path)
	}
}

// ladderClimbExecute carries out action for task 1 of f's epic, and fails the test when the store wrote nothing.
func ladderClimbExecute(f *passFixture, action Action) {
	f.t.Helper()
	if !f.runner.execute(f.ctx, f.epic(), View{Epic: passEpic, Task: f.task()}, action) {
		f.t.Fatalf("execute(%s) = false, want the climb written", action.Kind)
	}
}

// TestTaskLadderFailuresClimbOneRung: a unit's failure on any attempt is a fail with no move named, a confirmed finding
// is a fail moved by the finding at its gate, and a fail decision is a fail moved by the decision.
func TestTaskLadderFailuresClimbOneRung(t *testing.T) {
	t.Parallel()
	const failure = "the judge found a defect in the second attempt"
	for _, attempt := range []int64{1, 2} {
		got := spend(taskView(store.TaskBuild{Stage: StageGreen, Attempt: attempt}), failure)
		if want := (Action{Kind: ActFail, Reason: failure}); !reflect.DeepEqual(got, want) {
			t.Errorf("spend on attempt %d = %+v, want %+v", attempt, got, want)
		}
	}

	findings := View{
		Epic: decideEpic, ReviewGates: []string{"lint", "security"},
		Task: store.EpicTask{Seq: 3, Reviews: verifyReviews(1, "dropped", "confirmed"), Build: store.TaskBuild{
			Stage: StageVerify, Base: decideBranch, Attempt: 1, GatePos: 0, FindingPos: 2, RedRun: "red-run",
			GreenRun: "green-run", Revision: "rev",
		}},
	}
	wantFinding := Action{
		Kind: ActFail, Reason: "confirmed: F2 claim two",
		Move: store.RungMove{MovedBy: store.MovedByFinding, Gate: "lint", Assertion: "F2 claim two"},
	}
	if got := Decide(findings); !reflect.DeepEqual(got, wantFinding) {
		t.Errorf("settled finding = %+v, want %+v", got, wantFinding)
	}

	decided := orchView(2, orchDone(orchReturn(t, map[string]any{
		"decision": "fail", "failure": "the approach cannot meet the gate", "reason": "the gate is unmeetable",
		"needs_files": []string{"b.go"},
	})))
	wantDecided := Action{
		Kind: ActFail, Reason: "the approach cannot meet the gate", NeedsFiles: []string{"b.go"},
		Decision: &store.TaskDecision{ID: "T3-request-3", Text: "T3 fail: the gate is unmeetable"},
		Move:     store.RungMove{MovedBy: store.MovedByFailDecision},
	}
	if got := Decide(decided); !reflect.DeepEqual(got, wantDecided) {
		t.Errorf("fail decision = %+v, want %+v", got, wantDecided)
	}
}

// TestTaskLadderExecuteClimbs: executing a fail climbs the task over a real store, writing its rungs, its build, its
// needs files and its decision, and keeps its worktree.
func TestTaskLadderExecuteClimbs(t *testing.T) {
	t.Parallel()
	t.Run("a green attempt one climbs to rung two", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: 1, Revision: f.base})
		from := ladderClimbBefore(f, 1)
		const cause = "green not met: exit 1, verdict fails"
		ladderClimbExecute(f, Action{Kind: ActFail, Reason: cause})
		task := f.task()
		if want := store.ClimbedBuild(from, 2, cause); !reflect.DeepEqual(task.Build, want) || task.Status != taskOpen {
			t.Errorf("task status %q build %+v, want open on %+v", task.Status, task.Build, want)
		}
		want := []store.TaskRung{
			{Rung: 1, Kind: store.RungAttempt, Created: passClock(), Outcome: store.RungFailed, Reason: cause,
				Ended: passClock()},
			{Rung: 2, Kind: store.RungAttempt, MovedBy: store.MovedByUnit, Cause: cause, Created: passClock()},
		}
		if !reflect.DeepEqual(task.Rungs, want) {
			t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, want)
		}
	})

	t.Run("a red build before its first attempt climbs to attempt two in red", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageRed, Base: f.base})
		ladderClimbBefore(f, 0)
		const cause = "red: the conclusion holds at the base"
		ladderClimbExecute(f, Action{Kind: ActFail, Reason: cause})
		task := f.task()
		if task.Build.Attempt != 2 || task.Build.Stage != StageRed || task.Status != taskOpen {
			t.Errorf("task status %q build %+v, want open in red at attempt 2", task.Status, task.Build)
		}
		want := []store.TaskRung{
			{Rung: 1, Kind: store.RungAttempt, Created: passClock(), Outcome: store.RungFailed, Reason: cause,
				Ended: passClock()},
			{Rung: 2, Kind: store.RungAttempt, MovedBy: store.MovedByUnit, Cause: cause, Created: passClock()},
		}
		if !reflect.DeepEqual(task.Rungs, want) {
			t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, want)
		}
	})

	t.Run("a finding move records its gate and assertion on the new rung", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageVerify, Base: f.base, Attempt: 1, Revision: f.base})
		const claim = "F1 the thing is wrong"
		ladderClimbExecute(f, Action{Kind: ActFail, Reason: "confirmed: " + claim, Move: store.RungMove{
			MovedBy: store.MovedByFinding, Gate: "review", Assertion: claim,
		}})
		want := []store.TaskRung{
			{Rung: 1, Kind: store.RungAttempt, Created: passClock(), Outcome: store.RungFailed,
				Reason: "confirmed: " + claim, Ended: passClock()},
			{
				Rung: 2, Kind: store.RungAttempt, MovedBy: store.MovedByFinding, Cause: "confirmed: " + claim,
				Gate: "review", Assertion: claim, Created: passClock(),
			},
		}
		if got := f.task().Rungs; !reflect.DeepEqual(got, want) {
			t.Errorf("rungs:\n got %+v\nwant %+v", got, want)
		}
	})

	t.Run("needs files are stored as the task's needs files", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base})
		ladderClimbExecute(f, Action{
			Kind: ActFail, Reason: "returned needs_context: stuck", NeedsFiles: []string{"internal/other/a.go"},
		})
		if got, want := f.task().NeedsFiles, []string{"internal/other/a.go"}; !reflect.DeepEqual(got, want) {
			t.Errorf("NeedsFiles = %v, want %v", got, want)
		}
	})

	t.Run("a fail decision is appended to the decisions and its cause keeps the decision's length", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base})
		long := strings.Repeat("x", store.TaskDecisionMax+200)
		decision := &store.TaskDecision{ID: "T1-request-1", Text: "T1 fail: the code says so"}
		ladderClimbExecute(f, Action{
			Kind: ActFail, Reason: long, Decision: decision,
			Move: store.RungMove{MovedBy: store.MovedByFailDecision},
		})
		if got := f.task().Build.Failure; got != long[:store.TaskDecisionMax] {
			t.Errorf("failure of %d bytes, want the first %d bytes of the reason", len(got), store.TaskDecisionMax)
		}
		d := f.epic().Decisions
		if len(d) == 0 || d[len(d)-1].ID != decision.ID || d[len(d)-1].Text != decision.Text {
			t.Errorf("decisions = %+v, want %s recorded last", d, decision.ID)
		}
	})

	t.Run("the implement worktree is kept after the climb", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base})
		worktree := f.addTaskWorktree("implement")
		ladderClimbBefore(f, 1)
		ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})
		ladderClimbCheck(f, 1, taskOpen, store.MovedByUnit)
		ladderClimbKeepsWorktrees(f, worktree)
	})
}

// TestTaskLadderClimbFromLastRungRemovesWorktrees: a fail on rung 6 climbs the task onto the gap rung, which ends the
// task as a gap with its gap trigger pending and removes its task directory.
func TestTaskLadderClimbFromLastRungRemovesWorktrees(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: store.LastRung, Revision: f.base})
	worktree := f.addTaskWorktree("implement")
	const cause = "green not met: exit 1, verdict fails"
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: cause})

	task := f.task()
	if task.Status != store.TaskGap || task.Build.Attempt != store.GapRung {
		t.Errorf("task status %q attempt %d, want gap on rung %d", task.Status, task.Build.Attempt, store.GapRung)
	}
	if n := len(task.Rungs); n == 0 || task.Rungs[n-1].Rung != store.GapRung || task.Rungs[n-1].Kind != store.RungGap {
		t.Errorf("rungs = %+v, want a last rung %d of kind gap", task.Rungs, store.GapRung)
	}
	if got := f.pendingTriggers(); !slices.Contains(got, "T1-gap") {
		t.Errorf("pending triggers = %v, want T1-gap", got)
	}
	f.expectNoWorktrees(worktree)
}

// TestTaskLadderRedPassKeepsRung: a red run that passes on attempt two moves the build into implement on attempt two,
// not attempt one, since a build that climbed while in red keeps its rung.
func TestTaskLadderRedPassKeepsRung(t *testing.T) {
	t.Parallel()
	passed := &store.EvidenceRun{
		ID: "red-run", Ended: time.Unix(1700000900, 0), Verdict: evidence.VerdictFails, Passed: true,
	}
	build := store.TaskBuild{Stage: StageRed, Base: decideBranch, Attempt: 2, Rerun: 1, NoVerdict: 1}
	got := Decide(View{Epic: decideEpic, Run: passed, Task: store.EpicTask{Seq: 3, Build: build}})
	want := Action{Kind: ActMove, Next: store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 2, RedRun: "red-run",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("red pass at attempt 2 = %+v, want %+v", got, want)
	}
}

// TestTaskLadderWaitOnGapTaskClimbs: a build waiting on a task that ended as a gap climbs, naming the task as ended as
// a gap.
func TestTaskLadderWaitOnGapTaskClimbs(t *testing.T) {
	t.Parallel()
	gapped := integrationOf(store.EpicTask{Seq: 4, Status: store.TaskGap}, nil)
	view := View{
		Epic: decideEpic, Peers: []Peer{gapped},
		Task: store.EpicTask{Seq: 3, Status: taskOpen, Build: store.TaskBuild{
			Stage: StageImplement, Base: decideBranch, Attempt: 1, Held: HeldWaiting, After: "4",
		}},
	}
	want := Action{Kind: ActFail, Reason: "T3 waited on T4, which ended as a gap"}
	if got := Decide(view); !reflect.DeepEqual(got, want) {
		t.Errorf("wait on a gap task = %+v, want %+v", got, want)
	}
}
