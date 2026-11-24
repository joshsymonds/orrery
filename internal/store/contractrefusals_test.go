package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestRecordsRowsRefusalsKeptInOrder checks that each refusal stored for a contract is numbered as the contract's next
// refusal, that a refusal may name an id no contract holds yet, and that a contract's refusals read back oldest first,
// through the store and through a Reader, each with the version, field, text, session and time it was stored with. A
// contract with none has none.
func TestRecordsRowsRefusalsKeptInOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const design, id, unproposed = "orrery-6a7b8c9/design-refused", "orrery-6a7b8c9/contract-refused",
		"orrery-6a7b8c9/contract-unproposed"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, id, designProposal("/work/synthetic-repo", "Refused contract", contractProposedAt))

	const checks = "checks: the Quality Bar line is not verbatim"
	refusals := []store.ContractRefusal{
		{Version: 2, Field: "checks", Text: checks, Session: rowWriteSession, At: rowWriteStart},
		{Version: 2, Field: "intent", Text: "intent: empty", At: rowWriteStart + 10},
	}
	for _, refusal := range refusals {
		if err := s.RefuseContract(ctx, id, refusal); err != nil {
			t.Fatalf("RefuseContract %s: %v", refusal.Field, err)
		}
	}
	if err := s.RefuseContract(ctx, unproposed, store.ContractRefusal{
		Version: 1, Field: "id", Text: "id: differs from the contract's id", Session: rowWriteSession,
		At: rowWriteStart + 20,
	}); err != nil {
		t.Fatalf("RefuseContract of an id no contract holds: %v", err)
	}

	want := []store.ContractRefusal{
		{Contract: id, Seq: 1, Version: 2, Field: "checks", Text: checks, Session: rowWriteSession, At: rowWriteStart},
		{Contract: id, Seq: 2, Version: 2, Field: "intent", Text: "intent: empty", At: rowWriteStart + 10},
	}
	got, err := s.ContractRefusals(ctx, id)
	if err != nil {
		t.Fatalf("ContractRefusals: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refusals = %+v, want %+v", got, want)
	}
	read, err := store.NewReader(path).ContractRefusals(ctx, id)
	if err != nil {
		t.Fatalf("Reader.ContractRefusals: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("refusals through the Reader = %+v, want %+v", read, want)
	}

	unproposedRefusals, err := s.ContractRefusals(ctx, unproposed)
	if err != nil {
		t.Fatalf("ContractRefusals of an id no contract holds: %v", err)
	}
	wantUnproposed := []store.ContractRefusal{{
		Contract: unproposed, Seq: 1, Version: 1, Field: "id", Text: "id: differs from the contract's id",
		Session: rowWriteSession, At: rowWriteStart + 20,
	}}
	if !reflect.DeepEqual(unproposedRefusals, wantUnproposed) {
		t.Fatalf("refusals of an id no contract holds = %+v, want %+v", unproposedRefusals, wantUnproposed)
	}
	none, err := s.ContractRefusals(ctx, "orrery-6a7b8c9/contract-none")
	if err != nil || len(none) != 0 {
		t.Fatalf("refusals of a contract with none = %+v, %v, want none", none, err)
	}
}

// TestRecordsRowsWorkingReviseDeletesWorkingCopy checks that rows written one at a time into a proposed contract's
// working copy are made into version 2 by a revise, which deletes the working copy in the same write, and that a row
// written after the revise starts a new working copy over version 2, as a copy of its body and decisions.
func TestRecordsRowsWorkingReviseDeletesWorkingCopy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design, id = "orrery-6a7b8c9/design-revised", "orrery-6a7b8c9/contract-revised"
	newDesign(t, s, design)
	proposal := designProposal("/work/synthetic-repo", "Revised contract", contractProposedAt)
	proposeInDesign(t, s, design, id, proposal)
	var seen []store.ContractBody

	for offset, row := range []string{"R1", "F2"} {
		if _, _, err := s.WriteContractRow(ctx, store.ContractRowChange{
			Contract: id, Row: row, Action: store.RowActionWrite, Session: rowWriteSession,
			At: rowWriteStart + int64(offset)*10,
		}, appendRowEdit(row, &seen)); err != nil {
			t.Fatalf("WriteContractRow %s: %v", row, err)
		}
	}
	revision := proposal
	revision.Version = 2
	revision.Body = proposal.Body + "R1;F2;"
	revision.Words = "Revise the working copy"
	revision.ChangedRows = []string{"R1", "F2"}
	revision.At = rowWriteStart + 20
	if err := s.ReviseContract(ctx, id, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	if _, err := s.ContractWorkingCopy(ctx, id); !errors.Is(err, store.ErrNoWorking) {
		t.Fatalf("ContractWorkingCopy after the revise = %v, want ErrNoWorking", err)
	}

	if _, _, err := s.WriteContractRow(ctx, store.ContractRowChange{
		Contract: id, Row: "R3", Action: store.RowActionWrite, Session: rowWriteSession, At: rowWriteStart + 30,
	}, appendRowEdit("R3", &seen)); err != nil {
		t.Fatalf("WriteContractRow R3: %v", err)
	}
	if len(seen) != 3 || seen[2].Body != revision.Body {
		t.Fatalf("the edit after the revise was given %q, want version 2's body", seen[len(seen)-1].Body)
	}
	working, err := s.ContractWorkingCopy(ctx, id)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	wantWorking := store.ContractWorkingCopy{
		ContractWorking: store.ContractWorking{
			Contract: id, Version: 2, Body: revision.Body + "R3;", Decisions: revision.Decisions,
			Writer: rowWriteSession, Created: rowWriteStart + 30, Updated: rowWriteStart + 30,
		},
		ChangedRows: []string{"R3"},
	}
	if !reflect.DeepEqual(working, wantWorking) {
		t.Fatalf("working copy = %+v, want %+v", working, wantWorking)
	}
	record, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(record.Versions) != 2 || record.Version != 2 || record.Versions[1].Body != revision.Body {
		t.Fatalf("contract = %+v, want versions 1 and 2 with version 2 as revised", record)
	}
	writes, err := s.ContractRowWrites(ctx, id)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	versions := make([]int64, 0, len(writes))
	for _, write := range writes {
		versions = append(versions, write.Version)
	}
	if want := []int64{1, 1, 2}; !reflect.DeepEqual(versions, want) {
		t.Fatalf("row writes are over versions %v, want %v", versions, want)
	}
}
