package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// usageSpendWindow is the fixed message for a spend read whose window ends before it starts.
const usageSpendWindow = "spend window ends before it starts"

// Spend answers what each seat spent over a span, from the finalized requests the ledger holds. A nil ledger answers
// unavailable, and a window that ends before it starts answers invalid argument.
func (u usageServer) Spend(
	ctx context.Context,
	req *connect.Request[orreryv1.UsageSpendRequest],
) (*connect.Response[orreryv1.UsageSpendResponse], error) {
	if u.ledger == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	from, to := req.Msg.GetFrom(), req.Msg.GetTo()
	if to < from {
		return nil, listError(connect.CodeInvalidArgument, usageSpendWindow)
	}
	spend, err := u.ledger.Spend(ctx, from, to)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read spend")
	}
	seats := make([]*orreryv1.UsageSeatSpend, 0, len(spend))
	for _, s := range spend {
		seats = append(seats, &orreryv1.UsageSeatSpend{
			Seat:                s.Seat,
			BilledCostNanoUsd:   s.BilledCostNanoUSD,
			ListCostNanoUsd:     s.ListCostNanoUSD,
			UnknownCostRequests: s.UnknownCostRequests,
		})
	}
	return connect.NewResponse(&orreryv1.UsageSpendResponse{Seats: seats}), nil
}
