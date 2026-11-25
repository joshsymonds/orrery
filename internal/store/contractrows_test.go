package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rowWriteStart is the second the row write tests make their first write at. The tests stamp each later write later.
const rowWriteStart int64 = 1700004000

// rowWriteSession is the sessions row id that makes the row writes of the tests.
const rowWriteSession = contractWriter

// appendRowEdit returns the edit that appends row to the text of the body it is given, and records each body it is given
// in seen. It names the repository and title a draft keeps.
func appendRowEdit(row string, seen *[]store.ContractBody) store.ContractEdit {
	return func(current store.ContractBody) (store.ContractBody, error) {
		*seen = append(*seen, current)
		current.Body += row + ";"
		current.Repository, current.Title = "/work/synthetic-repo", "Synthetic draft"
		return current, nil
	}
}

// TestRecordsRowsWorkingDraftRowsWrittenSingly checks that rows written one at a time into a new draft create it empty
// under its design, that a draft which exists keeps its design, and that each write is stored numbered as the contract's
// next row write, with its maker, words and time.
func TestRecordsRowsWorkingDraftRowsWrittenSingly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design, id = "orrery-6a7b8c9/design-drafts", "orrery-6a7b8c9/contract-draft"
	newDesign(t, s, design)
	var seen []store.ContractBody

	first, firstDesign, err := s.WriteContractRow(ctx, store.ContractRowChange{
		Contract: id, Design: design, Row: "F1", Action: store.RowActionWrite, Words: "First failure",
		Session: rowWriteSession, At: rowWriteStart,
	}, appendRowEdit("F1", &seen))
	if err != nil {
		t.Fatalf("WriteContractRow F1: %v", err)
	}
	second, secondDesign, err := s.WriteContractRow(ctx, store.ContractRowChange{
		Contract: id, Row: "R1", Action: store.RowActionWrite, Depends: "F1", Words: "Second row",
		Maker: store.StageMaker{
			Kind: store.StageMakerConversation, Reason: "The owner decided", Delegation: "Decide the rows",
		},
		Session: rowWriteSession, At: rowWriteStart + 10,
	}, appendRowEdit("R1", &seen))
	if err != nil {
		t.Fatalf("WriteContractRow R1: %v", err)
	}

	wantFirst := store.ContractRowWrite{
		Contract: id, Seq: 1, Version: 0, Row: "F1", Action: store.RowActionWrite, Words: "First failure",
		Session: rowWriteSession, At: rowWriteStart,
	}
	if first != wantFirst || firstDesign != design {
		t.Fatalf("first write = %+v in %q, want %+v in %q", first, firstDesign, wantFirst, design)
	}
	wantSecond := store.ContractRowWrite{
		Contract: id, Seq: 2, Version: 0, Row: "R1", Action: store.RowActionWrite, Depends: "F1",
		Words: "Second row", Maker: store.StageMakerConversation, MakerReason: "The owner decided",
		Delegation: "Decide the rows", Session: rowWriteSession, At: rowWriteStart + 10,
	}
	if second != wantSecond || secondDesign != design {
		t.Fatalf("second write = %+v in %q, want %+v in %q", second, secondDesign, wantSecond, design)
	}
	if got := bodiesOf(seen); !reflect.DeepEqual(got, []string{"", "F1;"}) {
		t.Fatalf("bodies the edit was given = %q, want an empty draft and then the first row", got)
	}

	draft, err := s.ContractDraft(ctx, id)
	if err != nil {
		t.Fatalf("ContractDraft: %v", err)
	}
	if draft.Design != design || draft.Body != "F1;R1;" || draft.Repository != "/work/synthetic-repo" ||
		draft.Title != "Synthetic draft" || draft.Words != "Second row" || draft.Writer != rowWriteSession ||
		draft.Created.Unix() != rowWriteStart || draft.Updated.Unix() != rowWriteStart+10 {
		t.Fatalf("draft = %+v, want the two rows written into the draft of %s", draft, design)
	}
	writes, err := s.ContractRowWrites(ctx, id)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	if !reflect.DeepEqual(writes, []store.ContractRowWrite{first, second}) {
		t.Fatalf("row writes = %+v, want the two writes as they were stored", writes)
	}
}

// bodiesOf returns the body of each of bodies, in order.
func bodiesOf(bodies []store.ContractBody) []string {
	out := make([]string, 0, len(bodies))
	for _, body := range bodies {
		out = append(out, body.Body)
	}
	return out
}

