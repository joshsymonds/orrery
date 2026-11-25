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

// ProposeWorking proposes what the stage is writing for the id req names. When a contract holds the id, its working copy
// is stored as the contract's next version, by the sessions row the caller resolves to, as ReviseContract stores a body,
// and the working copy is gone after it. When no contract holds the id, the draft of the id is stored as version 1, into
// the draft's design, as ProposeContract stores a body. Both run the checks of a contract, and the reply carries the
// length warnings. An id with no draft is not found, and a contract with no working copy is refused.
func (s *contractService) ProposeWorking(
	ctx context.Context,
	req *connect.Request[v1.ProposeWorkingRequest],
) (*connect.Response[v1.ProposeWorkingResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	record, err := s.contracts.Store.Contract(ctx, req.Msg.GetId())
	switch {
	case errors.Is(err, store.ErrNoContract):
		return s.proposeDraft(ctx, req)
	case err != nil:
		return nil, epicStoreError(err)
	default:
		return s.reviseWorking(ctx, req, record)
	}
}

// reviseWorking stores the working copy of the contract record as its next version, as ReviseContract stores a body. The
// working copy's body and decisions are checked as a revise's are, and a refusal of those checks is kept under the version
// after the newest.
func (s *contractService) reviseWorking(
	ctx context.Context,
	req *connect.Request[v1.ProposeWorkingRequest],
	record store.ContractRecord,
) (*connect.Response[v1.ProposeWorkingResponse], error) {
	id := record.ID
	working, err := s.contracts.Store.ContractWorkingCopy(ctx, id)
	if errors.Is(err, store.ErrNoWorking) {
		return nil, refuseEpic(connect.CodeFailedPrecondition, "working: none")
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	contract, decisions, err := storedContractBody(id, working.Body, working.Decisions)
	if err != nil {
		return nil, err
	}
	if err = s.checkContract(ctx, id, contract, decisions); err != nil {
		return nil, s.refuseWrite(ctx, req.Header(), id, record.Version+1, "", err)
	}
	warnings, err := s.contractLengthWarnings(contract, decisions)
	if err != nil {
		return nil, err
	}
	words := req.Msg.GetWords()
	if words == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "words: empty")
	}
	writer, err := s.contractWriter(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	version, err := s.workingRevision(record, id, contract, decisions, words, writer)
	if err != nil {
		return nil, err
	}
	if err = s.contracts.Store.ReviseContract(ctx, id, version); err != nil {
		return nil, reviseRefusal(id, err)
	}
	err = s.logContract(ctx, id, fmt.Sprintf("revised to version %d", version.Version), writer, words)
	if err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ProposeWorkingResponse{Contract: stored, Warnings: warnings}), nil
}

// workingRevision returns the store version a working copy of the contract record makes: its body and decisions, with the
// words and writer, and the rows and sections that differ from the newest version. It is the version after the newest.
func (s *contractService) workingRevision(
	record store.ContractRecord,
	id string,
	contract *v1.EpicContract,
	decisions []*v1.ContractDecision,
	words, writer string,
) (store.ContractVersion, error) {
	version, err := contractVersionOf(id, contract, decisions, words, writer, s.contracts.Now().Unix())
	if err != nil {
		return store.ContractVersion{}, err
	}
	newest := record.Versions[len(record.Versions)-1]
	prev, err := contractVersionMessage(newest)
	if err != nil {
		return store.ContractVersion{}, epicStoreError(
			fmt.Errorf("read version %d of contract %s: %w", newest.Version, id, err),
		)
	}
	rows, sections := contractChanges(prev, &v1.ContractVersion{Contract: contract, Decisions: decisions})
	version.Version = record.Version + 1
	version.ChangedRows = rows
	version.ChangedSections = sections
	return version, nil
}

// proposeDraft stores the draft of the id req names as version 1, into the draft's design, as ProposeContract stores a body.
// A draft no one holds is not found. A draft the checks refuse is kept under version 1, as a propose's refusal is.
func (s *contractService) proposeDraft(
	ctx context.Context,
	req *connect.Request[v1.ProposeWorkingRequest],
) (*connect.Response[v1.ProposeWorkingResponse], error) {
	id := req.Msg.GetId()
	draft, err := s.contracts.Store.ContractDraft(ctx, id)
	if errors.Is(err, store.ErrNoDraft) {
		return nil, contractNotFound(id)
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	contract, decisions, err := storedContractBody(id, draft.Body, draft.Decisions)
	if err != nil {
		return nil, err
	}
	if err = s.checkContract(ctx, id, contract, decisions); err != nil {
		return nil, s.refuseWrite(ctx, req.Header(), id, 1, draft.Design, err)
	}
	warnings, err := s.contractLengthWarnings(contract, decisions)
	if err != nil {
		return nil, err
	}
	writer, err := s.contractWriter(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	version, err := contractVersionOf(
		id, contract, decisions, req.Msg.GetWords(), writer, s.contracts.Now().Unix())
	if err != nil {
		return nil, err
	}
	err = s.storeProposal(ctx, id, draft.Design, version)
	if err != nil {
		return nil, err
	}
	err = s.logContract(ctx, id, "proposed", writer, req.Msg.GetWords())
	if err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ProposeWorkingResponse{Contract: stored, Warnings: warnings}), nil
}

// storedContractBody returns the contract and decisions a draft or working copy of the contract id holds: body is the
// EpicContract in protobuf JSON, which a draft no write has given a body holds as "", for an empty contract. The decisions
// are returned as messages. A body that does not decode is internal.
func storedContractBody(
	id, body string,
	decisions []store.ContractDecision,
) (*v1.EpicContract, []*v1.ContractDecision, error) {
	contract := &v1.EpicContract{}
	if body != "" {
		if err := protojson.Unmarshal([]byte(body), contract); err != nil {
			return nil, nil, epicStoreError(fmt.Errorf("read body of %s: %w", id, err))
		}
	}
	return contract, epicMap(decisions, contractDecisionMessage), nil
}
