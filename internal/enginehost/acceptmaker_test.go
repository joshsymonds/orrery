package enginehost

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// The owner's words, and the reason and delegation, the records-left-accept tests give their makers.
const (
	recordsLeftAcceptQuote      = "Synthetic owner words."
	recordsLeftAcceptReason     = "Synthetic reason."
	recordsLeftAcceptDelegation = "Synthetic delegation."
)

// recordsLeftAcceptServer starts a Contracts server over a store holding the Contracts tests' sessions. Its clock reads
// contractTestAt, so every state the tests store is at contractHour(0).
func recordsLeftAcceptServer(t *testing.T) contractTestServer {
	t.Helper()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	return startContractServer(t, st, contractTestContracts(st))
}

// proposeUndecided proposes the contract of the Contracts tests from the headless session with no decisions, so that an
// accept of it has no open decision to refuse it.
func proposeUndecided(t *testing.T, server contractTestServer) {
	t.Helper()
	if _, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo), nil); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}
}

// acceptWithMaker sends an accept of epicServiceID with contractTestAcceptWords and maker from the caller headers set.
func acceptWithMaker(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	maker *v1.StageMaker,
) error {
	call := connect.NewRequest(&v1.AcceptContractRequest{
		Id: epicServiceID, Words: contractTestAcceptWords, Maker: maker,
	})
	headers(call.Header())
	_, err := client.AcceptContract(ctx, call)
	return err
}

// checkRecordsLeftAcceptStates checks that the contract epicServiceID reads back with the proposal's state, made by
// the headless session, followed by want, the accept's state.
func checkRecordsLeftAcceptStates(t *testing.T, server contractTestServer, want *v1.ContractStateChange) {
	t.Helper()
	got, err := server.contracts.GetContract(t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	wantStates := []*v1.ContractStateChange{
		wantStateChange("proposed", 1, "session", contractTestHeadless, contractTestWords, 0),
		want,
	}
	gotStates := got.Msg.GetContract().GetStates()
	if !proto.Equal(&v1.StoredContract{States: gotStates}, &v1.StoredContract{States: wantStates}) {
		t.Errorf("states = %v, want %v", gotStates, wantStates)
	}
}

// TestContractRecordsLeftAcceptFromNoSessionIsOwnerServe checks that an accept whose caller resolves to no session, with
// no maker named, is recorded as made by owner_serve, and that a read carries that maker on the accepted
// state.
func TestContractRecordsLeftAcceptFromNoSessionIsOwnerServe(t *testing.T) {
	t.Parallel()
	server := recordsLeftAcceptServer(t)
	proposeUndecided(t, server)
	if err := acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	checkRecordsLeftAcceptStates(t, server, &v1.ContractStateChange{
		State: "accepted", Version: 1, By: "owner", Words: contractTestAcceptWords, Time: contractHour(0),
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	})
}

// TestContractRecordsLeftAcceptOwnerConversationReadsBackTheQuote checks that an accept by a session naming the maker
// owner_conversation with the owner's quote is stored with that quote, and reads back as the owner's.
func TestContractRecordsLeftAcceptOwnerConversationReadsBackTheQuote(t *testing.T) {
	t.Parallel()
	server := recordsLeftAcceptServer(t)
	proposeUndecided(t, server)
	maker := &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: recordsLeftAcceptQuote}
	if err := acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	checkRecordsLeftAcceptStates(t, server, &v1.ContractStateChange{
		State: "accepted", Version: 1, By: "session", Session: contractTestHeadless, Words: contractTestAcceptWords,
		Time:  contractHour(0),
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: recordsLeftAcceptQuote},
	})
}

// TestContractRecordsLeftAcceptConversationReadsBackAsMadeForHim checks that an accept by a pane naming the maker
// conversation, with its reason and delegation, is stored with them, and reads back as made for the owner.
func TestContractRecordsLeftAcceptConversationReadsBackAsMadeForHim(t *testing.T) {
	t.Parallel()
	server := recordsLeftAcceptServer(t)
	proposeUndecided(t, server)
	maker := &v1.StageMaker{
		Kind: store.StageMakerConversation, Reason: recordsLeftAcceptReason, Delegation: recordsLeftAcceptDelegation,
	}
	if err := acceptWithMaker(t.Context(), server.contracts, paneCaller, maker); err != nil {
		t.Fatalf("AcceptContract from the pane: %v", err)
	}
	checkRecordsLeftAcceptStates(t, server, &v1.ContractStateChange{
		State: "accepted", Version: 1, By: "session", Session: contractTestPane, Words: contractTestAcceptWords,
		Time: contractHour(0),
		Maker: &v1.StageMaker{
			Kind: store.StageMakerConversation, Reason: recordsLeftAcceptReason, Delegation: recordsLeftAcceptDelegation,
		},
	})
}

// TestContractRecordsLeftAcceptRefusesAMakerAndStoresNothing checks that each accept whose maker the check refuses gets
// the refusal the check's reason names, and that the store holds the same contracts after it as before.
func TestContractRecordsLeftAcceptRefusesAMakerAndStoresNothing(t *testing.T) {
	t.Parallel()
	checkContractStateRefusals(t, []contractStateRefusal{
		{
			name: "owner_serve from a session",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: store.StageMakerOwnerServe}
				return acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker)
			},
			code:    connect.CodePermissionDenied,
			message: "maker: owner_serve is serve's call, which carries no session",
		},
		{
			name: "owner_conversation from no session",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: recordsLeftAcceptQuote}
				return acceptWithMaker(t.Context(), server.contracts, ownerCaller, maker)
			},
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name: "owner_conversation without a quote",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: store.StageMakerOwnerConversation}
				return acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker)
			},
			code:    connect.CodeInvalidArgument,
			message: "quote: empty",
		},
		{
			name: "conversation from no session",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{
					Kind:       store.StageMakerConversation,
					Reason:     recordsLeftAcceptReason,
					Delegation: recordsLeftAcceptDelegation,
				}
				return acceptWithMaker(t.Context(), server.contracts, ownerCaller, maker)
			},
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name: "conversation without a reason",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: store.StageMakerConversation, Delegation: recordsLeftAcceptDelegation}
				return acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker)
			},
			code:    connect.CodeInvalidArgument,
			message: "reason: empty",
		},
		{
			name: "conversation without a delegation",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: store.StageMakerConversation, Reason: recordsLeftAcceptReason}
				return acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker)
			},
			code:    connect.CodeInvalidArgument,
			message: "delegation: empty",
		},
		{
			name: "unknown kind",
			seed: proposeUndecided,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				maker := &v1.StageMaker{Kind: "owner"}
				return acceptWithMaker(t.Context(), server.contracts, headlessCaller, maker)
			},
			code:    connect.CodeInvalidArgument,
			message: `kind: "owner" is not owner_serve, owner_conversation or conversation`,
		},
	})
}
