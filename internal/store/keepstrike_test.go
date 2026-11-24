package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// errKeepStrikeRefused is the refusal the keep and strike tests give a call they must refuse.
var errKeepStrikeRefused = errors.New("refused by the test")

// keepStrikeStoreContract is the proposed contract the strike test writes rows to and strikes an addition of.
const keepStrikeStoreContract = "orrery-6a7b8c9/contract-strike"

// writeKeepStrikeRow stores one write of action to row of keepStrikeStoreContract, which depends on depends, from
// rowWriteSession at at. Its edit leaves the body as it is.
func writeKeepStrikeRow(t *testing.T, s *store.Store, row, action, depends string, at int64) {
	t.Helper()
	if _, _, err := s.WriteContractRow(t.Context(), store.ContractRowChange{
		Contract: keepStrikeStoreContract, Row: row, Action: action, Depends: depends, Session: rowWriteSession, At: at,
	}, func(current store.ContractBody) (store.ContractBody, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("WriteContractRow %s %s: %v", action, row, err)
	}
}

// TestRecordsKeepStrikeStoreKeepWritesTheNewestVersion checks that a keep stores its write at the newest version of a
// proposed contract with no working copy, that a refusing check stores nothing, and that a contract no one holds is not
// found.
func TestRecordsKeepStrikeStoreKeepWritesTheNewestVersion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design, id = "orrery-6a7b8c9/design-keep", "orrery-6a7b8c9/contract-keep"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, id, designProposal("/work/synthetic-repo", "Keep", contractProposedAt))
	change := store.ContractRowChange{
		Contract: id, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first",
		Maker: store.StageMaker{Kind: store.StageMakerOwnerServe}, At: rowWriteStart,
	}
	accept := func(store.ContractBody) error { return nil }

	_, _, err := s.KeepContractAddition(ctx, change, func(store.ContractBody) error { return errKeepStrikeRefused })
	if !errors.Is(err, errKeepStrikeRefused) {
		t.Fatalf("refused keep = %v, want the check's refusal", err)
	}
	if writes, err := s.ContractRowWrites(ctx, keepStrikeStoreContract); err != nil || len(writes) != 0 {
		t.Fatalf("row writes after a refused keep = %+v, %v, want none", writes, err)
	}

	stored, kept, err := s.KeepContractAddition(ctx, change, accept)
	if err != nil {
		t.Fatalf("KeepContractAddition: %v", err)
	}
	wantWrite := store.ContractRowWrite{
		Contract: id, Seq: 1, Version: 1, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first",
		Maker: store.StageMakerOwnerServe, At: rowWriteStart,
	}
	if stored != wantWrite || kept != design {
		t.Fatalf("keep = %+v in %q, want %+v in %q", stored, kept, wantWrite, design)
	}
	if _, err = s.ContractWorkingCopy(ctx, id); !errors.Is(err, store.ErrNoWorking) {
		t.Fatalf("ContractWorkingCopy after a keep = %v, want ErrNoWorking", err)
	}

	_, _, err = s.KeepContractAddition(ctx, store.ContractRowChange{Contract: "orrery-6a7b8c9/contract-missing"},
		accept)
	if !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("keep of an unknown contract = %v, want ErrNoContract", err)
	}
}

// TestRecordsKeepStrikeStoreStrikeRemovesNewestDependents checks that a strike passes the edit the rows whose newest
// row write is a write naming the addition in depends, and stores the strike and one remove write for each of them. A
// row whose newest write names nothing, or is a removal, is not among them. A strike whose edit refuses stores nothing.
func TestRecordsKeepStrikeStoreStrikeRemovesNewestDependents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design = "orrery-6a7b8c9/design-strike"
	newDesign(t, s, design)
	proposeInDesign(
		t,
		s,
		design,
		keepStrikeStoreContract,
		designProposal("/work/synthetic-repo", "Strike", contractProposedAt),
	)
	at := rowWriteStart
	writeKeepStrikeRow(t, s, "R1", store.RowActionWrite, "A1", at)
	writeKeepStrikeRow(t, s, "R2", store.RowActionWrite, "A1", at+1)
	writeKeepStrikeRow(t, s, "R2", store.RowActionWrite, "", at+2)
	writeKeepStrikeRow(t, s, "R3", store.RowActionWrite, "A1", at+3)
	writeKeepStrikeRow(t, s, "R3", store.RowActionRemove, "", at+4)
	writeKeepStrikeRow(t, s, "R4", store.RowActionWrite, "A2", at+5)

	var seen [][]string
	strike := func(current store.ContractBody, _ string, dependents []string) (store.ContractBody, error) {
		seen = append(seen, dependents)
		return current, nil
	}
	result, struck, err := s.StrikeContractAddition(ctx, store.ContractRowChange{
		Contract: keepStrikeStoreContract, Row: "A1", Action: store.RowActionStrike, Words: "Strike the first",
		Maker:   store.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Strike it."},
		Session: rowWriteSession, At: at + 10,
	}, strike)
	if err != nil {
		t.Fatalf("StrikeContractAddition: %v", err)
	}
	if !reflect.DeepEqual(seen, [][]string{{"R1"}}) || struck != design {
		t.Fatalf("edit was given dependents %q in %q, want [R1] in %q", seen, struck, design)
	}
	wantStrike := store.ContractRowWrite{
		Contract: keepStrikeStoreContract,
		Seq:      7,
		Version:  1,
		Row:      "A1",
		Action:   store.RowActionStrike,
		Words:    "Strike the first",
		Maker:    store.StageMakerOwnerConversation,
		Quote:    "Strike it.",
		Session:  rowWriteSession,
		At:       at + 10,
	}
	if result.Strike != wantStrike {
		t.Fatalf("strike write = %+v, want %+v", result.Strike, wantStrike)
	}
	if len(result.Removed) != 1 || result.Removed[0].Row != "R1" || result.Removed[0].Action != store.RowActionRemove ||
		result.Removed[0].Seq != 8 || result.Removed[0].Version != 1 {
		t.Fatalf("removed writes = %+v, want one remove of R1 after the strike", result.Removed)
	}

	writes, err := s.ContractRowWrites(ctx, keepStrikeStoreContract)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	if len(writes) != 8 || writes[6] != result.Strike || writes[7] != result.Removed[0] {
		t.Fatalf("row writes end with %+v, want the strike and the remove of R1", writes)
	}

	_, _, err = s.StrikeContractAddition(ctx, store.ContractRowChange{
		Contract: keepStrikeStoreContract, Row: "A2", Action: store.RowActionStrike, Session: rowWriteSession,
		At: at + 20,
	}, func(store.ContractBody, string, []string) (store.ContractBody, error) {
		return store.ContractBody{}, errKeepStrikeRefused
	})
	if !errors.Is(err, errKeepStrikeRefused) {
		t.Fatalf("refused strike = %v, want the edit's refusal", err)
	}
	if after, err := s.ContractRowWrites(ctx, keepStrikeStoreContract); err != nil || len(after) != 8 {
		t.Fatalf("row writes after a refused strike = %d, %v, want the 8 stored before", len(after), err)
	}
}
