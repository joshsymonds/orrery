package server_test

import (
	"reflect"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractIDK is the proposed contract the keep and strike tests keep and strike the additions of.
const contractIDK = "synthetic-repo/keep-strike"

// keepStrikeBody returns the body contractIDK is proposed with: the body of contractTestBody with the additions A1 and
// A2 added.
func keepStrikeBody() *enginev1.EpicContract {
	body := contractTestBody(contractIDK, "Synthetic keep", "serious")
	body.Additions = []*enginev1.EpicAddition{
		{Location: "internal/stage", What: "first", Why: "Needed", Cost: "None"},
		{Location: "internal/store", What: "second", Why: "Needed", Cost: "None"},
	}
	return body
}

// TestRecordsKeepStrikeServeKeepsAndStrikesAsOwnerServe checks that Contracts.Keep and Contracts.Strike through serve
// store their writes with the maker owner_serve and no session, and that the striking removes the requirement that
// depends on the addition it strikes.
func TestRecordsKeepStrikeServeKeepsAndStrikesAsOwnerServe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openContractStore(t)
	proposal := contractTestVersion(t, keepStrikeBody(), 1, contractWriterA, "Synthetic proposal.", contractTimeA, nil)
	if err := st.ProposeContract(ctx, contractIDK, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	// R1, which the contract's body already holds, is written to depend on A2, so striking A2 removes it.
	if _, _, err := st.WriteContractRow(ctx, store.ContractRowChange{
		Contract: contractIDK, Row: "R1", Action: store.RowActionWrite, Depends: "A2", Session: contractWriterA,
		At: contractTimeA,
	}, func(current store.ContractBody) (store.ContractBody, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("WriteContractRow R1: %v", err)
	}
	client := contractsClient(t, designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st)))

	// The owner's calls name no session, so serve forwards none and orreryd records the owner.
	kept, err := client.Keep(ctx, connect.NewRequest(&orreryv1.ContractsKeepRequest{
		Id: contractIDK, Addition: "A1", Words: "Keep the first.",
	}))
	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if got := kept.Msg.GetContract().GetState(); got != "proposed" {
		t.Fatalf("Keep answered state %q, want the contract as proposed", got)
	}
	struck, err := client.Strike(ctx, connect.NewRequest(&orreryv1.ContractsStrikeRequest{
		Id: contractIDK, Addition: "A2", Words: "Strike the second.",
	}))
	if err != nil {
		t.Fatalf("Strike: %v", err)
	}
	if got := struck.Msg.GetRemoved(); !slices.Equal(got, []string{"R1"}) {
		t.Fatalf("Strike removed %q, want the requirement that depends on A2", got)
	}

	at := designAnswerTime
	want := []store.ContractRowWrite{
		{
			Contract: contractIDK, Seq: 1, Version: 1, Row: "R1", Action: store.RowActionWrite, Depends: "A2",
			Session: contractWriterA, At: contractTimeA,
		},
		{
			Contract: contractIDK, Seq: 2, Version: 1, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first.",
			Maker: store.StageMakerOwnerServe, At: at,
		},
		{
			Contract: contractIDK, Seq: 3, Version: 1, Row: "A2", Action: store.RowActionStrike,
			Words: "Strike the second.", Maker: store.StageMakerOwnerServe, At: at,
		},
		{
			Contract: contractIDK, Seq: 4, Version: 1, Row: "R1", Action: store.RowActionRemove, Depends: "A2",
			Words: "Strike the second.", Maker: store.StageMakerOwnerServe, At: at,
		},
	}
	stored, err := st.ContractRowWrites(ctx, contractIDK)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("row writes = %+v, want %+v", stored, want)
	}
	working, err := st.ContractWorkingCopy(ctx, contractIDK)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	var body enginev1.EpicContract
	if err := protojson.Unmarshal([]byte(working.Body), &body); err != nil {
		t.Fatalf("decode working body: %v", err)
	}
	if len(body.GetRequirements()) != 0 || len(body.GetAdditions()) != 1 ||
		body.GetAdditions()[0].GetWhat() != "first" {
		t.Fatalf("working body = %v, want the first addition alone and no requirement", &body)
	}
}

// TestRecordsKeepStrikeServePassesTheRefusalThrough checks that a keep of a contract that is not proposed is refused
// through serve as a failed precondition that names the state, and stores nothing.
func TestRecordsKeepStrikeServePassesTheRefusalThrough(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	client := contractsClient(t, designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st)))

	_, err := client.Keep(ctx, connect.NewRequest(&orreryv1.ContractsKeepRequest{
		Id: contractIDB, Addition: "A1", Words: "Keep the first.",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("Keep of an accepted contract code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
	writes, err := st.ContractRowWrites(ctx, contractIDB)
	if err != nil || len(writes) != 0 {
		t.Fatalf("row writes of the accepted contract = %+v, %v, want none", writes, err)
	}
}
