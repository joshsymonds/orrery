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

// TestHeldQueueControlWithdrawForwardsAndReturnsTheTexts withdraws one held message through Control. The engine receives
// the session id and the prompt id, and the call answers with the texts the engine took back, in the engine's order.
func TestHeldQueueControlWithdrawForwardsAndReturnsTheTexts(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	resp, err := control.Withdraw(t.Context(), connect.NewRequest(&orreryv1.WithdrawRequest{
		Id: synthSessionID, PromptId: "p-held",
	}))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if want := fakeWithdrawnTexts(); !slices.Equal(resp.Msg.GetTexts(), want) {
		t.Fatalf("Withdraw texts = %q, want %q", resp.Msg.GetTexts(), want)
	}
	want := &enginev1.WithdrawRequest{Id: synthSessionID, PromptId: "p-held"}
	if calls := fake.recorded(); len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("Withdraw forwarded %v, want one request %v", calls, want)
	}
}

// TestHeldQueueControlWithdrawPassesFailedPreconditionThrough makes the engine refuse a prompt id it does not hold.
// Control answers the caller with the same code and the engine's message.
func TestHeldQueueControlWithdrawPassesFailedPreconditionThrough(t *testing.T) {
	t.Parallel()
	refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New("synthetic no held message"))
	fake := &fakeEngine{err: refusal}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	_, err := control.Withdraw(t.Context(), connect.NewRequest(&orreryv1.WithdrawRequest{
		Id: synthSessionID, PromptId: "p-gone",
	}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, "synthetic no held message")
}

// TestHeldQueueControlWithdrawRefusesAStepWithoutForward withdraws with a step id. Control refuses it as
// failed_precondition and forwards nothing to the engine.
func TestHeldQueueControlWithdrawRefusesAStepWithoutForward(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	steps := &fakeSteps{known: synthStepID}
	control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

	_, err := control.Withdraw(t.Context(), connect.NewRequest(&orreryv1.WithdrawRequest{Id: synthStepID}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, stepRefusedMessage)
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("Withdraw on a step forwarded %d requests, want none", len(calls))
	}
}
