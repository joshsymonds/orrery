package server_test

import (
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// TestDeclineControlForwards declines an open request through Control. The engine receives the session id and the
// request id unchanged, and Control answers with the texts the engine returns.
func TestDeclineControlForwards(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	resp, err := control.Decline(t.Context(), connect.NewRequest(&orreryv1.DeclineRequest{
		Id: synthSessionID, RequestId: "synthetic-request",
	}))
	if err != nil {
		t.Fatalf("Decline: %v", err)
	}
	want := &enginev1.DeclineRequest{Id: synthSessionID, RequestId: "synthetic-request"}
	calls := fake.recorded()
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("Decline forwarded %v, want one request %v", calls, want)
	}
	if got := resp.Msg.GetTexts(); !slices.Equal(got, fakeInterruptedTexts()) {
		t.Fatalf("Decline texts = %q, want %q", got, fakeInterruptedTexts())
	}
}

// TestDeclineControlRefusesStep declines on a step id. Control answers failed_precondition before any forward.
func TestDeclineControlRefusesStep(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	steps := &fakeSteps{known: synthStepID}
	control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

	_, err := control.Decline(t.Context(), connect.NewRequest(&orreryv1.DeclineRequest{
		Id: synthStepID, RequestId: "synthetic-request",
	}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, stepRefusedMessage)
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("Decline on a step forwarded %d requests, want none", len(calls))
	}
}

// TestDeclineControlPassesRefusal makes the engine refuse a request that is not open. Control answers the caller with
// the same code and the engine's message.
func TestDeclineControlPassesRefusal(t *testing.T) {
	t.Parallel()
	refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New("synthetic question not open"))
	fake := &fakeEngine{err: refusal}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	_, err := control.Decline(t.Context(), connect.NewRequest(&orreryv1.DeclineRequest{
		Id: synthSessionID, RequestId: "synthetic-request",
	}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, "synthetic question not open")
}
