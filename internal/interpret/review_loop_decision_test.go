package interpret

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The answer the owner gives the review-finding-scope question of these tests: its decision id, its choice, which closes
// security-1, and its text.
const (
	decisionLoopAnswerID = "owner-Q1-keep-fix"
	decisionLoopKeep     = "keep-fix"
	decisionLoopAnswer   = "Keep the change and close security-1 with no code change."
)

// decisionLoopQuestion is the question the fix plan asks instead of planning a correction: whether to keep the change
// security-1 names. Its keep-fix choice closes security-1.
func decisionLoopQuestion() store.EpicQuestionInput {
	return store.EpicQuestionInput{
		Label: "review-finding-scope", Text: "The review confirmed security-1. Keep the change it names?",
		Choices: []store.EpicChoice{
			{
				ID: decisionLoopKeep, Does: "Keep the change, and close security-1 with no code change.",
				Closes: []string{"security-1"},
			},
			{ID: "revert-fix", Does: "Plan a task that reverts the change."},
		},
		Recommended: decisionLoopKeep, Why: "The contract needs the change.", Confidence: "high",
	}
}

// decisionLoopTurn returns a planner turn that, on the turn claiming the trigger with cause askOn, asks
// decisionLoopQuestion with outcome wait and has the owner answer it with keep-fix at once, as the owner answering the
// question would. Every other turn completes with no task.
func decisionLoopTurn(askOn string) reviewLoopTurn {
	return func(t *testing.T, s *store.Store) []string {
		t.Helper()
		ctx := context.Background()
		turn, err := s.ClaimTurn(ctx, runEpic, runClock())
		if errors.Is(err, store.ErrNotClaimable) {
			return nil
		}
		if err != nil {
			t.Fatalf("ClaimTurn: %v", err)
		}
		triggers, err := s.TurnTriggers(ctx, runEpic, turn.Seq)
		if err != nil {
			t.Fatalf("TurnTriggers of turn %d: %v", turn.Seq, err)
		}
		causes := make([]string, 0, len(triggers))
		for _, trigger := range triggers {
			causes = append(causes, trigger.Cause)
		}
		plan := store.EpicPlan{Decisions: []string{}, Outline: "no task", Outcome: "complete"}
		ask := slices.Contains(causes, askOn)
		if ask {
			plan.Questions, plan.Outcome = []store.EpicQuestionInput{decisionLoopQuestion()}, "wait"
		}
		if err = s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
			t.Fatalf("CompleteTurn of turn %d: %v", turn.Seq, err)
		}
		if ask {
			if _, err = s.AnswerQuestion(ctx, runEpic, decisionLoopAnswerID, decisionLoopAnswer, 1, decisionLoopKeep,
				runClock()); err != nil {
				t.Fatalf("AnswerQuestion: %v", err)
			}
		}
		return causes
	}
}

// decisionLoopClosure is the closure the owner's answer gives security-1 in round.
func decisionLoopClosure(round int64) store.Closure {
	return store.Closure{
		Finding: "security-1", Round: round, Result: "closed",
		Reason: "closed by decision " + decisionLoopAnswerID + " (answers Q1 with " + decisionLoopKeep + "): " +
			decisionLoopAnswer,
	}
}

// decisionLoopStarted reports whether the store holds a step with the id of part of the node run at path.
func decisionLoopStarted(t *testing.T, got reviewLoopOutcome, path, part string) bool {
	t.Helper()
	id := workID(t, got.s, path, part)
	return slices.ContainsFunc(reviewLoopFlowOf(t, got.s).Steps, func(step store.StepRow) bool { return step.ID == id })
}

// TestReviewLoopFindingTheOwnerKeptClosesByAnswerInRoundOne is the web-catalog shape: the round one fix plan asks the
// owner whether to keep the change security-1 names instead of planning a correction, and the owner keeps it. The
// round's closure closes security-1 by the answer, starts no closure verifier for it, and the epic ends clean with its
// conclusion naming the finding closed by decision.
func TestReviewLoopFindingTheOwnerKeptClosesByAnswerInRoundOne(t *testing.T) {
	t.Parallel()
	got := reviewLoopCaseWith(t, reviewLoopFindingScript(), decisionLoopTurn(reviewLoopRoundOneCause))

	if len(got.review.Ledger) != 1 {
		t.Fatalf("ledger = %+v, want the one finding security-1", got.review.Ledger)
	}
	if want := []store.Closure{decisionLoopClosure(1)}; !reflect.DeepEqual(got.review.Ledger[0].Closures, want) {
		t.Errorf("closures of security-1 = %+v, want %+v", got.review.Ledger[0].Closures, want)
	}
	if decisionLoopStarted(t, got, "correct@1/closure", "close security-1") {
		t.Error("the round one closure started a verifier for the finding the owner's answer closed")
	}
	wantRounds := []store.ReviewRound{{Round: 1, Revision: runBase, Outcome: "passed"}}
	if !reflect.DeepEqual(got.review.Rounds, wantRounds) {
		t.Errorf("rounds = %+v, want %+v", got.review.Rounds, wantRounds)
	}
	reviewLoopWantCauses(t, got, []string{
		reviewLoopCreatedCause, reviewLoopRoundOneCause, "answer " + decisionLoopAnswerID + " to Q1",
	})
	reviewLoopWantClean(t, got)
	const output = "epic " + runEpic + " ended clean at " + runBase + "\nClosed by decision:\n- security-1 in round 1"
	if conclude := runAt(t, got.s, "conclude"); conclude.Output != output {
		t.Errorf("conclude output = %q, want %q", conclude.Output, output)
	}
}

// TestReviewLoopObservationTheDirectorRecordedClosesByAnswerInRoundTwo is the say-now shape: the round one closure
// verifier leaves security-1 open on the unchanged code, the round two fix plan asks whether to keep it as an
// observation, and the answer keeps it. The round two closure closes it by the answer with no verifier, so the loop
// passes in round two instead of ending with gaps.
func TestReviewLoopObservationTheDirectorRecordedClosesByAnswerInRoundTwo(t *testing.T) {
	t.Parallel()
	got := reviewLoopCaseWith(t, reviewLoopFindingScript("open"), decisionLoopTurn(reviewLoopRoundTwoCause))

	if len(got.review.Ledger) != 1 {
		t.Fatalf("ledger = %+v, want the one finding security-1", got.review.Ledger)
	}
	want := []store.Closure{
		{
			Finding: "security-1", Round: 1, Step: workID(t, got.s, "correct@1/closure", "close security-1"),
			Result: "open", Reason: "checked",
		},
		decisionLoopClosure(2),
	}
	if !reflect.DeepEqual(got.review.Ledger[0].Closures, want) {
		t.Errorf("closures of security-1 = %+v, want %+v", got.review.Ledger[0].Closures, want)
	}
	if decisionLoopStarted(t, got, "correct@2/closure", "close security-1") {
		t.Error("the round two closure started a verifier for the finding the answer closed")
	}
	wantRounds := []store.ReviewRound{
		{Round: 1, Revision: runBase, Outcome: "failed"},
		{Round: 2, Revision: runBase, Outcome: "passed"},
	}
	if !reflect.DeepEqual(got.review.Rounds, wantRounds) {
		t.Errorf("rounds = %+v, want %+v", got.review.Rounds, wantRounds)
	}
	reviewLoopWantClean(t, got)
}
