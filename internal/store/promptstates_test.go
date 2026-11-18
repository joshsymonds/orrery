package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestQueueLifecycleRecordsStatesAndReadsThemUnderEachPromptID records states for some of a session's prompts and reads
// them back, from the writer and from the Reader: each recorded prompt is listed once under its prompt id, with its text,
// sender and newest state, and a prompt with no state, or another session's prompt, is absent.
func TestQueueLifecycleRecordsStatesAndReadsThemUnderEachPromptID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "first", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "second", PromptID: "p2", Sender: PromptSenderEvent, WriteID: "w2"},
		EnginePrompt{ID: "e1", Text: "unrecorded", PromptID: "p3", Sender: PromptSenderOwner, WriteID: "w3"},
		EnginePrompt{ID: "e1", Text: "bare", WriteID: "w4"},
		EnginePrompt{ID: "e2", Text: "elsewhere", PromptID: "p9", Sender: PromptSenderOwner, WriteID: "x1"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCompleted},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w4", State: PromptQueued},
		EnginePromptState{ID: "e2", WriteID: "x1", State: PromptCancelled},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := []PromptState{
		{PromptID: "p1", Text: "first", Sender: PromptSenderOwner, State: PromptCompleted},
		{PromptID: "p2", Text: "second", Sender: PromptSenderEvent, State: PromptQueued},
		{PromptID: "", Text: "bare", Sender: "", State: PromptQueued},
	}
	got, err := s.PromptStates(ctx, "e1")
	if err != nil {
		t.Fatalf("PromptStates: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates = %+v, want %+v", got, want)
	}
	read, err := NewReader(path).PromptStates(ctx, "e1")
	if err != nil {
		t.Fatalf("Reader.PromptStates: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.PromptStates = %+v, want %+v", read, want)
	}
}

// TestQueueLifecycleDiscardStoresDiscardedOnlyForPromptsNotStarted runs the discard after a launch, over a session with a
// prompt before the launch, a queued, a started, a completed and an unrecorded prompt above it, and another session's
// unrecorded prompt. The prompt before the launch and the started and completed prompts keep their states, the queued and
// unrecorded prompts are discarded, and the other session gets no state.
func TestQueueLifecycleDiscardStoresDiscardedOnlyForPromptsNotStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "before launch", PromptID: "p0", WriteID: "w0"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "queued", PromptID: "p1", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "started", PromptID: "p2", WriteID: "w2"},
		EnginePrompt{ID: "e1", Text: "completed", PromptID: "p3", WriteID: "w3"},
		EnginePrompt{ID: "e1", Text: "unrecorded", PromptID: "p4", WriteID: "w4"},
		EnginePrompt{ID: "e2", Text: "other session", PromptID: "p5", WriteID: "x1"},
		EnginePromptState{ID: "e1", WriteID: "w0", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w3", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w3", State: PromptCompleted},
		EnginePromptsDiscard{ID: "e1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := []PromptState{
		{PromptID: "p0", Text: "before launch", State: PromptQueued},
		{PromptID: "p1", Text: "queued", State: PromptDiscarded},
		{PromptID: "p2", Text: "started", State: PromptStarted},
		{PromptID: "p3", Text: "completed", State: PromptCompleted},
		{PromptID: "p4", Text: "unrecorded", State: PromptDiscarded},
	}
	got, err := s.PromptStates(ctx, "e1")
	if err != nil {
		t.Fatalf("PromptStates e1: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates after the discard = %+v, want %+v", got, want)
	}
	other, err := s.PromptStates(ctx, "e2")
	if err != nil {
		t.Fatalf("PromptStates e2: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("PromptStates of another session = %+v, want none", other)
	}
}

// TestQueueLifecycleUnansweredCountExcludesCanceledAndDiscarded counts a prompt as answered when its newest state is
// canceled or discarded. A prompt canceled and then started is owed again, since its newest state is started.
func TestQueueLifecycleUnansweredCountExcludesCanceledAndDiscarded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "one", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "two", WriteID: "w2"},
		EnginePrompt{ID: "e1", Text: "three", WriteID: "w3"},
		EnginePrompt{ID: "e1", Text: "four", WriteID: "w4"},
		EnginePrompt{ID: "e1", Text: "five", WriteID: "w5"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine prompts: %v", err)
	}
	if got := holderPromptsUnanswered(t, s, "e1"); got != 5 {
		t.Fatalf("before any state: got %d, want 5", got)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptDiscarded},
		EnginePromptState{ID: "e1", WriteID: "w3", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w4", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w5", State: PromptCancelled},
		EnginePromptState{ID: "e1", WriteID: "w5", State: PromptStarted},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine states: %v", err)
	}
	if got := holderPromptsUnanswered(t, s, "e1"); got != 3 {
		t.Fatalf("after canceling w1, discarding w2 and canceling then starting w5: got %d, want 3 (w3, w4, w5)", got)
	}
}

// TestQueueLifecycleCancelOrDiscardAfterStartedLeavesStarted records a canceled state and a discarded state, each after
// a started state of its own prompt. Neither is recorded, so each prompt's newest state stays started.
func TestQueueLifecycleCancelOrDiscardAfterStartedLeavesStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "canceled", PromptID: "p1", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "discarded", PromptID: "p2", WriteID: "w2"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptDiscarded},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := []PromptState{
		{PromptID: "p1", Text: "canceled", State: PromptStarted},
		{PromptID: "p2", Text: "discarded", State: PromptStarted},
	}
	got, err := s.PromptStates(ctx, "e1")
	if err != nil {
		t.Fatalf("PromptStates: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates = %+v, want %+v", got, want)
	}
}

// TestQueueLifecycleCancelAfterQueuedIsRecorded records a canceled state after a queued state of the same prompt. The
// prompt was never started, so the canceled state is recorded and is its newest state.
func TestQueueLifecycleCancelAfterQueuedIsRecorded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "withdrawn", PromptID: "p1", WriteID: "w1"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := []PromptState{{PromptID: "p1", Text: "withdrawn", State: PromptCancelled}}
	got, err := s.PromptStates(ctx, "e1")
	if err != nil {
		t.Fatalf("PromptStates: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptStates = %+v, want %+v", got, want)
	}
}
