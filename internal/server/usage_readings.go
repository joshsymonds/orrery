package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// Readings answers the usage limit readings the ledger holds, one for each account and claim. A nil ledger answers
// unavailable, and a read that fails answers unavailable.
func (u usageServer) Readings(
	ctx context.Context,
	_ *connect.Request[orreryv1.UsageReadingsRequest],
) (*connect.Response[orreryv1.UsageReadingsResponse], error) {
	if u.ledger == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	readings, err := u.ledger.Readings(ctx)
	if err != nil {
		return nil, listError(connect.CodeUnavailable, "read readings")
	}
	messages := make([]*orreryv1.UsageReading, 0, len(readings))
	for _, reading := range readings {
		messages = append(messages, &orreryv1.UsageReading{
			Account:     reading.Account,
			Claim:       reading.Claim,
			Utilization: reading.Utilization,
			Reset_:      reading.Reset,
			Status:      reading.Status,
			Observed:    reading.Observed,
			Dropped:     reading.Dropped,
		})
	}
	return connect.NewResponse(&orreryv1.UsageReadingsResponse{Readings: messages}), nil
}
