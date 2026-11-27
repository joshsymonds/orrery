package interpret

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// fivePlannerReplans is how many stops in a row a test answers with a planner's retry_epic.
const fivePlannerReplans = 5

// patchTagStep is a release step that names the tag kind, with p as its version prefix.
func patchTagStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{
		Step: "push a patch tag", Target: "origin's tags", Undo: "delete the tag on origin",
		Kind: releasestep.KindTag, Params: releasestep.Params{Prefix: "p"},
	}
}

// newReplanStepsWorld returns a replan world whose epic runs replanFlow with its plan ended, stored clean with steps as its
// release steps and reviewerList as its merged reviewers, as an epic that ended clean leaves it. Origin's main has not
// moved past the epic's base, so the release's candidate is the epic's branch tip. The runner runs testOrrery, so a step
// that names a kind starts as a check.
func newReplanStepsWorld(t *testing.T, reviewerList []reviewers.Reviewer, steps ...store.EpicReleaseStep) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.runner.orrery = testOrrery
	w.runner.reviewers = &releaseReviewers{}
	w.reviewerList = reviewerList
	plans := &fakePlanner{known: []string{runEpic}}
	w.runner.planner = plans
	base := nodeGit(t, w.repo, "rev-parse", "main")
	mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpicOn(replanFlow, base, runEpic, store.PhaseClean, steps...)
	plan := store.NodeRun{Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: kindPlan}
	if err := w.s.StartNodes(t.Context(), runEpic, []store.NodeRun{plan}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	ended := []store.NodeEnd{{Path: "plan", Outcome: "complete"}}
	if err := w.s.EndNodes(t.Context(), runEpic, ended, runClock()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	return replanWorld{conflictWorld: conflictWorld{releaseWorld: w, tip: tip}, planner: plans}
}

// passCheck ends the check of release step position at the current attempt with exit 0 and stdout, and passes, so the
// step passes and the next step starts.
func (w replanWorld) passCheck(position int, stdout string) {
	w.t.Helper()
	w.endCheck(w.stepID(position), 0, stdout, "")
	w.pass()
}

// failCheck ends the check of release step position at the current attempt with exit 1 and stderr, and passes, which
// stops the release at that step.
func (w replanWorld) failCheck(position int, stderr string) {
	w.t.Helper()
	w.endCheck(w.stepID(position), 1, "", stderr)
	w.pass()
}

// answerStop answers the release's stop with actions, as the planner's turn does: it claims the turn the stop's trigger
// wakes, completes it with actions, carries each pending action out, and passes.
func (w replanWorld) answerStop(actions ...store.EpicActionInput) {
	w.t.Helper()
	ctx := w.t.Context()
	turn, err := w.s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		w.t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Decisions: []string{}, Outline: "answer the release's stop", Outcome: "work", Actions: actions,
	}
	if err = w.s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
		w.t.Fatalf("CompleteTurn %d: %v", turn.Seq, err)
	}
	pending, err := w.s.PendingActions(ctx)
	if err != nil {
		w.t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range pending {
		if _, err = w.s.CarryOutAction(ctx, action, runClock()); err != nil {
			w.t.Fatalf("CarryOutAction: %v", err)
		}
	}
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// retryStop answers the release's stop with a planner's retry_epic, which replans the release at its stopped attempt, and
// passes, so the epic goes back to planning.
func (w replanWorld) retryStop() {
	w.t.Helper()
	retry := store.EpicActionInput{Kind: store.ActionRetryEpic, Reason: "the release runs again from the candidate"}
	w.answerStop(retry)
}

// finishReplan runs the replan's plan to its end with a turn that has no task, which resumes the release at its next
// attempt.
func (w replanWorld) finishReplan() {
	w.t.Helper()
	turn, _ := w.claimReplanTurn()
	w.completeReplanTurn(turn, "complete")
}

// replanFromStop is retryStop, then finishReplan.
func (w replanWorld) replanFromStop() {
	w.t.Helper()
	w.retryStop()
	w.finishReplan()
}

// TestReplanRerunsStepsWalkFromStepOne: after a replan every release step runs again from the new candidate. Steps 1 to 3
// pass at attempt 0 and step 4 stops the release, which the planner's retry replans at attempt 0. At attempt 1 the new
// candidate is reviewed by the release reviewer, and only then do steps 1, 2 and 3 start, in that order, from it.
func TestReplanRerunsStepsWalkFromStepOne(t *testing.T) {
	t.Parallel()
	w := newReplanStepsWorld(
		t, defaultReleaseReviewer(t, true), pushMainStep(), tagKindStep(), patchTagStep(), publishStep(),
	)
	w.pass()
	w.endStep(w.reviewID(), "done", "done", map[string]any{"status": "done"})
	w.pass()
	w.passCheck(1, "origin's main contains the candidate at attempt 0\n")
	w.passCheck(2, "the version tag pushed at attempt 0\n")
	w.passCheck(3, "the patch tag pushed at attempt 0\n")
	w.endStep(w.stepID(4), "returned", "blocked", map[string]any{
		"status": "blocked", "commits": []string{}, "evidence": "the tag was not published",
	})
	w.pass()
	w.retryStop()
	replans := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReplan)
	if len(replans) != 1 || replans[0].Attempt != 0 {
		t.Fatalf("replan runs = %+v, want one replan at attempt 0", replans)
	}
	w.finishReplan()

	checks := len(w.fakes.checks.calls)
	w.pass()
	release := runAt(t, w.s, releaseNodePath)
	if release.Attempt != 1 {
		t.Fatalf("release run = %+v, want attempt 1", release)
	}
	if !slices.ContainsFunc(w.flowOf(runEpic).Releases, func(run store.ReleaseRun) bool {
		return run.Part == store.ReleasePartCandidate && run.Attempt == 1 && run.Passed
	}) {
		t.Errorf("release runs = %+v, want a candidate passed at attempt 1", w.flowOf(runEpic).Releases)
	}
	review := w.reviewID()
	steps := w.fakes.steps.calls
	if len(steps) == 0 || steps[len(steps)-1].ID != review || steps[len(steps)-1].Revision != release.Revision {
		t.Fatalf("step starts = %+v, want the release reviewer started at candidate %s", steps, release.Revision)
	}
	if got := w.fakes.checks.calls[checks:]; len(got) != 0 {
		t.Fatalf("check starts = %+v, want no step started before the review of the new candidate held", got)
	}

	w.endStep(review, "done", "done", map[string]any{"status": "done"})
	w.pass()
	if got := w.fakes.checks.calls[checks:]; len(got) != 1 || got[0].ID != w.stepID(1) {
		t.Fatalf("check starts = %+v, want step 1 started alone once the review held", got)
	}
	w.passCheck(1, "origin's main contains the candidate at attempt 1\n")
	w.passCheck(2, "the version tag pushed at attempt 1\n")

	got := w.fakes.checks.calls[checks:]
	want := []string{w.stepID(1), w.stepID(2), w.stepID(3)}
	ids := make([]string, 0, len(got))
	for _, check := range got {
		ids = append(ids, check.ID)
		if check.Revision != release.Revision {
			t.Errorf("check %s starts at %s, want the new candidate %s", check.ID, check.Revision, release.Revision)
		}
	}
	if !slices.Equal(ids, want) {
		t.Errorf("check starts = %q, want steps 1, 2 and 3 started in that order: %q", ids, want)
	}
}

