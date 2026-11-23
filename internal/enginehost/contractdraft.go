package enginehost

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// DraftContract stores the contract as a draft under id at the clock's time, by the sessions row the caller resolves
// to. The id must have the form epicIDPattern names and must be the body's. A design the draft names must be a stored
// design. The body's checks run at propose, not here. The reply is the draft stored, read back.
func (s *contractService) DraftContract(
	ctx context.Context,
	req *connect.Request[v1.DraftContractRequest],
) (*connect.Response[v1.DraftContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	contract := req.Msg.GetContract()
	if !epicIDPattern.MatchString(id) {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: bad form, want <repo-id>-<7 hex>/<slug>")
	}
	if id != contract.GetId() {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: differs from the contract's id")
	}
	writer, err := s.contractWriter(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	now := s.contracts.Now()
	version, err := contractVersionOf(id, contract, req.Msg.GetDecisions(), req.Msg.GetWords(), writer, now.Unix())
	if err != nil {
		return nil, err
	}
	design := req.Msg.GetDesign()
	if err = s.contracts.Store.DraftContract(ctx, store.ContractDraft{
		ID: id, Design: design, Repository: version.Repository, Title: version.Title, Body: version.Body,
		Decisions: version.Decisions, Words: version.Words, Writer: version.Writer, Created: now, Updated: now,
	}); err != nil {
		return nil, draftRefusal(err, id, design)
	}
	stored, err := s.contracts.Store.ContractDraft(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	message, err := contractDraftMessage(stored)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.DraftContractResponse{Draft: message}), nil
}

// draftRefusal returns the refusal a draft of id in design gets when the store refuses it. An id a contract, an epic or
// a design holds is already held, and a design no one holds is not found. Any other error is internal.
func draftRefusal(err error, id, design string) error {
	switch {
	case errors.Is(err, store.ErrContractExists):
		return refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s is already held by a contract or an epic", id))
	case errors.Is(err, store.ErrNoDesign):
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("design: %s not found", design))
	default:
		return epicStoreError(err)
	}
}

// proposeDesign returns the design a propose of id joins. A propose of a drafted id that names no design joins the
// draft's design, and one that names another design than the draft's is refused as invalid. A propose of an id with no
// draft joins the design it names.
func (s *contractService) proposeDesign(ctx context.Context, id, design string) (string, error) {
	draft, err := s.contracts.Store.ContractDraft(ctx, id)
	if errors.Is(err, store.ErrNoDraft) {
		return design, nil
	}
	if err != nil {
		return "", epicStoreError(err)
	}
	if design == "" {
		return draft.Design, nil
	}
	if design != draft.Design {
		return "", refuseEpic(connect.CodeInvalidArgument, "design: differs from the design of the draft")
	}
	return design, nil
}

// contractDraftMessage returns the ContractDraft message of draft, its body decoded from protobuf JSON.
func contractDraftMessage(draft store.ContractDraft) (*v1.ContractDraft, error) {
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(draft.Body), &contract); err != nil {
		return nil, epicStoreError(fmt.Errorf("read body of draft %s: %w", draft.ID, err))
	}
	return &v1.ContractDraft{
		Id:        draft.ID,
		Design:    draft.Design,
		Contract:  &contract,
		Decisions: epicMap(draft.Decisions, contractDecisionMessage),
		Words:     draft.Words,
		Writer:    draft.Writer,
		Created:   draft.Created.Unix(),
		Updated:   draft.Updated.Unix(),
	}, nil
}
