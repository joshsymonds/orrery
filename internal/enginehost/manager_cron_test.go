package enginehost

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// requireWoke receives the next id the cron hook was called with, and fails unless it is want. The hook runs on its own
// goroutine, so each test receives a call before it makes the next one, and the calls come in the order it made them.
func requireWoke(t *testing.T, woke <-chan string, want string) {
	t.Helper()
	if got := <-woke; got != want {
		t.Fatalf("cron hook called with %q, want %q", got, want)
	}
}

func TestCronDeliverManagerSendsWithSenderCron(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})

	req := DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"}
	if err := m.DeliverCron(t.Context(), req); err != nil {
		t.Fatalf("DeliverCron: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the cron prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: "the cron text", PromptID: "c1", Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}

	if err := m.DeliverCron(t.Context(), req); err != nil {
		t.Fatalf("repeated DeliverCron: %v", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a repeated DeliverCron = %+v, want %+v", got, want)
	}
}

func TestCronDeliverManagerBusyDuringTurn(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	// WAITOPEN holds its turn open until fakeclaude.open exists, so the turn is still running when the cron fire comes.
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "WAITOPEN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	err = m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"})
	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("DeliverCron during a turn error = %v, want ErrSessionBusy", err)
	}
	got := enginePromptsOf(t, historyOf(t, path, id))
	if !reflect.DeepEqual(got, []deliveredPrompt{{Text: "WAITOPEN"}}) {
		t.Fatalf("prompts after a busy DeliverCron = %+v, want only the start's prompt", got)
	}
}

func TestCronDeliverManagerBackgroundDoesNotHold(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// BACKGROUND ends its turn while its background task runs, so the session is not busy but holds a task.
	waitHistory(t, engineOf(m), path, id, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})

	if err := m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"}); err != nil {
		t.Fatalf("DeliverCron with a background task listed: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the cron prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "BACKGROUND"},
		{Text: "the cron text", PromptID: "c1", Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCronDeliverManagerOpenRequestHolds(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	requestID := requestIDOf(t, history)

	err = m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"})
	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("DeliverCron with an open question error = %v, want ErrSessionBusy", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "ASK"}}) {
		t.Fatalf("prompts after a busy DeliverCron = %+v, want only the start's prompt", got)
	}
	// The request is still open for the owner, so the DeliverCron declined nothing and the answer is accepted.
	if err := m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer red after a busy DeliverCron: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1")
	})
}

func TestCronDeliverManagerQuietHolds(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	if _, err = m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	req := DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"}

	if err := m.DeliverCron(t.Context(), req); !errors.Is(err, ErrCallerQuiet) {
		t.Fatalf("DeliverCron to a live quiet session error = %v, want ErrCallerQuiet", err)
	}
	pid := fakePID(t, dir)
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	requireGone(t, pid)

	if err := m.DeliverCron(t.Context(), req); !errors.Is(err, ErrCallerQuiet) {
		t.Fatalf("DeliverCron to an idle-stopped quiet session error = %v, want ErrCallerQuiet", err)
	}
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after a refused DeliverCron: err = %v, want ErrNoSession, so nothing resumed it", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "HANG"}}) {
		t.Fatalf("prompts after the refused DeliverCrons = %+v, want only the start's prompt", got)
	}

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the owner's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	if err := m.DeliverCron(t.Context(), req); err != nil {
		t.Fatalf("DeliverCron after the owner's Say: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the cron prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	want := []deliveredPrompt{
		{Text: "HANG"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: "the cron text", PromptID: "c1", Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCronDeliverManagerFailedRowWaits(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "DIE"}); err != nil {
		t.Fatalf("Say DIE: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == stateFailed
	})
	waitUntil(t, engineOf(m), "the failed session to leave the live set", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.sessions[id]
		return !ok
	})

	if err := m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"}); !errors.Is(
		err, ErrCallerFailed,
	) {
		t.Fatalf("DeliverCron to a failed session error = %v, want ErrCallerFailed", err)
	}
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after a refused DeliverCron: err = %v, want ErrNoSession", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want the start only: a failed session starts no process", args)
	}
	got := enginePromptsOf(t, historyOf(t, path, id))
	// The DIE Say carried no prompt id, so it is stored under the one orreryd minted for it, with sender owner.
	if len(got) != 2 || got[1].PromptID == "" {
		t.Fatalf("prompts after a refused DeliverCron = %+v, want the start's and the DIE Say's", got)
	}
	want := []deliveredPrompt{{Text: "hi"}, {Text: "DIE", PromptID: got[1].PromptID, Sender: store.PromptSenderOwner}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a refused DeliverCron = %+v, want %+v", got, want)
	}
}

func TestCronDeliverManagerWakes(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t)
	woke := make(chan string, 8)
	m.cronWake = func(id string) { woke <- id }
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	requireWoke(t, woke, id)

	// STALL never ends its turn, so only the owner's Say below can wake its session.
	stalled, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start STALL: %v", err)
	}
	if err := m.Say(t.Context(), SayRequest{ID: stalled, Text: "later"}); err != nil {
		t.Fatalf("Say to the stalled session: %v", err)
	}
	requireWoke(t, woke, stalled)

	// The stop of the stalled session ends its process.
	if err := m.Stop(t.Context(), stalled); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireWoke(t, woke, "")

	// The idle stop of the first session ends its process.
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	requireWoke(t, woke, "")

	// A cron fire to the idle-stopped session finds its directory gone, so its resume refuses.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove session directory: %v", err)
	}
	err = m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"})
	if !errors.Is(err, ErrBadDir) {
		t.Fatalf("DeliverCron to a session whose directory is gone error = %v, want ErrBadDir", err)
	}
}
