package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestOrchestratorParkNonCatastropheParksWakeThePlanner checks that each park of a planner class, here a failed check
// node, a task build park, a loop out of rounds and an epic not built, parks its epic with the class it stores and
// reason, opens no question, adds no tell, and adds exactly one pending trigger whose cause is the reason.
func TestOrchestratorParkNonCatastropheParksWakeThePlanner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rows := []struct {
		name   string
		class  ParkClass
		reason string
		park   func(t *testing.T, s *Store, reason string)
	}{
		{
			name: "a failed check node", class: ParkClassNode, reason: "no verdict twice",
			park: func(t *testing.T, s *Store, reason string) {
				t.Helper()
				runs := []NodeRun{{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1}}
				if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
					t.Fatalf("StartNodes: %v", err)
				}
				if err := s.ParkNode(
					ctx, claimEpic, "check", 1, "failed", "no verdict", ParkClassNode, reason, completedAt(),
				); err != nil {
					t.Fatalf("ParkNode: %v", err)
				}
			},
		},
		{
			name: "a task build park", class: ParkClassTask, reason: "the red run did not capture",
			park: func(t *testing.T, s *Store, reason string) {
				t.Helper()
				to := TaskBuild{Stage: "red", Base: "base-rev", Held: "parked"}
				if err := s.ParkTask(
					ctx,
					claimEpic,
					1,
					TaskBuild{},
					to,
					ParkClassTask,
					reason,
					completedAt(),
				); err != nil {
					t.Fatalf("ParkTask: %v", err)
				}
			},
		},
		{
			name:   "a loop out of rounds",
			class:  ParkClassLoop,
			reason: "loop fix spent its 2 rounds without review.complete",
			park: func(t *testing.T, s *Store, reason string) {
				t.Helper()
				runs := []NodeRun{{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2}}
				if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
					t.Fatalf("StartNodes: %v", err)
				}
				if err := s.ParkLoop(ctx, claimEpic, "fix", 2, reason, completedAt()); err != nil {
					t.Fatalf("ParkLoop: %v", err)
				}
			},
		},
		{
			name:   "epic not built",
			class:  ParkClassNotBuilt,
			reason: "epic not built: R1 has no red at the epic's base",
			park: func(t *testing.T, s *Store, reason string) {
				t.Helper()
				trigger := NodeTrigger{ID: "workflow:unbuilt-1", Cause: reason}
				if err := s.ParkEpicWaking(ctx, claimEpic, reason, trigger, completedAt()); err != nil {
					t.Fatalf("ParkEpicWaking: %v", err)
				}
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openPlannedClaimStore(t)
			questions := questionsOf(t, s)
			row.park(t, s, row.reason)

			epic, err := s.Epic(ctx, claimEpic)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			if epic.Parked != row.reason || epic.ParkClass != row.class {
				t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, row.class, row.reason)
			}
			if got := questionsOf(t, s); !reflect.DeepEqual(got, questions) {
				t.Errorf("questions after the park = %+v, want %+v: a planner park opens none", got, questions)
			}
			history, err := s.EpicHistory(ctx, claimEpic)
			if err != nil {
				t.Fatalf("EpicHistory: %v", err)
			}
			if len(history.Tells) != 0 {
				t.Errorf("tells after the park = %+v, want none", history.Tells)
			}
			var pending []string
			for _, trigger := range history.Triggers {
				if trigger.State == "pending" {
					pending = append(pending, trigger.Cause)
				}
			}
			if want := []string{row.reason}; !reflect.DeepEqual(pending, want) {
				t.Errorf("pending trigger causes after the park = %q, want %q", pending, want)
			}
		})
	}
}

// TestOrchestratorParkClaimTurnClaimsAPlannerParkAndRefusesAnOwnerPark checks that ClaimTurn claims a turn for an epic a
// planner park parked, and that an owner park opens its question and refuses the claim. The epic keeps its created
// trigger pending, so the owner park is the only reason the claim is refused.
func TestOrchestratorParkClaimTurnClaimsAPlannerParkAndRefusesAnOwnerPark(t *testing.T) {
	t.Parallel()
	const reason = "credit ran out"

	t.Run("a planner park is claimed", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		createEpic(t, s, fullEpic(claimEpic))
		if err := s.ParkEpic(ctx, claimEpic, ParkClassNode, reason, completedAt()); err != nil {
			t.Fatalf("ParkEpic: %v", err)
		}

		turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
		if err != nil {
			t.Fatalf("ClaimTurn of a planner park: %v", err)
		}
		if turn.Epic != claimEpic {
			t.Errorf("claimed turn of epic = %q, want %q", turn.Epic, claimEpic)
		}
	})

	t.Run("an owner park is refused", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		createEpic(t, s, fullEpic(claimEpic))
		if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, reason, completedAt()); err != nil {
			t.Fatalf("ParkEpic: %v", err)
		}

		if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("ClaimTurn of an owner park err = %v, want ErrNotClaimable", err)
		}
		if want := []EpicQuestion{parkQuestionOf(1, reason)}; !reflect.DeepEqual(questionsOf(t, s), want) {
			t.Errorf("questions after the owner park = %+v, want %+v", questionsOf(t, s), want)
		}
	})
}