// TestRecordsRowsWorkingProposedRowsWriteWorkingCopyOverNewest checks that the first write to a proposed contract makes a
// working copy of its newest version, that later writes go into that copy, and that the version itself is untouched. The
// working copy's changed rows are the distinct rows its writes named.
func TestRecordsRowsWorkingProposedRowsWriteWorkingCopyOverNewest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design, id = "orrery-6a7b8c9/design-working", "orrery-6a7b8c9/contract-working"
	newDesign(t, s, design)
	proposal := designProposal("/work/synthetic-repo", "Working contract", contractProposedAt)
	proposeInDesign(t, s, design, id, proposal)
	var seen []store.ContractBody

	for offset, row := range []string{"R1", "F2", "R1"} {
		_, writtenDesign, err := s.WriteContractRow(ctx, store.ContractRowChange{
			Contract: id, Row: row, Action: store.RowActionWrite, Session: rowWriteSession,
			At: rowWriteStart + int64(offset)*10,
		}, appendRowEdit(row, &seen))
		if err != nil {
			t.Fatalf("WriteContractRow %s: %v", row, err)
		}
		if writtenDesign != design {
			t.Fatalf("design of write %s = %q, want %q", row, writtenDesign, design)
		}
	}

	if len(seen) != 3 || seen[0].Body != proposal.Body || !reflect.DeepEqual(seen[0].Decisions, proposal.Decisions) {
		t.Fatalf("first edit was given %+v, want the newest version's body and decisions", seen)
	}
	if seen[1].Body != proposal.Body+"R1;" || seen[2].Body != proposal.Body+"R1;F2;" {
		t.Fatalf("later edits were given %q and %q, want the working copy's body", seen[1].Body, seen[2].Body)
	}

	record, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(record.Versions) != 1 || record.Versions[0].Body != proposal.Body ||
		!reflect.DeepEqual(record.Versions[0].Decisions, proposal.Decisions) || record.State != store.ContractProposed {
		t.Fatalf("contract = %+v, want version 1 and the proposed state untouched by the writes", record)
	}

	working, err := s.ContractWorkingCopy(ctx, id)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	wantWorking := store.ContractWorkingCopy{
		ContractWorking: store.ContractWorking{
			Contract: id, Version: 1, Body: proposal.Body + "R1;F2;R1;",
			Decisions: proposal.Decisions, Writer: rowWriteSession, Created: rowWriteStart, Updated: rowWriteStart + 20,
		},
		ChangedRows: []string{"R1", "F2"},
	}
	if !reflect.DeepEqual(working, wantWorking) {
		t.Fatalf("working copy = %+v, want %+v", working, wantWorking)
	}
	writes, err := s.ContractRowWrites(ctx, id)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	for _, write := range writes {
		if write.Version != 1 {
			t.Fatalf("row write %d is over version %d, want version 1", write.Seq, write.Version)
		}
	}
	if len(writes) != 3 {
		t.Fatalf("row writes = %d, want 3", len(writes))
	}
}

// TestRecordsRowsWorkingWritesRefusedOnAcceptedOrSetAside checks that a row write to an accepted or set-aside contract is
// refused as a contract in another state, without calling its edit, and that it stores no row write and no working copy.
func TestRecordsRowsWorkingWritesRefusedOnAcceptedOrSetAside(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	const design = "orrery-6a7b8c9/design-settled"
	const accepted, setAside = "orrery-6a7b8c9/contract-accepted", "orrery-6a7b8c9/contract-set-aside"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, accepted, designProposal("/work/synthetic-repo", "Accepted", contractProposedAt))
	proposeInDesign(t, s, design, setAside, designProposal("/work/synthetic-repo", "Set aside", contractProposedAt))
	if err := s.AcceptContract(ctx, accepted, store.ContractChange{
		Words: "Accepted", At: contractProposedAt + 5,
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if err := s.SetAsideContract(ctx, setAside, store.ContractChange{
		Words: "Set aside", At: contractProposedAt + 5,
	}); err != nil {
		t.Fatalf("SetAsideContract: %v", err)
	}

	for _, id := range []string{accepted, setAside} {
		var called bool
		_, _, err := s.WriteContractRow(ctx, store.ContractRowChange{
			Contract: id, Row: "R1", Action: store.RowActionWrite, Session: rowWriteSession, At: rowWriteStart,
		}, func(current store.ContractBody) (store.ContractBody, error) {
			called = true
			return current, nil
		})
		if !errors.Is(err, store.ErrContractState) {
			t.Fatalf("WriteContractRow on %s = %v, want ErrContractState", id, err)
		}
		if called {
			t.Fatalf("the edit ran for %s, which a row write must refuse before it edits", id)
		}
		if _, err = s.ContractWorkingCopy(ctx, id); !errors.Is(err, store.ErrNoWorking) {
			t.Fatalf("ContractWorkingCopy of %s = %v, want ErrNoWorking", id, err)
		}
		writes, err := s.ContractRowWrites(ctx, id)
		if err != nil || len(writes) != 0 {
			t.Fatalf("row writes of %s = %+v, %v, want none", id, writes, err)
		}
	}
}
