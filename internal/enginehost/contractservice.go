package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractUnavailableMessage is the message every Contracts RPC is refused with when there is no contract store.
const contractUnavailableMessage = "contract store unavailable"

// The owner and session names a contract's state change is made by, as its by field names them.
const (
	contractByOwner   = "owner"
	contractBySession = "session"
)

// ContractStore is the set of contract operations the Contracts service answers with. *store.Store satisfies it.
type ContractStore interface {
	CallerSession(ctx context.Context, caller store.EpicCaller) (string, bool, error)
	ProposeContract(ctx context.Context, id string, v store.ContractVersion) error
	Contract(ctx context.Context, id string) (store.ContractRecord, error)
	Contracts(ctx context.Context) ([]store.ContractListing, error)
}

// Contracts is what orreryd's Contracts service answers with. Now is the clock every stored time is read from. Getenv
// is the environment a contract's checks resolve in; nil reads the process environment.
type Contracts struct {
	Store  ContractStore
	Now    func() time.Time
	Getenv func(string) string
}

// contractService implements the Contracts RPCs over contracts. A nil contracts answers the RPCs it serves as
// unavailable.
type contractService struct {
	enginev1connect.UnimplementedContractsHandler
	contracts *Contracts
}

// ready returns the refusal every Contracts RPC it serves gets when there is no contract store.
func (s *contractService) ready() error {
	if s.contracts == nil {
		return refuseEpic(connect.CodeUnavailable, contractUnavailableMessage)
	}
	return nil
}

// ProposeContract stores the contract as version 1 in state proposed, at the clock's time, by the sessions row the
// caller resolves to. The id must be the body's. The body's checks run first, then the decisions, then the caller.
// The reply is the stored contract read back.
func (s *contractService) ProposeContract(
	ctx context.Context,
	req *connect.Request[v1.ProposeContractRequest],
) (*connect.Response[v1.ProposeContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	contract := req.Msg.GetContract()
	if id != contract.GetId() {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: differs from the contract's id")
	}
	getenv := s.contracts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if err := checkContractBody(ctx, contract, getenv); err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	if err := checkContractDecisions(req.Msg.GetDecisions()); err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	caller, _ := epicCallerOf(req.Header())
	writer, ok, err := s.contracts.Store.CallerSession(ctx, caller)
	if err != nil {
		return nil, epicStoreError(err)
	}
	if !ok {
		return nil, refuseEpic(connect.CodePermissionDenied, "caller: resolves to no session")
	}
	body, err := protojson.Marshal(contract)
	if err != nil {
		return nil, epicStoreError(fmt.Errorf("encode contract %s: %w", id, err))
	}
	version := store.ContractVersion{
		Repository: contract.GetRepository(),
		Title:      contract.GetTitle(),
		Care:       contract.GetCare(),
		Body:       string(body),
		Decisions:  storeDecisions(req.Msg.GetDecisions()),
		Words:      req.Msg.GetWords(),
		Writer:     writer,
		At:         s.contracts.Now().Unix(),
	}
	if err = s.contracts.Store.ProposeContract(ctx, id, version); err != nil {
		if errors.Is(err, store.ErrContractExists) {
			return nil, refuseEpic(
				connect.CodeAlreadyExists,
				fmt.Sprintf("id: %s is already held by a contract or an epic", id),
			)
		}
		return nil, epicStoreError(err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ProposeContractResponse{Contract: stored}), nil
}

// GetContract returns the contract with its versions and states.
func (s *contractService) GetContract(
	ctx context.Context,
	req *connect.Request[v1.GetContractRequest],
) (*connect.Response[v1.GetContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.GetContractResponse{Contract: stored}), nil
}

// ListContracts returns the listing of every contract, without versions or states.
func (s *contractService) ListContracts(
	ctx context.Context,
	_ *connect.Request[v1.ListContractsRequest],
) (*connect.Response[v1.ListContractsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	listings, err := s.contracts.Store.Contracts(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ListContractsResponse{
		Contracts: epicMap(listings, storedListingOf),
	}), nil
}

// read returns the contract with id as the store holds it, with its versions and states. A contract no one holds is
// refused as not found.
func (s *contractService) read(ctx context.Context, id string) (*v1.StoredContract, error) {
	record, err := s.contracts.Store.Contract(ctx, id)
	if errors.Is(err, store.ErrNoContract) {
		return nil, refuseEpic(connect.CodeNotFound, fmt.Sprintf("contract %s not found", id))
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	stored := storedListingOf(record.ContractListing)
	for _, version := range record.Versions {
		message, convertErr := contractVersionMessage(version)
		if convertErr != nil {
			return nil, epicStoreError(
				fmt.Errorf("read version %d of contract %s: %w", version.Version, id, convertErr),
			)
		}
		stored.Versions = append(stored.Versions, message)
	}
	stored.States = epicMap(record.States, contractStateMessage)
	return stored, nil
}

// checkContractDecisions refuses a decision with an empty id, a repeated id, or an empty question. Propose and revise
// check their decisions as written, as CreateEpic checks its seed decisions.
func checkContractDecisions(decisions []*v1.ContractDecision) error {
	seen := make(map[string]bool, len(decisions))
	for i, decision := range decisions {
		id := decision.GetId()
		if id == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonEmpty}
		}
		if seen[id] {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonDuplicate}
		}
		seen[id] = true
		if decision.GetQuestion() == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "question"), reason: epicReasonEmpty}
		}
	}
	return nil
}

