package enginehost

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractWorkingChecks are checks that lack the Quality Bar line, so a propose or revise of them is refused as not
// verbatim, and contractWorkingRefusal is that refusal's message.
const (
	contractWorkingChecks  = "go test ./..."
	contractWorkingRefusal = "checks: the Quality Bar line is not verbatim"
)

// bodyEdit returns the edit that sets the body of a draft or working copy to body, with decisions, as a row write of the
// stage leaves it. It fails the test when body does not encode.
func bodyEdit(t *testing.T, body *v1.EpicContract, decisions []*v1.ContractDecision) store.ContractEdit {
	t.Helper()
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return func(current store.ContractBody) (store.ContractBody, error) {
		current.Body = string(encoded)
		current.Decisions = storeDecisions(decisions)
		current.Repository, current.Title = body.GetRepository(), body.GetTitle()
		return current, nil
	}
}

// writeContractRow writes row of the contract id into st through edit, from the headless session at hours after
// contractTestAt, into design when the id is a new draft's. It fails the test on error.
func writeContractRow(t *testing.T, st *store.Store, design, id, row string, edit store.ContractEdit, hours int64) {
	t.Helper()
	if _, _, err := st.WriteContractRow(t.Context(), store.ContractRowChange{
		Contract: id, Design: design, Row: row, Action: store.RowActionWrite, Session: contractTestHeadless,
		At: contractHour(hours),
	}, edit); err != nil {
		t.Fatalf("WriteContractRow %s of %s: %v", row, id, err)
	}
}

// proposeWorking sends a ProposeWorking of id with words from the caller headers set, and returns the reply's contract.
func proposeWorking(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	id, words string,
) (*v1.StoredContract, error) {
	call := connect.NewRequest(&v1.ProposeWorkingRequest{Id: id, Words: words})
	headers(call.Header())
	reply, err := client.ProposeWorking(ctx, call)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetContract(), nil
}

// wantContractRefusals fails the test unless got holds the refusals of want, in order.
func wantContractRefusals(t *testing.T, got, want []*v1.ContractRefusal) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("refusals = %v, want %v", got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("refusal %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestRecordsRowsWorkingDraftWrittenByRowsProposedAsVersionOne checks that rows written one at a time into a draft make
// the draft's body and decisions the contract's version 1 when ProposeWorking proposes it, into the draft's design, and
// that the draft is gone after the propose.
func TestRecordsRowsWorkingDraftWrittenByRowsProposedAsVersionOne(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	body := contractTestBody(stepGitTestRepo(t))
	decisions := contractTestDecisions()
	writeContractRow(t, st, draftTestDesign, epicServiceID, "F1", bodyEdit(t, body, nil), 0)
	writeContractRow(t, st, draftTestDesign, epicServiceID, "D1", bodyEdit(t, body, decisions), 1)

	got, err := proposeWorking(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestWords)
	if err != nil {
		t.Fatalf("ProposeWorking: %v", err)
	}
	want := wantStoredContract(body, decisions, contractTestHeadless)
	if !proto.Equal(got, want) {
		t.Errorf("ProposeWorking reply = %v, want %v", got, want)
	}
	if _, err = st.ContractDraft(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoDraft) {
		t.Errorf("ContractDraft after ProposeWorking: error %v, want one that matches ErrNoDraft", err)
	}
}

// TestRecordsRowsWorkingRowsInWorkingCopyProposedAsVersionTwo checks that rows written one at a time into the working
// copy of a proposed contract are the body ProposeWorking stores as version 2, with the words and writer it is given,
// the changed rows and sections that differ from version 1, and that the working copy is gone after it.
func TestRecordsRowsWorkingRowsInWorkingCopyProposedAsVersionTwo(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	body := contractTestBody(server.repo)
	if _, err := proposeContract(t, server.contracts, headlessCaller, body, contractTestDecisions()); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}
	revised := contractTestBody(server.repo)
	revised.Title = "Synthetic revised title"
	answered := contractTestDecisions()
	answered[0].Answer = "Synthetic option one."
	writeContractRow(t, st, "", epicServiceID, "F1", bodyEdit(t, revised, contractTestDecisions()), 1)
	writeContractRow(t, st, "", epicServiceID, "D1", bodyEdit(t, revised, answered), 2)

	before, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract before ProposeWorking: %v", err)
	}
	working := before.Msg.GetContract().GetWorking()
	if working.GetVersion() != 1 || working.GetContract().GetTitle() != revised.GetTitle() ||
		!slices.Equal(working.GetChangedRows(), []string{"F1", "D1"}) {
		t.Fatalf("working copy = %v, want version 1 with the revised title and rows F1 and D1", working)
	}

	got, err := proposeWorking(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestReviseWords)
	if err != nil {
		t.Fatalf("ProposeWorking: %v", err)
	}
	if n := len(got.GetVersions()); n != 2 {
		t.Fatalf("ProposeWorking reply holds %d versions, want 2", n)
	}
	wantFirst := wantStoredContract(body, contractTestDecisions(), contractTestHeadless).GetVersions()[0]
	if !proto.Equal(got.GetVersions()[0], wantFirst) {
		t.Errorf("version 1 = %v, want it untouched: %v", got.GetVersions()[0], wantFirst)
	}
	second := got.GetVersions()[1]
	if second.GetVersion() != 2 || second.GetContract().GetTitle() != revised.GetTitle() ||
		second.GetWords() != contractTestReviseWords || second.GetWriter() != contractTestHeadless ||
		!slices.Equal(second.GetChangedRows(), []string{"D1"}) ||
		!slices.Equal(second.GetChangedSections(), []string{"title", "decisions"}) {
		t.Errorf("version 2 = %v, want the working copy's title and decisions, with changed rows D1 and changed "+
			"sections title and decisions", second)
	}

	after, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract after ProposeWorking: %v", err)
	}
	if after.Msg.GetContract().GetWorking() != nil || after.Msg.GetContract().GetVersion() != 2 {
		t.Errorf("contract after ProposeWorking = %v, want version 2 with no working copy", after.Msg.GetContract())
	}
	if _, err = st.ContractWorkingCopy(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoWorking) {
		t.Errorf("ContractWorkingCopy after ProposeWorking: error %v, want one that matches ErrNoWorking", err)
	}
}

