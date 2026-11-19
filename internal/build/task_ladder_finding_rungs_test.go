package build

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The review gates the finding rung tests run under, in order: the epic's review gates.
const (
	ladderFindingRungsLint     = "lint"
	ladderFindingRungsSecurity = "security"
)

// ladderFindingRungsGates returns the review gates the finding rung tests run under, in order.
func ladderFindingRungsGates() []string {
	return []string{ladderFindingRungsLint, ladderFindingRungsSecurity}
}

// ladderFindingRungsReview returns a failed review at attempt and gate position pos of gate, with one finding F1 whose
// claim is claim and which the verifier confirmed.
func ladderFindingRungsReview(attempt, pos int64, gate, claim string) store.TaskReview {
	return store.TaskReview{
		Attempt: attempt, Position: pos, Gate: gate, Verdict: verdictFails,
		Findings: []store.TaskFinding{{Position: 0, ID: "F1", Claim: claim, Result: resultConfirmed}},
	}
}

// ladderFindingRungsView returns the view of open task 1 of passEpic on build, with the review gates, the green run and
// the step given, for the decisions the orchestrator attempt test makes without a store.
func ladderFindingRungsView(build store.TaskBuild, run *store.EvidenceRun, step *store.StepRow) View {
	return View{
		Epic: passEpic, ReviewGates: ladderFindingRungsGates(), Run: run, Step: step,
		Task: store.EpicTask{Seq: 1, Status: taskOpen, Build: build},
	}
}

// ladderFindingRungsRow returns the row of rung in rungs, and fails the test when the task holds no row for it.
func ladderFindingRungsRow(t *testing.T, rungs []store.TaskRung, rung int64) store.TaskRung {
	t.Helper()
	for _, row := range rungs {
		if row.Rung == rung {
			return row
		}
	}
	t.Fatalf("rungs = %+v, want a row for rung %d", rungs, rung)
	return store.TaskRung{}
}

// ladderFindingRungsCheckRungs fails the test unless rungs are rungs 1 to 7 of a ladder that ended as a gap: each row's
// kind is the kind of its rung, rungs 1 to 6 ended failed and rung 7 gapped, rung 1 was moved by nothing, and each rung
// after it was moved by the finding of the rung before, at the gates in turn.
func ladderFindingRungsCheckRungs(t *testing.T, rungs []store.TaskRung, gates []string) {
	t.Helper()
	kinds := []string{
		store.RungAttempt, store.RungAttempt, store.RungRescue, store.RungOrchestrator, store.RungApproach,
		store.RungApproach, store.RungGap,
	}
	if len(rungs) != len(kinds) {
		t.Fatalf("rungs = %+v, want %d rungs", rungs, len(kinds))
	}
	for i, row := range rungs {
		number := int64(i + 1)
		if row.Rung != number || row.Kind != kinds[i] {
			t.Errorf("rung %d = number %d kind %q, want number %d kind %q", i+1, row.Rung, row.Kind, number, kinds[i])
		}
		wantOutcome := store.RungFailed
		if number == store.GapRung {
			wantOutcome = store.RungGapped
		}
		if row.Outcome != wantOutcome {
			t.Errorf("rung %d outcome = %q, want %q", number, row.Outcome, wantOutcome)
		}
		if number == 1 {
			if row.MovedBy != "" || row.Gate != "" || row.Assertion != "" {
				t.Errorf("rung 1 = %+v, want no moved_by, gate or assertion", row)
			}
			continue
		}
		wantGate := gates[(number-2)%2]
		wantAssertion := fmt.Sprintf("F1 claim on rung %d", number-1)
		if row.MovedBy != store.MovedByFinding || row.Gate != wantGate || row.Assertion != wantAssertion {
			t.Errorf("rung %d = moved by %q gate %q assertion %q, want moved by %q gate %q assertion %q",
				number, row.MovedBy, row.Gate, row.Assertion, store.MovedByFinding, wantGate, wantAssertion)
		}
	}
}

