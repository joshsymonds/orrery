package server

import (
	"context"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// epicCommandsUnavailable is the fixed message for an epic command when serve has no epic command client.
const epicCommandsUnavailable = "epic command service unavailable"

// Pause forwards a pause of the named epic to orreryd, so its planner starts no turn.
func (e epicsServer) Pause(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsPauseRequest],
) (*connect.Response[orreryv1.EpicsPauseResponse], error) {
	id := req.Msg.GetId()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.PauseEpic(ctx, connect.NewRequest(&enginev1.PauseEpicRequest{Id: id}))
		return wrapEngine("pause", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsPauseResponse{}), nil
}

// Resume forwards a resume of the named epic to orreryd, and answers the limited steps it resumed and the limited steps
// a full step cap left limited.
func (e epicsServer) Resume(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsResumeRequest],
) (*connect.Response[orreryv1.EpicsResumeResponse], error) {
	id := req.Msg.GetId()
	var reply *connect.Response[enginev1.ResumeEpicResponse]
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		var err error
		reply, err = commands.ResumeEpic(ctx, connect.NewRequest(&enginev1.ResumeEpicRequest{Id: id}))
		return wrapEngine("resume", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsResumeResponse{
		Resumed:      reply.Msg.GetResumed(),
		StillLimited: reply.Msg.GetStillLimited(),
	}), nil
}

// Stop forwards a stop of the named epic to orreryd, which pauses it and stops the step of its running turn.
func (e epicsServer) Stop(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsStopRequest],
) (*connect.Response[orreryv1.EpicsStopResponse], error) {
	id := req.Msg.GetId()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.StopEpic(ctx, connect.NewRequest(&enginev1.StopEpicRequest{Id: id}))
		return wrapEngine("stop", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsStopResponse{}), nil
}

// Retry forwards a retry of the named epic to orreryd: it ends the epic's park, or reruns the parked runs of the node
// the request names.
func (e epicsServer) Retry(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsRetryRequest],
) (*connect.Response[orreryv1.EpicsRetryResponse], error) {
	id := req.Msg.GetId()
	node := req.Msg.GetNode()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.RetryEpic(ctx, connect.NewRequest(&enginev1.RetryEpicRequest{Id: id, Node: node}))
		return wrapEngine("retry", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsRetryResponse{}), nil
}

// Kick forwards the owner's text on the named epic to orreryd, which records it as a decision and wakes the epic's
// planner with it at the epic's next plan run.
func (e epicsServer) Kick(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsKickRequest],
) (*connect.Response[orreryv1.EpicsKickResponse], error) {
	id, text := req.Msg.GetId(), req.Msg.GetText()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.KickEpic(ctx, connect.NewRequest(&enginev1.KickEpicRequest{Id: id, Text: text}))
		return wrapEngine("kick", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsKickResponse{}), nil
}

// Answer forwards the owner's answer to orreryd with the decision id, text and question the request names, and answers
// the decision orreryd recorded with any warning it gave.
func (e epicsServer) Answer(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsAnswerRequest],
) (*connect.Response[orreryv1.EpicsAnswerResponse], error) {
	msg := req.Msg
	var reply *connect.Response[enginev1.AnswerEpicResponse]
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		var err error
		reply, err = commands.AnswerEpic(ctx, connect.NewRequest(&enginev1.AnswerEpicRequest{
			EpicId: msg.GetEpicId(), Id: msg.GetId(), Text: msg.GetText(), Question: msg.GetQuestion(),
		}))
		return wrapEngine("answer", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsAnswerResponse{
		Decision: epicDecisionMessage(reply.Msg.GetDecision()),
		Warning:  reply.Msg.GetWarning(),
	}), nil
}

// Seen forwards the seen mark of the named epic to orreryd, which stores the time of this call as the mark.
func (e epicsServer) Seen(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsSeenRequest],
) (*connect.Response[orreryv1.EpicsSeenResponse], error) {
	id := req.Msg.GetId()
	err := e.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.EpicsClient) error {
		_, err := commands.SeeEpic(ctx, connect.NewRequest(&enginev1.SeeEpicRequest{Id: id}))
		return wrapEngine("seen", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.EpicsSeenResponse{}), nil
}

// forwardCommand runs call against the epic command client. A nil client answers unavailable. A call whose connection
// was never made is retried while rpcretry.NeverConnected accepts its error: a call cut after it connected may already
// have been taken, so it is answered and not sent again. Any other error answers as translateError does.
func (e epicsServer) forwardCommand(
	ctx context.Context,
	call func(context.Context, enginev1connect.EpicsClient) error,
) error {
	if e.commands == nil {
		return listError(connect.CodeUnavailable, epicCommandsUnavailable)
	}
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		return call(ctx, e.commands)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// epicDecisionMessage returns the EpicDecision message of decision, the decision orreryd recorded.
func epicDecisionMessage(decision *enginev1.EpicDecision) *orreryv1.EpicDecision {
	return &orreryv1.EpicDecision{
		Seq:      decision.GetSeq(),
		Id:       decision.GetId(),
		Text:     decision.GetText(),
		Time:     decision.GetTime(),
		Question: decision.GetQuestion(),
	}
}