// TestRecordsRowsWorkingRefusedProposalKeptWithItsRefusals checks that a ProposeWorking whose working copy fails the
// checks is refused with the checks' message, that the refusal is kept as a refusal of version 2 with its field and
// text, and logged as 'refused' in the contract's design. The contract stays at version 1 and the working copy stays.
func TestRecordsRowsWorkingRefusedProposalKeptWithItsRefusals(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	body := contractTestBody(stepGitTestRepo(t))
	if err := proposeIntoDesign(t, server.contracts, draftTestDesign, body); err != nil {
		t.Fatalf("ProposeContract into %s: %v", draftTestDesign, err)
	}
	unverbatim := contractTestBody(body.GetRepository())
	unverbatim.Checks = contractWorkingChecks
	writeContractRow(t, st, draftTestDesign, body.GetId(), "F1", bodyEdit(t, unverbatim, nil), 1)
	writeContractRow(t, st, draftTestDesign, body.GetId(), "R1", bodyEdit(t, unverbatim, nil), 2)

	_, err := proposeWorking(t.Context(), server.contracts, headlessCaller, body.GetId(), contractTestReviseWords)
	wantContractRefusal(t, err, connect.CodeInvalidArgument, contractWorkingRefusal)

	got, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: body.GetId()}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	stored := got.Msg.GetContract()
	if stored.GetVersion() != 1 || len(stored.GetVersions()) != 1 {
		t.Errorf("contract after the refused ProposeWorking = %v, want version 1 alone", stored)
	}
	wantContractRefusals(t, stored.GetRefusals(), []*v1.ContractRefusal{{
		Seq: 1, Version: 2, Field: "checks", Text: contractWorkingRefusal, Session: contractTestHeadless,
		Time: contractTestAt().Unix(),
	}})
	if !slices.Equal(stored.GetWorking().GetChangedRows(), []string{"F1", "R1"}) {
		t.Errorf("working copy after the refusal = %v, want rows F1 and R1 kept", stored.GetWorking())
	}
	wantDesignLog(t, st, draftTestDesign, []store.DesignLogLine{
		{
			Design: draftTestDesign, Seq: 1, At: contractHour(0), Maker: store.LogMakerConversation,
			Session: contractTestHeadless, What: "proposed", IDs: []string{body.GetId()}, Words: contractTestWords,
		},
		{
			Design: draftTestDesign, Seq: 2, At: contractHour(0), Maker: store.LogMakerConversation,
			Session: contractTestHeadless, What: "refused", IDs: []string{body.GetId()}, Words: contractWorkingRefusal,
		},
	})
}

