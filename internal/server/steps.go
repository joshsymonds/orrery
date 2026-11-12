package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// stepNotFound is the fixed message for a Steps call on an id that is not a labeled step.
const stepNotFound = "step not found"

// stepsUnavailable is the fixed message for a Steps call when serve has no step client.
const stepsUnavailable = "step service unavailable"

// stepsServer answers orrery.v1.Steps by forwarding Hold, Nudge and Stop to steps. Each call first asks StepStatus, and
// forwards only a step whose row carries an epic label. A nil steps answers unavailable.
type stepsServer struct {
	steps   StepClient
	timeout time.Duration
}

// Hold forwards a hold on a labeled step to the engine.
func (s stepsServer) Hold(
	ctx context.Context,
	req *connect.Request[orreryv1.StepsHoldRequest],
) (*connect.Response[orreryv1.StepsHoldResponse], error) {
	id := req.Msg.GetId()
	err := s.forward(ctx, id, func(ctx context.Context, steps StepClient) error {
		_, err := steps.HoldStep(ctx, connect.NewRequest(&enginev1.HoldStepRequest{Id: id}))
		return wrapEngine("hold", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StepsHoldResponse{}), nil
}

// Nudge forwards the owner's text to a labeled step.
func (s stepsServer) Nudge(
	ctx context.Context,
	req *connect.Request[orreryv1.StepsNudgeRequest],
) (*connect.Response[orreryv1.StepsNudgeResponse], error) {
	id := req.Msg.GetId()
	text := req.Msg.GetText()
	err := s.forward(ctx, id, func(ctx context.Context, steps StepClient) error {
		_, err := steps.NudgeStep(ctx, connect.NewRequest(&enginev1.NudgeStepRequest{Id: id, Text: text}))
		return wrapEngine("nudge", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StepsNudgeResponse{}), nil
}

// Stop forwards a stop on a labeled step to the engine.
func (s stepsServer) Stop(
	ctx context.Context,
	req *connect.Request[orreryv1.StepsStopRequest],
) (*connect.Response[orreryv1.StepsStopResponse], error) {
	id := req.Msg.GetId()
	err := s.forward(ctx, id, func(ctx context.Context, steps StepClient) error {
		_, err := steps.StopStep(ctx, connect.NewRequest(&enginev1.StopStepRequest{Id: id}))
		return wrapEngine("stop", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StepsStopResponse{}), nil
}

// forward answers unavailable for a nil client, asks StepStatus about id, and then runs call under the timeout. A
// lookup that finds no labeled step answers not_found and runs nothing.
func (s stepsServer) forward(
	ctx context.Context,
	id string,
	call func(context.Context, StepClient) error,
) error {
	if s.steps == nil {
		return listError(connect.CodeUnavailable, stepsUnavailable)
	}
	if err := s.labeled(ctx, id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := call(ctx, s.steps); err != nil {
		return translateError(err)
	}
	return nil
}

// labeled returns nil when StepStatus finds id with an epic label. not_found, and a row with no label, answer not_found
// "step not found". Any other StepStatus error answers as translateError does. The lookup runs under the timeout.
func (s stepsServer) labeled(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := s.steps.StepStatus(ctx, connect.NewRequest(&enginev1.StepStatusRequest{Id: id}))
	var connectErr *connect.Error
	switch {
	case err != nil && errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound:
		return listError(connect.CodeNotFound, stepNotFound)
	case err != nil:
		return translateError(err)
	case resp.Msg.GetStep().GetEpic() == "":
		return listError(connect.CodeNotFound, stepNotFound)
	}
	return nil
}

// newStepsHandler returns the path and handler that serve Steps over steps, reading at most maxControlBody bytes of each
// request. A zero timeout is defaultControlTimeout. Steps has no start: serve forwards no StartStep.
func newStepsHandler(steps StepClient, timeout time.Duration) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewStepsHandler(
		stepsServer{steps: steps, timeout: timeout},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
