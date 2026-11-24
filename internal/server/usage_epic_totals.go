package server

import (
	"context"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// EpicTotals answers the totals of each session of an epic. It is not built yet, so it answers unimplemented.
func (u usageServer) EpicTotals(
	_ context.Context,
	_ *connect.Request[orreryv1.UsageEpicTotalsRequest],
) (*connect.Response[orreryv1.UsageEpicTotalsResponse], error) {
	return nil, listError(connect.CodeUnimplemented, "epic totals not built")
}