// TestReplanRerunsStepsPassedAfterReplanStays: a step that passed at an attempt after the last replan is passed over. The
// planner's retry replans at attempt 0, step 1 then passes at attempt 1, and step 2 stops the release there. The owner's
// release resumes at attempt 2, where step 1 is not started again and step 2 starts from the candidate.
func TestReplanRerunsStepsPassedAfterReplanStays(t *testing.T) {
	t.Parallel()
	w := newReplanStepsWorld(t, nil, pushMainStep(), tagKindStep())
	w.pass()
	w.failCheck(1, "the push was refused\n")
	w.replanFromStop()
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.passCheck(1, "origin's main contains the candidate at attempt 1\n")
	w.failCheck(2, "the tag was refused\n")
	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}

	checks := len(w.fakes.checks.calls)
	w.pass()
	got := w.fakes.checks.calls[checks:]
	if len(got) != 1 || got[0].ID != w.stepID(2) || got[0].Revision != candidate {
		t.Fatalf("check starts = %+v, want step 2 alone started at %s, with step 1 passed over", got, candidate)
	}
	if attempt := runAt(t, w.s, releaseNodePath).Attempt; attempt != 2 {
		t.Errorf("release attempt = %d, want 2", attempt)
	}
}

// TestReplanRerunsStepsResumeAtStepOne: after a replan, a release stopped at step 1 is resumed by the owner at step 1, though
// steps 1 and 2 passed at attempt 0, before the replan, and step 3 stopped it there. The tell names step 1, the first step
// the replan starts over.
func TestReplanRerunsStepsResumeAtStepOne(t *testing.T) {
	t.Parallel()
	w := newReplanStepsWorld(t, nil, pushMainStep(), tagKindStep(), patchTagStep())
	w.pass()
	w.passCheck(1, "origin's main contains the candidate at attempt 0\n")
	w.passCheck(2, "the version tag pushed at attempt 0\n")
	w.failCheck(3, "the patch tag was refused\n")
	w.replanFromStop()
	w.pass()
	w.failCheck(1, "the push was refused again\n")
	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}

	want := "release resumed at step 1, " + pushMainStep().Step
	if tells := nodeTells(t, w.path); !slices.Contains(tells, want) {
		t.Errorf("tells = %q, want %q", tells, want)
	}
}

