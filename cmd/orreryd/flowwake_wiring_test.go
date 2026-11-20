package main

import (
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epics and the step are invented; the attention epic and its step come from the wiring
// tests' helpers.
const (
	flowWakeWiringEpicB = "flow-wiring-epic-b"
	flowWakeWiringEpicC = "flow-wiring-epic-c"
	flowWakeWiringStep  = "flow-wiring-step"
	// flowWakeWiringBranch is a branch state the store accepts; any other is refused by its check constraint.
	flowWakeWiringBranch = "created"
)

// flowWakeWiringRecorder is the interpreter wake a test gives the flow relay. It records each call.
type flowWakeWiringRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

// record is the interpreter wake: it records the epics it is handed.
func (r *flowWakeWiringRecorder) record(epics []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(epics))
}

// take returns the calls recorded since the last take.
func (r *flowWakeWiringRecorder) take() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := r.calls
	r.calls = nil
	return calls
}

// requireFlowWakeWiringCalls fails the test unless the calls recorded since the last take are want, in order.
func requireFlowWakeWiringCalls(t *testing.T, rec *flowWakeWiringRecorder, want [][]string) {
	t.Helper()
	got := rec.take()
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("interpreter wake calls = %q, want %q", got, want)
	}
}

// TestOrrerydFlowWakeStoreWriteWakesInterpreter checks that the store hook orreryd subscribes to wakes the interpreter
// with the epic of a store write that moves its revision. A write that moves no epic wakes nothing, and once the hooks
// are cleared at shutdown, a write wakes nothing.
func TestOrrerydFlowWakeStoreWriteWakesInterpreter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openLimitStore(t)
	createAttentionEpic(t, st, "")
	host := newSessionHost(slog.New(slog.DiscardHandler))
	rec := &flowWakeWiringRecorder{}
	host.subscribeFlowWake(st, rec.record)

	if err := st.ParkEpic(
		ctx, attentionWiringEpic, store.ParkClassBranch, "synthetic reason", time.Unix(attentionWiringStamp, 0),
	); err != nil {
		t.Fatalf("park epic %s: %v", attentionWiringEpic, err)
	}
	requireFlowWakeWiringCalls(t, rec, [][]string{{attentionWiringEpic}})

	// A step names the epic but moves no epic's revision, so nothing wakes.
	addOpenStep(t, st, flowWakeWiringStep, attentionWiringEpic)
	requireFlowWakeWiringCalls(t, rec, nil)

	clearStoreHooks(st)
	if err := st.SetEpicBranch(ctx, attentionWiringEpic, flowWakeWiringBranch); err != nil {
		t.Fatalf("set branch of epic %s: %v", attentionWiringEpic, err)
	}
	requireFlowWakeWiringCalls(t, rec, nil)
}

// TestOrrerydFlowWakeStepEndWakesItsEpic checks that the steps' EpicWake, which orreryd gives the steps, hands the epic
// of a step end to the interpreter's wake. Before the interpreter is set, it does nothing and does not block.
func TestOrrerydFlowWakeStepEndWakesItsEpic(t *testing.T) {
	t.Parallel()
	host := newSessionHost(slog.New(slog.DiscardHandler))
	rec := &flowWakeWiringRecorder{}

	host.stepEpicWake(flowWakeWiringEpicB)
	requireFlowWakeWiringCalls(t, rec, nil)

	host.flowWake.set(rec.record)
	host.stepEpicWake(flowWakeWiringEpicB)
	requireFlowWakeWiringCalls(t, rec, [][]string{{flowWakeWiringEpicB}})
}

// TestOrrerydFlowWakeStepFreedWakesRefusedEpics checks that the manager's StepFreed, which orreryd gives the manager,
// hands the epics a freed step slot refused to the interpreter's wake, and hands nothing for none.
func TestOrrerydFlowWakeStepFreedWakesRefusedEpics(t *testing.T) {
	t.Parallel()
	host := newSessionHost(slog.New(slog.DiscardHandler))
	rec := &flowWakeWiringRecorder{}
	host.flowWake.set(rec.record)

	host.stepFreedWake([]string{flowWakeWiringEpicC})
	requireFlowWakeWiringCalls(t, rec, [][]string{{flowWakeWiringEpicC}})

	host.stepFreedWake(nil)
	host.stepFreedWake([]string{})
	requireFlowWakeWiringCalls(t, rec, nil)
}
