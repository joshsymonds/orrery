package store

import (
	"context"
	"reflect"
	"testing"
)

// cancelTestSession is the engine session the cancel and discard tests send their prompts to.
const cancelTestSession = "e1"

// applyPromptOps applies ops to the store at the fixed engine time, and fails the test when they do not apply.
func applyPromptOps(t *testing.T, s *Store, ops ...EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(context.Background(), ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// markEventFor records the event delivered to cancelTestSession at the fixed engine time.
func markEventFor(t *testing.T, s *Store, event string) {
	t.Helper()
	if err := s.MarkCallerEvent(context.Background(), event, cancelTestSession, engineNowAt()); err != nil {
		t.Fatalf("MarkCallerEvent %s: %v", event, err)
	}
}

// markedOf reports whether the event is marked delivered to cancelTestSession.
func markedOf(t *testing.T, s *Store, event string) bool {
	t.Helper()
	marked, err := s.CallerEventMarked(context.Background(), event, cancelTestSession)
	if err != nil {
		t.Fatalf("CallerEventMarked %s: %v", event, err)
	}
	return marked
}

// sentOf reports whether cancelTestSession has an engine_prompt that carries promptID.
func sentOf(t *testing.T, s *Store, promptID string) bool {
	t.Helper()
	sent, err := s.EnginePromptSent(context.Background(), cancelTestSession, promptID)
	if err != nil {
		t.Fatalf("EnginePromptSent %s: %v", promptID, err)
	}
	return sent
}

// TestDeliverNowCanceledEventPromptLosesItsMarkAndReadsUnsent marks an event prompt that was queued and then recorded
// canceled. The cancel deletes the mark, so the event reads not sent. A second engine_prompt with the same prompt id is
// sent, and once it is delivered the event can be marked again.
func TestDeliverNowCanceledEventPromptLosesItsMarkAndReadsUnsent(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "event", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w1"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
	)
	markEventFor(t, s, "ev1")
	applyPromptOps(t, s, EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled})

	if markedOf(t, s, "ev1") {
		t.Fatalf("event ev1 is still marked after its prompt was canceled, want the mark deleted")
	}
	if sentOf(t, s, "ev1") {
		t.Fatalf("event ev1 reads sent after its prompt was canceled, want not sent")
	}

	applyPromptOps(t, s,
		EnginePrompt{ID: "e1", Text: "event again", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w2"})
	if !sentOf(t, s, "ev1") {
		t.Fatalf("event ev1 reads not sent after a second engine_prompt with its prompt id, want sent")
	}
}

// TestDeliverNowCancelOrDiscardAfterStartedKeepsMark marks two event prompts that were started, then records one
// canceled and the other discarded. Neither state is recorded, so both marks stay and both events read sent.
func TestDeliverNowCancelOrDiscardAfterStartedKeepsMark(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "canceled", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "discarded", PromptID: "ev2", Sender: PromptSenderEvent, WriteID: "w2"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptStarted},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptStarted},
	)
	markEventFor(t, s, "ev1")
	markEventFor(t, s, "ev2")
	applyPromptOps(t, s,
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptDiscarded},
	)

	for _, promptID := range []string{"ev1", "ev2"} {
		if !markedOf(t, s, promptID) {
			t.Fatalf("event %s lost its mark when canceled or discarded after started, want the mark kept", promptID)
		}
		if !sentOf(t, s, promptID) {
			t.Fatalf("event %s reads not sent after canceled or discarded after started, want sent", promptID)
		}
	}
}

// TestDeliverNowDiscardUnmarksNotStartedEventPrompt discards the unstarted prompts of a session after a launch. The
// event prompt that was only queued loses its mark and reads not sent, and the event prompt that was started keeps its
// mark and reads sent.
func TestDeliverNowDiscardUnmarksNotStartedEventPrompt(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "queued", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "started", PromptID: "ev2", Sender: PromptSenderEvent, WriteID: "w2"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptStarted},
	)
	markEventFor(t, s, "ev1")
	markEventFor(t, s, "ev2")
	applyPromptOps(t, s, EnginePromptsDiscard{ID: "e1"})

	if markedOf(t, s, "ev1") {
		t.Fatalf("event ev1 is still marked after its not-started prompt was discarded, want the mark deleted")
	}
	if sentOf(t, s, "ev1") {
		t.Fatalf("event ev1 reads sent after its not-started prompt was discarded, want not sent")
	}
	if !markedOf(t, s, "ev2") {
		t.Fatalf("event ev2 lost its mark though its prompt was started, want the mark kept")
	}
	if !sentOf(t, s, "ev2") {
		t.Fatalf("event ev2 reads not sent though its prompt was started, want sent")
	}
}

