package store

import (
	"context"
	"slices"
	"testing"
)

// ownerCallCase is one call an owner makes on an epic that a conversation steers. setup stores the accepted state the
// call runs from, call makes it as maker, and action is the Action its caller event names. A decision case is an Answer
// or a Kick, which stores a decision owed to the steerer; any other case stores an action entry.
type ownerCallCase struct {
	name     string
	epic     string
	setup    func(t *testing.T, s *Store)
	call     func(s *Store, maker Maker) error
	action   string
	decision bool
}

// ownerCallCases returns the nine calls: the seven controls, each from an accepted state, and the note, the answer and
// the kick, which write decisions. Every call is made at a fixed second.
func ownerCallCases() []ownerCallCase {
	ctx := context.Background()
	return []ownerCallCase{
		{
			name: "pause", epic: claimEpic, setup: actionLogCreate, action: ActionPause,
			call: func(s *Store, maker Maker) error {
				return s.PauseEpic(ctx, claimEpic, maker, actionLogAt())
			},
		},
		{
			name: "resume", epic: claimEpic, setup: actionLogPaused, action: ActionResume,
			call: func(s *Store, maker Maker) error {
				return s.ResumeEpic(ctx, claimEpic, maker, actionLogAt())
			},
		},
		{
			name: "stop", epic: claimEpic, setup: actionLogRunning, action: ActionStop,
			call: func(s *Store, maker Maker) error {
				_, err := s.StopEpic(ctx, claimEpic, maker, actionLogAt())
				return err
			},
		},
		{
			name: "retry", epic: claimEpic, setup: actionLogParked, action: ActionRetry,
			call: func(s *Store, maker Maker) error {
				return s.RetryEpic(ctx, claimEpic, maker, actionLogAt())
			},
		},
		{
			name: "release", epic: claimEpic, setup: actionLogEndedWithGaps, action: ActionRelease,
			call: func(s *Store, maker Maker) error {
				return s.ReleaseEpic(ctx, claimEpic, nil, maker, actionLogAt())
			},
		},
		{
			name: "close", epic: claimEpic, setup: actionLogCreate, action: ActionClose,
			call: func(s *Store, maker Maker) error {
				_, err := s.CloseEpic(ctx, claimEpic, kindShipped, "", CloserOwner, maker, actionLogAt())
				return err
			},
		},
		{
			name: "amend", epic: amendEpic, setup: createAmendEpic, action: ActionAmend,
			call: func(s *Store, maker Maker) error {
				_, err := s.AmendEvidence(ctx, amendEpic, Amendment{
					Requirement: "R1", Item: captureAmendItem(), Reason: "the test moved to cmd/orreryd",
				}, maker, actionLogAt())
				return err
			},
		},
		{
			name: "note", epic: steerMoveEpic, setup: ownerCallQuestionedEpic, action: "answer", decision: true,
			call: func(s *Store, maker Maker) error {
				_, err := s.AnswerEpic(ctx, steerMoveEpic, "N1", "Keep the old tasks", maker, steerMoveAt())
				return err
			},
		},
		{
			name: "answer", epic: steerMoveEpic, setup: ownerCallQuestionedEpic, action: "answer", decision: true,
			call: func(s *Store, maker Maker) error {
				_, err := s.AnswerQuestion(ctx, steerMoveEpic, "A1", "Keep them", 1, ChoiceOther, maker, steerMoveAt())
				return err
			},
		},
		{
			name: "kick", epic: steerMoveEpic, setup: ownerCallQuestionedEpic, action: "kick", decision: true,
			call: func(s *Store, maker Maker) error {
				return s.KickEpic(ctx, steerMoveEpic, "Try the sqlite plan", maker, steerMoveAt())
			},
		},
	}
}

