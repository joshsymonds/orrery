package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
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
	clock   rpcretry.Clock
}

// Hold forwards a hold on a labeled step to the engine.
func (s stepsServer) Hold(
	ctx context.Context,
	req *connect.Request[orreryv1.StepsHoldRequest],
) (*connect.Response[orreryv1.StepsHoldResponse], error) {
	id := req.Msg.GetId()
	err := s.forward(ctx, id, rpcretry.Unavailable, func(ctx context.Context, steps StepClient) error {
		_, err := steps.HoldStep(ctx, connect.NewRequest(&enginev1.HoldStepRequest{Id: id}))
		return wrapEngine("hold", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StepsHoldResponse{}), nil
}

// Nudge forwards the owner's text to a labeled step. It is retried only when its connection was never made, so a nudge
// cut mid-call, which the step may already have taken, is answered and not sent again.
func (s stepsServer) Nudge(
	ctx context.Context,
	req *connect.Request[orreryv1.StepsNudgeRequest],
) (*connect.Response[orreryv1.StepsNudgeResponse], error) {
	id := req.Msg.GetId()
	text := req.Msg.GetText()
	err := s.forward(ctx, id, rpcretry.NeverConnected, func(ctx context.Context, steps StepClient) error {
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
	err := s.forward(ctx, id, rpcretry.Unavailable, func(ctx context.Context, steps StepClient) error {
		_, err := steps.StopStep(ctx, connect.NewRequest(&enginev1.StopStepRequest{Id: id}))
		return wrapEngine("stop", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StepsStopResponse{}), nil
}

// forward answers unavailable for a nil client, asks StepStatus about id, and then runs call. A lookup that finds no
// labeled step answers not_found and runs nothing. call is retried while retryable accepts its error.
func (s stepsServer) forward(
	ctx context.Context,
	id string,
	retryable func(error) bool,
	call func(context.Context, StepClient) error,
) error {
	if s.steps == nil {
		return listError(connect.CodeUnavailable, stepsUnavailable)
	}
	if err := s.labeled(ctx, id); err != nil {
		return err
	}
	err := retryUnder(ctx, s.clock, s.timeout, retryable, func(ctx context.Context) error {
		return call(ctx, s.steps)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// labeled returns nil when StepStatus finds id with an epic label. not_found, and a row with no label, answer not_found
// "step not found". Any other StepStatus error answers as translateError does. An unavailable answer is retried.
func (s stepsServer) labeled(ctx context.Context, id string) error {
	var resp *connect.Response[enginev1.StepStatusResponse]
	err := retryUnder(ctx, s.clock, s.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = s.steps.StepStatus(ctx, connect.NewRequest(&enginev1.StepStatusRequest{Id: id}))
		if err != nil {
			return fmt.Errorf("server: step status: %w", err)
		}
		return nil
	})
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

// retryUnder runs attempt under timeout, and while retryable accepts its error and rpcretry.Window has not passed on
// clock, runs it again. It returns the error of the last attempt. rpcretry.Do returns a context error instead when ctx
// ends the retries, and translateError answers that as an internal failure, so the attempt's own error is returned.
func retryUnder(
	ctx context.Context,
	clock rpcretry.Clock,
	timeout time.Duration,
	retryable func(error) bool,
	attempt func(context.Context) error,
) error {
	var last error
	err := rpcretry.Do(ctx, clock, retryable, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		last = attempt(ctx)
		return last
	})
	if err != nil {
		return last
	}
	return nil
}

// newStepsHandler returns the path and handler that serve Steps over steps, reading at most maxControlBody bytes of each
// request. A zero timeout is defaultControlTimeout. clock times the retries. Steps has no start: serve forwards no
// StartStep.
func newStepsHandler(steps StepClient, timeout time.Duration, clock rpcretry.Clock) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewStepsHandler(
		stepsServer{steps: steps, timeout: timeout, clock: clock},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
