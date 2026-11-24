package enginehost

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// promptStatesOf returns the prompt states of session id in the store at path, failing the test when the read fails.
func promptStatesOf(t *testing.T, path, id string) []store.PromptState {
	t.Helper()
	states, err := store.NewReader(path).PromptStates(t.Context(), id)
	if err != nil {
		t.Fatalf("PromptStates %s: %v", id, err)
	}
	return states
}

// promptQueued reports whether the prompt promptID of session id is stored with the newest state queued. A read that
// fails reports false, so a caller waiting on it reads again.
func promptQueued(t *testing.T, path, id, promptID string) bool {
	t.Helper()
	states, err := store.NewReader(path).PromptStates(t.Context(), id)
	if err != nil {
		return false
	}
	for _, state := range states {
		if state.PromptID == promptID {
			return state.State == store.PromptQueued
		}
	}
	return false
}

// TestQueueLifecycleStopDiscardsTheQueuedSay holds a Say queued during a HANG turn, then stops the session. The Say is
// stored discarded, and the opening prompt, which claude started, keeps its started state.
func TestQueueLifecycleStopDiscardsTheQueuedSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id := holdWithdrawnLine(t, m, path, t.TempDir())
	waitUntil(t, engineOf(m), "the held line queued", func() bool {
		return promptQueued(t, path, id, "p-held")
	})

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []store.PromptState{
		{Text: "HANG", State: store.PromptStarted},
		{PromptID: "p-held", Text: "withdrawn words", Sender: store.PromptSenderOwner, State: store.PromptDiscarded},
	}
	if got := promptStatesOf(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates after Stop = %+v, want %+v", got, want)
	}
}

// TestQueueLifecycleCloseDiscardsTheQueuedSay holds a Say queued during a HANG turn, then closes a manager with no
// holder scope. Close records the interrupted session, and the Say is stored discarded.
func TestQueueLifecycleCloseDiscardsTheQueuedSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id := holdWithdrawnLine(t, m, path, t.TempDir())
	waitUntil(t, engineOf(m), "the held line queued", func() bool {
		return promptQueued(t, path, id, "p-held")
	})

	m.Close()
	want := []store.PromptState{
		{Text: "HANG", State: store.PromptStarted},
		{PromptID: "p-held", Text: "withdrawn words", Sender: store.PromptSenderOwner, State: store.PromptDiscarded},
	}
	if got := promptStatesOf(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates after Close = %+v, want %+v", got, want)
	}
}

// TestQueueLifecycleProcessEndingDiscardsTheQueuedSay starts a DIEQUEUED session and sends it a Say once its first
// block is stored. The fake reads the Say, queues it and exits on its own, with no Stop or Close, so the turn it ends
// fails the session. The session's exit stores the Say discarded, and the opening prompt keeps its started state.
func TestQueueLifecycleProcessEndingDiscardsTheQueuedSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "DIEQUEUED"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake diequeued 1")
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "dying words", PromptID: "p-dying"}); err != nil {
		t.Fatalf("Say during the turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the process's exit", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})

	want := []store.PromptState{
		{Text: "DIEQUEUED", State: store.PromptStarted},
		{PromptID: "p-dying", Text: "dying words", Sender: store.PromptSenderOwner, State: store.PromptDiscarded},
	}
	if got := promptStatesOf(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates after the process ended = %+v, want %+v", got, want)
	}
}

// TestQueueLifecycleStopKeepsTheSayClaudeCompleted sends a Say that the turn takes in at its tool call, so the turn
// completes it before the session is stopped. The completed Say keeps its state, and is not discarded.
func TestQueueLifecycleStopKeepsTheSayClaudeCompleted(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "MIDTURN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "taken words", PromptID: "p-taken"}); err != nil {
		t.Fatalf("Say during the turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 1") && turnEndCount(h) == 1 && h.State == "done"
	})

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []store.PromptState{
		{Text: "MIDTURN", State: store.PromptCompleted},
		{PromptID: "p-taken", Text: "taken words", Sender: store.PromptSenderOwner, State: store.PromptCompleted},
	}
	if got := promptStatesOf(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates after Stop = %+v, want %+v", got, want)
	}
}