// TestDeliverNowOwnerPromptCanceledStillSent records an owner prompt and an event prompt in one session, both canceled.
// The owner prompt's id still reads sent, so a late retry of a withdrawn owner message is not sent again, while the
// event prompt's id reads not sent.
func TestDeliverNowOwnerPromptCanceledStillSent(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "owner", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "event", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w2"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w2", State: PromptCancelled},
	)

	if !sentOf(t, s, "p1") {
		t.Fatalf("owner prompt p1 reads not sent after it was canceled, want sent")
	}
	if sentOf(t, s, "ev1") {
		t.Fatalf("event prompt ev1 reads sent after it was canceled, want not sent")
	}
}

// TestDeliverNowMarkAfterCancelRecordsNoMark cancels an event prompt, then marks its event, which records no mark. An
// event that no engine_prompt carries is marked as before. A second engine_prompt with the event's prompt id is then
// the newest prompt the event carries, so marking the event records a mark again.
func TestDeliverNowMarkAfterCancelRecordsNoMark(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "event", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w1"},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptQueued},
		EnginePromptState{ID: "e1", WriteID: "w1", State: PromptCancelled},
	)
	markEventFor(t, s, "ev1")
	if markedOf(t, s, "ev1") {
		t.Fatalf("MarkCallerEvent of ev1 after its prompt was canceled recorded a mark, want none")
	}

	markEventFor(t, s, "epic:a:Q1")
	if !markedOf(t, s, "epic:a:Q1") {
		t.Fatalf("MarkCallerEvent of an event with no engine_prompt recorded no mark, want one")
	}

	applyPromptOps(t, s,
		EnginePrompt{ID: "e1", Text: "event again", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w2"})
	markEventFor(t, s, "ev1")
	if !markedOf(t, s, "ev1") {
		t.Fatalf("MarkCallerEvent of ev1 after a second engine_prompt with its prompt id recorded no mark, want one")
	}
}

// TestEscapeQueuedPromptsOfWritesReturnsListedPromptsInSendOrder sends owner, event, bare, and unlisted prompts to a
// session, and another prompt to another session. PromptsOfWrites over the write ids of the owner, event and bare
// prompts, listed out of send order and with an unknown id and the other session's write id, returns those three in
// send order, each with its prompt id, text and sender. An empty list returns none.
func TestEscapeQueuedPromptsOfWritesReturnsListedPromptsInSendOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	applyPromptOps(t, s,
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "owner text", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "event text", PromptID: "ev1", Sender: PromptSenderEvent, WriteID: "w2"},
		EnginePrompt{ID: "e1", Text: "bare", WriteID: "w3"},
		EnginePrompt{ID: "e1", Text: "unlisted", PromptID: "p9", Sender: PromptSenderOwner, WriteID: "w4"},
		EnginePrompt{ID: "e2", Text: "other session", PromptID: "p8", Sender: PromptSenderOwner, WriteID: "x1"},
	)

	got, err := s.PromptsOfWrites(ctx, "e1", []string{"w3", "x1", "missing", "w2", "w1"})
	if err != nil {
		t.Fatalf("PromptsOfWrites: %v", err)
	}
	want := []WritePrompt{
		{WriteID: "w1", PromptID: "p1", Text: "owner text", Sender: PromptSenderOwner},
		{WriteID: "w2", PromptID: "ev1", Text: "event text", Sender: PromptSenderEvent},
		{WriteID: "w3", PromptID: "", Text: "bare", Sender: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PromptsOfWrites = %+v, want %+v", got, want)
	}

	for _, writeIDs := range [][]string{nil, {}} {
		none, err := s.PromptsOfWrites(ctx, "e1", writeIDs)
		if err != nil {
			t.Fatalf("PromptsOfWrites(%v): %v", writeIDs, err)
		}
		if len(none) != 0 {
			t.Fatalf("PromptsOfWrites(%v) = %+v, want none", writeIDs, none)
		}
	}
}
