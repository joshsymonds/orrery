package enginehost

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// draftTestDesign is the id of the design the Contract draft tests join.
const draftTestDesign = "design-one"

// draftTestRequest returns a DraftContract request of body with decisions, words and design, from the headless session.
func draftTestRequest(
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
	words, design string,
) *connect.Request[v1.DraftContractRequest] {
	req := connect.NewRequest(&v1.DraftContractRequest{
		Id: body.GetId(), Contract: body, Decisions: decisions, Words: words, Design: design,
	})
	headlessCaller(req.Header())
	return req
}

// wantContractDraft returns the ContractDraft that drafting body with decisions, words and design by writer gives, with
// the draft created at created and last written at updated.
func wantContractDraft(
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
	words, design, writer string,
	created, updated time.Time,
) *v1.ContractDraft {
	return &v1.ContractDraft{
		Id:        body.GetId(),
		Design:    design,
		Contract:  body,
		Decisions: decisions,
		Words:     words,
		Writer:    writer,
		Created:   created.Unix(),
		Updated:   updated.Unix(),
	}
}

// TestStageDraftStoresDraftWithNoVersions checks that DraftContract stores a contract as a draft at the clock's time
// and replies with the draft as the store reads it back, and that the draft has no version: GetContract refuses its id
// as not found.
func TestStageDraftStoresDraftWithNoVersions(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	body := contractTestBody(server.repo)
	decisions := contractTestDecisions()

	reply, err := server.contracts.DraftContract(t.Context(), draftTestRequest(body, decisions, contractTestWords, ""))
	if err != nil {
		t.Fatalf("DraftContract: %v", err)
	}
	want := wantContractDraft(body, decisions, contractTestWords, "", contractTestHeadless,
		contractTestAt(), contractTestAt())
	if !proto.Equal(reply.Msg.GetDraft(), want) {
		t.Errorf("DraftContract reply = %v, want %v", reply.Msg.GetDraft(), want)
	}

	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: body.GetId()}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+body.GetId()+" not found")
}

// TestStageDraftRevisesInPlaceKeepingCreated checks that a second DraftContract of an id a draft holds replaces the
// draft in place: the draft keeps the created time of its first write, takes the time of its second, reads back the
// second write's body, decisions and words, and still has no version.
func TestStageDraftRevisesInPlaceKeepingCreated(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	var clock atomic.Int64
	clock.Store(contractTestAt().Unix())
	contracts := contractTestContracts(st)
	contracts.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	server := startContractServer(t, st, contracts)
	body := contractTestBody(server.repo)
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(body, nil, contractTestWords, "")); err != nil {
		t.Fatalf("DraftContract: %v", err)
	}

	later := contractTestAt().Add(time.Hour)
	clock.Store(later.Unix())
	revised := contractTestBody(server.repo)
	revised.Title = "Synthetic revised title."
	decisions := contractTestDecisions()
	reply, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(revised, decisions, "Synthetic revised words.", ""))
	if err != nil {
		t.Fatalf("DraftContract revision: %v", err)
	}
	want := wantContractDraft(revised, decisions, "Synthetic revised words.", "", contractTestHeadless,
		contractTestAt(), later)
	if !proto.Equal(reply.Msg.GetDraft(), want) {
		t.Errorf("DraftContract revision reply = %v, want %v", reply.Msg.GetDraft(), want)
	}

	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: body.GetId()}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+body.GetId()+" not found")
}

// TestStageDraftProposesVersionOneIntoItsDesign checks that a draft naming a design joins it, that a propose of the
// drafted id that names no design stores the contract as version 1 in the draft's design, and that the draft is gone
// once the contract is proposed.
func TestStageDraftProposesVersionOneIntoItsDesign(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	body := contractTestBody(stepGitTestRepo(t))
	decisions := contractTestDecisions()
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(body, decisions, contractTestWords, draftTestDesign)); err != nil {
		t.Fatalf("DraftContract into %s: %v", draftTestDesign, err)
	}
	drafts, err := st.DesignDrafts(t.Context(), draftTestDesign)
	if err != nil || len(drafts) != 1 || drafts[0].ID != body.GetId() {
		t.Fatalf("DesignDrafts(%s) = %v, %v, want the one draft %s", draftTestDesign, drafts, err, body.GetId())
	}

	req := connect.NewRequest(&v1.ProposeContractRequest{
		Id: body.GetId(), Contract: body, Decisions: decisions, Words: contractTestWords,
	})
	headlessCaller(req.Header())
	proposed, err := server.contracts.ProposeContract(t.Context(), req)
	if err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	want := wantStoredContract(body, decisions, contractTestHeadless)
	if !proto.Equal(proposed.Msg.GetContract(), want) {
		t.Errorf("ProposeContract reply = %v, want %v", proposed.Msg.GetContract(), want)
	}

	design, err := server.designs.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: draftTestDesign}))
	if err != nil {
		t.Fatalf("GetDesign: %v", err)
	}
	wantMember := &v1.StoredDesignContract{
		Id: body.GetId(), Title: body.GetTitle(), Repository: body.GetRepository(), State: "proposed", Position: 1,
	}
	members := design.Msg.GetDesign().GetContracts()
	if len(members) != 1 || !proto.Equal(members[0], wantMember) {
		t.Errorf("%s contracts = %v, want [%v]", draftTestDesign, members, wantMember)
	}

	if _, err = st.ContractDraft(t.Context(), body.GetId()); !errors.Is(err, store.ErrNoDraft) {
		t.Errorf("ContractDraft after propose: error %v, want one that matches ErrNoDraft", err)
	}
	if drafts, err = st.DesignDrafts(t.Context(), draftTestDesign); err != nil || len(drafts) != 0 {
		t.Errorf("DesignDrafts(%s) after propose = %v, %v, want none", draftTestDesign, drafts, err)
	}
}