// storedListingOf returns the StoredContract of a listing, with neither versions nor states.
func storedListingOf(listing store.ContractListing) *v1.StoredContract {
	return &v1.StoredContract{
		Id:            listing.ID,
		Repository:    listing.Repository,
		Title:         listing.Title,
		Care:          listing.Care,
		State:         listing.State,
		Version:       listing.Version,
		OpenDecisions: listing.OpenDecisions,
		Conversation:  listing.Conversation,
		Created:       listing.Created,
		Changed:       listing.Changed,
	}
}

// contractVersionMessage returns the ContractVersion message of version, its body decoded from protobuf JSON.
func contractVersionMessage(version store.ContractVersion) (*v1.ContractVersion, error) {
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(version.Body), &contract); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	return &v1.ContractVersion{
		Version:         version.Version,
		Contract:        &contract,
		Decisions:       epicMap(version.Decisions, contractDecisionMessage),
		Words:           version.Words,
		Writer:          version.Writer,
		Time:            version.At,
		ChangedRows:     version.ChangedRows,
		ChangedSections: version.ChangedSections,
	}, nil
}

// contractDecisionMessage returns the ContractDecision message of decision.
func contractDecisionMessage(decision store.ContractDecision) *v1.ContractDecision {
	return &v1.ContractDecision{
		Id:             decision.ID,
		Question:       decision.Question,
		Options:        decision.Options,
		Recommendation: decision.Recommendation,
		Reason:         decision.Reason,
		Row:            decision.Row,
		Answer:         decision.Answer,
	}
}

// storeDecisions returns the store decisions of the decision messages decisions.
func storeDecisions(decisions []*v1.ContractDecision) []store.ContractDecision {
	return epicMap(decisions, func(decision *v1.ContractDecision) store.ContractDecision {
		return store.ContractDecision{
			ID:             decision.GetId(),
			Question:       decision.GetQuestion(),
			Options:        decision.GetOptions(),
			Recommendation: decision.GetRecommendation(),
			Reason:         decision.GetReason(),
			Row:            decision.GetRow(),
			Answer:         decision.GetAnswer(),
		}
	})
}

// contractStateMessage returns the ContractStateChange message of state. A change its owner made names the owner.
func contractStateMessage(state store.ContractStateChange) *v1.ContractStateChange {
	by := contractByOwner
	if state.Session != "" {
		by = contractBySession
	}
	return &v1.ContractStateChange{
		State:   state.State,
		Version: state.Version,
		By:      by,
		Session: state.Session,
		Words:   state.Words,
		Time:    state.At,
	}
}
