package interpret

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestOwnerCorrectAfterClaimedTurnBuildsItsTaskGapQuestion: the owner's correction to a gaps question is held as the
// pending trigger gaps:<decision id>, and a planner turn claims that trigger before the next pass starts the release's
// replan plan. The plan starts marked below that turn, so the task the turn plans builds.
func TestOwnerCorrectAfterClaimedTurnBuildsItsTaskGapQuestion(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	ctx := t.Context()
	gaps := store.GapsQuestion{
		Gaps: []string{"the budget lines are missing"}, Recommended: store.ChoiceCorrect, Why: "the plan can add them",
	}
	if err := w.s.EndEpicWithGaps(ctx, runEpic, "the epic ended with gaps", gaps, nil, runClock()); err != nil {
		t.Fatalf("EndEpicWithGaps: %v", err)
	}
	open, err := w.s.OpenQuestions(ctx, runEpic)
	if err != nil || len(open) != 1 {
		t.Fatalf("open questions = %+v and %v, want the one gaps question", open, err)
	}
	if _, err = w.s.AnswerQuestion(
		ctx, runEpic, gapsCorrectionID, gapsCorrection, open[0].Seq, store.ChoiceCorrect, store.Maker{}, runClock(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}

	turn, _ := w.claimReplanTurn()
	triggers, err := w.s.TurnTriggers(ctx, runEpic, turn.Seq)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if !slices.ContainsFunc(triggers, func(trigger store.EpicTrigger) bool { return trigger.ID == gapsTriggerID }) {
		t.Fatalf("turn %d triggers = %+v, want %s among them", turn.Seq, triggers, gapsTriggerID)
	}
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}
	plan := runAt(t, w.s, replanPlanPath)
	if plan.State != store.NodeRunning || plan.Kind != kindPlan || plan.Node != "plan" || plan.Mark != turn.Seq-1 {
		t.Fatalf("replan plan = %+v, want the plan running with mark %d", plan, turn.Seq-1)
	}

	task := runTask("the budget lines are added", "README")
	task.Requirements = []string{}
	w.completeReplanTurn(turn, "work", task)
	if build := runAt(t, w.s, replanBuildPath); build.State != store.NodeRunning || build.Task != 1 {
		t.Fatalf("build = %+v, want the task's build running under the replan's plan", build)
	}
}
