package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// Spend answers what each seat spent over a span. A nil ledger answers unavailable, and a ledger answers unimplemented
// until the read is written.
func (u usageServer) Spend(
	_ context.Context,
	_ *connect.Request[orreryv1.UsageSpendRequest],
) (*connect.Response[orreryv1.UsageSpendResponse], error) {
	if u.ledger == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	return nil, listError(connect.CodeUnimplemented, usageNotImplemented)
}