// TestTaskLadderFindingRowsEachRung: a confirmed finding that climbs a task off each of rungs 1 to 6 stores on the rung
// it climbs to the gate and assertion of that finding, so every rung from 2 to the gap rung carries them, and the task
// ends as a gap.
func TestTaskLadderFindingRowsEachRung(t *testing.T) {
	t.Parallel()
	gates := ladderFindingRungsGates()
	f := newPassFixture(t, gates...)
	for r := int64(1); r <= store.LastRung; r++ {
		stored := f.task().Build
		f.setBuild(store.TaskBuild{
			Stage: StageVerify, Base: stored.Base, Attempt: r, GatePos: (r - 1) % 2, FindingPos: 1,
			Revision: stored.Revision,
		})
		gate := gates[(r-1)%2]
		claim := fmt.Sprintf("claim on rung %d", r)
		assertion := "F1 " + claim
		task := f.task()
		task.Reviews = []store.TaskReview{ladderFindingRungsReview(r, (r-1)%2, gate, claim)}
		action := Decide(View{Epic: passEpic, ReviewGates: gates, Task: task})
		want := store.RungMove{MovedBy: store.MovedByFinding, Gate: gate, Assertion: assertion}
		if action.Kind != ActFail || !reflect.DeepEqual(action.Move, want) {
			t.Fatalf("rung %d decision = %v %+v, want a fail moved %+v", r, action.Kind, action.Move, want)
		}
		ladderClimbExecute(f, action)

		after := f.task()
		if after.Build.Attempt != r+1 {
			t.Errorf("rung %d climb left the build on rung %d, want rung %d", r, after.Build.Attempt, r+1)
		}
		row := ladderFindingRungsRow(t, after.Rungs, r+1)
		if row.MovedBy != store.MovedByFinding || row.Gate != gate || row.Assertion != assertion ||
			row.Kind != store.RungKind(r+1) {
			t.Errorf("rung %d = %+v, want moved by %q at gate %q for %q, of kind %q",
				r+1, row, store.MovedByFinding, gate, assertion, store.RungKind(r+1))
		}
		if prev := ladderFindingRungsRow(t, after.Rungs, r); prev.Outcome != store.RungFailed {
			t.Errorf("rung %d outcome = %q, want %q", r, prev.Outcome, store.RungFailed)
		}
	}

	task := f.task()
	if task.Status != store.TaskGap {
		t.Errorf("task status = %q, want %q", task.Status, store.TaskGap)
	}
	ladderFindingRungsCheckRungs(t, task.Rungs, gates)
}

// TestTaskLadderOrchestratorAttemptReviewed: the orchestrator's own attempt, on rung 4, runs under the task's review
// gates as any attempt does. Its green run moves it to the first gate, the gates' reviews start and move it through
// them, and a confirmed finding of its verify climbs it to rung 5 moved by the finding.
func TestTaskLadderOrchestratorAttemptReviewed(t *testing.T) {
	t.Parallel()
	gates := ladderFindingRungsGates()
	const attempt = store.OrchestratorRung
	reviewStep := StepID(passEpic, 1, StageReview, attempt, 0, 0, 0)
	greenRun := passRunID(StageGreen, attempt)

	t.Run("a passed green run moves to the first review gate", func(t *testing.T) {
		t.Parallel()
		green := store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: attempt, Revision: "rev"}
		run := &store.EvidenceRun{ID: greenRun, Ended: passClock(), Verdict: evidence.VerdictHolds, Passed: true}
		got := Decide(ladderFindingRungsView(green, run, nil))
		want := Action{Kind: ActMove, Next: store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: attempt, Revision: "rev", GreenRun: greenRun,
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("green run at attempt %d = %+v, want %+v", attempt, got, want)
		}
	})

	t.Run("the first review gate starts under its derived id", func(t *testing.T) {
		t.Parallel()
		review := store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: attempt, Revision: "rev", GreenRun: greenRun,
		}
		got := Decide(ladderFindingRungsView(review, nil, nil))
		want := Action{Kind: ActStartStep, Unit: StageReview, ID: reviewStep}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("review with no step = %+v, want %+v", got, want)
		}
	})

	t.Run("a lint review that ends done moves to the security gate", func(t *testing.T) {
		t.Parallel()
		review := store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: attempt, Revision: "rev", GreenRun: greenRun,
		}
		step := &store.StepRow{ID: reviewStep, End: endDone}
		got := Decide(ladderFindingRungsView(review, nil, step))
		if got.Kind != ActReview || got.Review.Attempt != attempt || got.Review.Gate != ladderFindingRungsLint ||
			got.Next.GatePos != 1 {
			t.Errorf("review done = %+v, want a review of attempt %d at gate %s moving to gate 1",
				got, attempt, ladderFindingRungsLint)
		}
	})

	t.Run("the review starts over the store and a confirmed verify finding climbs to rung 5", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t, gates...)
		f.setGreen(attempt, evidence.VerdictHolds)
		f.setBuild(store.TaskBuild{
			Stage: StageReview, Base: f.base, Attempt: attempt, Revision: f.base, GreenRun: greenRun,
		})
		f.pass()
		if got := f.lastStart().ID; got != reviewStep {
			t.Errorf("review start ID = %s, want the review step %s", got, reviewStep)
		}
		// The verify of the same attempt follows the review on the stored build, so the climb starts from it.
		f.setBuild(store.TaskBuild{
			Stage: StageVerify, Base: f.base, Attempt: attempt, Revision: f.base, FindingPos: 1,
		})
		ladderClimbBefore(f, attempt)
		const claim = "claim on attempt 4"
		task := f.task()
		task.Reviews = []store.TaskReview{ladderFindingRungsReview(attempt, 0, ladderFindingRungsLint, claim)}
		action := Decide(View{Epic: passEpic, ReviewGates: gates, Task: task})
		want := store.RungMove{MovedBy: store.MovedByFinding, Gate: ladderFindingRungsLint, Assertion: "F1 " + claim}
		if action.Kind != ActFail || !reflect.DeepEqual(action.Move, want) {
			t.Fatalf("verify at attempt %d decision = %v %+v, want a fail moved %+v",
				attempt, action.Kind, action.Move, want)
		}
		ladderClimbExecute(f, action)
		ladderClimbCheck(f, attempt, store.MovedByFinding)
	})
}
