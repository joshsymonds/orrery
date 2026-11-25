package server_test

import (
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The contract the provenance tests read and write, and the step that reads its version 1.
const (
	contractIDP    = "synthetic-repo/provenance"
	provenanceStep = "step-provenance"
)

// provenanceBody returns the protobuf JSON of contractIDP's body with its one failure rated rating.
func provenanceBody(t *testing.T, rating string) string {
	t.Helper()
	body := contractTestBody(contractIDP, "Synthetic provenance", "serious")
	body.Failures[0].Rating = rating
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return string(encoded)
}

// seedProvenance proposes contractIDP at version 1, and has provenanceStep read version 1. Then the contract's F1 row
// is written twice: first rated serious by the writing session on its own account, then rated minor by the owner in
// the conversation. Last, contractIDP is revised to version 2, which no step read.
func seedProvenance(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	proposal := contractTestVersion(t, contractTestBody(contractIDP, "Synthetic provenance", "serious"), 1,
		contractWriterA, "Synthetic proposal.", contractTimeA, nil)
	if err := st.ProposeContract(ctx, contractIDP, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	add := store.StepAdd{ID: provenanceStep, Agent: "steelman-agent", Prompt: "check the packet"}
	if err := st.ApplyEngine(ctx, []store.EngineOp{add}, time.Unix(contractTimeA, 0)); err != nil {
		t.Fatalf("ApplyEngine add step: %v", err)
	}
	if err := st.RecordStepReads(ctx, []store.StepRead{
		{Step: provenanceStep, Contract: contractIDP, Version: 1},
	}); err != nil {
		t.Fatalf("RecordStepReads: %v", err)
	}
	writeProvenanceRow(t, st, store.ContractRowChange{
		Contract: contractIDP, Row: "F1", Action: store.RowActionWrite, Words: "Rated serious",
		Session: contractWriterA, At: contractTimeB,
	}, provenanceBody(t, "serious"))
	writeProvenanceRow(t, st, store.ContractRowChange{
		Contract: contractIDP, Row: "F1", Action: store.RowActionWrite, Words: "Rated minor",
		Maker:   store.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Minor is fine."},
		Session: contractWriterA, At: contractTimeBRev,
	}, provenanceBody(t, "minor"))
	revision := contractTestVersion(t, contractTestBody(contractIDP, "Synthetic provenance", "serious"), 2,
		contractWriterA, "Synthetic revision.", contractTimeBRev, nil)
	if err := st.ReviseContract(ctx, contractIDP, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
}

// writeProvenanceRow stores change, which writes the row of contractIDP, and leaves its body as body.
func writeProvenanceRow(t *testing.T, st *store.Store, change store.ContractRowChange, body string) {
	t.Helper()
	if _, _, err := st.WriteContractRow(t.Context(), change,
		func(current store.ContractBody) (store.ContractBody, error) {
			current.Body = body
			return current, nil
		},
	); err != nil {
		t.Fatalf("WriteContractRow %s: %v", change.Row, err)
	}
}

// TestRecordsProvenanceGetServesRatingWithMaker checks that Get gives every row write of the contract oldest first,
// each with the maker and the words the write stored. The newest write of a failure's row is its rating, so the rating
// served is the owner's, with his quote.
func TestRecordsProvenanceGetServesRatingWithMaker(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedProvenance(t, st)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	reply, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDP}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []*orreryv1.ContractRowWrite{
		{
			Seq: 1, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated serious",
			Maker: &orreryv1.RecordMaker{Session: contractWriterA}, Time: contractTimeB,
		},
		{
			Seq: 2, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated minor",
			Maker: &orreryv1.RecordMaker{
				Kind: store.StageMakerOwnerConversation, Quote: "Minor is fine.", Session: contractWriterA,
			},
			Time: contractTimeBRev,
		},
	}
	got := reply.Msg.GetRows()
	if !proto.Equal(&orreryv1.ContractsGetResponse{Rows: got}, &orreryv1.ContractsGetResponse{Rows: want}) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// TestRecordsProvenanceGetGivesReadByOfEachVersion checks that Get names, on each version, the steps that read it. The
// step read version 1 is named there, and version 2, which no step read, names none.
func TestRecordsProvenanceGetGivesReadByOfEachVersion(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedProvenance(t, st)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	reply, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDP}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	versions := reply.Msg.GetVersions()
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want versions 1 and 2", len(versions))
	}
	if got := versions[0].GetReadBy(); !reflect.DeepEqual(got, []string{provenanceStep}) {
		t.Fatalf("read_by of version 1 = %q, want %q", got, []string{provenanceStep})
	}
	if got := versions[1].GetReadBy(); len(got) != 0 {
		t.Fatalf("read_by of version 2 = %q, want none", got)
	}
}

// TestRecordsProvenanceEngineGetContractGivesReadByAndRows checks that orreryd's GetContract gives the same read_by and
// row writes Get serves, each row write with the maker the write stored.
func TestRecordsProvenanceEngineGetContractGivesReadByAndRows(t *testing.T) {
	t.Parallel()
	st, _ := openContractStore(t)
	seedProvenance(t, st)
	engine := serveContractEngine(t, st)

	reply, err := engine.GetContract(t.Context(), connect.NewRequest(&enginev1.GetContractRequest{Id: contractIDP}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	stored := reply.Msg.GetContract()
	versions := stored.GetVersions()
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want versions 1 and 2", len(versions))
	}
	if got := versions[0].GetReadBy(); !reflect.DeepEqual(got, []string{provenanceStep}) {
		t.Fatalf("read_by of version 1 = %q, want %q", got, []string{provenanceStep})
	}
	if got := versions[1].GetReadBy(); len(got) != 0 {
		t.Fatalf("read_by of version 2 = %q, want none", got)
	}
	want := []*enginev1.StageRowWrite{
		{
			Seq: 1, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated serious",
			Maker: &enginev1.StageMaker{}, Session: contractWriterA, At: contractTimeB,
		},
		{
			Seq: 2, Version: 1, Row: "F1", Action: store.RowActionWrite, Words: "Rated minor",
			Maker: &enginev1.StageMaker{
				Kind: store.StageMakerOwnerConversation, Quote: "Minor is fine.",
			},
			Session: contractWriterA, At: contractTimeBRev,
		},
	}
	got := stored.GetRows()
	if !proto.Equal(&enginev1.StoredContract{Rows: got}, &enginev1.StoredContract{Rows: want}) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}
