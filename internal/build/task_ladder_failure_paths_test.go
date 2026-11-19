package build

import (
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// ladderPathsStep is the id of the implementer step the failure path tests end, which an orchestrate build keeps as its
// step.
const ladderPathsStep = "impl-step"

// ladderPathsWantFail fails the test unless action is a fail for reason.
func ladderPathsWantFail(t *testing.T, action Action, reason string) {
	t.Helper()
	if action.Kind != ActFail || action.Reason != reason {
		t.Fatalf("decision = %v %q, want %v %q", action.Kind, action.Reason, ActFail, reason)
	}
}

// ladderPathsHoldingRun returns the red run of task 1 that ended with a verdict that holds at the base, and did not
// pass.
func ladderPathsHoldingRun() *store.EvidenceRun {
	return &store.EvidenceRun{
		ID: passRunID(StageRed, 1), Ended: time.Unix(1700000900, 0), Verdict: evidence.VerdictHolds, Passed: false,
	}
}

// ladderPathsCheckingVersion stores the evidence of task 1 changed at its stored build from, as an orchestrator's rerun
// does, so the task has a checking version for a red run to settle.
func ladderPathsCheckingVersion(f *passFixture, from store.TaskBuild) {
	f.t.Helper()
	item := evidence.Item{
		Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the thing holds at its base", Judge: "the judge",
	}
	to := store.TaskBuild{Stage: StageRed, Base: f.base, Attempt: 1, Rerun: 1, Revision: f.base}
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "orch-step", Reason: "the evidence moved", Evidence: &item,
	}
	decision := store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: the evidence moved"}
	if err := f.store.RerunTask(f.ctx, passEpic, 1, from, to, version, nil, decision, passClock()); err != nil {
		f.t.Fatalf("RerunTask: %v", err)
	}
}

// ladderPathsImplementBuild returns task 1's build at implement on attempt 1 at base, whose implementer step is
// ladderPathsStep, with decisions orchestrator decisions made.
func ladderPathsImplementBuild(base string, decisions int64) store.TaskBuild {
	return store.TaskBuild{
		Stage: StageImplement, Base: base, Attempt: 1, Revision: base, Step: ladderPathsStep, Decisions: decisions,
	}
}

// ladderPathsReturnedStep returns the implementer step of task 1, ended returned with status and the kept return
// result.
func ladderPathsReturnedStep(status, result string) *store.StepRow {
	return &store.StepRow{ID: ladderPathsStep, End: endReturned, Status: status, Result: result}
}

// ladderPathsVerifyBuild returns task 1's build at verify on attempt 1 past its two findings, at the base.
func ladderPathsVerifyBuild(f *passFixture) store.TaskBuild {
	return store.TaskBuild{Stage: StageVerify, Base: f.base, Attempt: 1, Revision: f.base, FindingPos: 2}
}

// ladderPathsReturned fails the test unless an implementer step ended returned with status and the kept return result,
// on a build that has made decisionLimit decisions, climbs task 1 with reason, carrying the needs files wantNeeds. It
// also fails the test unless the same return on a build with no decisions made moves the build to orchestrate and
// climbs nothing.
func ladderPathsReturned(t *testing.T, status, result, reason string, wantNeeds []string) {
	t.Helper()
	f := newPassFixture(t)
	f.setBuild(ladderPathsImplementBuild(f.base, decisionLimit))
	ladderClimbBefore(f, 1)
	action := Decide(View{Epic: passEpic, Task: f.task(), Step: ladderPathsReturnedStep(status, result)})
	ladderPathsWantFail(t, action, reason)
	if !slices.Equal(action.NeedsFiles, wantNeeds) {
		t.Errorf("NeedsFiles = %v, want %v", action.NeedsFiles, wantNeeds)
	}
	ladderClimbExecute(f, action)
	ladderClimbCheck(f, 1, store.MovedByUnit)
	if !slices.Equal(f.task().NeedsFiles, wantNeeds) {
		t.Errorf("task NeedsFiles = %v, want %v", f.task().NeedsFiles, wantNeeds)
	}

	g := newPassFixture(t)
	g.setBuild(ladderPathsImplementBuild(g.base, 0))
	ladderClimbBefore(g, 1)
	move := Decide(View{Epic: passEpic, Task: g.task(), Step: ladderPathsReturnedStep(status, result)})
	if move.Kind != ActMove || move.Next.Stage != StageOrchestrate {
		t.Fatalf("decision with no decisions made = %v %+v, want a move to orchestrate", move.Kind, move.Next)
	}
	ladderClimbExecute(g, move)
	if task := g.task(); task.Build.Stage != StageOrchestrate || task.Build.Attempt != 1 || len(task.Rungs) != 0 {
		t.Errorf("task build %+v rungs %+v, want orchestrate at attempt 1 with no rungs", task.Build, task.Rungs)
	}
}

