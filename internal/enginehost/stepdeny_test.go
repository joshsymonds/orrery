package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// startDenyStep starts the step stepA on m with prompt, handing its turn ends to obs.
func startDenyStep(t *testing.T, m *Manager, prompt string, obs StepObserver) {
	t.Helper()
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: prompt, Agent: "reviewer", Schema: stepSchema, Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep with prompt %q: %v", prompt, err)
	}
}

// requireNeverWaits fails when history records a request, the state waiting, or a question on the step's row.
func requireNeverWaits(t *testing.T, history store.EngineHistory) {
	t.Helper()
	for _, event := range history.Events {
		if event.Kind == store.EventEngineRequest {
			t.Fatalf("step history records a request, want none")
		}
	}
	for _, fields := range changedFields(t, history) {
		if fields["state"] == string(protocol.StateWaiting) {
			t.Fatalf("step history sets state waiting: %v", fields)
		}
		if fields["question"] != "" {
			t.Fatalf("step history sets question %q: %v", fields["question"], fields)
		}
	}
}

func TestStepAskIsDeniedWithoutAnswer(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	obs := &stepObserver{}
	startDenyStep(t, m, "ASK", obs)

	history := waitHistory(t, path, stepA, "the step's answered reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1")
	})
	requireNeverWaits(t, history)
	waitHistory(t, path, stepA, "the step's done state", func(h store.EngineHistory) bool {
		return lastState(t, h) == string(protocol.StateDone)
	})
	waitReturns(t, obs, 1)
}

func TestStepPermitIsDeniedWithoutAnswer(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	obs := &stepObserver{}
	startDenyStep(t, m, "PERMIT", obs)

	history := waitHistory(t, path, stepA, "the step's denied reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake denied 1")
	})
	requireNeverWaits(t, history)
	waitHistory(t, path, stepA, "the step's done state", func(h store.EngineHistory) bool {
		return lastState(t, h) == string(protocol.StateDone)
	})
	waitReturns(t, obs, 1)
}

func TestSessionAskStillWaitsForAnswer(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	if question, ok := lastQuestion(t, history); !ok || question != "Which color?" {
		t.Fatalf("question = %q (set %v), want %q", question, ok, "Which color?")
	}
}
