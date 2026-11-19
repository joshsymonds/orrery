package enginehost

import (
	"context"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEndComesBackStepStatusReadStillSendsStepEnd has a headless caller read its ended step through StepStatus with
// its caller headers. The step's end still goes to the caller: the first pass sends exactly one step_end for the step,
// and a second pass sends none.
func TestEndComesBackStepStatusReadStillSendsStepEnd(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	const caller = "session-1"
	add := store.EngineAdd{ID: caller, Dir: "/work", State: "idle", Kind: store.KindEngine}
	if err := f.store.ApplyEngine(t.Context(), []store.EngineOp{add}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine add session %s: %v", caller, err)
	}
	addEndedCallerStep(t, f.store, store.EpicCaller{Session: caller}, "all green")

	status := connect.NewRequest(&v1.StepStatusRequest{Id: callerStepID})
	status.Header().Set(protocol.HeaderSession, caller)
	svc := &stepService{steps: f.steps}
	if _, err := svc.StepStatus(t.Context(), status); err != nil {
		t.Fatalf("StepStatus: %v", err)
	}

	var delivered []DeliverRequest
	sender := NewCallerSender(CallerSenderConfig{
		Deliver: func(_ context.Context, req DeliverRequest) error {
			delivered = append(delivered, req)
			return nil
		},
		WakePane: func(context.Context, string, string, string) (bool, error) {
			t.Error("a pane caller was woken in a headless test")
			return false, nil
		},
		Store: f.store,
		Now:   fixedClock,
	})
	passStep(t, stepEndPass(sender, f.store))
	want := []DeliverRequest{{ID: caller, Text: wantStepEndBlock("all green"), PromptID: callerStepEvent}}
	if !reflect.DeepEqual(delivered, want) {
		t.Fatalf("deliveries after the status read = %+v, want %+v", delivered, want)
	}

	passStep(t, stepEndPass(sender, f.store))
	if len(delivered) != len(want) {
		t.Fatalf("deliveries after a second pass = %+v, want no more than %+v", delivered, want)
	}
}
