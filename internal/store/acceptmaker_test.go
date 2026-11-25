package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// recordsLeftAcceptAt is the second the records-left-accept tests accept their contract at.
const recordsLeftAcceptAt = contractProposedAt + 60

// recordsLeftAcceptSession is the sessions row id of the session the records-left-accept tests accept as.
const recordsLeftAcceptSession = "orrery-9"

// checkRecordsLeftAccept proposes the contract id by contractWriter, revises it so every decision is answered, and
// accepts it with words of session and maker at recordsLeftAcceptAt. It checks that the contract reads back with its
// proposal and with the accept, the accept's maker as it was stored.
func checkRecordsLeftAccept(t *testing.T, s *store.Store, id, session string, maker store.StageMaker) {
	t.Helper()
	ctx := context.Background()
	proposal := syntheticProposal(contractWriter, contractProposedAt)
	if err := s.ProposeContract(ctx, id, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	change := store.ContractChange{
		Session: session, Words: "Accept the synthetic contract", At: recordsLeftAcceptAt, Maker: maker,
	}
	if err := s.AcceptContract(ctx, id, change); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	want := []store.ContractStateChange{
		contractStateOf(store.ContractProposed, 1, contractWriter, proposal.Words, contractProposedAt),
		{
			State: store.ContractAccepted, Version: 2, Session: session, Words: change.Words,
			At: recordsLeftAcceptAt, Maker: maker,
		},
	}
	if !reflect.DeepEqual(got.States, want) {
		t.Fatalf("States after the accept = %+v, want %+v", got.States, want)
	}
}

func TestContractRecordsLeftAcceptOwnerServeReadsBackAsOwnerServe(t *testing.T) {
	t.Parallel()
	s, _ := openEpicCallStore(t)
	checkRecordsLeftAccept(t, s, "orrery-5e6f7a8/records-owner-serve", "",
		store.StageMaker{Kind: store.StageMakerOwnerServe})
}

func TestContractRecordsLeftAcceptOwnerConversationReadsBackWithTheOwnersQuote(t *testing.T) {
	t.Parallel()
	s, _ := openEpicCallStore(t)
	checkRecordsLeftAccept(t, s, "orrery-5e6f7a8/records-owner-conversation", recordsLeftAcceptSession,
		store.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Synthetic owner words."})
}

func TestContractRecordsLeftAcceptConversationReadsBackWithItsReasonAndDelegation(t *testing.T) {
	t.Parallel()
	s, _ := openEpicCallStore(t)
	checkRecordsLeftAccept(t, s, "orrery-5e6f7a8/records-conversation", recordsLeftAcceptSession,
		store.StageMaker{
			Kind: store.StageMakerConversation, Reason: "Synthetic reason.", Delegation: "Synthetic delegation.",
		})
}
