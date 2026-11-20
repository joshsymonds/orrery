package server_test

import (
	"path/filepath"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// ownerMessage is the engine_prompt of an owner message to eventEngine: promptID is its prompt id, writeID the write it
// is recorded under, and text its text.
func ownerMessage(promptID, writeID, text string) store.EnginePrompt {
	return store.EnginePrompt{
		ID: eventEngine, Text: text, PromptID: promptID, Sender: store.PromptSenderOwner, WriteID: writeID,
	}
}

// setPromptState is the EnginePromptState that records state as the newest state of the prompt written under writeID.
func setPromptState(writeID, state string) store.EnginePromptState {
	return store.EnginePromptState{ID: eventEngine, WriteID: writeID, State: state}
}

// queuedMessage is the QueuedMessage of the prompt promptID with text, state and waits.
func queuedMessage(promptID, text, state, waits string) *orreryv1.QueuedMessage {
	return &orreryv1.QueuedMessage{PromptId: promptID, Text: text, State: state, Waits: waits}
}

// ownerItem is the conversation item of the owner message promptID with text, as the stream shows it.
func ownerItem(promptID, text string) *orreryv1.ConversationItem {
	return &orreryv1.ConversationItem{
		Kind: "prompt", Time: eventAt, Text: text, Sender: store.PromptSenderOwner, PromptId: promptID,
	}
}

// wantQueued fails the test unless got holds the queued messages want, in order.
func wantQueued(t *testing.T, got, want []*orreryv1.QueuedMessage) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b *orreryv1.QueuedMessage) bool { return proto.Equal(a, b) }) {
		t.Fatalf("queued = %v, want %v", got, want)
	}
}

// TestQueueLifecycleQueuedMessageIsServedOnReload sends an owner message that is still queued. A reload opens a new
// stream at seq 0, and its first message serves the message in queued with its state.
func TestQueueLifecycleQueuedMessageIsServedOnReload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "fix the bug"),
		setPromptState("w-1", store.PromptQueued),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	wantQueued(t, msg.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-1", "fix the bug", store.PromptQueued, "start"),
	})
}

// TestQueueLifecycleStartedAndCompletedAreNotServed sends four owner messages: one completed, one started, one still
// queued, and one completed last. Only the queued one is served, since a started or completed message is never served,
// whatever follows it.
func TestQueueLifecycleStartedAndCompletedAreNotServed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "done"),
		setPromptState("w-1", store.PromptCompleted),
		ownerMessage("p-2", "w-2", "running"),
		setPromptState("w-2", store.PromptStarted),
		ownerMessage("p-3", "w-3", "waiting"),
		setPromptState("w-3", store.PromptQueued),
		ownerMessage("p-4", "w-4", "last"),
		setPromptState("w-4", store.PromptCompleted),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	wantQueued(t, msg.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-3", "waiting", store.PromptQueued, "start"),
	})
}

// TestQueueLifecycleCanceledAndDiscardedServedUntilLaterStart sends four owner messages: a canceled one, a started
// one, a canceled one, and a discarded one. The canceled message before the started one is not served, since an owner
// message after it has started. The canceled and discarded messages after it are served with their states, since none
// after them has started.
func TestQueueLifecycleCanceledAndDiscardedServedUntilLaterStart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "withdrawn"),
		setPromptState("w-1", store.PromptCancelled),
		ownerMessage("p-2", "w-2", "sent"),
		setPromptState("w-2", store.PromptStarted),
		ownerMessage("p-3", "w-3", "dropped"),
		setPromptState("w-3", store.PromptCancelled),
		ownerMessage("p-4", "w-4", "discarded"),
		setPromptState("w-4", store.PromptDiscarded),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	wantQueued(t, msg.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-3", "dropped", store.PromptCancelled, "start"),
		queuedMessage("p-4", "discarded", store.PromptDiscarded, "start"),
	})
}

// TestQueueLifecycleCanceledAndDiscardedAbsentFromItems sends three owner messages between replies: a canceled one, a
// discarded one, and a queued one. The stream's items and Sessions.Older's items both leave out the first two prompt
// items and keep the queued one, which is not withheld.
func TestQueueLifecycleCanceledAndDiscardedAbsentFromItems(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "first"),
		replyBlock(1),
		ownerMessage("p-2", "w-2", "second"),
		replyBlock(2),
		ownerMessage("p-3", "w-3", "third"),
		replyBlock(3),
		setPromptState("w-1", store.PromptCancelled),
		setPromptState("w-2", store.PromptDiscarded),
		setPromptState("w-3", store.PromptQueued),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))
	want := []*orreryv1.ConversationItem{reply(1), reply(2), ownerItem("p-3", "third"), reply(3)}

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	if !sameItems(msg.GetItems(), want) {
		t.Fatalf("items = %v, want %v", msg.GetItems(), want)
	}

	resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: eventEngine, Before: msg.GetSeq() + 1,
	}))
	if err != nil {
		t.Fatalf("Older: %v", err)
	}
	if !sameItems(resp.Msg.GetItems(), want) {
		t.Fatalf("Older items = %v, want %v", resp.Msg.GetItems(), want)
	}
}

// TestQueueLifecycleCallerEventPromptNeverServed sends an owner message that is canceled, then a caller prompt that is
// still queued and another that has started. The owner message is served, and neither caller prompt is: a caller
// prompt is never served, and a caller's started state does not end the serving of an owner message.
func TestQueueLifecycleCallerEventPromptNeverServed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "owner"),
		setPromptState("w-1", store.PromptCancelled),
		store.EnginePrompt{
			ID: eventEngine, Text: "caller queued", PromptID: "c-1", Sender: store.PromptSenderEvent, WriteID: "x-1",
		},
		setPromptState("x-1", store.PromptQueued),
		store.EnginePrompt{
			ID: eventEngine, Text: "caller started", PromptID: "c-2", Sender: store.PromptSenderEvent, WriteID: "x-2",
		},
		setPromptState("x-2", store.PromptStarted),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	wantQueued(t, msg.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-1", "owner", store.PromptCancelled, "start"),
	})
}
