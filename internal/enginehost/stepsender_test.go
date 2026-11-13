package enginehost

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// storedPrompt is an engine_prompt event of a session: its text, and the sender and round it stores.
type storedPrompt struct {
	Text   string `json:"text"`
	Sender string `json:"sender"`
	Round  int    `json:"round"`
}

// storedPromptsOf returns the engine_prompt events of history in seq order, each decoded to its text, sender and round.
func storedPromptsOf(t *testing.T, history store.EngineHistory) []storedPrompt {
	t.Helper()
	var prompts []storedPrompt
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var prompt storedPrompt
		if err := json.Unmarshal([]byte(event.Payload), &prompt); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		prompts = append(prompts, prompt)
	}
	return prompts
}

// senderRound is the sender and round of a prompt sent to a step, without its text.
type senderRound struct {
	Sender string
	Round  int
}

// senderRoundsOf returns the sender and round of each prompt, in order.
func senderRoundsOf(prompts []StepPrompt) []senderRound {
	out := make([]senderRound, 0, len(prompts))
	for _, prompt := range prompts {
		out = append(out, senderRound{Sender: prompt.Sender, Round: prompt.Round})
	}
	return out
}

// stepNudgeRun returns the loop of the step id of f, failing the test when the step has none.
func stepNudgeRun(t *testing.T, f *stepNudgeFixture, id string) *stepRun {
	t.Helper()
	f.steps.mu.Lock()
	defer f.steps.mu.Unlock()
	run := f.steps.runs[id]
	if run == nil {
		t.Fatalf("step %s has no running loop", id)
	}
	return run
}

func TestStartStepStoresBriefSender(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	history := waitHistory(t, path, stepA, "the step's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitReturns(t, obs, 1)

	want := []storedPrompt{{Text: "hi", Sender: store.PromptSenderBrief}}
	if got := storedPromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored prompts = %+v, want %+v", got, want)
	}
}

func TestContinueStoresSenderAndRound(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitReturns(t, obs, 1)

	prompt := StepPrompt{Text: "again", Sender: store.PromptSenderEngine, Round: 2}
	if err := m.Continue(t.Context(), stepA, prompt); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	history := waitHistory(t, path, stepA, "the reply to the continued prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []storedPrompt{
		{Text: "hi", Sender: store.PromptSenderBrief},
		{Text: "again", Sender: store.PromptSenderEngine, Round: 2},
	}
	if got := storedPromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored prompts = %+v, want %+v", got, want)
	}
}

func TestSayStoresNoSenderOrRound(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(t, path, id, "the reply to the said prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	prompts := storedPromptsOf(t, history)
	if len(prompts) != 2 {
		t.Fatalf("stored prompts = %+v, want 2", prompts)
	}
	if want := (storedPrompt{Text: "again"}); prompts[1] != want {
		t.Fatalf("said prompt = %+v, want %+v", prompts[1], want)
	}
}

func TestStepRetrySendsBackWithItsRound(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)
	failing := session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)}

	f.run.Return(failing)
	f.run.Return(failing)
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)

	want := []senderRound{{Sender: store.PromptSenderEngine, Round: 1}, {Sender: store.PromptSenderEngine, Round: 2}}
	if got := senderRoundsOf(f.sessions.continuedPrompts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent back as %+v, want %+v", got, want)
	}
}

func TestJudgeContinueSendsBackWithItsRound(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{
		judgeLoopVerdict("unmet", "the report is missing its summary"),
		judgeLoopVerdict("met", "the summary is there now"),
	}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	f.run.Return(judgeLoopDone("second"))
	f.run.Return(judgeLoopDone("third"))
	waitRunEnd(t, done)

	want := []senderRound{{Sender: store.PromptSenderEngine, Round: 1}, {Sender: store.PromptSenderEngine, Round: 2}}
	if got := senderRoundsOf(f.sessions.continuedPrompts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent back as %+v, want %+v", got, want)
	}
}

func TestWatchdogTextAloneSendsEngineNoRound(t *testing.T) {
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	const watchdog = "watchdog nudge text"
	stepNudgeRun(t, f, id).nudge = watchdog

	f.endTurn(t, true)
	f.settle(t, id)
	want := []StepPrompt{{Text: watchdog, Sender: store.PromptSenderEngine}}
	if got := f.sessions.continuedPrompts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("continued with %+v, want %+v", got, want)
	}
	wantNoRound(t, f.status(t, id))
}

func TestOwnerNudgeAloneSendsOwnerNoRound(t *testing.T) {
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)

	if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
		t.Fatalf("Nudge of a running turn: %v", err)
	}
	f.endTurn(t, false)
	f.settle(t, id)
	want := []StepPrompt{{Text: "owner text", Sender: store.PromptSenderOwner}}
	if got := f.sessions.continuedPrompts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("continued with %+v, want %+v", got, want)
	}
	wantNoRound(t, f.status(t, id))
}

func TestOwnerNudgeResumingHoldSendsOwnerNoRound(t *testing.T) {
	t.Run("held turn already ended", func(t *testing.T) {
		f := newStepNudgeFixture(t)
		id := f.start(t)
		f.beginTurn(t)
		if err := f.steps.Hold(t.Context(), id); err != nil {
			t.Fatalf("Hold: %v", err)
		}
		f.endTurn(t, false)
		f.settle(t, id)

		if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
			t.Fatalf("Nudge of a held step whose turn ended: %v", err)
		}
		want := []StepPrompt{{Text: "owner text", Sender: store.PromptSenderOwner}}
		if got := f.sessions.continuedPrompts(); !reflect.DeepEqual(got, want) {
			t.Fatalf("continued with %+v, want %+v", got, want)
		}
		wantNoRound(t, f.status(t, id))
	})
	t.Run("held turn end still to come", func(t *testing.T) {
		f := newStepNudgeFixture(t)
		id := f.start(t)
		f.beginTurn(t)
		if err := f.steps.Hold(t.Context(), id); err != nil {
			t.Fatalf("Hold: %v", err)
		}
		if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
			t.Fatalf("Nudge of a held step: %v", err)
		}
		f.endTurn(t, false)
		f.settle(t, id)

		want := []StepPrompt{{Text: "owner text", Sender: store.PromptSenderOwner}}
		if got := f.sessions.continuedPrompts(); !reflect.DeepEqual(got, want) {
			t.Fatalf("continued with %+v, want %+v", got, want)
		}
		wantNoRound(t, f.status(t, id))
	})
}

func TestOwnerNudgeJoinedAfterWatchdogSendsOwnerNoRound(t *testing.T) {
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	const watchdog = "watchdog nudge text"
	stepNudgeRun(t, f, id).nudge = watchdog

	if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
		t.Fatalf("Nudge: %v", err)
	}
	f.endTurn(t, false)
	f.settle(t, id)
	want := []StepPrompt{{Text: watchdog + "\nowner text", Sender: store.PromptSenderOwner}}
	if got := f.sessions.continuedPrompts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("continued with %+v, want %+v", got, want)
	}
	wantNoRound(t, f.status(t, id))
}
