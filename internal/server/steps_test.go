package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// stepsLabeled is the id of a step the fake finds with an epic label, and stepsUnlabeled is the id of one it finds
// with no label.
const (
	stepsLabeled   = "step-labeled"
	stepsUnlabeled = "step-unlabeled"
	stepsEpic      = "repo/epic"
	stepsNudgeText = "synthetic nudge"
	stepsRefusal   = "synthetic refusal"
)

// stepsCall is one forwarded call the fake recorded: the op name, the step id and, for a nudge, its text.
type stepsCall struct {
	op   string
	id   string
	text string
}

// stepsFake is a recording StepClient. StepStatus finds stepsLabeled and stepsUnlabeled, and answers statusErr for
// every id when it is set. Hold, Nudge and Stop record the call and answer forwardErr when it is set. StartStep is not
// part of StepClient, so no test can reach it through the fake.
type stepsFake struct {
	statusErr  error
	forwardErr error
	calls      []stepsCall
}

func (f *stepsFake) StepStatus(
	_ context.Context,
	req *connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	switch req.Msg.GetId() {
	case stepsLabeled:
		step := &enginev1.Step{Id: stepsLabeled, Epic: stepsEpic}
		return connect.NewResponse(&enginev1.StepStatusResponse{Step: step}), nil
	case stepsUnlabeled:
		return connect.NewResponse(&enginev1.StepStatusResponse{Step: &enginev1.Step{Id: stepsUnlabeled}}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic step not found"))
}

func (f *stepsFake) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *stepsFake) HoldStep(
	_ context.Context,
	req *connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	f.calls = append(f.calls, stepsCall{op: "hold", id: req.Msg.GetId()})
	if f.forwardErr != nil {
		return nil, f.forwardErr
	}
	return connect.NewResponse(&enginev1.HoldStepResponse{}), nil
}

func (f *stepsFake) NudgeStep(
	_ context.Context,
	req *connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	f.calls = append(f.calls, stepsCall{op: "nudge", id: req.Msg.GetId(), text: req.Msg.GetText()})
	if f.forwardErr != nil {
		return nil, f.forwardErr
	}
	return connect.NewResponse(&enginev1.NudgeStepResponse{}), nil
}

func (f *stepsFake) StopStep(
	_ context.Context,
	req *connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	f.calls = append(f.calls, stepsCall{op: "stop", id: req.Msg.GetId()})
	if f.forwardErr != nil {
		return nil, f.forwardErr
	}
	return connect.NewResponse(&enginev1.StopStepResponse{}), nil
}

// stepsClient serves newStepsHandler(steps, 0) on an httptest server and returns a generated Steps client for it.
func stepsClient(t *testing.T, steps StepClient) orreryv1connect.StepsClient {
	t.Helper()
	path, handler := newStepsHandler(steps, 0)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewStepsClient(srv.Client(), srv.URL)
}

// stepsOp is one Steps call under test: its name, the op name the fake records for it, and a call on a step id.
type stepsOp struct {
	name string
	call func(ctx context.Context, c orreryv1connect.StepsClient, id string) error
	op   string
}

// stepsOps returns the three Steps calls the forwarder carries, each with the fake's op name.
func stepsOps() []stepsOp {
	return []stepsOp{
		{
			name: "Hold",
			op:   "hold",
			call: func(ctx context.Context, c orreryv1connect.StepsClient, id string) error {
				_, err := c.Hold(ctx, connect.NewRequest(&orreryv1.StepsHoldRequest{Id: id}))
				return err
			},
		},
		{
			name: "Nudge",
			op:   "nudge",
			call: func(ctx context.Context, c orreryv1connect.StepsClient, id string) error {
				_, err := c.Nudge(ctx, connect.NewRequest(&orreryv1.StepsNudgeRequest{Id: id, Text: stepsNudgeText}))
				return err
			},
		},
		{
			name: "Stop",
			op:   "stop",
			call: func(ctx context.Context, c orreryv1connect.StepsClient, id string) error {
				_, err := c.Stop(ctx, connect.NewRequest(&orreryv1.StepsStopRequest{Id: id}))
				return err
			},
		},
	}
}

// requireConnectError asserts err is a Connect error with code and message.
func requireConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a connect error", err)
	}
	if connectErr.Code() != code {
		t.Fatalf("code = %v, want %v", connectErr.Code(), code)
	}
	if connectErr.Message() != message {
		t.Fatalf("message = %q, want %q", connectErr.Message(), message)
	}
}

