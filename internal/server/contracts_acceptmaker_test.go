package server_test

import (
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestContractRecordsLeftAcceptThroughServeReadsBackAsOwnerServe checks that an accept through serve, which carries no
// session, is stored with maker owner_serve, and that a read through serve carries that maker on the accepted state.
func TestContractRecordsLeftAcceptThroughServeReadsBackAsOwnerServe(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	proposal := contractTestVersion(t, contractTestBody(contractIDD, "Synthetic delta", "limited"), 1,
		contractWriterD, "Synthetic proposal.", contractTimeD, nil)
	if err := st.ProposeContract(t.Context(), contractIDD, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), serveContractEngine(t, st)))

	accept := connect.NewRequest(&orreryv1.ContractsAcceptRequest{Id: contractIDD, Words: "Synthetic accept."})
	if _, err := client.Accept(t.Context(), accept); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	got, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDD}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertContractStates(t, got.Msg.GetStates(), []*orreryv1.ContractStateChange{
		{State: "proposed", Version: 1, By: "session", Session: contractWriterD, Words: "Synthetic proposal.",
			Time: contractTimeD},
		{State: "accepted", Version: 1, By: "owner", Words: "Synthetic accept.", Time: contractTimeEngine,
			Maker: &orreryv1.RecordMaker{Kind: store.StageMakerOwnerServe}},
	})
}

// TestContractRecordsLeftAcceptStoredConversationMakerReadsBackWithItsSession checks that a read through serve carries
// the maker an accept stored as a RecordMaker: the maker's kind, reason and delegation, and the session that made the
// accept.
func TestContractRecordsLeftAcceptStoredConversationMakerReadsBackWithItsSession(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	proposal := contractTestVersion(t, contractTestBody(contractIDD, "Synthetic delta", "limited"), 1,
		contractWriterD, "Synthetic proposal.", contractTimeD, nil)
	if err := st.ProposeContract(t.Context(), contractIDD, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := st.AcceptContract(t.Context(), contractIDD, store.ContractChange{
		Session: contractSessionID, Words: "Synthetic accept.", At: contractTimeEngine,
		Maker: store.StageMaker{
			Kind: store.StageMakerConversation, Reason: "Synthetic reason.", Delegation: "Synthetic delegation.",
		},
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	got, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDD}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertContractStates(t, got.Msg.GetStates(), []*orreryv1.ContractStateChange{
		{State: "proposed", Version: 1, By: "session", Session: contractWriterD, Words: "Synthetic proposal.",
			Time: contractTimeD},
		{State: "accepted", Version: 1, By: "session", Session: contractSessionID, Words: "Synthetic accept.",
			Time: contractTimeEngine, Maker: &orreryv1.RecordMaker{
				Kind: store.StageMakerConversation, Reason: "Synthetic reason.", Delegation: "Synthetic delegation.",
				Session: contractSessionID,
			}},
	})
}
