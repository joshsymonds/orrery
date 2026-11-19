package enginehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeOf returns the store m writes through, which newTestManager opens.
func storeOf(t *testing.T, m *Manager) *store.Store {
	t.Helper()
	s, ok := m.store.(*store.Store)
	if !ok {
		t.Fatalf("manager store is %T, want *store.Store", m.store)
	}
	return s
}

// callerStepID is the id of the step the caller event tests end, and callerStepEvent is the event id of its end.
const (
	callerStepID    = "step-1"
	callerStepEvent = stepEventPrefix + callerStepID
)

// addEndedCallerStep starts the step callerStepID under caller with result, and ends it done, passed for gate green.
func addEndedCallerStep(t *testing.T, s *store.Store, caller store.EpicCaller, result string) {
	t.Helper()
	endCallerStep(t, s, store.StepAdd{ID: callerStepID, Agent: "implementer", Prompt: "do it", Caller: caller}, result)
}

// endCallerStep starts the step add, sets its result to result, and ends it done, passed for gate green.
func endCallerStep(t *testing.T, s *store.Store, add store.StepAdd, result string) {
	t.Helper()
	ops := []store.EngineOp{
		add,
		store.StepReturn{ID: add.ID, Result: result},
		store.StepEnd{ID: add.ID, End: "done", Status: "passed", Reason: "gate green"},
	}
	for _, op := range ops {
		if err := s.ApplyEngine(t.Context(), []store.EngineOp{op}, fixedClock()); err != nil {
			t.Fatalf("ApplyEngine %T of step %s: %v", op, add.ID, err)
		}
	}
}

// wantStepEndBlock returns the block the end of step-1 sends a caller whose result is summary, for a step of the
// implementer agent that no epic holds.
func wantStepEndBlock(summary string) string {
	return "<orrery-event kind=\"step_end\" id=\"step:step-1\">\n" +
		"Step step-1 ended: end done, status passed, reason gate green.\n" +
		"Agent: implementer\n" +
		"Summary: " + summary + "\n" +
		"step_status gives the full result.\n" +
		"</orrery-event>"
}

// stepEndTextToPane starts the step add under a pane, ends it with the result all green, and returns the text a pass
// sends that pane, which is idle to take it. The pane takes the text as keys, so the test runs no headless session.
func stepEndTextToPane(t *testing.T, s *store.Store, add store.StepAdd) string {
	t.Helper()
	return stepEndTextOfResultToPane(t, s, add, "all green")
}

// stepEndTextOfResultToPane is stepEndTextToPane for a step that ends with result.
func stepEndTextOfResultToPane(t *testing.T, s *store.Store, add store.StepAdd, result string) string {
	t.Helper()
	const server, pane = "-S /tmp/tmux-1000/orrery", "%7"
	if err := s.SyncPane(t.Context(), server, store.Pane{ID: "orrery-1", Pane: pane}, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	add.Caller = store.EpicCaller{Server: server, Pane: pane}
	endCallerStep(t, s, add, result)
	var woken []string
	sender := NewCallerSender(CallerSenderConfig{
		Deliver: func(context.Context, DeliverRequest) error {
			t.Error("a pane caller received a headless delivery")
			return nil
		},
		WakePane: func(_ context.Context, _, _, text string) (bool, error) {
			woken = append(woken, text)
			return true, nil
		},
		Store: s,
		Now:   fixedClock,
	})
	passStep(t, stepEndPass(sender, s))
	if len(woken) != 1 {
		t.Fatalf("wakes = %d, want 1 for the step end", len(woken))
	}
	return woken[0]
}

// eventMarked reports whether the end of the caller step is marked delivered to session in s.
func eventMarked(t *testing.T, s *store.Store, session string) bool {
	t.Helper()
	marked, err := s.CallerEventMarked(t.Context(), callerStepEvent, session)
	if err != nil {
		t.Fatalf("CallerEventMarked %s of %s: %v", callerStepEvent, session, err)
	}
	return marked
}

// newStepEndSender returns a CallerSender that sends headless callers through m and marks through marks. It fails the
// test if a pane caller is woken, as the headless tests have none.
func newStepEndSender(t *testing.T, m *Manager, marks CallerEventStore) *CallerSender {
	t.Helper()
	return NewCallerSender(CallerSenderConfig{
		Deliver: m.Deliver,
		WakePane: func(context.Context, string, string, string) (bool, error) {
			t.Error("a pane caller was woken in a headless test")
			return false, nil
		},
		Store: marks,
		Now:   fixedClock,
	})
}

// stepEndPass returns a StepCallerEvents over steps that sends through sender.
func stepEndPass(sender *CallerSender, steps StepCallerStore) *StepCallerEvents {
	return NewStepCallerEvents(StepCallerEventsConfig{Store: steps, Sender: sender})
}

// passStep runs pass once over the test's context, and fails the test on its error.
func passStep(t *testing.T, pass *StepCallerEvents) {
	t.Helper()
	if err := pass.Pass(t.Context()); err != nil {
		t.Fatalf("step caller-event pass: %v", err)
	}
}

// lostMark is a CallerEventStore whose first mark fails, as a write that fails after a delivery does.
type lostMark struct {
	*store.Store
	failed bool
}

// MarkCallerEvent fails the first mark and records the rest in the store.
func (l *lostMark) MarkCallerEvent(ctx context.Context, event, session string, at time.Time) error {
	if !l.failed {
		l.failed = true
		return errors.New("write refused")
	}
	return l.Store.MarkCallerEvent(ctx, event, session, at)
}

func TestCallerEventStepDeliveredOnceWhileTurnRuns(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	// WAITOPEN holds the turn open until fakeclaude.open exists, so the turn ends by itself and not by the owner's
	// Interrupt, which would leave the caller quiet (see TestOwnerInterruptQuietsCallerEventsUntilHisSay).
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "WAITOPEN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The result carries ASK, so the caller's reply to the event asks a question, which it leaves open.
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "ASK which color is the banner?")

	block := wantStepEndBlock("ASK which color is the banner?")
	caller := store.CallerSession{ID: id, Kind: store.KindEngine}
	if sent, err := newStepEndSender(t, m, s).Send(t.Context(), caller, callerStepEvent, block); !sent || err != nil {
		t.Fatalf("Send to a caller whose turn runs = %v, %v, want true, nil", sent, err)
	}
	if !eventMarked(t, s, id) {
		t.Fatalf("step end not marked after its delivery")
	}
	// A pass over the same store lists the step no more, so it sends nothing.
	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	want := []deliveredPrompt{
		{Text: "WAITOPEN"},
		{
			Text:     block,
			PromptID: callerStepEvent,
			Sender:   store.PromptSenderEvent,
		},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts while the turn runs = %+v, want %+v", got, want)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the turn: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the caller's open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if question, ok := lastQuestion(t, history); !ok || question != "Which color?" {
		t.Fatalf("question = %q (set %v), want the caller's question left open", question, ok)
	}
	if requestIDOf(t, history) == "" {
		t.Fatalf("no request of the caller is open after the delivery")
	}

	// A later pass over the same store sends the step end no second time.
	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a later pass = %+v, want %+v", got, want)
	}
}

