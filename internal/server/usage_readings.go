package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// Readings answers the usage limit readings. A nil ledger answers unavailable, and a ledger answers unimplemented until
// the read is written.
func (u usageServer) Readings(
	_ context.Context,
	_ *connect.Request[orreryv1.UsageReadingsRequest],
) (*connect.Response[orreryv1.UsageReadingsResponse], error) {
	if u.ledger == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	return nil, listError(connect.CodeUnimplemented, usageNotImplemented)
}
