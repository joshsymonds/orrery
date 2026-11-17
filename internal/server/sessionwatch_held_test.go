package server_test

import (
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// heldMessage is the HeldMessage of the prompt id promptID with text.
func heldMessage(promptID, text string) *orreryv1.HeldMessage {
	return &orreryv1.HeldMessage{PromptId: promptID, Text: text}
}

// nextHeldChange returns the next message that changes what the stream sent before it: an item, a state, an unfinished
// set or a held set that differs from the message before it. A keepalive changes none of them, so the keepalives
// between changes are skipped. It has no deadline of its own.
func (s *sessionStream) nextHeldChange(t *testing.T) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	for {
		prev := s.last
		msg := s.next(t)
		unchanged := len(msg.GetItems()) == 0 && msg.GetState() == nil &&
			slices.EqualFunc(msg.GetUnfinished(), prev.GetUnfinished(), func(a, b *orreryv1.EngineText) bool {
				return proto.Equal(a, b)
			}) &&
			slices.EqualFunc(msg.GetHeld(), prev.GetHeld(), func(a, b *orreryv1.HeldMessage) bool {
				return proto.Equal(a, b)
			})
		if !unchanged {
			return msg
		}
	}
}

// TestHeldQueueFirstDeltaListsHeldMessagesInOrder holds two messages before the stream opens. The first delta is a reset
// with the whole state, and its held set lists both messages, oldest first.
func TestHeldQueueFirstDeltaListsHeldMessagesInOrder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		store.EngineHold{ID: eventEngine, PromptID: "p-1", Text: "first"},
		store.EngineHold{ID: eventEngine, PromptID: "p-2", Text: "second"},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Reset_:     true,
		Held:       []*orreryv1.HeldMessage{heldMessage("p-1", "first"), heldMessage("p-2", "second")},
		State:      &orreryv1.ConversationState{Session: engineSession("idle")},
	})
}

// TestHeldQueueAddedMessageSendsTheNewSet holds one message before the stream opens, then holds a second one while the
// stream is open. The delta that follows lists both messages in order, and it carries nothing else.
func TestHeldQueueAddedMessageSendsTheNewSet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineHold{ID: eventEngine, PromptID: "p-1", Text: "first"})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, store.EngineHold{ID: eventEngine, PromptID: "p-2", Text: "second"})
	wantStreamMessage(t, stream.nextHeldChange(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Held:       []*orreryv1.HeldMessage{heldMessage("p-1", "first"), heldMessage("p-2", "second")},
	})
}

// TestHeldQueueReleasedMessageLeavesTheSet holds two messages before the stream opens. Releasing the first, as the write
// that sends it does, sends a delta whose held set lists only the second.
func TestHeldQueueReleasedMessageLeavesTheSet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		store.EngineHold{ID: eventEngine, PromptID: "p-1", Text: "first"},
		store.EngineHold{ID: eventEngine, PromptID: "p-2", Text: "second"},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, store.EngineHeldRelease{ID: eventEngine, PromptID: "p-1"})
	wantStreamMessage(t, stream.nextHeldChange(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Held:       []*orreryv1.HeldMessage{heldMessage("p-2", "second")},
	})
}

// TestHeldQueueKeepaliveCarriesHeldSet holds one message and leaves the session unchanged. The keepalive after the first
// delta carries the held set, with no items and no state.
func TestHeldQueueKeepaliveCarriesHeldSet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineHold{ID: eventEngine, PromptID: "p-1", Text: "first"})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        1,
		Held:       []*orreryv1.HeldMessage{heldMessage("p-1", "first")},
	})
}

// TestHeldQueueStepSessionHoldsNothing holds a message on a labeled step's row, which the store accepts. The step's first
// delta carries no held messages, because a step holds none.
func TestHeldQueueStepSessionHoldsNothing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepSession(t, s, stepSessionName)
	applyEngineOps(t, s, store.EngineHold{ID: stepSessionName, PromptID: "p-1", Text: "held"})
	steps := &stepStatusFake{}
	client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), steps))

	stream := openSessionStream(t, client, sessionReq(stepSessionName, 0, 0))
	if held := stream.next(t).GetHeld(); len(held) != 0 {
		t.Fatalf("step held %v, want no held messages", held)
	}
}
