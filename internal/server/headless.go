package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// ListHeadless answers Sessions.ListHeadless as unimplemented. A later change implements the list.
func (s sessionsServer) ListHeadless(
	_ context.Context,
	_ *connect.Request[orreryv1.ListHeadlessRequest],
) (*connect.Response[orreryv1.ListHeadlessResponse], error) {
	return nil, errors.Join(connect.NewError(
		connect.CodeUnimplemented,
		errors.New("ListHeadless is not implemented yet"),
	))
}