// TestRecordsRowsWorkingRefusedNewProposeKeptUnderItsID checks that a ProposeContract of an id no contract holds, whose
// checks refuse it, keeps the refusal as a refusal of version 1 under that id, and logs it as 'refused' in the design it
// names. No contract is stored.
func TestRecordsRowsWorkingRefusedNewProposeKeptUnderItsID(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	body := contractTestBody(stepGitTestRepo(t))
	body.Checks = contractWorkingChecks
	req := connect.NewRequest(&v1.ProposeContractRequest{
		Id: body.GetId(), Contract: body, Decisions: contractTestDecisions(), Words: contractTestWords,
		Design: draftTestDesign,
	})
	headlessCaller(req.Header())

	_, err := server.contracts.ProposeContract(t.Context(), req)
	wantContractRefusal(t, err, connect.CodeInvalidArgument, contractWorkingRefusal)
	refusals, err := st.ContractRefusals(t.Context(), body.GetId())
	if err != nil {
		t.Fatalf("ContractRefusals: %v", err)
	}
	wantRefusals := []store.ContractRefusal{{
		Contract: body.GetId(), Seq: 1, Version: 1, Field: "checks", Text: contractWorkingRefusal,
		Session: contractTestHeadless, At: contractTestAt().Unix(),
	}}
	if !slices.Equal(refusals, wantRefusals) {
		t.Errorf("refusals of %s = %+v, want %+v", body.GetId(), refusals, wantRefusals)
	}
	wantDesignLog(t, st, draftTestDesign, []store.DesignLogLine{
		{
			Design: draftTestDesign, Seq: 1, At: contractHour(0), Maker: store.LogMakerConversation,
			Session: contractTestHeadless, What: "refused", IDs: []string{body.GetId()}, Words: contractWorkingRefusal,
		},
	})
	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: body.GetId()}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+body.GetId()+" not found")
}

// TestRecordsRowsWorkingRefusedDraftProposalKeptUnderVersionOne checks that a ProposeWorking of a draft whose checks
// refuse it keeps the refusal as a refusal of version 1, and leaves the draft in place.
func TestRecordsRowsWorkingRefusedDraftProposalKeptUnderVersionOne(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	body := contractTestBody(stepGitTestRepo(t))
	body.Checks = contractWorkingChecks
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(body, contractTestDecisions(), contractTestWords, draftTestDesign)); err != nil {
		t.Fatalf("DraftContract into %s: %v", draftTestDesign, err)
	}

	_, err := proposeWorking(t.Context(), server.contracts, headlessCaller, body.GetId(), contractTestReviseWords)
	wantContractRefusal(t, err, connect.CodeInvalidArgument, contractWorkingRefusal)
	refusals, err := st.ContractRefusals(t.Context(), body.GetId())
	if err != nil {
		t.Fatalf("ContractRefusals: %v", err)
	}
	if len(refusals) != 1 || refusals[0].Version != 1 || refusals[0].Field != "checks" ||
		refusals[0].Text != contractWorkingRefusal {
		t.Errorf("refusals of the draft = %+v, want one refusal of version 1 on checks", refusals)
	}
	if _, err = st.ContractDraft(t.Context(), body.GetId()); err != nil {
		t.Errorf("ContractDraft after the refused ProposeWorking: %v, want the draft kept", err)
	}
}

// TestRecordsRowsWorkingRefusedReviseKept checks that a ReviseContract whose checks refuse it keeps the refusal as a
// refusal of the version after the newest, with the caller's session, and that a refusal that is not a check, such as a
// revise from another conversation, keeps nothing.
func TestRecordsRowsWorkingRefusedReviseKept(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	seedProposed(t, server)

	checked := reviseRequest(server.repo)
	checked.Contract.Checks = contractWorkingChecks
	_, err := reviseContract(t.Context(), server.contracts, headlessCaller, checked)
	wantContractRefusal(t, err, connect.CodeInvalidArgument, contractWorkingRefusal)
	_, err = reviseContract(t.Context(), server.contracts, paneCaller, reviseRequest(server.repo))
	wantContractRefusal(t, err, connect.CodePermissionDenied, "caller: not the contract's conversation")

	got, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if version := got.Msg.GetContract().GetVersion(); version != 1 {
		t.Errorf("contract is at version %d after the refused revise, want 1", version)
	}
	wantContractRefusals(t, got.Msg.GetContract().GetRefusals(), []*v1.ContractRefusal{{
		Seq: 1, Version: 2, Field: "checks", Text: contractWorkingRefusal, Session: contractTestHeadless,
		Time: contractTestAt().Unix(),
	}})
}

// TestRecordsRowsWorkingProposeWithNoWorkingRefused checks that a ProposeWorking of a proposed contract with no working
// copy is refused as failed precondition, with 'working: none', and that an id no contract or draft holds is not found.
// Neither stores a version or a refusal.
func TestRecordsRowsWorkingProposeWithNoWorkingRefused(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	seedProposed(t, server)

	_, err := proposeWorking(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestReviseWords)
	wantContractRefusal(t, err, connect.CodeFailedPrecondition, "working: none")
	_, err = proposeWorking(t.Context(), server.contracts, headlessCaller, contractTestMissing, contractTestReviseWords)
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+contractTestMissing+" not found")

	got, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	stored := got.Msg.GetContract()
	if stored.GetVersion() != 1 || len(stored.GetVersions()) != 1 || stored.GetWorking() != nil ||
		len(stored.GetRefusals()) != 0 {
		t.Errorf("contract after the refused ProposeWorking = %v, want version 1 with no working copy or refusal",
			stored)
	}
}