func TestCallerEventStepLostMarkNotDeliveredAgain(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	if err = stepEndPass(newStepEndSender(t, m, &lostMark{Store: s}), s).Pass(t.Context()); err == nil {
		t.Fatalf("pass whose mark was refused returned no error")
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the step end done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
	})
	if eventMarked(t, s, id) {
		t.Fatalf("step end marked although its mark was refused")
	}

	// The step is still listed, and its prompt id is already stored, so the send repeats nothing and marks the event.
	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: wantStepEndBlock("all green"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatalf("step end not marked by the pass that found its prompt stored")
	}
}

func TestCallerEventStepReadStepSendsNothing(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: wantStepEndBlock("all green"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventStepStepCallerReceivesNothing(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	stepCaller := store.EngineAdd{ID: "step-caller", Dir: t.TempDir(), State: "working", Kind: store.KindStep}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{stepCaller}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine add step caller: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: "step-caller"}, "all green")

	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	// A step caller is marked without a delivery, as a delivery to it would fail with no resumable session.
	if !eventMarked(t, s, "step-caller") {
		t.Fatalf("step end of a step caller not marked")
	}
}

func TestCallerEventStepIdleStoppedCallerResumed(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	s := storeOf(t, m)
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
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	history := waitHistory(t, engineOf(m), path, id, "the reply to the step end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: wantStepEndBlock("all green"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventStepOwnerStoppedCallerNotRestarted(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
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
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	caller := store.CallerSession{ID: id, Kind: store.KindEngine}
	block := wantStepEndBlock("all green")
	if sent, err := newStepEndSender(t, m, s).Send(t.Context(), caller, callerStepEvent, block); sent || err != nil {
		t.Fatalf("Send to an owner-stopped caller = %v, %v, want false, nil", sent, err)
	}
	passStep(t, stepEndPass(newStepEndSender(t, m, s), s))
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want the start only: the stopped caller is not restarted", args)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "hi"}}) {
		t.Fatalf("prompts = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatalf("step end marked for a stopped caller that received nothing")
	}
}

func TestPaneWakeStepHeldUntilKeysSentThenMarked(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	const server, pane = "-S /tmp/tmux-1000/orrery", "%7"
	if err := s.SyncPane(t.Context(), server, store.Pane{ID: "orrery-1", Pane: pane}, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Server: server, Pane: pane}, "all green")

	idle := false
	var woken []string
	sender := NewCallerSender(CallerSenderConfig{
		Deliver: func(context.Context, DeliverRequest) error {
			t.Error("a pane caller received a headless delivery")
			return nil
		},
		WakePane: func(_ context.Context, gotServer, gotPane, text string) (bool, error) {
			if gotServer != server || gotPane != pane {
				t.Errorf("WakePane to %q %q, want %q %q", gotServer, gotPane, server, pane)
			}
			woken = append(woken, text)
			return idle, nil
		},
		Store: s,
		Now:   fixedClock,
	})
	pass := NewStepCallerEvents(StepCallerEventsConfig{Store: s, Sender: sender})

	passStep(t, pass)
	if len(woken) != 1 {
		t.Fatalf("wakes while the pane is not idle = %d, want 1", len(woken))
	}
	if eventMarked(t, s, "orrery-1") {
		t.Fatalf("step end marked while its pane was not idle")
	}

	idle = true
	passStep(t, pass)
	if len(woken) != 2 {
		t.Fatalf("wakes after the pane is idle = %d, want 2", len(woken))
	}
	if woken[1] != wantStepEndBlock("all green") {
		t.Fatalf("woken text = %q, want the step end block", woken[1])
	}
	if !eventMarked(t, s, "orrery-1") {
		t.Fatalf("step end not marked after its keys were sent")
	}

	passStep(t, pass)
	if len(woken) != 2 {
		t.Fatalf("wakes after the mark = %d, want still 2", len(woken))
	}
}

