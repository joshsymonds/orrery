package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// releaseSteps are the release steps of claimEpic in these tests. The publish step's undo is not none, so a stop at it is a
// release_step stop.
func releaseSteps() []EpicReleaseStep {
	return []EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "delete version"},
	}
}

// missingTaskRefusal is the refusal of a send_back action on claimEpic that names a task the epic does not hold.
const missingTaskRefusal = "task 9 of epic " + claimEpic + " does not exist"

// releaseParkReason is the reason of the release stop of claimEpic at the blocked round of generation n.
func releaseParkReason(n int64) string {
	return fmt.Sprintf("the release integrator stopped round %d", n)
}

// integratorRoundStop stops the release of claimEpic on the blocked round of its release integrator at generation, from a
// tip of its own, at attempt 1 with reason. A retry of such a stop is taken: it replans the round.
func integratorRoundStop(t *testing.T, s *Store, generation int64, reason string) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordRelease(ctx, claimEpic, ReleaseRun{
		Part: ReleasePartIntegrator, Position: ReleaseIntegratorPosition(generation), Attempt: 1,
		Step: "release integrator", Start: fmt.Sprintf("tip-%d", generation), End: integratorReturnedEnd,
		Reason: reason, At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease of round %d: %v", generation, err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, reason, claimedAt()); err != nil {
		t.Fatalf("StopRelease of round %d: %v", generation, err)
	}
}

// releaseStepStop stops the release of claimEpic at its publish step, which failed at attempt 1, with reason. That is a
// release_step stop, and a retry_epic on it replans the release.
func releaseStepStop(t *testing.T, s *Store, reason string) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordRelease(ctx, claimEpic, ReleaseRun{
		Part: ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Reason: "rejected", At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease of the failed step: %v", err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, reason, claimedAt()); err != nil {
		t.Fatalf("StopRelease at the failed step: %v", err)
	}
}

// requireReleasePark fails unless claimEpic holds the release_step park with reason as the planner's to answer: no
// question is open, a pending trigger wakes the planner with the reason, and no chain of its holds the park at the bound.
func requireReleasePark(t *testing.T, s *Store, reason string) {
	t.Helper()
	if epic := closeRow(t, s, claimEpic); epic.Parked != reason || epic.ParkClass != ParkClassReleaseStep {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassReleaseStep, reason)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: the planner answers a release stop", got)
	}
	if !slices.Contains(pendingCauses(t, s), reason) {
		t.Errorf("pending triggers = %v, want one that wakes the planner with %q", pendingCauses(t, s), reason)
	}
	if chain := boundChainOf(t, s); chain.Seq != 0 {
		t.Errorf("bound chain = %+v, want none: a release stop is outside the park bound", chain)
	}
}

// TestReplanRerunsStepsParkOutsideChainBound checks that release_step parks on one release never reach the chain bound:
// five stops in a row, each answered by a taken retry_epic, all stay the planner's, and none opens a retry_bound question.
func TestReplanRerunsStepsParkOutsideChainBound(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	startRelease(t, s, releaseSteps())
	for n := int64(1); n <= 5; n++ {
		reason := releaseParkReason(n)
		integratorRoundStop(t, s, n, reason)
		requireReleasePark(t, s, reason)
		answerPark(t, s, retryEpicAction("the release runs again from the round's tip"))
		if epic := closeRow(t, s, claimEpic); epic.Parked != "" {
			t.Fatalf("park after retry %d = %q %q, want none: the retry is taken", n, epic.ParkClass, epic.Parked)
		}
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: no release stop goes to the owner as retry_bound", got)
	}
}

// TestReplanRerunsStepsParkOutsideStreak checks that a release_step park is outside the no-progress streak: after four node
// parks answered with no progress it is still the planner's, and it neither counts toward a later node park's streak nor
// ends it.
func TestReplanRerunsStepsParkOutsideStreak(t *testing.T) {
	t.Parallel()
	t.Run("after four node parks", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		startRelease(t, s, releaseSteps())
		parkChainNewTwice(t, s)
		reason := "the publish step failed"
		releaseStepStop(t, s, reason)
		requireReleasePark(t, s, reason)
	})
	t.Run("not counted toward a later node park", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		startRelease(t, s, releaseSteps())
		parkChainToBound(t, s)
		answerPark(t, s,
			EpicActionInput{Kind: ActionParkNew, Reason: "run 3 failed another way"},
			retryEpicAction("the check runs for the new failure"),
		)
		reason := releaseParkReason(1)
		integratorRoundStop(t, s, 1, reason)
		answerPark(t, s, retryEpicAction("the release runs again from the round's tip"))
		if epic := closeRow(t, s, claimEpic); epic.Parked != "" {
			t.Fatalf("park of epic after the release retry = %q %q, want none", epic.ParkClass, epic.Parked)
		}

		parkChainNode(t, s, 4)
		requirePlannerPark(t, s, 4)
	})
}

// TestReplanRerunsStepsParkRefusalsNotBound checks that refused actions on a release_step park do not bound it: two
// refused send_back actions, each naming a task the epic does not hold, leave the park the planner's, with the refusal of
// the last as its one pending trigger.
func TestReplanRerunsStepsParkRefusalsNotBound(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	startRelease(t, s, releaseSteps())
	reason := "the publish step failed"
	releaseStepStop(t, s, reason)
	for range 2 {
		answerPark(t, s, EpicActionInput{Kind: ActionSendBack, Task: 9, Reason: "the publish step runs again"})
	}

	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassReleaseStep || epic.Parked != reason {
		t.Errorf("park of epic after two refusals = %q %q, want %q %q", epic.ParkClass, epic.Parked,
			ParkClassReleaseStep, reason)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions after two refusals = %+v, want none: the planner answers a release stop", got)
	}
	want := []string{"planner send back task 9 refused: " + missingTaskRefusal}
	if got := pendingCauses(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("pending triggers after two refusals = %v, want %v", got, want)
	}
	if chain := boundChainOf(t, s); chain.Seq != 0 {
		t.Errorf("bound chain after two refusals = %+v, want none: a release stop is outside the park bound", chain)
	}
}
