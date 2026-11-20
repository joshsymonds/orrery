package store

import (
	"context"
	"testing"
)

// correctionPlanPath is the path of the replan plan the correction tests start, under the release's first attempt.
const correctionPlanPath = "release/A0/plan"

// answerGapsCorrect ends epic with the gaps of claimGapsQuestion and answers its gaps question correct with text, so
// epic is correcting and the trigger gaps:<decision> is pending.
func answerGapsCorrect(t *testing.T, s *Store, epic, decision string) {
	t.Helper()
	ctx := context.Background()
	if err := s.EndEpicWithGaps(ctx, epic, "Ended with 2 gaps.", claimGapsQuestion(), nil, claimedAt()); err != nil {
		t.Fatalf("EndEpicWithGaps %s: %v", epic, err)
	}
	if _, err := s.AnswerQuestion(
		ctx, epic, decision, "Add the budget lines", 1, ChoiceCorrect, Maker{}, claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion correct of %s: %v", epic, err)
	}
}

// storedNodeRun returns the node run of epic at path as the store holds it, and fails when no run has that path.
func storedNodeRun(t *testing.T, s *Store, epic, path string) NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), epic)
	if err != nil {
		t.Fatalf("NodeRuns %s: %v", epic, err)
	}
	for _, run := range runs {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("epic %s holds no run at %s", epic, path)
	return NodeRun{}
}

// TestStartCorrectionPlanMarksBelowClaimingTurnGapQuestion: the owner's correct answer to a gaps question leaves the
// gaps:<decision> trigger pending. A planner turn that claims it before the replan plan starts gets the plan marked
// below its own seq, so the turn's tasks build. A correction no turn has claimed keeps the mark it was given.
func TestStartCorrectionPlanMarksBelowClaimingTurnGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const claimed, unclaimed = claimEpic, "orrery-1a2b3c4/epic-store-unclaimed"
	createEpic(t, s, fullEpic(claimed))
	createEpic(t, s, fullEpic(unclaimed))
	answerGapsCorrect(t, s, claimed, "owner-correct")
	turn, err := s.ClaimTurn(ctx, claimed, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	plan := NodeRun{
		Path: correctionPlanPath, ID: NodeRunID(claimed, correctionPlanPath, 0, "start"), Node: "plan", Kind: "plan",
		Mark: turn.Seq,
	}
	if err := s.StartCorrectionPlan(ctx, claimed, plan, claimedAt()); err != nil {
		t.Fatalf("StartCorrectionPlan claimed: %v", err)
	}
	if run := storedNodeRun(t, s, claimed, correctionPlanPath); run.State != NodeRunning || run.Mark != turn.Seq-1 {
		t.Errorf("plan of the claimed correction = %+v, want running with mark %d", run, turn.Seq-1)
	}

	// The second epic's correction is unclaimed, so its plan keeps the mark it is given.
	const givenMark = 2
	answerGapsCorrect(t, s, unclaimed, "owner-correct")
	plan.ID = NodeRunID(unclaimed, correctionPlanPath, 0, "start")
	plan.Mark = givenMark
	if err := s.StartCorrectionPlan(ctx, unclaimed, plan, claimedAt()); err != nil {
		t.Fatalf("StartCorrectionPlan unclaimed: %v", err)
	}
	if run := storedNodeRun(t, s, unclaimed, correctionPlanPath); run.State != NodeRunning || run.Mark != givenMark {
		t.Errorf("plan of the unclaimed correction = %+v, want running with mark %d", run, givenMark)
	}
}
