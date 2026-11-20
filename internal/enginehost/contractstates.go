package enginehost

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// ReviseContract stores the body and decisions as the contract's next version, in the state it is in, by the sessions
// row the caller resolves to. The version carries the rows and sections that differ from the newest version.
func (s *contractService) ReviseContract(
	ctx context.Context,
	req *connect.Request[v1.ReviseContractRequest],
) (*connect.Response[v1.ReviseContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	contract := req.Msg.GetContract()
	decisions := req.Msg.GetDecisions()
	if err := s.checkContract(ctx, id, contract, decisions); err != nil {
		return nil, err
	}
	if req.Msg.GetWords() == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "words: empty")
	}
	writer, err := s.contractWriter(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	record, err := s.contracts.Store.Contract(ctx, id)
	if errors.Is(err, store.ErrNoContract) {
		return nil, contractNotFound(id)
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	newest := record.Versions[len(record.Versions)-1]
	prev, err := contractVersionMessage(newest)
	if err != nil {
		return nil, epicStoreError(fmt.Errorf("read version %d of contract %s: %w", newest.Version, id, err))
	}
	rows, sections := contractChanges(prev, &v1.ContractVersion{Contract: contract, Decisions: decisions})
	version, err := contractVersionOf(
		id, contract, decisions, req.Msg.GetWords(), writer, s.contracts.Now().Unix())
	if err != nil {
		return nil, err
	}
	version.Version = newest.Version + 1
	version.ChangedRows = rows
	version.ChangedSections = sections
	if err = s.contracts.Store.ReviseContract(ctx, id, version); err != nil {
		return nil, reviseRefusal(id, err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ReviseContractResponse{Contract: stored}), nil
}

// reviseRefusal returns the refusal a store revise of the contract id gets.
func reviseRefusal(id string, err error) error {
	switch {
	case errors.Is(err, store.ErrNoContract):
		return contractNotFound(id)
	case errors.Is(err, store.ErrContractState):
		return refuseEpic(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrNotContractConversation):
		return refuseEpic(connect.CodePermissionDenied, "caller: not the contract's conversation")
	case errors.Is(err, store.ErrContractVersion):
		return refuseEpic(connect.CodeAborted, "version: moved, read the contract again")
	default:
		return epicStoreError(err)
	}
}

// AcceptContract accepts the contract at its newest version, by the sessions row the caller resolves to or by the owner
// when it resolves to none. Every decision of that version must have an answer.
func (s *contractService) AcceptContract(
	ctx context.Context,
	req *connect.Request[v1.AcceptContractRequest],
) (*connect.Response[v1.AcceptContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	session, err := s.changeSession(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	err = s.contracts.Store.AcceptContract(ctx, id, s.contractChange(session, req.Msg.GetWords()))
	if err != nil {
		return nil, acceptRefusal(id, err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.AcceptContractResponse{Contract: stored}), nil
}

// acceptRefusal returns the refusal a store accept of the contract id gets.
func acceptRefusal(id string, err error) error {
	switch {
	case errors.Is(err, store.ErrNoContract):
		return contractNotFound(id)
	case errors.Is(err, store.ErrContractState):
		return refuseEpic(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrOpenDecisions):
		return refuseEpic(connect.CodeFailedPrecondition, "decisions: "+err.Error())
	default:
		return epicStoreError(err)
	}
}

// SetAsideContract sets the contract aside with words, by the sessions row the caller resolves to or by the owner when
// it resolves to none.
func (s *contractService) SetAsideContract(
	ctx context.Context,
	req *connect.Request[v1.SetAsideContractRequest],
) (*connect.Response[v1.SetAsideContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if req.Msg.GetWords() == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "words: empty")
	}
	session, err := s.changeSession(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	err = s.contracts.Store.SetAsideContract(ctx, id, s.contractChange(session, req.Msg.GetWords()))
	if err != nil {
		return nil, setAsideRefusal(id, err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.SetAsideContractResponse{Contract: stored}), nil
}

// setAsideRefusal returns the refusal a store set aside of the contract id gets.
func setAsideRefusal(id string, err error) error {
	switch {
	case errors.Is(err, store.ErrNoContract):
		return contractNotFound(id)
	case errors.Is(err, store.ErrContractEpic):
		return refuseEpic(connect.CodeFailedPrecondition, "state: accepted, and an epic holds its id")
	case errors.Is(err, store.ErrContractState):
		return refuseEpic(connect.CodeFailedPrecondition, "state: not proposed, or accepted with no epic")
	default:
		return epicStoreError(err)
	}
}

// ReopenContract makes a set aside contract proposed again, by the sessions row the caller resolves to or by the owner
// when it resolves to none.
func (s *contractService) ReopenContract(
	ctx context.Context,
	req *connect.Request[v1.ReopenContractRequest],
) (*connect.Response[v1.ReopenContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	session, err := s.changeSession(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	err = s.contracts.Store.ReopenContract(ctx, id, s.contractChange(session, req.Msg.GetWords()))
	if err != nil {
		return nil, reopenRefusal(id, err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ReopenContractResponse{Contract: stored}), nil
}

// reopenRefusal returns the refusal a store reopen of the contract id gets.
func reopenRefusal(id string, err error) error {
	switch {
	case errors.Is(err, store.ErrNoContract):
		return contractNotFound(id)
	case errors.Is(err, store.ErrContractState):
		return refuseEpic(connect.CodeFailedPrecondition, "state: not set aside")
	default:
		return epicStoreError(err)
	}
}

// contractChange returns the change of a contract's state that session makes with words, at the clock's time.
func (s *contractService) contractChange(session, words string) store.ContractChange {
	return store.ContractChange{Session: session, Words: words, At: s.contracts.Now().Unix()}
}
