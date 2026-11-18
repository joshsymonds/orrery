package server_test

import (
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// TestEscapeQueuedInterruptReturnsCancelledTexts checks that Control.Interrupt forwards the session id and answers with
// the texts the engine cancelled from the queue, in send order.
func TestEscapeQueuedInterruptReturnsCancelledTexts(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	resp, err := control.Interrupt(t.Context(), connect.NewRequest(&orreryv1.InterruptRequest{Id: synthSessionID}))
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if got, want := resp.Msg.GetTexts(), fakeInterruptedTexts(); !slices.Equal(got, want) {
		t.Fatalf("Interrupt texts = %q, want %q in send order", got, want)
	}
	want := &enginev1.InterruptRequest{Id: synthSessionID}
	if calls := fake.recorded(); len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("Interrupt forwarded %v, want one request %v", calls, want)
	}
}

// TestEscapeQueuedInterruptRefusesStepIDWithoutForward checks that Control.Interrupt refuses a step id and forwards
// nothing to the engine.
func TestEscapeQueuedInterruptRefusesStepIDWithoutForward(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	steps := &fakeSteps{known: synthStepID}
	control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

	_, err := control.Interrupt(t.Context(), connect.NewRequest(&orreryv1.InterruptRequest{Id: synthStepID}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, stepRefusedMessage)
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("Interrupt on a step forwarded %d requests, want none", len(calls))
	}
}
