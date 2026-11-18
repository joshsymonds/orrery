package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// deliverNowSummary is the summary of the step end the DeliverNow tests send, as addEndedCallerStep stores it.
const deliverNowSummary = "the work is in"

// stepEndText is the first line of the step end the DeliverNow tests send. The fake claude's stdin carries it once the
// step end is written.
const stepEndText = "Step " + callerStepID + " ended"

// stepEndPrompt is the engine_prompt the step end of the DeliverNow tests is stored as, sent by the event sender.
func stepEndPrompt() deliveredPrompt {
	return deliveredPrompt{
		Text: wantStepEndBlock(deliverNowSummary), PromptID: callerStepEvent, Sender: store.PromptSenderEvent,
	}
}

// stepEndWrites returns how many step ends the fake claude in dir has read from its stdin, across every process it has
// run in dir. A resumed process appends to the same log as the processes before it, so a test takes a process's own
// step ends as the count beyond those the earlier processes read (see awaitStepEndWritesFrom).
func stepEndWrites(t *testing.T, dir string) int {
	t.Helper()
	return stdinLinesWith(t, dir, stepEndText)
}

// awaitStepEndWrites waits until the fake claude in dir has read step ends from its stdin, then requires exactly n.
func awaitStepEndWrites(t *testing.T, m *Manager, dir string, n int) {
	t.Helper()
	awaitStepEndWritesFrom(t, m, dir, 0, n)
}

// awaitStepEndWritesFrom waits until the fake claude in dir has read step ends from its stdin beyond the first
// before of them, then requires exactly n of those. before is what the processes that ran before the one under test
// read, which has ended by the time the test calls this, so the count is the one under test's own.
func awaitStepEndWritesFrom(t *testing.T, m *Manager, dir string, before, n int) {
	t.Helper()
	waitUntil(t, engineOf(m), fmt.Sprintf("the fake claude to read %d step end(s)", n), func() bool {
		return stepEndWrites(t, dir)-before >= n
	})
	if got := stepEndWrites(t, dir) - before; got != n {
		t.Fatalf("step ends read by the fake claude = %d, want %d", got, n)
	}
}

// sayMarker has the owner Say text to id as promptID, then waits until the fake claude in dir has read that line, and
// returns how many step ends the fake had read by then. The fake reads its stdin in order, so each step end the test
// wrote before the Say is read before the line and counted, and a step end the test did not write is not counted.
func sayMarker(t *testing.T, m *Manager, dir, id, text, promptID string) int {
	t.Helper()
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: text, PromptID: promptID}); err != nil {
		t.Fatalf("Say %q: %v", text, err)
	}
	waitUntil(t, engineOf(m), "the fake claude to read "+text, func() bool {
		return stdinLinesWith(t, dir, text) >= 1
	})
	return stepEndWrites(t, dir)
}

// waitsForOwnerSpeech reports whether history's newest engine_interrupt is newer than its newest owner prompt, the
// condition under which the server labels the session's pending caller events as waiting for the owner to speak.
func waitsForOwnerSpeech(t *testing.T, history store.EngineHistory) bool {
	t.Helper()
	var interruptSeq, ownerSeq int64
	for _, event := range history.Events {
		switch event.Kind {
		case store.EventEngineInterrupt:
			interruptSeq = event.Seq
		case store.EventEnginePrompt:
			var prompt deliveredPrompt
			if err := json.Unmarshal([]byte(event.Payload), &prompt); err != nil {
				t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
			}
			if prompt.Sender == store.PromptSenderOwner {
				ownerSeq = event.Seq
			}
		}
	}
	return interruptSeq > ownerSeq
}

// TestDeliverNowRunningTurnWritesStepEndOnce ends the step of a caller whose HANG turn runs. One pass stores the step
// end and writes it to the fake's stdin while the turn runs, and marks it. A second pass writes nothing more.
func TestDeliverNowRunningTurnWritesStepEndOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging turn running", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1") && h.State == string(protocol.StateWorking)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	want := []deliveredPrompt{{Text: "HANG"}, stepEndPrompt()}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after one pass = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
	awaitStepEndWrites(t, m, dir, 1)

	pass.Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a second pass = %+v, want %+v", got, want)
	}
	if n := sayMarker(t, m, dir, id, "owner marker", "p-marker"); n != 1 {
		t.Fatalf("step ends read by the fake claude after a second pass = %d, want 1", n)
	}
}

