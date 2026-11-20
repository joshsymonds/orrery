package store

import (
	"context"
	"testing"
)

// ownerActionStore returns the steer-move store with planner turn 1 of its epic complete and one open question, which
// is question 1, so an owner or conversation answer has a question to close.
func ownerActionStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s := steerMoveStore(t)
	if _, err := s.ClaimTurn(ctx, steerMoveEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedTurn()
	plan.Questions = []EpicQuestionInput{shapedQuestion("which-store", "Which store?")}
	if err := s.CompleteTurn(ctx, steerMoveEpic, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return s
}

// ownerActionDecisions returns the decisions of the steer-move epic in seq order.
func ownerActionDecisions(t *testing.T, s *Store) []EpicDecision {
	t.Helper()
	epic, err := s.Epic(context.Background(), steerMoveEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return epic.Decisions
}

// ownerActionRecipient returns the Recipient of the decision with id, and fails the test when no decision has it.
func ownerActionRecipient(t *testing.T, s *Store, id string) string {
	t.Helper()
	for _, decision := range ownerActionDecisions(t, s) {
		if decision.ID == id {
			return decision.Recipient
		}
	}
	t.Fatalf("no decision %s on epic %s", id, steerMoveEpic)
	return ""
}

func TestOwnerActionEventDecisionBeforeMoveKeepsTheOldSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := ownerActionStore(t)
	owner := Maker{By: MakerOwner}
	if _, err := s.AnswerEpic(ctx, steerMoveEpic, "N1", "Before the move", owner, steerMoveAt()); err != nil {
		t.Fatalf("AnswerEpic before the move: %v", err)
	}
	if _, err := s.MoveEpic(ctx, steerMoveEpic, "engine-2", owner, steerMoveAt()); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	if _, err := s.AnswerEpic(ctx, steerMoveEpic, "N2", "After the move", owner, steerMoveAt()); err != nil {
		t.Fatalf("AnswerEpic after the move: %v", err)
	}

	if got := ownerActionRecipient(t, s, "N1"); got != "engine-1" {
		t.Errorf("Recipient of N1 made before the move = %q, want the old steerer engine-1", got)
	}
	if got := ownerActionRecipient(t, s, "N2"); got != "engine-2" {
		t.Errorf("Recipient of N2 made after the move = %q, want the new steerer engine-2", got)
	}
}
