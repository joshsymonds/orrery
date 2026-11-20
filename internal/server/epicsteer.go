package server

import (
	"context"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// Move forwards the move of the named epic's steering to the session the request names to orreryd, which records it.
func (e epicsServer) Move(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsMoveRequest],
) (*connect.Response[orreryv1.EpicsMoveResponse], error) {
	id, session := req.Msg.GetId(), req.Msg.GetSession()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.MoveEpic(ctx, connect.NewRequest(&enginev1.MoveEpicRequest{Id: id, Session: session}))
		return wrapEngine("move", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsMoveResponse{}), nil
}

// Continue forwards the continue of the named epic to orreryd, which starts a steering session for it and moves its
// steering to that session, and answers the session orreryd started.
func (e epicsServer) Continue(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsContinueRequest],
) (*connect.Response[orreryv1.EpicsContinueResponse], error) {
	id := req.Msg.GetId()
	var reply *connect.Response[enginev1.ContinueEpicResponse]
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		var err error
		reply, err = commands.ContinueEpic(ctx, connect.NewRequest(&enginev1.ContinueEpicRequest{Id: id}))
		return wrapEngine("continue", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsContinueResponse{Session: reply.Msg.GetSession()}), nil
}
