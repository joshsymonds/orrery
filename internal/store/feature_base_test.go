package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// featureBase is the commit the base tests accept their contract at.
const featureBase = "3f9c1e2d4b5a69788796a5b4c3d2e1f00f9e8d7c"

// TestFeatureBaseAcceptStoresTheBaseOnTheAcceptedState checks that accepting a contract stores the base the change
// carries on the accepted state, and that the proposed state it follows reads back with a blank base.
func TestFeatureBaseAcceptStoresTheBaseOnTheAcceptedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-base"
	proposal := syntheticProposal(contractWriter, contractProposedAt)
	proposal.Decisions[1].Answer = "no"
	if err := s.ProposeContract(ctx, id, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	change := store.ContractChange{
		Words: "Accept the synthetic contract", Base: featureBase, At: contractProposedAt + 20,
	}
	if err := s.AcceptContract(ctx, id, change); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	want := []store.ContractStateChange{
		contractStateOf("proposed", 1, contractWriter, "Propose the synthetic contract", contractProposedAt),
		{
			State: "accepted", Version: 1, Words: "Accept the synthetic contract", Base: featureBase,
			At: contractProposedAt + 20,
		},
	}
	if !reflect.DeepEqual(got.States, want) {
		t.Fatalf("States = %+v, want %+v", got.States, want)
	}
}
