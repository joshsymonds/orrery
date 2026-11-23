package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// The contracts the held-epic tests build a design of: idleContract has no epic, runningContract has an epic that is
// running, and heldContract is the contract whose epic is created after its predecessors.
const (
	heldDesign      = "orrery-7a8b9c0/design-held"
	idleContract    = "orrery-7a8b9c0/contract-idle"
	runningContract = "orrery-7a8b9c0/contract-running"
	heldContract    = "orrery-7a8b9c0/contract-held"
)

// proposeAcceptInDesign proposes the contract id into heldDesign titled title at at, and accepts it as its owner at
// at+10. It fails the test on error.
func proposeAcceptInDesign(t *testing.T, s *store.Store, id, title string, at int64) {
	t.Helper()
	proposeInDesign(t, s, heldDesign, id, designProposal("/work/synthetic-repo", title, at))
	if err := s.AcceptContract(
		t.Context(), id, contractChangeBy("", "Accept the contract", at+10),
	); err != nil {
		t.Fatalf("AcceptContract %s: %v", id, err)
	}
}

// TestDesignHeldEpicStoredHeld asserts that CreateContractEpic stores the epic of a contract that starts after a
// predecessor with no epic, or after one whose epic is running, in phase held, and that the epic reads back held.
func TestDesignHeldEpicStoredHeld(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		predecessors []string
	}{
		{name: "no epic", predecessors: []string{idleContract}},
		{name: "running epic", predecessors: []string{runningContract}},
		{name: "no epic and running epic", predecessors: []string{idleContract, runningContract}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, _ := openEpicCallStore(t)
			newDesign(t, s, heldDesign)
			proposeAcceptInDesign(t, s, idleContract, "Idle contract", contractProposedAt)
			proposeAcceptInDesign(t, s, runningContract, "Running contract", contractProposedAt+20)
			if err := s.CreateContractEpic(ctx, contractEpicRow(runningContract, contractProposedAt+40), 1); err != nil {
				t.Fatalf("CreateContractEpic of the running predecessor: %v", err)
			}
			proposeAcceptInDesign(t, s, heldContract, "Held contract", contractProposedAt+60)
			for _, predecessor := range tc.predecessors {
				if err := s.AddDesignEdge(
					ctx, heldDesign, heldContract, predecessor, time.Unix(contractProposedAt+80, 0),
				); err != nil {
					t.Fatalf("AddDesignEdge after %s: %v", predecessor, err)
				}
			}

			if err := s.CreateContractEpic(ctx, contractEpicRow(heldContract, contractProposedAt+100), 1); err != nil {
				t.Fatalf("CreateContractEpic: %v", err)
			}
			row, err := s.Epic(ctx, heldContract)
			if err != nil {
				t.Fatalf("Epic %s: %v", heldContract, err)
			}
			if row.Phase != store.PhaseHeld {
				t.Fatalf("epic phase = %q, want %q", row.Phase, store.PhaseHeld)
			}
		})
	}
}

// TestDesignHeldEpicNoPredecessorStartsAsToday asserts that CreateContractEpic stores the epic of a contract with no
// predecessor in phase accepted, as it stored every epic before held epics: a contract in no design, a design member
// with no edge, and a design member that another contract starts after.
func TestDesignHeldEpicNoPredecessorStartsAsToday(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, s *store.Store)
	}{
		{
			name: "contract in no design",
			setup: func(t *testing.T, s *store.Store) {
				t.Helper()
				proposeAndAccept(t, s, heldContract)
			},
		},
		{
			name: "design member with no edge",
			setup: func(t *testing.T, s *store.Store) {
				t.Helper()
				newDesign(t, s, heldDesign)
				proposeAcceptInDesign(t, s, heldContract, "Held contract", contractProposedAt)
			},
		},
		{
			name: "design member another contract starts after",
			setup: func(t *testing.T, s *store.Store) {
				t.Helper()
				newDesign(t, s, heldDesign)
				proposeAcceptInDesign(t, s, heldContract, "Held contract", contractProposedAt)
				proposeInDesign(
					t, s, heldDesign, idleContract,
					designProposal("/work/synthetic-repo", "Idle contract", contractProposedAt+5),
				)
				if err := s.AddDesignEdge(
					t.Context(), heldDesign, idleContract, heldContract, time.Unix(contractProposedAt+10, 0),
				); err != nil {
					t.Fatalf("AddDesignEdge: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, _ := openEpicCallStore(t)
			tc.setup(t, s)

			if err := s.CreateContractEpic(ctx, contractEpicRow(heldContract, contractProposedAt+60), 1); err != nil {
				t.Fatalf("CreateContractEpic: %v", err)
			}
			row, err := s.Epic(ctx, heldContract)
			if err != nil {
				t.Fatalf("Epic %s: %v", heldContract, err)
			}
			if row.Phase != "accepted" {
				t.Fatalf("epic phase = %q, want accepted", row.Phase)
			}
		})
	}
}

// TestDesignHeldEpicPredecessorPhaseDecidesHold asserts that a predecessor whose epic is in a phase that ships does not
// hold the epic of the contract that starts after it, and that a predecessor whose epic closed some other way does.
func TestDesignHeldEpicPredecessorPhaseDecidesHold(t *testing.T) {
	t.Parallel()
	const predecessor = "orrery-7a8b9c0/contract-predecessor"
	cases := []struct {
		phase, want string
	}{
		{phase: "released", want: "accepted"},
		{phase: "done", want: "accepted"},
		{phase: store.PhaseClosedShipped, want: "accepted"},
		{phase: store.PhaseClosedSuperseded, want: store.PhaseHeld},
		{phase: store.PhaseClosedAbandoned, want: store.PhaseHeld},
	}
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, _ := openEpicCallStore(t)
			newDesign(t, s, heldDesign)
			proposeAcceptInDesign(t, s, predecessor, "Predecessor contract", contractProposedAt)
			ended := contractEpicRow(predecessor, contractProposedAt+20)
			ended.Phase = tc.phase
			if err := s.CreateEpic(ctx, ended); err != nil {
				t.Fatalf("CreateEpic of the predecessor in %s: %v", tc.phase, err)
			}
			proposeAcceptInDesign(t, s, heldContract, "Held contract", contractProposedAt+40)
			if err := s.AddDesignEdge(
				ctx, heldDesign, heldContract, predecessor, time.Unix(contractProposedAt+60, 0),
			); err != nil {
				t.Fatalf("AddDesignEdge: %v", err)
			}

			if err := s.CreateContractEpic(ctx, contractEpicRow(heldContract, contractProposedAt+80), 1); err != nil {
				t.Fatalf("CreateContractEpic: %v", err)
			}
			row, err := s.Epic(ctx, heldContract)
			if err != nil {
				t.Fatalf("Epic %s: %v", heldContract, err)
			}
			if row.Phase != tc.want {
				t.Fatalf("epic phase = %q, want %q", row.Phase, tc.want)
			}
		})
	}
}