// TestStageDraftProposeNamingOtherDesignRefused checks that a propose of a drafted id that names a design other than
// the draft's is refused as invalid, and that it stores no contract and leaves the draft in place.
func TestStageDraftProposeNamingOtherDesignRefused(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, draftTestDesign, "Design one")
	createDesign(t, server.designs, "design-two", "Design two")
	body := contractTestBody(stepGitTestRepo(t))
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(body, nil, contractTestWords, draftTestDesign)); err != nil {
		t.Fatalf("DraftContract into %s: %v", draftTestDesign, err)
	}

	req := connect.NewRequest(&v1.ProposeContractRequest{
		Id: body.GetId(), Contract: body, Words: contractTestWords, Design: "design-two",
	})
	headlessCaller(req.Header())
	_, err := server.contracts.ProposeContract(t.Context(), req)
	wantContractRefusal(t, err, connect.CodeInvalidArgument, "design: differs from the design of the draft")

	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: body.GetId()}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+body.GetId()+" not found")
	if _, err = st.ContractDraft(t.Context(), body.GetId()); err != nil {
		t.Errorf("ContractDraft after refused propose: %v, want the draft in place", err)
	}
}

// TestStageDraftRefusedUnderHeldID checks that a draft under an id a contract holds is refused as already held, and
// that the store keeps no draft under that id.
func TestStageDraftRefusedUnderHeldID(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	body := contractTestBody(server.repo)
	if _, err := proposeContract(t, server.contracts, headlessCaller, body, nil); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}

	_, err := server.contracts.DraftContract(t.Context(), draftTestRequest(body, nil, contractTestWords, ""))
	wantContractRefusal(t, err, connect.CodeAlreadyExists,
		"id: "+epicServiceID+" is already held by a contract or an epic")
	if _, err = st.ContractDraft(t.Context(), body.GetId()); !errors.Is(err, store.ErrNoDraft) {
		t.Errorf("ContractDraft of a refused draft: error %v, want one that matches ErrNoDraft", err)
	}
}

// TestStageDraftRefusesAndStoresNothing checks that each draft the Contracts service refuses gets the refusal its
// reason names, and that the store keeps no draft under the id.
func TestStageDraftRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		headers func(h http.Header)
		change  func(req *v1.DraftContractRequest)
		code    connect.Code
		message string
	}{
		{
			name:    "id has bad form",
			headers: headlessCaller,
			change:  func(req *v1.DraftContractRequest) { req.Id = "synthetic-other/id" },
			code:    connect.CodeInvalidArgument,
			message: "id: bad form, want <repo-id>-<7 hex>/<slug>",
		},
		{
			name:    "id differs from the body's",
			headers: headlessCaller,
			change:  func(req *v1.DraftContractRequest) { req.Id = "synthetic-repo-6a7b8c9/epic-other" },
			code:    connect.CodeInvalidArgument,
			message: "id: differs from the contract's id",
		},
		{
			name:    "caller resolves to no session",
			headers: func(h http.Header) { h.Set(protocol.HeaderSession, "engine-missing") },
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name:    "design no one holds",
			headers: headlessCaller,
			change:  func(req *v1.DraftContractRequest) { req.Design = "no-such-design" },
			code:    connect.CodeNotFound,
			message: "design: no-such-design not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			seedContractSessions(t, st)
			server := startContractServer(t, st, contractTestContracts(st))
			body := contractTestBody(server.repo)
			req := connect.NewRequest(&v1.DraftContractRequest{
				Id: body.GetId(), Contract: body, Decisions: contractTestDecisions(), Words: contractTestWords,
			})
			if tc.change != nil {
				tc.change(req.Msg)
			}
			tc.headers(req.Header())

			_, err := server.contracts.DraftContract(t.Context(), req)
			wantContractRefusal(t, err, tc.code, tc.message)
			if _, err = st.ContractDraft(t.Context(), body.GetId()); !errors.Is(err, store.ErrNoDraft) {
				t.Errorf("ContractDraft of a refused draft: error %v, want one that matches ErrNoDraft", err)
			}
		})
	}
}
