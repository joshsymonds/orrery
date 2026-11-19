package server_test

import (
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The counted step of the step facts tests, its pending event, its prompts' texts, and the step with no row that the
// conversation item test names.
const (
	factsStep       = "step-facts"
	factsStepEvent  = "step:" + factsStep
	factsOther      = "step-missing"
	factsOwnerText  = "fix the bug"
	factsStepText   = "step-facts ended: review the change"
	factsOtherText  = "step-missing ended: review the change"
	factsOwnerID    = "p-owner"
	factsOtherEvent = "step:" + factsOther
)

// applyEngineOpsAt applies ops to the store in one write, stamped at the unix second at.
func applyEngineOpsAt(t *testing.T, s *store.Store, at int64, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(at, 0)); err != nil {
		t.Fatalf("ApplyEngine at %d: %v", at, err)
	}
}

// addStepRun writes the step id, which eventEngine calls. It starts at the unix second started, loops in round, returns
// with lines, which are nil when the step's lines are not counted, and ends at the unix second ended.
func addStepRun(t *testing.T, s *store.Store, id string, started, ended int64, round int, lines *store.StepLines) {
	t.Helper()
	applyEngineOpsAt(t, s, started, store.StepAdd{
		ID: id, Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: eventEngine},
	})
	applyEngineOpsAt(t, s, ended,
		store.StepLoop{ID: id, Round: round},
		store.StepReturn{ID: id, Result: "First line\nSecond line", Lines: lines},
		store.StepEnd{ID: id, End: "done", Status: "passed", Reason: "gate green"},
	)
}

// addFactsStep writes factsStep. It starts 30 seconds after eventAt, loops in round 3, changes two files, adding 14
// lines and removing 5, and ends 120 seconds after eventAt, so it ran for 90 seconds.
func addFactsStep(t *testing.T, s *store.Store) {
	t.Helper()
	lines := &store.StepLines{Counted: true, Files: 2, Added: 14, Removed: 5}
	addStepRun(t, s, factsStep, eventAt+30, eventAt+120, 3, lines)
}

// factsStepFacts is the StepFacts addFactsStep's step carries.
func factsStepFacts() *orreryv1.StepFacts {
	return &orreryv1.StepFacts{Rounds: 3, Seconds: 90, Counted: true, Files: 2, Added: 14, Removed: 5}
}

// addStepEndSession writes eventEngine with factsStep, an owner prompt, the event prompt of factsStep, and the event
// prompt of factsOther, whose step has no row. The prompts are stamped at eventAt, the time addEngine writes.
func addStepEndSession(t *testing.T, s *store.Store) {
	t.Helper()
	addEngine(t, s)
	addFactsStep(t, s)
	applyEngineOps(t, s,
		store.EnginePrompt{
			ID: eventEngine, Text: factsOwnerText, PromptID: factsOwnerID, Sender: store.PromptSenderOwner,
		},
		store.EnginePrompt{
			ID: eventEngine, Text: factsStepText, PromptID: factsStepEvent, Sender: store.PromptSenderEvent,
		},
		store.EnginePrompt{
			ID: eventEngine, Text: factsOtherText, PromptID: factsOtherEvent, Sender: store.PromptSenderEvent,
		},
	)
}

// factsStepItems is the items addStepEndSession's session holds, oldest first. Only the prompt of factsStep carries
// StepFacts.
func factsStepItems() []*orreryv1.ConversationItem {
	return []*orreryv1.ConversationItem{
		{Kind: "prompt", Time: eventAt, Text: factsOwnerText, Sender: store.PromptSenderOwner, PromptId: factsOwnerID},
		{
			Kind: "prompt", Time: eventAt, Text: factsStepText, Sender: store.PromptSenderEvent,
			PromptId: factsStepEvent, StepFacts: factsStepFacts(),
		},
		{
			Kind: "prompt", Time: eventAt, Text: factsOtherText, Sender: store.PromptSenderEvent,
			PromptId: factsOtherEvent,
		},
	}
}

// TestStepEndFactsPendingEvent serves the conversation of eventEngine, which called the card step, an uncounted step
// that ends at eventAt, and factsStep, which ran for 90 seconds. Its first delta lists both step ends and the epic's
// release. The step ends carry their facts, and the release carries none.
func TestStepEndFactsPendingEvent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	addFactsStep(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))

	pending := stream.next(t).GetState().GetPending()
	if len(pending) != 3 {
		t.Fatalf("pending events: got %d, want 3: the card step, factsStep and the epic release", len(pending))
	}
	byID := make(map[string]*orreryv1.PendingEvent, len(pending))
	for _, event := range pending {
		byID[event.GetId()] = event
	}
	for _, id := range []string{cardStepEvent, factsStepEvent, cardEpicEvent} {
		if byID[id] == nil {
			t.Fatalf("pending events %v hold no event %s", pending, id)
		}
	}
	if got, want := byID[cardStepEvent].GetStepFacts(), (&orreryv1.StepFacts{}); !proto.Equal(got, want) {
		t.Fatalf("uncounted step end facts = %v, want %v", got, want)
	}
	if got, want := byID[factsStepEvent].GetStepFacts(), factsStepFacts(); !proto.Equal(got, want) {
		t.Fatalf("counted step end facts = %v, want %v", got, want)
	}
	if got := byID[cardEpicEvent].GetStepFacts(); got != nil {
		t.Fatalf("epic release carries step facts %v, want none", got)
	}
}

// TestStepEndFactsConversationItem serves the conversation of eventEngine, which holds the owner prompt, the event
// prompt of factsStep and the event prompt of factsOther, whose step has no row. The reset's prompt of factsStep
// carries its StepFacts and its text as stored. The other prompts carry none, and no other field changes.
func TestStepEndFactsConversationItem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepEndSession(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))

	msg := stream.next(t)
	if !msg.GetReset_() || !sameItems(msg.GetItems(), factsStepItems()) {
		t.Fatalf("reset items = %v, want %v", msg.GetItems(), factsStepItems())
	}
}

// TestStepEndFactsOlderPage pages Sessions.Older over the session of addStepEndSession. The page holds the same items
// as the reset of TestStepEndFactsConversationItem, so the prompt of factsStep carries its StepFacts and the same text.
func TestStepEndFactsOlderPage(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepEndSession(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: eventEngine, Before: 1000,
	}))
	if err != nil {
		t.Fatalf("Older: %v", err)
	}
	if !sameItems(resp.Msg.GetItems(), factsStepItems()) {
		t.Fatalf("older page = %v, want %v", resp.Msg.GetItems(), factsStepItems())
	}
}
