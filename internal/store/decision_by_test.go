package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestDecisionByPlannerActionIsOrreryd checks that the decision a planner action records names orreryd as its maker,
// with no session.
func TestDecisionByPlannerActionIsOrreryd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.ParkEpic(ctx, claimEpic, ParkClassNode, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic of %s: %v", claimEpic, err)
	}
	action := storeActions(t, s, claimEpic, EpicActionInput{Kind: ActionRetryEpic, Reason: "the credit is back"})[0]
	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}

	want := Maker{By: MakerOrreryd}
	if got := decisionNamed(t, s, claimEpic, "action-1-1").Maker; got != want {
		t.Errorf("maker of the planner action's decision = %+v, want %+v", got, want)
	}
}

// TestDecisionByBuildTaskIsOrreryd checks that the decision a build task's climb records names orreryd as its maker,
// with no session.
func TestDecisionByBuildTaskIsOrreryd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByFinding, Cause: climbCause, Gate: "review", Assertion: "the claim", Step: "step-3"}
	decision := TaskDecision{ID: "T1-climb-1", Text: "T1 climbed: the claim is not met"}
	to := ClimbedBuild(from, 2, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, &decision, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	want := Maker{By: MakerOrreryd}
	if got := decisionNamed(t, s, climbEpic, "T1-climb-1").Maker; got != want {
		t.Errorf("maker of the build task's decision = %+v, want %+v", got, want)
	}
}

// TestDecisionByAmendmentSettlingIsOrreryd checks that the decision an amendment's settling records names orreryd as
// its maker, with no session.
func TestDecisionByAmendmentSettlingIsOrreryd(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := amendOwner(t, s, newAmendItem())
	judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
	settle(t, s)

	want := Maker{By: MakerOrreryd}
	if got := decisionNamed(t, s, amendEpic, "amend-1").Maker; got != want {
		t.Errorf("maker of the amendment's decision = %+v, want %+v", got, want)
	}
}

// TestDecisionByDecisionStoredBeforeMakersReadsEmpty checks that a decision stored before migration 79 reads back
// with an empty maker and session once the store migrates up.
func TestDecisionByDecisionStoredBeforeMakersReadsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	fixture := openStoreAtVersion(t, path, 78)
	seedEpic(t, fixture)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 79: %v", err)
	}
	defer s.Close()

	row, err := s.Epic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Decisions) != 1 {
		t.Fatalf("decisions = %d, want the seeded one", len(row.Decisions))
	}
	if got := row.Decisions[0].Maker; got != (Maker{}) {
		t.Errorf("maker of the migrated decision = %+v, want empty", got)
	}
}
