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

// ownerActionWrites makes a note N1, an answer A1 to question 1 and a kick as maker on the steer-move epic, in that
// order, and returns the id of the kick's decision.
func ownerActionWrites(t *testing.T, s *Store, maker Maker) string {
	t.Helper()
	ctx := context.Background()
	at := steerMoveAt()
	if _, err := s.AnswerEpic(ctx, steerMoveEpic, "N1", "Keep the old tasks", maker, at); err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, steerMoveEpic, "A1", "Keep them", 1, ChoiceOther, maker, at); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	if err := s.KickEpic(ctx, steerMoveEpic, "Try the sqlite plan", maker, at); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	decisions := ownerActionDecisions(t, s)
	return decisions[len(decisions)-1].ID
}

func TestOwnerActionEventNoteAnswerAndKickStoreTheSteererAsRecipient(t *testing.T) {
	t.Parallel()
	s := ownerActionStore(t)
	kick := ownerActionWrites(t, s, Maker{By: MakerOwner})

	for _, id := range []string{"N1", "A1", kick} {
		if got := ownerActionRecipient(t, s, id); got != "engine-1" {
			t.Errorf("Recipient of owner decision %s = %q, want the steerer engine-1", id, got)
		}
	}
}

func TestOwnerActionEventConversationNoteAnswerAndKickStoreNoRecipient(t *testing.T) {
	t.Parallel()
	s := ownerActionStore(t)
	kick := ownerActionWrites(t, s, Maker{By: MakerConversation, Session: "engine-1"})

	for _, id := range []string{"N1", "A1", kick} {
		if got := ownerActionRecipient(t, s, id); got != "" {
			t.Errorf("Recipient of conversation decision %s = %q, want none", id, got)
		}
	}
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
