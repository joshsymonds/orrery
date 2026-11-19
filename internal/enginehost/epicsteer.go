package enginehost

import (
	"context"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// MoveEpic is not served yet: the steer move answers unimplemented until its handler lands.
func (s *epicService) MoveEpic(
	_ context.Context,
	_ *connect.Request[v1.MoveEpicRequest],
) (*connect.Response[v1.MoveEpicResponse], error) {
	return nil, refuseEpic(connect.CodeUnimplemented, "MoveEpic is not implemented")
}

// ContinueEpic is not served yet: the steer continue answers unimplemented until its handler lands.
func (s *epicService) ContinueEpic(
	_ context.Context,
	_ *connect.Request[v1.ContinueEpicRequest],
) (*connect.Response[v1.ContinueEpicResponse], error) {
	return nil, refuseEpic(connect.CodeUnimplemented, "ContinueEpic is not implemented")
}
