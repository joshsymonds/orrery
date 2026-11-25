package build

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/reviewers"
)

// TestStepPurposeImplementerNamesRoleAndTask: the implementer start names its task by label and title, as the step
// that implements it.
func TestStepPurposeImplementerNamesRoleAndTask(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	const want = "Implement T3: Start the steps."
	if got := onlyStart(t, steps).Purpose; got != want {
		t.Errorf("implementer start Purpose = %q, want %q", got, want)
	}
}

// TestStepPurposeTaskReviewerNamesRoleAndTask: the task reviewer start names the task it reviews and the reviewer gate
// that runs it.
func TestStepPurposeTaskReviewerNamesRoleAndTask(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	setup.epic.ReviewGates = []string{"review"}
	setup.epic.Reviewers = []reviewers.Reviewer{agentReviewer("review")}
	revision := buildCommit(t, setup.epic.Repository)
	task := reviewTask(setup.base, revision, 0)
	id := StepID(setup.epic.ID, task.Seq, StageReview, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageReview, ID: id},
	); err != nil {
		t.Fatalf("startStep(review) = %v, want nil", err)
	}
	const want = "Review T3 as review."
	if got := onlyStart(t, steps).Purpose; got != want {
		t.Errorf("task reviewer start Purpose = %q, want %q", got, want)
	}
}

// TestStepPurposeFindingVerifierNamesFindingAndTask: the finding verifier start names the finding it rules on and the
// task's review that raised it.
func TestStepPurposeFindingVerifierNamesFindingAndTask(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	revision := buildCommit(t, setup.epic.Repository)
	task := startFindingTask(setup.base, revision)
	id := StepID(setup.epic.ID, task.Seq, StageVerify, 1, 0, 0, 1)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageVerify, ID: id},
	); err != nil {
		t.Fatalf("startStep(verify) = %v, want nil", err)
	}
	const want = "Verify finding F2 of T3's review."
	if got := onlyStart(t, steps).Purpose; got != want {
		t.Errorf("finding verifier start Purpose = %q, want %q", got, want)
	}
}

// TestStepPurposeEpicVerifierNamesFindingAndEpic: a finding verifier of an epic-stage review names the finding and the
// epic it reviews, since it has no task.
func TestStepPurposeEpicVerifierNamesFindingAndEpic(t *testing.T) {
	t.Parallel()
	start := VerifierStart(
		Subject{Epic: verifierEpic(), Base: "base111", Revision: "rev222"},
		verifierFinding(), verifyStepID, verifyAgent,
	)
	const want = "Verify finding F7 of epic repo/epic-x's review."
	if start.Purpose != want {
		t.Errorf("epic verifier start Purpose = %q, want %q", start.Purpose, want)
	}
}

// TestStepPurposeDesignJudgeNamesRoleAndTask: the design judge start names the design task it judges.
func TestStepPurposeDesignJudgeNamesRoleAndTask(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	view := designView(nil)
	id := StepID(setup.epic.ID, view.Task.Seq, StageDesign, view.Task.Build.Attempt, view.Task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, view,
		Action{Kind: ActStartStep, Unit: StageDesign, ID: id},
	); err != nil {
		t.Fatalf("startStep(design) = %v, want nil", err)
	}
	const want = "Judge T3's design."
	if got := onlyStart(t, steps).Purpose; got != want {
		t.Errorf("design judge start Purpose = %q, want %q", got, want)
	}
}

// TestStepPurposeOrchestratorNamesRoleAndTask: the orchestrator start names the task whose request it decides. The
// request fixture's last start is that orchestrator step.
func TestStepPurposeOrchestratorNamesRoleAndTask(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	start := f.lastStart()
	if start.ID != orchID(0, 0) {
		t.Fatalf("last start ID = %s, want the orchestrator step %s", start.ID, orchID(0, 0))
	}
	const want = "Decide T1's request."
	if start.Purpose != want {
		t.Errorf("orchestrator start Purpose = %q, want %q", start.Purpose, want)
	}
}
