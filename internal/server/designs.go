package server

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// designNotFound is the fixed message for a Get of an id no design holds.
const designNotFound = "design not found"

// designStoreUnavailable is the fixed message for a design read the store cannot answer.
const designStoreUnavailable = "design store unavailable"

// designsServer answers orrery.v1.Designs. List and Get read the store reader. A nil reader answers them unavailable.
// Answer is not served here yet, so it answers unimplemented.
type designsServer struct {
	orreryv1connect.UnimplementedDesignsHandler
	reader *store.Reader
}

// List answers every design the store reader gives, in the order it gives them.
func (d designsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.DesignsListRequest],
) (*connect.Response[orreryv1.DesignsListResponse], error) {
	if d.reader == nil {
		return nil, listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	records, err := d.reader.Designs(ctx)
	if err != nil {
		return nil, designReadError(err, "read designs")
	}
	designs := make([]*orreryv1.Design, 0, len(records))
	for _, record := range records {
		designs = append(designs, designMessage(record))
	}
	return connect.NewResponse(&orreryv1.DesignsListResponse{Designs: designs}), nil
}

// Get answers the design of the id List gave it. An id no design holds answers not_found.
func (d designsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.DesignsGetRequest],
) (*connect.Response[orreryv1.DesignsGetResponse], error) {
	if d.reader == nil {
		return nil, listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	record, err := d.reader.Design(ctx, req.Msg.GetId())
	if errors.Is(err, store.ErrNoDesign) {
		return nil, listError(connect.CodeNotFound, designNotFound)
	}
	if err != nil {
		return nil, designReadError(err, "read design")
	}
	return connect.NewResponse(&orreryv1.DesignsGetResponse{Design: designMessage(record)}), nil
}

// designReadError returns the error a design read answers with for err: unavailable when the store is, and internal
// with message otherwise.
func designReadError(err error, message string) error {
	if errors.Is(err, store.ErrUnavailable) {
		return listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	return listError(connect.CodeInternal, message)
}

// designMessage converts a design record to its Design, copying every field, with its contracts in position order.
func designMessage(record store.DesignRecord) *orreryv1.Design {
	contracts := make([]*orreryv1.DesignContract, 0, len(record.Contracts))
	for _, contract := range record.Contracts {
		contracts = append(contracts, designContractMessage(contract))
	}
	return &orreryv1.Design{
		Id:           record.ID,
		Title:        record.Title,
		Conversation: record.Conversation,
		Created:      record.Created.Unix(),
		OfOne:        record.OfOne,
		State:        record.State,
		Contracts:    contracts,
	}
}

// readListDesigns returns every design the store reader gives, in the order it gives them, for a list to carry. A nil
// reader gives none. It fails as the list's other store reads fail: unavailable when the store is, and internal
// otherwise.
func readListDesigns(ctx context.Context, reader *store.Reader) ([]store.DesignRecord, error) {
	if reader == nil {
		return []store.DesignRecord{}, nil
	}
	records, err := reader.Designs(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read designs")
	}
	return records, nil
}

// designMessages converts each design record to its Design, in the order given.
func designMessages(records []store.DesignRecord) []*orreryv1.Design {
	designs := make([]*orreryv1.Design, 0, len(records))
	for _, record := range records {
		designs = append(designs, designMessage(record))
	}
	return designs
}

// conversationDesigns returns the designs whose conversation is conversation, in the order of designs.
func conversationDesigns(designs []*orreryv1.Design, conversation string) []*orreryv1.Design {
	var own []*orreryv1.Design
	for _, design := range designs {
		if design.GetConversation() == conversation {
			own = append(own, design)
		}
	}
	return own
}

// designContractMessage converts a contract of a design to its DesignContract, copying every field.
func designContractMessage(contract store.DesignContract) *orreryv1.DesignContract {
	return &orreryv1.DesignContract{
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

// newDesignsHandler returns the path and handler of the Designs service, reading designs from reader as designsServer
// does.
func newDesignsHandler(reader *store.Reader, opts ...connect.HandlerOption) (string, http.Handler) {
	return orreryv1connect.NewDesignsHandler(designsServer{reader: reader}, opts...)
}