// ownerCallQuestionedEpic stores steerMoveEpic in the accepted phase with its turn 1 complete and one open question, which
// is question 1, so an answer has a question to close.
func ownerCallQuestionedEpic(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(steerMoveEpic))
	if _, err := s.ClaimTurn(ctx, steerMoveEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedTurn()
	plan.Questions = []EpicQuestionInput{shapedQuestion("which-store", "Which store?")}
	if err := s.CompleteTurn(ctx, steerMoveEpic, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// ownerCallOpen opens a store with the sessions engine-1 and engine-2, stores the accepted state of tc, and steers its
// epic by engine-1 with a direct update, as steerPendingEpic does. It returns the store and the path a Reader reads it
// from.
func ownerCallOpen(t *testing.T, tc ownerCallCase) (*Store, string) {
	t.Helper()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	tc.setup(t, s)
	const steer = `UPDATE epics SET steerer = ? WHERE id = ?`
	if _, err := s.fixtureExec(context.Background(), steer, "engine-1", tc.epic); err != nil {
		t.Fatalf("steer epic %s by engine-1: %v", tc.epic, err)
	}
	return s, path
}

// ownerCallRecord returns the id of the caller event that tc's newest write holds, and the Recipient that write stored:
// its newest decision for a decision case, and its newest action otherwise. It fails the test when that action is not
// tc's kind.
func ownerCallRecord(t *testing.T, s *Store, tc ownerCallCase) (event, recipient string) {
	t.Helper()
	if tc.decision {
		decisions := ownerActionDecisions(t, s)
		last := decisions[len(decisions)-1]
		return EpicDecisionEvent(steerMoveEpic, last.Seq), last.Recipient
	}
	actions := actionLogOf(t, s, tc.epic)
	if len(actions) == 0 {
		t.Fatalf("epic %s has no action after %s", tc.epic, tc.name)
	}
	last := actions[len(actions)-1]
	if last.Kind != tc.action {
		t.Fatalf("newest action of %s = %+v, want kind %s", tc.epic, last, tc.action)
	}
	return EpicActionEvent(tc.epic, last.Seq), last.Recipient
}

// requireCallEventOwed fails unless exactly one action event with id event is owed to session, names action and was made by
// maker, and session's pending list holds its id exactly once.
func requireCallEventOwed(t *testing.T, s *Store, path, event, session, action string, maker Maker) {
	t.Helper()
	events, err := s.ActionEvents(context.Background())
	if err != nil {
		t.Fatalf("ActionEvents: %v", err)
	}
	matched := 0
	for _, got := range events {
		if got.ID != event {
			continue
		}
		matched++
		if got.Kind != CallerKindAction || got.Action != action || got.Recipient.ID != session || got.Maker != maker {
			t.Fatalf("ActionEvent %s = %+v, want a %s action event naming %s owed to %s and made by %+v",
				event, got, CallerKindAction, action, session, maker)
		}
	}
	if matched != 1 {
		t.Fatalf("ActionEvents lists %d events %s, want exactly one", matched, event)
	}
	if n := countOwedID(pendingIDs(readPending(t, path, session)), event); n != 1 {
		t.Fatalf("PendingCallerEvents(%s) lists %s %d times, want once", session, event, n)
	}
}

// requireCallEventUnowed fails unless no action event with id event is owed to any session, and session's pending list
// does not hold it.
func requireCallEventUnowed(t *testing.T, s *Store, path, event, session string) {
	t.Helper()
	events, err := s.ActionEvents(context.Background())
	if err != nil {
		t.Fatalf("ActionEvents: %v", err)
	}
	for _, got := range events {
		if got.ID == event {
			t.Fatalf("ActionEvents lists %s owed to %s, want none", event, got.Recipient.ID)
		}
	}
	if n := countOwedID(pendingIDs(readPending(t, path, session)), event); n != 0 {
		t.Fatalf("PendingCallerEvents(%s) lists %s %d times, want none", session, event, n)
	}
}

// countOwedID returns how many of ids equal id.
func countOwedID(ids []string, id string) int {
	n := 0
	for _, got := range ids {
		if got == id {
			n++
		}
	}
	return n
}

// TestOwnerActionEventOwnerCallIsOwedOnceToTheSteerer checks that each of the nine calls, made by the owner on an epic
// engine-1 steers, stores engine-1 as its recipient, lists exactly one action event for it, owed to engine-1 in
// ActionEvents and in its pending list, and lists it no more once engine-1 marks it.
func TestOwnerActionEventOwnerCallIsOwedOnceToTheSteerer(t *testing.T) {
	t.Parallel()
	owner := Maker{By: MakerOwner}
	for _, tc := range ownerCallCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, path := ownerCallOpen(t, tc)
			if err := tc.call(s, owner); err != nil {
				t.Fatalf("owner %s: %v", tc.name, err)
			}

			event, recipient := ownerCallRecord(t, s, tc)
			if recipient != "engine-1" {
				t.Fatalf("Recipient of owner %s = %q, want the steerer engine-1", tc.name, recipient)
			}
			requireCallEventOwed(t, s, path, event, "engine-1", tc.action, owner)

			if err := s.MarkCallerEvent(context.Background(), event, "engine-1", actionLogAt()); err != nil {
				t.Fatalf("MarkCallerEvent: %v", err)
			}
			requireCallEventUnowed(t, s, path, event, "engine-1")
		})
	}
}

// TestOwnerActionEventConversationCallIsOwedToNoSession checks that each of the nine calls, made by the conversation that
// steers the epic, stores no recipient and lists no action event for any session.
func TestOwnerActionEventConversationCallIsOwedToNoSession(t *testing.T) {
	t.Parallel()
	maker := Maker{By: MakerConversation, Session: "engine-1"}
	for _, tc := range ownerCallCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, path := ownerCallOpen(t, tc)
			if err := tc.call(s, maker); err != nil {
				t.Fatalf("conversation %s: %v", tc.name, err)
			}

			event, recipient := ownerCallRecord(t, s, tc)
			if recipient != "" {
				t.Fatalf("Recipient of conversation %s = %q, want none", tc.name, recipient)
			}
			requireCallEventUnowed(t, s, path, event, "engine-1")
			requireCallEventUnowed(t, s, path, event, "engine-2")
		})
	}
}

// TestOwnerActionEventMoveAfterOwnerCallLeavesItOwedToTheOldSteerer checks that when the owner moves an epic after one of
// the nine calls, the call's action event stays owed to the steerer that held the epic when the call was made, and not to
// the session that took it.
func TestOwnerActionEventMoveAfterOwnerCallLeavesItOwedToTheOldSteerer(t *testing.T) {
	t.Parallel()
	owner := Maker{By: MakerOwner}
	for _, tc := range ownerCallCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, path := ownerCallOpen(t, tc)
			if err := tc.call(s, owner); err != nil {
				t.Fatalf("owner %s: %v", tc.name, err)
			}
			event, _ := ownerCallRecord(t, s, tc)

			if _, err := s.MoveEpic(context.Background(), tc.epic, "engine-2", owner, actionLogAt()); err != nil {
				t.Fatalf("MoveEpic: %v", err)
			}
			requireCallEventOwed(t, s, path, event, "engine-1", tc.action, owner)
			if slices.Contains(pendingIDs(readPending(t, path, "engine-2")), event) {
				t.Fatalf("PendingCallerEvents(engine-2) lists %s, which is owed to the old steerer engine-1", event)
			}
		})
	}
}
