package enginehost

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestQueueLifecycleOpsRecordEachStateUnderItsWriteID feeds the recorder each of claude's five lifecycle states for one
// prompt. Each becomes the prompt state the store keeps, claude's cancelled as the store's canceled. A state the
// recorder does not keep, and a report with no uuid, yield no op.
func TestQueueLifecycleOpsRecordEachStateUnderItsWriteID(t *testing.T) {
	t.Parallel()
	r := newRecorder(recorderID)
	cases := []struct {
		state session.LifecycleState
		want  string
	}{
		{session.LifecycleQueued, store.PromptQueued},
		{session.LifecycleStarted, store.PromptStarted},
		{session.LifecycleCompleted, store.PromptCompleted},
		{session.LifecycleCancelled, store.PromptCancelled},
		{session.LifecycleDiscarded, store.PromptDiscarded},
	}
	for _, tc := range cases {
		got := r.ops(session.Lifecycle{CommandUUID: "uuid-1", State: tc.state})
		want := []store.EngineOp{store.EnginePromptState{ID: recorderID, WriteID: "uuid-1", State: tc.want}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ops of lifecycle %q = %#v, want %#v", tc.state, got, want)
		}
	}

	if got := r.ops(session.Lifecycle{CommandUUID: "uuid-1", State: "unknown"}); len(got) != 0 {
		t.Errorf("ops of an unknown lifecycle state = %#v, want none", got)
	}
	if got := r.ops(session.Lifecycle{State: session.LifecycleStarted}); len(got) != 0 {
		t.Errorf("ops of a lifecycle with an empty uuid = %#v, want none", got)
	}
}

// TestQueueLifecycleExitDiscardsThePromptsClaudeDidNotStart checks that a clean exit's ops discard the prompts claude
// had not started, with EnginePromptsDiscard ahead of the exit's state set.
func TestQueueLifecycleExitDiscardsThePromptsClaudeDidNotStart(t *testing.T) {
	t.Parallel()
	r := newRecorder(recorderID)
	want := []store.EngineOp{
		store.EngineTextClear{ID: recorderID},
		store.EnginePromptsDiscard{ID: recorderID},
		store.EngineSet{ID: recorderID, Changes: store.EngineChanges{State: new(stateStopped)}},
	}
	if got := r.ops(session.Exited{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("ops of a clean exit = %#v, want %#v", got, want)
	}
}

// TestQueueLifecycleFakeClaudeOpeningPromptEndsCompleted starts a session through the Manager with the fake claude, which
// reports the opening prompt queued, started and then completed before its turn's result. Once the turn has ended, the
// store lists the opening prompt with its newest state completed.
func TestQueueLifecycleFakeClaudeOpeningPromptEndsCompleted(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "plain one"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the opening turn to end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && lastState(t, h) == string(protocol.StateDone)
	})

	states, err := store.NewReader(path).PromptStates(t.Context(), id)
	if err != nil {
		t.Fatalf("PromptStates: %v", err)
	}
	want := []store.PromptState{{Text: "plain one", State: store.PromptCompleted}}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("PromptStates = %+v, want %+v", states, want)
	}
}
