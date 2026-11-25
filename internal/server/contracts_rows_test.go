package server_test

import (
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractIDW is the proposed contract the working copy test writes rows to. Its version keeps the title it was proposed
// with, and the rows set the working copy's body to a title of its own.
const (
	contractIDW          = "synthetic-repo/working"
	contractTitleVersion = "Synthetic working"
	contractTitleRows    = "Synthetic working revised"
)

// rowWriteBody returns the protobuf JSON of the contract body contractIDW holds with title, as a row write leaves it.
func rowWriteBody(t *testing.T, title string) string {
	t.Helper()
	encoded, err := protojson.Marshal(contractTestBody(contractIDW, title, "serious"))
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return string(encoded)
}

// writeWorkingRow stores one write to row of contractIDW in st, from contractWriterA at at, which leaves the body as body.
func writeWorkingRow(t *testing.T, st *store.Store, row, body string, at int64) {
	t.Helper()
	if _, _, err := st.WriteContractRow(t.Context(), store.ContractRowChange{
		Contract: contractIDW, Row: row, Action: store.RowActionWrite, Session: contractWriterA, At: at,
	}, func(current store.ContractBody) (store.ContractBody, error) {
		current.Body = body
		return current, nil
	}); err != nil {
		t.Fatalf("WriteContractRow %s: %v", row, err)
	}
}

func TestRecordsRowsWorkingGetGivesChangedRowsOfWorkingCopy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openContractStore(t)
	proposal := contractTestVersion(
		t, contractTestBody(contractIDW, contractTitleVersion, "serious"), 1,
		contractWriterA, "Synthetic proposal.", contractTimeA, nil,
	)
	if err := st.ProposeContract(ctx, contractIDW, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	before, err := client.Get(ctx, connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDW}))
	if err != nil {
		t.Fatalf("Get before any write: %v", err)
	}
	if before.Msg.GetWorking() != nil {
		t.Fatalf("working copy before any write = %v, want none", before.Msg.GetWorking())
	}

	rows := rowWriteBody(t, contractTitleRows)
	writeWorkingRow(t, st, "F1", rows, contractTimeBRev)
	writeWorkingRow(t, st, "R1", rows, contractTimeBRev+10)
	writeWorkingRow(t, st, "F1", rows, contractTimeBRev+20)

	after, err := client.Get(ctx, connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDW}))
	if err != nil {
		t.Fatalf("Get after the writes: %v", err)
	}
	working := after.Msg.GetWorking()
	if working.GetVersion() != 1 || working.GetWriter() != contractWriterA ||
		working.GetUpdated() != contractTimeBRev+20 || working.GetContract().GetTitle() != contractTitleRows {
		t.Fatalf("working copy = %v, want version 1 written by %s last at %d", working, contractWriterA,
			contractTimeBRev+20)
	}
	if want := []string{"F1", "R1"}; !reflect.DeepEqual(working.GetChangedRows(), want) {
		t.Fatalf(
			"changed rows = %q, want %q: each row once, in the order first written",
			working.GetChangedRows(),
			want,
		)
	}
	versions := after.Msg.GetVersions()
	if len(versions) != 1 || versions[0].GetContract().GetTitle() != contractTitleVersion {
		t.Fatalf("versions = %v, want version 1 with its proposed title", versions)
	}
}
