package interpret

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// TestStepPurposeEpicReviewerNamesItsReviewerAndEpic: an epic-stage reviewer's step says which reviewer it is and whose
// epic it reviews, by the epic's slug.
func TestStepPurposeEpicReviewerNamesItsReviewerAndEpic(t *testing.T) {
	t.Parallel()
	got, err := NewEpicReviewers().Start(
		context.Background(), rvEpic(rvModel()), "correctness", "base111", "frozen222", nil, nil,
	)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if want := "Review epic-x as correctness."; got.Purpose != want {
		t.Errorf("Purpose = %q, want %q", got.Purpose, want)
	}
}

// TestStepPurposeEpicVerifierNamesItsFindingAndEpic: the epic finding verifier's step says which finding it verifies
// and whose review raised it.
func TestStepPurposeEpicVerifierNamesItsFindingAndEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, &reviewWorkReviewers{}, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	verifyID := store.NodeWorkID("review-run", 1, "verify 1 1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs:   []string{verifyID},
		Units: []kinds.Unit{{ID: verifyID, Part: "verify 1 1", Reviewer: "security", Finding: finding}},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %d, want 1", len(steps.calls))
	}
	if want := "Verify finding security-1 of epic-runner's review."; steps.calls[0].Purpose != want {
		t.Errorf("Purpose = %q, want %q", steps.calls[0].Purpose, want)
	}
}

// TestStepPurposeClosureCheckerNamesItsFinding: the closure checker's step says which finding it checks is closed.
func TestStepPurposeClosureCheckerNamesItsFinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, &reviewWorkReviewers{}, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	closeID := store.NodeWorkID("closure-run", 1, "close security-1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "closure", NodeKind: closureNodeKind,
		Run:   store.NodeRun{Path: "closure", Attempt: 1, Revision: runBase},
		IDs:   []string{closeID},
		Units: []kinds.Unit{{ID: closeID, Part: "close security-1", Reviewer: "security", Finding: finding}},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %d, want 1", len(steps.calls))
	}
	if want := "Check finding security-1 is closed."; steps.calls[0].Purpose != want {
		t.Errorf("Purpose = %q, want %q", steps.calls[0].Purpose, want)
	}
}

// TestStepPurposeReleaseStepNamesItsPositionAndText: each release step's start says its position in the release and
// its step text, so the second step is named by 2 and not by the first.
func TestStepPurposeReleaseStepNamesItsPositionAndText(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep(), publishStep())
	w.pass()
	w.finishStep(1, "tagged main")
	w.pass()
	w.finishStep(2, "published the tag")
	w.pass()

	if len(w.fakes.steps.calls) != 3 {
		t.Fatalf("step starts = %+v, want the first step, the second, then the judge started", w.fakes.steps.calls)
	}
	if want := "Release step 1: tag the release."; w.fakes.steps.calls[0].Purpose != want {
		t.Errorf("first step Purpose = %q, want %q", w.fakes.steps.calls[0].Purpose, want)
	}
	if want := "Release step 2: publish the tag."; w.fakes.steps.calls[1].Purpose != want {
		t.Errorf("second step Purpose = %q, want %q", w.fakes.steps.calls[1].Purpose, want)
	}
}

// TestStepPurposeReleaseJudgeNamesTheRelease: the release judge's start says it judges its epic's release.
func TestStepPurposeReleaseJudgeNamesTheRelease(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.pass()
	w.finishStep(1, "tagged main")
	w.pass()

	if len(w.fakes.steps.calls) != 2 {
		t.Fatalf("step starts = %+v, want the judge started after the step", w.fakes.steps.calls)
	}
	if want := "Judge epic-runner's release."; w.fakes.steps.calls[1].Purpose != want {
		t.Errorf("judge Purpose = %q, want %q", w.fakes.steps.calls[1].Purpose, want)
	}
}

// TestStepPurposeReleaseReviewNamesTheCandidate: the release reviewer's start says it reviews its epic's release
// candidate.
func TestStepPurposeReleaseReviewNamesTheCandidate(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.runner.reviewers = &releaseReviewers{}
	w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
	w.pass()

	if len(w.fakes.steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want the release reviewer's step started", w.fakes.steps.calls)
	}
	start := w.fakes.steps.calls[0]
	if start.ID != w.reviewID() {
		t.Fatalf("first step start = %+v, want the release reviewer's step %s", start, w.reviewID())
	}
	if want := "Review epic-runner's release candidate."; start.Purpose != want {
		t.Errorf("review Purpose = %q, want %q", start.Purpose, want)
	}
}

// TestStepPurposeReleaseIntegratorNamesTheMergeAndCandidate: the release integrator's start says it merges main into
// its epic's release candidate.
func TestStepPurposeReleaseIntegratorNamesTheMergeAndCandidate(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()

	start := w.started(w.integratorID())
	if want := "Merge main into epic-runner's release candidate."; start.Purpose != want {
		t.Errorf("integrator Purpose = %q, want %q", start.Purpose, want)
	}
}

// TestStepPurposeTaskIntegratorNamesTheTaskAndEpic: the task integrator's start says which task it integrates into
// its epic.
func TestStepPurposeTaskIntegratorNamesTheTaskAndEpic(t *testing.T) {
	t.Parallel()
	c := newTaskClash(t)
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	steps := &fakeSteps{store: s}
	runner := &Runner{steps: steps, stateDir: t.TempDir(), getenv: integratorRoles(t), hosts: hostexec.LocalHosts()}
	epic := store.EpicRow{
		ID: runEpic, Repository: c.repo, Base: runBase, Tasks: []store.EpicTask{{Seq: 2, Title: "two"}},
	}
	id := store.NodeWorkID("integrate-run", 1, "integrate 2")
	act := Action{
		Path: "integrate", IDs: []string{id}, Tasks: []int64{2},
		Items: []string{"start", "1", c.candidate, c.revision},
	}
	if err := runner.startTaskRound(context.Background(), epic, act, runClock()); err != nil {
		t.Fatalf("startTaskRound: %v", err)
	}

	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want the task integrator started", steps.calls)
	}
	if want := "Integrate T2 into epic-runner."; steps.calls[0].Purpose != want {
		t.Errorf("task integrator Purpose = %q, want %q", steps.calls[0].Purpose, want)
	}
}

// TestStepPurposeAgentNodeNamesItsNodeAndEpic: an agent node's start says which node runs and whose epic it is.
func TestStepPurposeAgentNodeNamesItsNodeAndEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	steps := &fakeSteps{store: s}
	runner := &Runner{steps: steps}
	epic := store.EpicRow{ID: runEpic, Repository: runRepo}
	act := Action{
		Path: "review", Run: store.NodeRun{ID: "review-run", Attempt: 1, Revision: runBase},
		Def: workflow.Node{Settings: &workflow.Settings{
			Agent: "reviewer", Prompt: "Review the change.", Schema: `{"type":"object"}`,
		}},
	}
	if err := runner.startAgent(ctx, epic, act); err != nil {
		t.Fatalf("startAgent: %v", err)
	}

	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want the agent node started", steps.calls)
	}
	if want := "Run node review of epic-runner."; steps.calls[0].Purpose != want {
		t.Errorf("agent node Purpose = %q, want %q", steps.calls[0].Purpose, want)
	}
}
