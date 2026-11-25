package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// refusedContractID is the proposed contract the refusal tests keep refusals of.
const refusedContractID = "synthetic-repo/refused"

// refusedChecksText is the text of a refusal of a working copy whose Quality Bar line is not verbatim.
const refusedChecksText = "checks: the Quality Bar line is not verbatim"

// TestRecordsRowsRefusalsServedOnGetOldestFirst checks that Get answers each refusal stored for a contract, oldest first,
// with its seq, version, field, text, session and time: a refusal of the checks of a working copy among them, and one
// with no session.
func TestRecordsRowsRefusalsServedOnGetOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openContractStore(t)
	proposal := contractTestVersion(
		t, contractTestBody(refusedContractID, "Synthetic refused", "serious"), 1,
		contractWriterA, "Synthetic proposal.", contractTimeA, nil,
	)
	if err := st.ProposeContract(ctx, refusedContractID, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	for _, refusal := range []store.ContractRefusal{
		{Version: 2, Field: "checks", Text: refusedChecksText, Session: contractWriterA, At: contractTimeB},
		{Version: 2, Field: "intent", Text: "intent: empty", At: contractTimeBRev},
	} {
		if err := st.RefuseContract(ctx, refusedContractID, refusal); err != nil {
			t.Fatalf("RefuseContract %s: %v", refusal.Field, err)
		}
	}
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	got, err := client.Get(ctx, connect.NewRequest(&orreryv1.ContractsGetRequest{Id: refusedContractID}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []*orreryv1.ContractRefusal{
		{Seq: 1, Version: 2, Field: "checks", Text: refusedChecksText, Session: contractWriterA, Time: contractTimeB},
		{Seq: 2, Version: 2, Field: "intent", Text: "intent: empty", Time: contractTimeBRev},
	}
	refusals := got.Msg.GetRefusals()
	if len(refusals) != len(want) {
		t.Fatalf("Get refusals = %v, want %v", refusals, want)
	}
	for i := range want {
		if !proto.Equal(refusals[i], want[i]) {
			t.Errorf("refusal %d = %v, want %v", i, refusals[i], want[i])
		}
	}
}
