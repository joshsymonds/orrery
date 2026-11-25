package enginehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// designUnavailableMessage is the message every Designs RPC is refused with when there is no design store.
const designUnavailableMessage = "design store unavailable"

// designNotFoundMessage is the message a design no one holds is refused with.
const designNotFoundMessage = "design not found"

// DesignStore is the set of design operations the Designs service answers with. *store.Store satisfies it.
type DesignStore interface {
	CallerSession(ctx context.Context, caller store.EpicCaller) (string, bool, error)
	CreateDesign(ctx context.Context, design store.Design) error
	Design(ctx context.Context, id string) (store.DesignRecord, error)
	Designs(ctx context.Context) ([]store.DesignRecord, error)
	AddDesignEdge(ctx context.Context, designID, contract, after string, at time.Time) error
}

// Designs is what orreryd's Designs service answers with. Now is the clock every stored time is read from: a design's
// creation and each starts-after edge it orders.
type Designs struct {
	Store DesignStore
	Now   func() time.Time
}

// designService implements the Designs RPCs over designs. A nil designs answers the RPCs as unavailable.
type designService struct {
	enginev1connect.UnimplementedDesignsHandler
	designs *Designs
}

// ready returns the refusal every Designs RPC gets when there is no design store.
func (s *designService) ready() error {
	if s.designs == nil {
		return refuseEpic(connect.CodeUnavailable, designUnavailableMessage)
	}
	return nil
}

// CreateDesign stores a design with no contracts, at the clock's time, with the conversation of the sessions row the
// caller resolves to. The id and the title must not be blank. The reply is the stored design read back.
func (s *designService) CreateDesign(
	ctx context.Context,
	req *connect.Request[v1.CreateDesignRequest],
) (*connect.Response[v1.CreateDesignResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id, title := req.Msg.GetId(), req.Msg.GetTitle()
	if isBlank(id) {
		return nil, refuseBlank("id")
	}
	if isBlank(title) {
		return nil, refuseBlank("title")
	}
	conversation, err := s.writerSession(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	err = s.designs.Store.CreateDesign(ctx, store.Design{
		ID: id, Title: title, Conversation: conversation, Created: s.designs.Now(),
	})
	if errors.Is(err, store.ErrDesignExists) {
		return nil, refuseEpic(
			connect.CodeAlreadyExists,
			fmt.Sprintf("id: %s is already held by a design or a contract", id),
		)
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.CreateDesignResponse{Design: stored}), nil
}

// GetDesign returns the design with its contracts.
func (s *designService) GetDesign(
	ctx context.Context,
	req *connect.Request[v1.GetDesignRequest],
) (*connect.Response[v1.GetDesignResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.GetDesignResponse{Design: stored}), nil
}

// ListDesigns returns every design, the contracts in no design included as designs of one.
func (s *designService) ListDesigns(
	ctx context.Context,
	_ *connect.Request[v1.ListDesignsRequest],
) (*connect.Response[v1.ListDesignsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	records, err := s.designs.Store.Designs(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ListDesignsResponse{Designs: epicMap(records, storedDesignOf)}), nil
}

// OrderDesign adds a starts-after edge from the contract to each contract in after, all of the design. The design, the
// contract and each after id must not be blank. The reply is the design read back.
func (s *designService) OrderDesign(
	ctx context.Context,
	req *connect.Request[v1.OrderDesignRequest],
) (*connect.Response[v1.OrderDesignResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	design, contract, afters := req.Msg.GetDesign(), req.Msg.GetContract(), req.Msg.GetAfter()
	if isBlank(design) {
		return nil, refuseBlank("design")
	}
	if isBlank(contract) {
		return nil, refuseBlank("contract")
	}
	for i, after := range afters {
		if isBlank(after) {
			return nil, refuseBlank(fmt.Sprintf("after[%d]", i))
		}
	}
	for _, after := range afters {
		if err := s.designs.Store.AddDesignEdge(ctx, design, contract, after, s.designs.Now()); err != nil {
			return nil, orderRefusal(err)
		}
	}
	stored, err := s.read(ctx, design)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.OrderDesignResponse{Design: stored}), nil
}

// writerSession returns the sessions row a design is written by, resolved as a contract's writer is. A caller that
// resolves to no session is refused, since only a session writes a design.
func (s *designService) writerSession(ctx context.Context, header http.Header) (string, error) {
	caller, _ := epicCallerOf(header)
	session, ok, err := s.designs.Store.CallerSession(ctx, caller)
	if err != nil {
		return "", epicStoreError(err)
	}
	if !ok {
		return "", refuseEpic(connect.CodePermissionDenied, "caller: resolves to no session")
	}
	return session, nil
}

// read returns the design with id as the store holds it, with its contracts. A design no one holds is refused as not
// found.
func (s *designService) read(ctx context.Context, id string) (*v1.StoredDesign, error) {
	record, err := s.designs.Store.Design(ctx, id)
	if errors.Is(err, store.ErrNoDesign) {
		return nil, refuseEpic(connect.CodeNotFound, designNotFoundMessage)
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	return storedDesignOf(record), nil
}

// orderRefusal returns the refusal a starts-after edge the store refuses gets. An unknown design is not found. A
// contract outside its design, an edge across designs and a cycle are a failed precondition whose message is the
// store's, which names why. Any other error is internal.
func orderRefusal(err error) error {
	switch {
	case errors.Is(err, store.ErrNoDesign):
		return refuseEpic(connect.CodeNotFound, designNotFoundMessage)
	case errors.Is(err, store.ErrDesignMember),
		errors.Is(err, store.ErrDesignAcross),
		errors.Is(err, store.ErrDesignCycle):
		return refuseEpic(connect.CodeFailedPrecondition, err.Error())
	default:
		return epicStoreError(err)
	}
}

// refuseBlank returns the refusal a blank field gets.
func refuseBlank(field string) error {
	return refuseEpic(connect.CodeInvalidArgument, (&epicFieldError{field: field, reason: epicReasonEmpty}).Error())
}

// storedDesignOf returns the StoredDesign of record, its contracts in position order.
func storedDesignOf(record store.DesignRecord) *v1.StoredDesign {
	return &v1.StoredDesign{
		Id:           record.ID,
		Title:        record.Title,
		Conversation: record.Conversation,
		Created:      record.Created.Unix(),
		OfOne:        record.OfOne,
		State:        record.State,
		Contracts:    epicMap(record.Contracts, storedDesignContractOf),
	}
}

// storedDesignContractOf returns the StoredDesignContract of contract as its design holds it.
func storedDesignContractOf(contract store.DesignContract) *v1.StoredDesignContract {
	return &v1.StoredDesignContract{
		Id:         contract.ID,
		Title:      contract.Title,
		Repository: contract.Repository,
		State:      contract.State,
		Position:   contract.Position,
		After:      contract.After,
		Epic:       contract.Epic,
		EpicPhase:  contract.EpicPhase,
	}
}
