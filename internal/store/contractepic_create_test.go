package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractEpicRow returns the head of an epic created at at under the contract id, with no sections.
func contractEpicRow(id string, at int64) store.EpicRow {
	return store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Care: "serious", Phase: "accepted",
		Created: time.Unix(at, 0), Changed: time.Unix(at, 0), PhaseAt: time.Unix(at, 0),
	}
}

// TestContractEpicCreateRefusesAContractNotAcceptedAtTheReadVersionAndStoresNothing asserts that CreateContractEpic
// refuses a contract that is set aside, and one accepted at a version after the version the create read, with an error
// that matches ErrContractState, and stores no epic.
func TestContractEpicCreateRefusesAContractNotAcceptedAtTheReadVersionAndStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, s *store.Store, id string)
	}{
		{
			name: "set aside",
			setup: func(t *testing.T, s *store.Store, id string) {
				t.Helper()
				proposeAndAccept(t, s, id)
				if err := s.SetAsideContract(
					t.Context(),
					id,
					contractChangeBy("", "Set the contract aside", contractProposedAt+20),
				); err != nil {
					t.Fatalf("SetAsideContract: %v", err)
				}
			},
		},
		{
			name: "version moved",
			setup: func(t *testing.T, s *store.Store, id string) {
				t.Helper()
				proposeAndAccept(t, s, id)
				if err := s.SetAsideContract(
					t.Context(),
					id,
					contractChangeBy("", "Set the contract aside", contractProposedAt+20),
				); err != nil {
					t.Fatalf("SetAsideContract: %v", err)
				}
				if err := s.ReopenContract(
					t.Context(),
					id,
					contractChangeBy("", "Reopen the contract", contractProposedAt+30),
				); err != nil {
					t.Fatalf("ReopenContract: %v", err)
				}
				revision := contractRevision(2, contractWriter, contractProposedAt+40)
				if err := s.ReviseContract(t.Context(), id, revision); err != nil {
					t.Fatalf("ReviseContract: %v", err)
				}
				if err := s.AcceptContract(
					t.Context(),
					id,
					contractChangeBy("", "Accept the revision", contractProposedAt+50),
				); err != nil {
					t.Fatalf("AcceptContract of version 2: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, _ := openEpicCallStore(t)
			const id = "orrery-5e6f7a8/contract-epic-create"
			tc.setup(t, s, id)

			err := s.CreateContractEpic(ctx, contractEpicRow(id, contractProposedAt+60), 1)
			if !errors.Is(err, store.ErrContractState) {
				t.Fatalf("CreateContractEpic err = %v, want ErrContractState", err)
			}
			if _, err = s.Epic(ctx, id); !errors.Is(err, store.ErrNoEpic) {
				t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
			}
		})
	}
}

// TestContractEpicCreateStoresAnAcceptedContractAtTheReadVersion asserts that CreateContractEpic stores the epic of a
// contract accepted at the version it is given.
func TestContractEpicCreateStoresAnAcceptedContractAtTheReadVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-epic-accepted"
	proposeAndAccept(t, s, id)

	if err := s.CreateContractEpic(ctx, contractEpicRow(id, contractProposedAt+60), 1); err != nil {
		t.Fatalf("CreateContractEpic: %v", err)
	}
	if _, err := s.Epic(ctx, id); err != nil {
		t.Fatalf("Epic %s: %v", id, err)
	}
}

// proposeAndAccept proposes the synthetic contract id as version 1 and accepts it as the owner.
func proposeAndAccept(t *testing.T, s *store.Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.ProposeContract(ctx, id, contractRevision(1, contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+10),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
}