func TestStepsForwardsLabeledStepOnce(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			fake := &stepsFake{}
			client := stepsClient(t, fake)
			if err := op.call(t.Context(), client, stepsLabeled); err != nil {
				t.Fatalf("%s: %v", op.name, err)
			}
			if len(fake.calls) != 1 {
				t.Fatalf("forwarded %d calls, want 1: %+v", len(fake.calls), fake.calls)
			}
			got := fake.calls[0]
			if got.op != op.op || got.id != stepsLabeled {
				t.Fatalf("forwarded %+v, want op %q on %q", got, op.op, stepsLabeled)
			}
			if op.op == "nudge" && got.text != stepsNudgeText {
				t.Fatalf("nudge text = %q, want %q", got.text, stepsNudgeText)
			}
		})
	}
}

func TestStepsRefusesUnknownAndUnlabeledSteps(t *testing.T) {
	for _, op := range stepsOps() {
		for _, id := range []string{"step-unknown", stepsUnlabeled} {
			t.Run(op.name+"/"+id, func(t *testing.T) {
				fake := &stepsFake{}
				client := stepsClient(t, fake)
				err := op.call(t.Context(), client, id)
				requireConnectError(t, err, connect.CodeNotFound, "step not found")
				if len(fake.calls) != 0 {
					t.Fatalf("forwarded %+v, want nothing", fake.calls)
				}
			})
		}
	}
}

func TestStepsPassesForwardedFailedPreconditionThrough(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			fake := &stepsFake{forwardErr: connect.NewError(connect.CodeFailedPrecondition, errors.New(stepsRefusal))}
			client := stepsClient(t, fake)
			err := op.call(t.Context(), client, stepsLabeled)
			requireConnectError(t, err, connect.CodeFailedPrecondition, stepsRefusal)
			if len(fake.calls) != 1 {
				t.Fatalf("forwarded %d calls, want 1", len(fake.calls))
			}
		})
	}
}

func TestStepsAnswersUnavailableAsTranslateErrorDoes(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			unavailable := connect.NewError(connect.CodeUnavailable, errors.New("synthetic engine down"))
			fake := &stepsFake{forwardErr: unavailable}
			client := stepsClient(t, fake)
			err := op.call(t.Context(), client, stepsLabeled)
			requireConnectError(t, err, connect.CodeUnavailable, controlUnavailable)
		})
	}
}

func TestStepsAnswersStatusUnavailableAsTranslateErrorDoes(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			unavailable := connect.NewError(connect.CodeUnavailable, errors.New("synthetic engine down"))
			fake := &stepsFake{statusErr: unavailable}
			client := stepsClient(t, fake)
			err := op.call(t.Context(), client, stepsLabeled)
			requireConnectError(t, err, connect.CodeUnavailable, controlUnavailable)
			if len(fake.calls) != 0 {
				t.Fatalf("forwarded %+v after a failed lookup, want nothing", fake.calls)
			}
		})
	}
}

func TestStepsNilClientAnswersUnavailable(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			client := stepsClient(t, nil)
			err := op.call(t.Context(), client, stepsLabeled)
			requireConnectError(t, err, connect.CodeUnavailable, "step service unavailable")
		})
	}
}

func TestStepsZeroTimeoutIsTheDefault(t *testing.T) {
	fake := &stepsFake{}
	client := stepsClient(t, fake)
	if err := stepsOps()[0].call(t.Context(), client, stepsLabeled); err != nil {
		t.Fatalf("Hold with a zero timeout: %v; a zero timeout must be the default, not an instant deadline", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("forwarded %d calls, want 1", len(fake.calls))
	}
}
