package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// startRelease creates claimEpic clean with the release steps given, takes its repository's release slot and starts its
// release node run, so the release runs and holds the slot.
func startRelease(t *testing.T, s *Store, steps []EpicReleaseStep) {
	t.Helper()
	ctx := context.Background()
	row := fullEpic(claimEpic)
	row.Phase = PhaseClean
	row.ReleaseSteps = steps
	createEpic(t, s, row)
	if _, err := s.TakeReleaseSlot(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("TakeReleaseSlot: %v", err)
	}
	runs := []NodeRun{{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
}

// releaseUndoNoneStops stops the release of claimEpic at its second release step, which has undo none and failed.
func releaseUndoNoneStops(t *testing.T, s *Store, park string) {
	t.Helper()
	ctx := context.Background()
	startRelease(t, s, []EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "none"},
	})
	if err := s.RecordRelease(ctx, claimEpic, ReleaseRun{
		Part: ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-publish",
		Reason: "rejected", At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease: %v", err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, park, claimedAt()); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
}

// releaseOwnerStops stops the release of claimEpic after the epic no longer holds its release slot, as an owner stop
// leaves it. ClaimTurn then claims no turn for the epic, so the park wakes no planner turn.
func releaseOwnerStops(t *testing.T, s *Store, park string) {
	t.Helper()
	ctx := context.Background()
	startRelease(t, s, []EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "none"},
	})
	if err := s.FreeReleaseSlot(ctx, claimEpic); err != nil {
		t.Fatalf("FreeReleaseSlot: %v", err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, park, claimedAt()); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("ClaimTurn of an epic whose release the owner stopped err = %v, want ErrNotClaimable", err)
	}
}

// metAtBaseParks parks the red run of a new claimEpic, which has had no planner turn, as an evidence run that judged its
// Requirement met at the base does.
func metAtBaseParks(t *testing.T, s *Store, _ string) {
	t.Helper()
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)
}

// refMoveParks parks the running check run of a new claimEpic, which has had no planner turn, when its refs check finds
// a move no step explains.
func refMoveParks(t *testing.T, s *Store, park string) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(claimEpic))
	runs := []NodeRun{{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, claimEpic, "check", 1, park, nil, claimedAt()); err != nil {
		t.Fatalf("CheckNodeRefs: %v", err)
	}
}

// failedTurnsPark fails three planner turns of a new claimEpic in a row, which parks it.
func failedTurnsPark(t *testing.T, s *Store, _ string) {
	t.Helper()
	createEpic(t, s, fullEpic(claimEpic))
	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	claimAndFail(t, s, "third failure")
}

// TestCatastropheParkOpensItsOwnerQuestionAndRoutesItsNotice checks that each owner-class park of a catastrophe, driven
// through the store entry point that parks it, stores its class and reason, opens one open park question with the reason,
// routes the reason as the owner's notice, and adds no planner park trigger. A park that adds a tell routes that tell,
// and Attention lists its epic although the epic has had no planner turn. A park that ends a turn adds no tell, so the
// turn it ended is the notice.
func TestCatastropheParkOpensItsOwnerQuestionAndRoutesItsNotice(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name      string
		class     ParkClass
		park      string
		drive     func(t *testing.T, s *Store, park string)
		turnEnded bool
	}{
		{
			name: "release_undo_none", class: ParkClassReleaseUndoNone,
			park: "publish failed and its undo is none", drive: releaseUndoNoneStops,
		},
		{
			name: "release_owner_stop", class: ParkClassReleaseOwnerStop,
			park: "the owner stopped the release", drive: releaseOwnerStops,
		},
		{name: "met_at_base", class: ParkClassMetAtBase, park: metAtBaseParkReason, drive: metAtBaseParks},
		{
			name: "ref_move", class: ParkClassRefMove,
			park: "refs/heads/main moved and no step explains the move", drive: refMoveParks,
		},
		{
			name: "orchestrator", class: ParkClassOrchestrator, park: parkPrefix + "third failure",
			drive: failedTurnsPark, turnEnded: true,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			row.drive(t, s, row.park)

			epic, err := s.Epic(ctx, claimEpic)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			if epic.Parked != row.park || epic.ParkClass != row.class {
				t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, row.class, row.park)
			}

			attention, err := s.Attention(ctx)
			if err != nil {
				t.Fatalf("Attention: %v", err)
			}
			idx := slices.IndexFunc(attention, func(entry EpicAttention) bool { return entry.Epic == claimEpic })
			if idx < 0 {
				t.Fatalf("Attention = %+v, want an entry for %s", attention, claimEpic)
			}
			entry := attention[idx]
			if len(entry.Open) != 1 || entry.Open[0].Label != ParkLabel || entry.Open[0].Text != row.park {
				t.Fatalf("open questions = %+v, want one park question with text %q", entry.Open, row.park)
			}
			if row.turnEnded {
				if len(entry.Tells) != 0 {
					t.Errorf("tells = %+v, want none: a park that ends a turn adds none", entry.Tells)
				}
				if entry.Latest.Seq != 3 || entry.Latest.State != "failed" || entry.Latest.Told {
					t.Errorf("latest turn = %+v, want the third failed turn, untold", entry.Latest)
				}
			} else if len(entry.Tells) != 1 || entry.Tells[0].Text != row.park || entry.Tells[0].Question == nil ||
				entry.Tells[0].Question.Seq != entry.Open[0].Seq {
				t.Errorf("tells = %+v, want one untold tell %q that carries the park question", entry.Tells, row.park)
			}

			history, err := s.EpicHistory(ctx, claimEpic)
			if err != nil {
				t.Fatalf("EpicHistory: %v", err)
			}
			for _, trigger := range history.Triggers {
				if strings.HasPrefix(trigger.ID, parkTriggerPrefix) {
					t.Errorf("trigger %s = %+v, want no planner park trigger", trigger.ID, trigger)
				}
			}
		})
	}
}