// TestReplanRerunsStepsJudgeReadsOnlySinceReplan: the judge's prompt holds the evidence of the steps passed after the last
// replan and none of the steps that passed before it.
func TestReplanRerunsStepsJudgeReadsOnlySinceReplan(t *testing.T) {
	t.Parallel()
	w := newReplanStepsWorld(t, nil, pushMainStep(), tagKindStep(), publishStep())
	w.pass()
	w.passCheck(1, "pushed main before the replan\n")
	w.passCheck(2, "tagged before the replan\n")
	w.endStep(w.stepID(3), "returned", "blocked", map[string]any{
		"status": "blocked", "commits": []string{}, "evidence": "the tag was not published",
	})
	w.pass()
	w.replanFromStop()
	w.pass()
	w.passCheck(1, "pushed main after the replan\n")
	w.passCheck(2, "tagged after the replan\n")
	w.finishStep(3, "published after the replan")
	w.pass()

	calls := w.fakes.steps.calls
	if len(calls) == 0 || calls[len(calls)-1].ID != w.judgeID() {
		t.Fatalf("step starts = %+v, want the judge started last", calls)
	}
	judge := calls[len(calls)-1]
	for _, want := range []string{"pushed main after the replan", "tagged after the replan", "published after the replan"} {
		if !strings.Contains(judge.Prompt, want) {
			t.Errorf("judge prompt = %q, want it to hold %q", judge.Prompt, want)
		}
	}
	if strings.Contains(judge.Prompt, "before the replan") {
		t.Errorf("judge prompt = %q, want no evidence of a step passed before the replan", judge.Prompt)
	}
}

// TestReplanRerunsStepsFivePlannerReplans: each of five stops in a row, answered by a planner's retry_epic, replans the
// release, which proceeds to correcting and resumes at its next attempt. Each stop is the planner's release_step park,
// with no park question, and none goes to the owner as retry_bound.
func TestReplanRerunsStepsFivePlannerReplans(t *testing.T) {
	t.Parallel()
	w := newReplanStepsWorld(t, nil, pushMainStep(), tagKindStep())
	w.pass()
	for n := range fivePlannerReplans {
		reason := fmt.Sprintf(
			"release step 1 (target %s) failed:\nlanded: none\nthe step exited 1\nstderr:\nthe push was refused at attempt %d",
			pushMainStep().Target,
			n,
		)
		w.failCheck(1, fmt.Sprintf("the push was refused at attempt %d\n", n))
		requireRefusalPlannerPark(t, w.s, reason)
		w.retryStop()
		if phase := w.flowOf(runEpic).Epic.Phase; phase != "correcting" {
			t.Fatalf("epic phase after retry %d = %q, want correcting", n+1, phase)
		}
		w.finishReplan()
		if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseReleasing {
			t.Fatalf("epic phase after replan %d = %q, want releasing again", n+1, phase)
		}
		if attempt := runAt(t, w.s, releaseNodePath).Attempt; attempt != int64(n+1) {
			t.Fatalf("release attempt after replan %d = %d, want %d", n+1, attempt, n+1)
		}
		w.pass()
	}
	replans := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReplan)
	if len(replans) != fivePlannerReplans {
		t.Errorf("replan runs = %d, want %d", len(replans), fivePlannerReplans)
	}
}