// TestDeliverNowBackgroundTaskWritesStepEndOnce ends the step of a caller whose turn has ended with a background task
// still listed. One pass writes the step end at once and marks it. A second pass writes nothing more.
func TestDeliverNowBackgroundTaskWritesStepEndOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	m.mu.Lock()
	background, turning := live.background, live.turning
	m.mu.Unlock()
	if !background || turning {
		t.Fatalf("background task listed = %v, turn running = %v, want listed and not running", background, turning)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	want := []deliveredPrompt{{Text: "BACKGROUND"}, stepEndPrompt()}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after one pass = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
	awaitStepEndWrites(t, m, dir, 1)

	pass.Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a second pass = %+v, want %+v", got, want)
	}
	if n := sayMarker(t, m, dir, id, "owner marker", "p-marker"); n != 1 {
		t.Fatalf("step ends read by the fake claude after a second pass = %d, want 1", n)
	}
}

// TestDeliverNowQuietCallerWaitsForOwnersSay interrupts an idle caller as its owner. A pass writes nothing and leaves
// the step end unmarked. The owner's Say clears the quiet, and the next pass writes the step end once.
func TestDeliverNowQuietCallerWaitsForOwnersSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if texts, err := m.OwnerInterrupt(t.Context(), id); err != nil || len(texts) != 0 {
		t.Fatalf("OwnerInterrupt of an idle session = %q, %v, want no texts and no error", texts, err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	if eventMarked(t, s, id) {
		t.Fatal("step end marked while the owner's interrupt keeps its caller quiet")
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "start"}}) {
		t.Fatalf("prompts after a quiet pass = %+v, want only the start's prompt", got)
	}
	if n := sayMarker(t, m, dir, id, "owner words", "p-owner"); n != 0 {
		t.Fatalf("step ends read by the fake claude before the owner's Say = %d, want none", n)
	}

	pass.Pass(t.Context())
	awaitStepEndWrites(t, m, dir, 1)
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after the owner's Say and the pass")
	}
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		stepEndPrompt(),
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

// TestDeliverNowStoppedCallerWaitsForOwnersSayToResume stops a caller as its owner. A pass neither resumes it nor
// writes the step end, and leaves the step end unmarked. The owner's Say resumes the session, and the next pass writes
// the step end once, to the resumed process.
func TestDeliverNowStoppedCallerWaitsForOwnersSayToResume(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if err = m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	if eventMarked(t, s, id) {
		t.Fatal("step end marked for a stopped caller that received nothing")
	}
	if _, err = m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after a pass to a stopped caller: err = %v, want ErrNoSession", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts after a pass = %q, want the start only", args)
	}
	// The stopped process has ended, so the step ends it read are all it read: none.
	first := stepEndWrites(t, dir)
	if first != 0 {
		t.Fatalf("step ends read by the stopped process = %d, want none", first)
	}
	if n := sayMarker(t, m, dir, id, "owner words", "p-owner"); n != first {
		t.Fatalf("step ends read by the resumed process before the pass = %d, want none", n-first)
	}
	if args := fakeArgs(t, dir); len(args) != 2 {
		t.Fatalf("fake claude starts after the owner's Say = %q, want the resumed process too", args)
	}

	pass.Pass(t.Context())
	awaitStepEndWritesFrom(t, m, dir, first, 1)
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after the resumed process read it")
	}
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		stepEndPrompt(),
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

// TestDeliverNowOpenQuestionWaitsForAnswer ends the step of a caller whose ASK turn has a question open. A pass writes
// nothing and leaves the step end unmarked. Once the question is answered, the next pass writes the step end once.
func TestDeliverNowOpenQuestionWaitsForAnswer(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	requestID := requestIDOf(t, history)
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	if eventMarked(t, s, id) {
		t.Fatal("step end marked while a question is open")
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "ASK"}}) {
		t.Fatalf("prompts after a pass with a question open = %+v, want only the start's prompt", got)
	}

	// The fake reads its next line as the answer, so a step end written during the question would be taken as it.
	if err = m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	waitUntil(t, engineOf(m), "the fake claude to read the answer", func() bool {
		return stdinLinesWith(t, dir, requestID) >= 1
	})
	if n := stepEndWrites(t, dir); n != 0 {
		t.Fatalf("step ends read by the fake claude before the answer = %d, want none", n)
	}

	pass.Pass(t.Context())
	awaitStepEndWrites(t, m, dir, 1)
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after the answer and the pass")
	}
	want := []deliveredPrompt{{Text: "ASK"}, stepEndPrompt()}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