func TestStepEventCardNamesAgentWithoutEpicLine(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	got := stepEndTextToPane(t, s, store.StepAdd{ID: callerStepID, Agent: "reviewer", Prompt: "check it"})
	want := "<orrery-event kind=\"step_end\" id=\"step:step-1\">\n" +
		"Step step-1 ended: end done, status passed, reason gate green.\n" +
		"Agent: reviewer\n" +
		"Summary: all green\n" +
		"step_status gives the full result.\n" +
		"</orrery-event>"
	if got != want {
		t.Fatalf("step end text = %q, want %q", got, want)
	}
}

func TestStepEventCardNamesEpicOfEpicHeldStep(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	got := stepEndTextToPane(t, s, store.StepAdd{
		ID: callerStepID, Agent: "implementer", Prompt: "do it", Epic: "epic-1",
	})
	want := "<orrery-event kind=\"step_end\" id=\"step:step-1\">\n" +
		"Step step-1 ended: end done, status passed, reason gate green.\n" +
		"Agent: implementer\n" +
		"Epic: epic-1\n" +
		"Summary: all green\n" +
		"step_status gives the full result.\n" +
		"</orrery-event>"
	if got != want {
		t.Fatalf("step end text = %q, want %q", got, want)
	}
}

// TestStepEndSummaryTellsAStructuredResultByItsShortFields: a step whose result is one JSON object ends with a summary
// of its short top-level fields in the object's order, its long answer, its array and its null left for step_status.
func TestStepEndSummaryTellsAStructuredResultByItsShortFields(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	result := `{"status":"done","answer":"` + strings.Repeat("x", 7000) +
		`","evidence":["a.go:1"],"verdict":"READY","count":3,"clean":true,"gap":null,"note":"two\nlines"}`
	add := store.StepAdd{ID: callerStepID, Agent: "implementer", Prompt: "do it"}
	got := stepEndTextOfResultToPane(t, s, add, result)
	want := wantStepEndBlock("status: done; verdict: READY; count: 3; clean: true; note: two lines")
	if got != want {
		t.Fatalf("step end text = %q, want %q", got, want)
	}
}

// TestStepEndSummaryOfOtherResults: a result that is not one JSON object with a short field is told by its start, cut to
// stepSummaryCap runes, and a short one whole.
func TestStepEndSummaryOfOtherResults(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", 7000)
	cut := strings.Repeat("y", stepSummaryCap) + "..."
	for _, tc := range []struct{ name, result, want string }{
		{"short text", "all green", "all green"},
		{"long text", long, cut},
		{"object with no short field", `{"answer":"` + long + `"}`, capped(`{"answer":"`+long+`"}`, stepSummaryCap)},
		{"array", `["a","b"]`, `["a","b"]`},
		{"object then more", `{"status":"done"} trailing`, `{"status":"done"} trailing`},
		{"broken object", `{"status":"done"`, `{"status":"done"`},
	} {
		if got := stepSummary(tc.result); got != tc.want {
			t.Errorf("%s: stepSummary = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestStepEndReasonIsItsFirstLineCut checks that a step end's event tells the step's reason by its first line, cut to
// stepReasonCap runes, so a failed gate's output tails in the reason never reach the caller's context.
func TestStepEndReasonIsItsFirstLineCut(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("z", 3000)
	for _, tc := range []struct{ name, reason, want string }{
		{"one line", "gate green", "gate green"},
		{"empty", "", ""},
		{"long line", long, strings.Repeat("z", stepReasonCap) + "..."},
		{"gate tails", "gate: exit 1\nstdout: " + long + "\nstderr: " + long, "gate: exit 1..."},
		{"trailing newline", "gate green\n", "gate green"},
	} {
		if got := stepReason(tc.reason); got != tc.want {
			t.Errorf("%s: stepReason = %q, want %q", tc.name, got, tc.want)
		}
	}
	body := stepEndBody(store.StepRow{ID: "s1", End: "checks_failed", Reason: "gate: exit 1\n" + long, Agent: "a"})
	lines := strings.Split(body, "\n")
	want := "Step s1 ended: end checks_failed, status , reason gate: exit 1...."
	if lines[0] != want || lines[1] != "Agent: a" {
		t.Errorf("stepEndBody's first lines = %q, %q; want %q, %q", lines[0], lines[1], want, "Agent: a")
	}
}