// TestTaskLadderFailurePathsRedHoldsAtBase: a red run whose conclusion holds at the base climbs a task at red one rung,
// and while the task has a checking version it settles that version refused instead.
func TestTaskLadderFailurePathsRedHoldsAtBase(t *testing.T) {
	t.Parallel()
	t.Run("climbs one rung", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageRed, Base: f.base, Attempt: 1, Revision: f.base})
		ladderClimbBefore(f, 1)
		action := Decide(View{Epic: passEpic, Run: ladderPathsHoldingRun(), Task: f.task()})
		ladderPathsWantFail(t, action, "red: the conclusion holds at the base")
		ladderClimbExecute(f, action)
		ladderClimbCheck(f, 1, store.MovedByUnit)
	})
	t.Run("a checking version is settled refused and climbs nothing", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		f.setBuild(store.TaskBuild{Stage: StageRed, Base: f.base, Attempt: 1, Revision: f.base})
		from := ladderClimbBefore(f, 1)
		ladderPathsCheckingVersion(f, from)
		version, ok := f.task().Checking()
		if !ok {
			t.Fatal("task has no checking version after its evidence changed")
		}
		action := Decide(View{Epic: passEpic, Run: ladderPathsHoldingRun(), Task: f.task()})
		want := store.TaskVersionSettle{
			Version: version.Version, State: store.VersionRefused, RedRun: passRunID(StageRed, 1),
			Refusal: "red: the conclusion holds at the base",
		}
		if action.Kind != ActSettleVersion || action.Settle == nil || *action.Settle != want {
			t.Fatalf("decision = %v %+v, want settling %+v refused", action.Kind, action.Settle, want)
		}
		ladderClimbExecute(f, action)
		task := f.task()
		if task.Build.Attempt != 1 || task.Status != taskOpen || len(task.Rungs) != 0 {
			t.Errorf("task status %q attempt %d rungs %+v, want open at attempt 1 with no rungs",
				task.Status, task.Build.Attempt, task.Rungs)
		}
		if _, ok := task.Checking(); ok {
			t.Errorf("task still has a checking version after the red run settled it")
		}
	})
}

// TestTaskLadderFailurePathsReturned: an implementer step that ended returned climbs a task one rung once its build has
// made decisionLimit decisions, with the status and summary of its return, and carries the files a needs_context return
// names. With no decisions made, the same return moves the build to orchestrate instead.
func TestTaskLadderFailurePathsReturned(t *testing.T) {
	t.Parallel()
	t.Run("needs_context", func(t *testing.T) {
		t.Parallel()
		ladderPathsReturned(t, "needs_context",
			`{"status":"needs_context","summary":"the spec is short","needs_files":["thing.go"]}`,
			"returned needs_context: the spec is short", []string{"thing.go"})
	})
	t.Run("blocked", func(t *testing.T) {
		t.Parallel()
		ladderPathsReturned(t, "blocked", `{"status":"blocked","summary":"the gate is broken"}`,
			"returned blocked: the gate is broken", nil)
	})
}

// TestTaskLadderFailurePathsOrchestratorBlocked: an orchestrator that ended done with status blocked settles its
// implementer's blocked return as a fail, which climbs the task one rung.
func TestTaskLadderFailurePathsOrchestratorBlocked(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.setBuild(
		store.TaskBuild{Stage: StageOrchestrate, Base: f.base, Attempt: 1, Revision: f.base, Step: ladderPathsStep},
	)
	ladderClimbBefore(f, 1)
	orchestrator := orchDone(orchReturn(t, map[string]any{
		"status": "blocked", "evidence": "a.go:3 calls b", "reason": "the implementer could not finish",
	}))
	action := Decide(View{
		Epic: passEpic, Task: f.task(), Step: orchestrator,
		Returned: ladderPathsReturnedStep("blocked", `{"status":"blocked","summary":"the gate cannot run"}`),
	})
	ladderPathsWantFail(t, action, "returned blocked: the gate cannot run")
	ladderClimbExecute(f, action)
	ladderClimbCheck(f, 1, store.MovedByUnit)
}

// TestTaskLadderFailurePathsOnlyConfirmedFindings: a review whose findings all settle dropped moves the build to the
// next gate, and only a confirmed finding climbs the task, moved by the finding at its gate.
func TestTaskLadderFailurePathsOnlyConfirmedFindings(t *testing.T) {
	t.Parallel()
	gates := []string{"lint", "security"}
	t.Run("dropped findings move to the next gate", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t, gates...)
		f.setBuild(ladderPathsVerifyBuild(f))
		ladderClimbBefore(f, 1)
		task := f.task()
		task.Reviews = verifyReviews(1, "dropped", "dropped")
		action := Decide(View{Epic: passEpic, ReviewGates: gates, Task: task})
		if action.Kind != ActMove || action.Next.Stage != StageReview || action.Next.GatePos != 1 {
			t.Fatalf("decision = %v %+v, want a move to the review of gate 1", action.Kind, action.Next)
		}
		ladderClimbExecute(f, action)
		if after := f.task(); after.Build.Attempt != 1 || len(after.Rungs) != 0 {
			t.Errorf("task attempt %d rungs %+v, want attempt 1 with no rungs", after.Build.Attempt, after.Rungs)
		}
	})
	t.Run("a confirmed finding climbs the task at its gate", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t, gates...)
		f.setBuild(ladderPathsVerifyBuild(f))
		ladderClimbBefore(f, 1)
		task := f.task()
		task.Reviews = verifyReviews(1, "dropped", "confirmed")
		action := Decide(View{Epic: passEpic, ReviewGates: gates, Task: task})
		ladderPathsWantFail(t, action, "confirmed: F2 claim two")
		move := action.Move
		if move.MovedBy != store.MovedByFinding || move.Gate != "lint" || move.Assertion != "F2 claim two" {
			t.Fatalf("move = %+v, want moved by %q at gate lint for F2 claim two", move, store.MovedByFinding)
		}
		ladderClimbExecute(f, action)
		ladderClimbCheck(f, 1, store.MovedByFinding)
	})
}
