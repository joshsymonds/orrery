package server_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The waits labels a card carries, as PendingEvent.waits names them.
const (
	cardFree  = "free"
	cardSpeak = "speak"
)

// The card tests' step and epic. engine-1 calls cardStep, which ends and is held by cardEpic, and creates cardEpic,
// which is released. The ids of their events are cardStepEvent and cardEpicEvent.
const (
	cardStep      = "step-card"
	cardEpic      = "orrery-1a2b3c4/card-epic"
	cardStepEvent = "step:step-card"
	cardEpicEvent = "epic:orrery-1a2b3c4/card-epic:released"
)

// addCardEvents writes eventEngine, which creates cardEpic, and the step cardStep that eventEngine calls and that
// ends with two lines of result.
func addCardEvents(t *testing.T, s *store.Store) {
	t.Helper()
	addEngine(t, s)
	at := time.Unix(eventAt, 0)
	epic := store.EpicRow{
		ID: cardEpic, Repository: "/work/card", Title: "Card epic", Phase: "released", Created: at, Changed: at,
	}
	if err := s.CreateEpic(t.Context(), epic); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	recorded, err := s.RecordEpicCall(
		t.Context(), cardEpic, store.EpicCaller{Session: eventEngine}, "epic_create", at,
	)
	if err != nil || !recorded {
		t.Fatalf("RecordEpicCall = %v, %v, want true, nil", recorded, err)
	}
	applyEngineOps(t, s, store.StepAdd{
		ID: cardStep, Agent: "implementer", Prompt: "do it", Epic: cardEpic,
		Caller: store.EpicCaller{Session: eventEngine},
	})
	applyEngineOps(t, s,
		store.StepReturn{ID: cardStep, Result: "First line\nSecond line"},
		store.StepEnd{ID: cardStep, End: "done", Status: "passed", Reason: "gate green"},
	)
}

// cardPending is the pending list of the card tests: the step end of cardStep, then the release of cardEpic, each
// labeled waits.
func cardPending(waits string) []*orreryv1.PendingEvent {
	return []*orreryv1.PendingEvent{cardStepEnd(waits), cardRelease(waits)}
}

// cardStepEnd is the card of cardStep's end, labeled waits.
func cardStepEnd(waits string) *orreryv1.PendingEvent {
	return &orreryv1.PendingEvent{
		Id:     cardStepEvent,
		Kind:   "step_end",
		Step:   cardStep,
		Agent:  "implementer",
		End:    "done",
		Status: "passed",
		Brief:  "Return: First line...\nNot shown: return (22 bytes)\nReason: gate green\nMore: step_result id step-card",
		Epic:   cardEpic,
		Waits:  waits,
	}
}

// cardRelease is the card of cardEpic's release, labeled waits.
func cardRelease(waits string) *orreryv1.PendingEvent {
	return &orreryv1.PendingEvent{
		Id: cardEpicEvent, Kind: "release", Epic: cardEpic, Title: "Card epic", Waits: waits,
	}
}

// wantPendingEvents fails the test unless the state of msg lists the pending events want, in order.
func wantPendingEvents(t *testing.T, msg *orreryv1.SessionsWatchConversationResponse, want ...*orreryv1.PendingEvent) {
	t.Helper()
	got := msg.GetState().GetPending()
	if len(got) != len(want) {
		t.Fatalf("pending events: got %v, want %v", got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("pending event %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSessionWatchStepEventCardCarriesStepEndAndEpicEvent serves the conversation of eventEngine, which called a step
// and created an epic. Its first delta lists the step's end with the card fields and the epic's release, both free.
func TestSessionWatchStepEventCardCarriesStepEndAndEpicEvent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantPendingEvents(t, stream.next(t), cardPending(cardFree)...)
}

// TestSessionWatchStepEventCardWaitsSpeakAfterOwnerInterrupt records an owner interrupt on eventEngine, which the
// pending events then label speak, and an owner prompt after it, which labels them free again.
func TestSessionWatchStepEventCardWaitsSpeakAfterOwnerInterrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), s, nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantPendingEvents(t, stream.next(t), cardPending(cardFree)...)

	applyEngineOps(t, s, store.EngineInterrupt{ID: eventEngine, Turning: false})
	wantPendingEvents(t, stream.nextDelta(t), cardPending(cardSpeak)...)

	applyEngineOps(t, s, store.EnginePrompt{
		ID: eventEngine, Text: "carry on", Sender: store.PromptSenderOwner,
	})
	wantPendingEvents(t, stream.nextDelta(t), cardPending(cardFree)...)
}

// TestSessionWatchStepEventCardDeliveredEventsLeaveThePending marks each pending event delivered to eventEngine, as the
// deliverer does. Each mark sends a delta without the event it marks, and the list is empty once both are marked.
func TestSessionWatchStepEventCardDeliveredEventsLeaveThePending(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), s, nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantPendingEvents(t, stream.next(t), cardPending(cardFree)...)

	at := time.Unix(eventAt, 0)
	if err := s.MarkCallerEvent(t.Context(), cardStepEvent, eventEngine, at); err != nil {
		t.Fatalf("MarkCallerEvent step end: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t), cardRelease(cardFree))

	if err := s.MarkCallerEvent(t.Context(), cardEpicEvent, eventEngine, at); err != nil {
		t.Fatalf("MarkCallerEvent release: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t))
}

// TestStepEventBriefPendingStateCarriesBrief serves the conversation of eventEngine, whose step cardStep ended with two
// lines of result. The state's pending step end of cardStep carries exactly its brief, and the message holds no second
// line of the result.
func TestStepEventBriefPendingStateCarriesBrief(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	msg := stream.next(t)

	var step *orreryv1.PendingEvent
	for _, event := range msg.GetState().GetPending() {
		if event.GetId() == cardStepEvent {
			step = event
		}
	}
	if step == nil {
		t.Fatalf("pending events %v hold no event %s", msg.GetState().GetPending(), cardStepEvent)
	}
	const brief = "Return: First line...\nNot shown: return (22 bytes)\nReason: gate green\nMore: step_result id step-card"
	if got := step.GetBrief(); got != brief {
		t.Fatalf("pending step end Brief = %q, want %q", got, brief)
	}
	if text := prototext.Format(msg); strings.Contains(text, "Second line") {
		t.Fatalf("the state holds the second line of the result:\n%s", text)
	}
}
