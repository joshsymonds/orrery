package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// synthPaneID and synthPaneHash name a synthetic pane and the question it asks, for the pane Decline tests.
const (
	synthPaneID   = "%4"
	synthPaneHash = "synthetic-question-hash"
)

// TestDeclinePaneControlForwards declines a pane's question through Control. The engine receives the kind, the pane
// id and the question hash unchanged.
func TestDeclinePaneControlForwards(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	if _, err := control.Decline(t.Context(), connect.NewRequest(&orreryv1.DeclineRequest{
		Kind: "pane", Id: synthPaneID, QuestionHash: synthPaneHash,
	})); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	want := &enginev1.DeclineRequest{Kind: "pane", Id: synthPaneID, QuestionHash: synthPaneHash}
	calls := fake.recorded()
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("Decline forwarded %v, want one request %v", calls, want)
	}
}

// TestDeclinePaneControlSkipsStepCheck declines a pane whose id is a step's id. Control does not ask the steps about a
// pane, so the decline is forwarded once.
func TestDeclinePaneControlSkipsStepCheck(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	steps := &fakeSteps{known: synthStepID}
	control := controlClient(t, newStepControlHandler(t, newFakeOrreryd(t, fake), steps))

	if _, err := control.Decline(t.Context(), connect.NewRequest(&orreryv1.DeclineRequest{
		Kind: "pane", Id: synthStepID, QuestionHash: synthPaneHash,
	})); err != nil {
		t.Fatalf("Decline pane: %v", err)
	}
	if calls := fake.recorded(); len(calls) != 1 {
		t.Fatalf("Decline of a pane forwarded %d requests, want one", len(calls))
	}
}
