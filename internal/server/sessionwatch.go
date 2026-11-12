package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// WatchConversation answers Sessions.WatchConversation as unimplemented. A later change implements the stream.
func (s sessionsServer) WatchConversation(
	_ context.Context,
	_ *connect.Request[orreryv1.SessionsWatchConversationRequest],
	_ *connect.ServerStream[orreryv1.SessionsWatchConversationResponse],
) error {
	return errors.Join(connect.NewError(
		connect.CodeUnimplemented,
		errors.New("WatchConversation is not implemented yet"),
	))
}
