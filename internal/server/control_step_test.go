package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// synthStepID is the step id the Steps fake finds, and the id the step refusal tests send.
const synthStepID = "step-1"

// synthOtherID is an id the Steps fake does not find.
const synthOtherID = "step-2"

// stepRefusedMessage is the fixed message for a session command on a step id.
const stepRefusedMessage = synthStepID + " is a step: drive it through Steps"

// fakeSteps is a recording StepClient. StepStatus finds only known, and answers err for every id when err is set. Any
// other method is never called by Control, and answers unimplemented.
type fakeSteps struct {
	known string
	err   error
}

func (f *fakeSteps) StepStatus(
	_ context.Context,
	req *connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	if req.Msg.GetId() != f.known {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic step not found"))
	}
	return connect.NewResponse(&enginev1.StepStatusResponse{}), nil
}

func (f *fakeSteps) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *fakeSteps) HoldStep(
	context.Context,
	*connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *fakeSteps) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *fakeSteps) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// newStepControlHandler returns the guarded handler that admits allowedLogin, forwards Control to engine, and asks
// steps before each session command. Its home is under t.TempDir().
func newStepControlHandler(
	t *testing.T,
	engine enginev1connect.EngineClient,
	steps server.StepClient,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins: []string{allowedLogin},
		Tmux:   newTmuxClient(t),
		Push:   newPushStore(t),
		Engine: engine,
		Steps:  steps,
		Home:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// stepSessionCalls returns the four session Control calls that a step id refuses, and Seen, each on id with every
// request field set. Each names the engine request it forwards and the reply it answers with.
func stepSessionCalls(id string) []controlCall {
	return []controlCall{
		{
			name: "Say",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Say(ctx, connect.NewRequest(&orreryv1.SayRequest{
					Id: id, Text: "synthetic text", PromptId: "synthetic-prompt",
				})))
			},
			engine: &enginev1.SayRequest{Id: id, Text: "synthetic text", PromptId: "synthetic-prompt"},
			reply:  &orreryv1.SayResponse{},
		},
		{
			name: "Answer",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Answer(ctx, connect.NewRequest(&orreryv1.AnswerRequest{
					Id: id, RequestId: "synthetic-request", Answer: "allow",
				})))
			},
			engine: &enginev1.AnswerRequest{Id: id, RequestId: "synthetic-request", Answer: "allow"},
			reply:  &orreryv1.AnswerResponse{},
		},
		{
			name: "Interrupt",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Interrupt(ctx, connect.NewRequest(&orreryv1.InterruptRequest{Id: id})))
			},
			engine: &enginev1.InterruptRequest{Id: id},
			reply:  &orreryv1.InterruptResponse{},
		},
		{
			name: "Stop",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Stop(ctx, connect.NewRequest(&orreryv1.StopRequest{Id: id})))
			},
			engine: &enginev1.StopRequest{Id: id},
			reply:  &orreryv1.StopResponse{},
		},
		{
			name: "Seen",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Seen(ctx, connect.NewRequest(&orreryv1.SeenRequest{Id: id})))
			},
			engine: &enginev1.SeenRequest{Id: id},
			reply:  &orreryv1.SeenResponse{},
		},
	}
}

func TestControlRefusesStepIDs(t *testing.T) {
	for _, tc := range stepSessionCalls(synthStepID) {
		if tc.name == "Seen" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			steps := &fakeSteps{known: synthStepID}
			control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

			_, err := tc.call(t.Context(), control)
			assertConnectError(t, err, connect.CodeFailedPrecondition, stepRefusedMessage)
			if calls := fake.recorded(); len(calls) != 0 {
				t.Fatalf("%s on a step forwarded %d requests, want none", tc.name, len(calls))
			}
		})
	}
}

func TestControlForwardsSessionIDsStepsDoNotFind(t *testing.T) {
	for _, tc := range stepSessionCalls(synthOtherID) {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			steps := &fakeSteps{known: synthStepID}
			control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

			got, err := tc.call(t.Context(), control)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !proto.Equal(got, tc.reply) {
				t.Fatalf("%s response = %v, want %v", tc.name, got, tc.reply)
			}
			calls := fake.recorded()
			if len(calls) != 1 || !proto.Equal(calls[0], tc.engine) {
				t.Fatalf("%s forwarded %v, want one request %v", tc.name, calls, tc.engine)
			}
		})
	}
}

func TestControlAnswersStepLookupFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "internal",
			err:  connect.NewError(connect.CodeInternal, errors.New("synthetic store detail")),
		},
		{
			name: "plain error",
			err:  errors.New("synthetic plain detail"),
		},
	}
	for _, lookup := range cases {
		for _, tc := range stepSessionCalls(synthStepID) {
			if tc.name == "Seen" {
				continue
			}
			t.Run(lookup.name+" "+tc.name, func(t *testing.T) {
				fake := &fakeEngine{}
				steps := &fakeSteps{known: synthStepID, err: lookup.err}
				control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

				_, err := tc.call(t.Context(), control)
				assertConnectError(t, err, connect.CodeInternal, "session command failed")
				if calls := fake.recorded(); len(calls) != 0 {
					t.Fatalf("%s after a failed step lookup forwarded %d requests, want none", tc.name, len(calls))
				}
			})
		}
	}
}

func TestControlForwardsSeenOnStepID(t *testing.T) {
	for _, tc := range stepSessionCalls(synthStepID) {
		if tc.name != "Seen" {
			continue
		}
		fake := &fakeEngine{}
		steps := &fakeSteps{known: synthStepID}
		control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

		got, err := tc.call(t.Context(), control)
		if err != nil {
			t.Fatalf("Seen on a step: %v", err)
		}
		if !proto.Equal(got, tc.reply) {
			t.Fatalf("Seen response = %v, want %v", got, tc.reply)
		}
		calls := fake.recorded()
		if len(calls) != 1 || !proto.Equal(calls[0], tc.engine) {
			t.Fatalf("Seen on a step forwarded %v, want one request %v", calls, tc.engine)
		}
	}
}