// TestDeliverNowEscapeCancelledStepEndPendingWaitsSpeakThenWrittenOnceAfterSay has the owner press Escape while a step
// end is queued behind a HANG turn. The step end is not among the texts returned to the composer, and it is unmarked,
// pending, and waiting for the owner to speak. A pass writes nothing until the owner's next Say, and then writes the
// step end once. A further pass writes nothing.
func TestDeliverNowEscapeCancelledStepEndPendingWaitsSpeakThenWrittenOnceAfterSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging turn running", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1") && h.State == string(protocol.StateWorking)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery during the turn")
	}
	awaitStepEndWrites(t, m, dir, 1)
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during the turn: %v", err)
	}
	// The fake reads stdin in order, so the owner's line read means the step end before it was read too.
	waitUntil(t, engineOf(m), "the fake claude to read the owner's line", func() bool {
		return stdinLinesWith(t, dir, "owner words") >= 1
	})

	texts, err := m.OwnerInterrupt(t.Context(), id)
	if err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	if want := []string{"owner words"}; !slices.Equal(texts, want) {
		t.Fatalf("OwnerInterrupt texts = %q, want %q: the step end is not put in the composer", texts, want)
	}
	waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	waitUntil(t, engineOf(m), "the store to unmark the cancelled step end", func() bool {
		return !eventMarked(t, s, id)
	})

	pending, err := store.NewReader(path).PendingCallerEvents(t.Context(), id)
	if err != nil {
		t.Fatalf("PendingCallerEvents: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != callerStepEvent || pending[0].Kind != store.CallerKindStepEnd {
		t.Fatalf("pending caller events = %+v, want the step end alone", pending)
	}
	if !waitsForOwnerSpeech(t, historyOf(t, path, id)) {
		t.Fatal("the newest interrupt is not newer than the owner's prompt, so events do not wait for speech")
	}

	pass.Pass(t.Context())
	if n := sayMarker(t, m, dir, id, "owner after", "p-after"); n != 1 {
		t.Fatalf("step ends read by the fake claude before the owner's next Say = %d, want the cancelled one alone", n)
	}
	pass.Pass(t.Context())
	awaitStepEndWrites(t, m, dir, 2)
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after the owner's next Say and the pass")
	}
	want := []deliveredPrompt{
		{Text: "HANG"},
		stepEndPrompt(),
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: "owner after", PromptID: "p-after", Sender: store.PromptSenderOwner},
		stepEndPrompt(),
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}

	pass.Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a further pass = %+v, want %+v", got, want)
	}
	if n := sayMarker(t, m, dir, id, "owner last", "p-last"); n != 2 {
		t.Fatalf("step ends read by the fake claude after a further pass = %d, want 2", n)
	}
}

// TestDeliverNowUnstartedStepEndAtProcessEndUnmarkedThenWrittenOnceOnResume has a step end queued behind a DIEQUEUED
// turn, whose process exits on its own without starting that line. The step end is unmarked once the exit is recorded.
// The next pass resumes the session and writes the step end once, to the resumed process. A further pass writes
// nothing.
func TestDeliverNowUnstartedStepEndAtProcessEndUnmarkedThenWrittenOnceOnResume(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "DIEQUEUED"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake diequeued 1")
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)

	pass.Pass(t.Context())
	first := []deliveredPrompt{{Text: "DIEQUEUED"}, stepEndPrompt()}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, first) {
		t.Fatalf("prompts after one pass = %+v, want %+v", got, first)
	}
	// The fake reads the step end, queues it and exits on its own.
	awaitStepEndWrites(t, m, dir, 1)
	waitHistory(t, engineOf(m), path, id, "the process's exit", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	waitUntil(t, engineOf(m), "the exited session to leave the manager", func() bool {
		_, err := m.live(id)
		return errors.Is(err, ErrNoSession)
	})
	if eventMarked(t, s, id) {
		t.Fatal("step end still marked after the process ended without starting it")
	}
	// The exited process has read the step end once, and nothing it read is the resumed process's.
	exited := stepEndWrites(t, dir)
	if exited != 1 {
		t.Fatalf("step ends read by the exited process = %d, want 1", exited)
	}

	pass.Pass(t.Context())
	awaitStepEndWritesFrom(t, m, dir, exited, 1)
	if args := fakeArgs(t, dir); len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want the start and the resume", args)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after the resumed process read it")
	}
	history := waitHistory(t, engineOf(m), path, id, "the resumed process's reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
	})
	// The unstarted write stays in the store, and the resumed process's write is a second prompt with the same id.
	want := []deliveredPrompt{{Text: "DIEQUEUED"}, stepEndPrompt(), stepEndPrompt()}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}

	pass.Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a further pass = %+v, want %+v", got, want)
	}
	if n := sayMarker(t, m, dir, id, "owner last", "p-last") - exited; n != 1 {
		t.Fatalf("step ends read by the resumed process after a further pass = %d, want 1", n)
	}
}
