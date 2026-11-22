package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// SessionTotals answers the requests and tokens of one session. A nil ledger answers unavailable, and a ledger answers
// unimplemented until the read is written.
func (u usageServer) SessionTotals(
	_ context.Context,
	_ *connect.Request[orreryv1.UsageSessionTotalsRequest],
) (*connect.Response[orreryv1.UsageSessionTotalsResponse], error) {
	if u.ledger == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	return nil, listError(connect.CodeUnimplemented, usageNotImplemented)
}
