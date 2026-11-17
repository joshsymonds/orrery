package enginehost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// deliveredPrompt is what a test reads of an engine_prompt event: its text, its prompt id and its sender.
type deliveredPrompt struct {
	Text     string `json:"text"`
	PromptID string `json:"promptId"`
	Sender   string `json:"sender"`
}

// enginePromptsOf returns the payload of each engine_prompt event of history, in seq order.
func enginePromptsOf(t *testing.T, history store.EngineHistory) []deliveredPrompt {
	t.Helper()
	var prompts []deliveredPrompt
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var prompt deliveredPrompt
		if err := json.Unmarshal([]byte(event.Payload), &prompt); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		prompts = append(prompts, prompt)
	}
	return prompts
}

func TestCallerEventDeliverStoresEventPrompt(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})

	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the event prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: "the event text", PromptID: "e1", Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventDeliverHeldWhileTurnRuns(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	// WAITOPEN holds the turn open until fakeclaude.open exists, so the turn ends by itself and not by the owner's
	// Interrupt, which would leave the caller quiet (see TestOwnerInterruptQuietsCallerEventsUntilHisSay).
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "WAITOPEN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); !errors.Is(
		err, ErrSessionBusy,
	) {
		t.Fatalf("Deliver during a turn error = %v, want ErrSessionBusy", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(
		got, []deliveredPrompt{{Text: "WAITOPEN"}},
	) {
		t.Fatalf("prompts after held Deliver = %+v, want only the start's prompt", got)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn ended and the session is done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})
	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver after the turn ended: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the event prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "WAITOPEN"},
		{Text: "the event text", PromptID: "e1", Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventDeliverLeavesOpenQuestion(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	requestID := requestIDOf(t, history)

	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); !errors.Is(
		err, ErrSessionBusy,
	) {
		t.Fatalf("Deliver with an open question error = %v, want ErrSessionBusy", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "ASK"}}) {
		t.Fatalf("prompts after held Deliver = %+v, want only the start's prompt", got)
	}
	if question, ok := lastQuestion(t, historyOf(t, path, id)); !ok || question != "Which color?" {
		t.Fatalf("question = %q (set %v), want the open question left as it was", question, ok)
	}
	// The request is still open for the owner to answer, so the answer is accepted.
	if err := m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer red after a held Deliver: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1")
	})
}

func TestCallerEventDeliverRepeatedPromptIDSendsOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	req := DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}
	if err := m.Deliver(t.Context(), req); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the event prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
	})

	if err := m.Deliver(t.Context(), req); err != nil {
		t.Fatalf("repeated Deliver: %v", err)
	}
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	restarted := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	if err := restarted.Deliver(t.Context(), req); err != nil {
		t.Fatalf("Deliver of a delivered prompt after a restart: %v", err)
	}
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: "the event text", PromptID: "e1", Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want the start only: a repeat sends nothing and resumes nothing", args)
	}
}

func TestCallerEventDeliverResumesIdleStopped(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after idle stop: err = %v, want ErrNoSession", err)
	}

	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver to an idle-stopped session: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the event prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: "the event text", PromptID: "e1", Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventDeliverRefusesOwnerStopped(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := historyOf(t, path, id).State; got != stateStopped {
		t.Fatalf("state after Stop = %q, want %q", got, stateStopped)
	}

	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); !errors.Is(
		err, ErrCallerStopped,
	) {
		t.Fatalf("Deliver to an owner-stopped session error = %v, want ErrCallerStopped", err)
	}
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after a refused Deliver: err = %v, want ErrNoSession", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want the start only", args)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "hi"}}) {
		t.Fatalf("prompts after a refused Deliver = %+v, want only the start's prompt", got)
	}
}

func TestCallerEventDeliverRefusesStepSession(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Schema: stepSchema,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, engineOf(m), path, stepA, "the step's reply block done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})

	// Without the step check, Deliver returns ErrSessionBusy or sends, and neither error text says it is a step session.
	err := m.Deliver(t.Context(), DeliverRequest{ID: stepA, Text: "the event text", PromptID: "e1"})
	if err == nil || !strings.Contains(err.Error(), stepA) || !strings.Contains(err.Error(), "is a step session") {
		t.Fatalf("Deliver to a step session error = %v, want an error naming %s as a step session", err, stepA)
	}
	want := []deliveredPrompt{{Text: "hi", Sender: store.PromptSenderBrief}}
	if got := enginePromptsOf(t, historyOf(t, path, stepA)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a refused Deliver = %+v, want only the start's prompt", got)
	}
}
