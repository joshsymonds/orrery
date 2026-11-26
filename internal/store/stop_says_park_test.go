package store

import (
	"context"
	"slices"
	"testing"
)

// TestStopSaysNoQuestionOnStepStop checks that a release stopped at a failed release step parks the release_step class with
// its reason and opens no park question: the planner answers that park, and no question of the epic is open.
func TestStopSaysNoQuestionOnStepStop(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	startRelease(t, s, releaseSteps())
	const reason = "release step 2 (publish) failed: rejected"
	releaseStepStop(t, s, reason)

	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassReleaseStep || epic.Parked != reason {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassReleaseStep, reason)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: a stop at a failed step is the planner's to answer", got)
	}
}

// TestStopSaysReviewRefusalIsPlannerPark checks that a release stopped by a release reviewer's refusal of its candidate
// parks the release_step class with the refusal as its reason, opens no park question and adds a pending trigger that wakes
// the planner.
func TestStopSaysReviewRefusalIsPlannerPark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	startRelease(t, s, releaseSteps())
	const reason = "the release reviewer refused the candidate: the tag is unsigned"
	if err := s.RecordRelease(ctx, claimEpic, ReleaseRun{
		Part: ReleasePartReview, Position: 1, Attempt: 1, Step: "release reviewer", Reason: reason, At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease of the refused review: %v", err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, reason, claimedAt()); err != nil {
		t.Fatalf("StopRelease at the refused review: %v", err)
	}

	requireReleasePark(t, s, reason)
}

// TestStopSaysOwnerStopOpensNoQuestion checks that a release the owner stopped parks the release_owner_stop class with
// its reason, opens no park question, adds that reason as a tell that carries no question, and adds no pending trigger.
func TestStopSaysOwnerStopOpensNoQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	const reason = "hold the release until the tag is re-cut"
	releaseOwnerStops(t, s, reason)

	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassReleaseOwnerStop || epic.Parked != reason {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassReleaseOwnerStop, reason)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: the owner's stop opens no park question", got)
	}

	if tells := attentionTellsOf(t, s); len(tells) != 1 || tells[0].Text != reason || tells[0].Question != nil {
		t.Errorf("tells = %+v, want one untold tell %q that carries no question", tells, reason)
	}
	if slices.Contains(pendingCauses(t, s), reason) {
		t.Errorf("pending triggers = %v, want none that wakes the planner with %q", pendingCauses(t, s), reason)
	}
}

// TestStopSaysOwnerStopOfAStoppedReleaseTellsOnce checks that the owner's StopEpic of an epic whose release a step stopped
// takes the release_owner_stop class with the park's reason kept, adds that reason as one tell with no question, and adds
// no second tell when the owner repeats the stop.
func TestStopSaysOwnerStopOfAStoppedReleaseTellsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	startRelease(t, s, releaseSteps())
	const reason = "release step 2 (publish) failed: rejected"
	releaseStepStop(t, s, reason)
	for range 2 {
		if _, err := s.StopEpic(ctx, claimEpic, Maker{By: MakerOwner}, claimedAt()); err != nil {
			t.Fatalf("StopEpic: %v", err)
		}
	}

	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassReleaseOwnerStop || epic.Parked != reason {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassReleaseOwnerStop, reason)
	}
	if tells := attentionTellsOf(t, s); len(tells) != 1 || tells[0].Text != reason || tells[0].Question != nil {
		t.Errorf("tells = %+v, want one untold tell %q that carries no question", tells, reason)
	}
}

// attentionTellsOf returns the untold tells of claimEpic as Attention lists them, and fails the test when Attention lists no
// entry for claimEpic.
func attentionTellsOf(t *testing.T, s *Store) []EpicTell {
	t.Helper()
	attention, err := s.Attention(context.Background())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	idx := slices.IndexFunc(attention, func(entry EpicAttention) bool { return entry.Epic == claimEpic })
	if idx < 0 {
		t.Fatalf("Attention = %+v, want an entry for %s", attention, claimEpic)
	}
	return attention[idx].Tells
}
