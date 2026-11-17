package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stdinLines returns the lines the fake claude in dir has read from its stdin, in order.
func stdinLines(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read the fake's stdin: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// isInterrupt reports whether line is an interrupt request.
func isInterrupt(line string) bool {
	return strings.Contains(line, `"subtype":"interrupt"`)
}

// isUserPrompt reports whether line is a user message whose text is text.
func isUserPrompt(line, text string) bool {
	return strings.Contains(line, `"type":"user"`) && strings.Contains(line, `"`+text+`"`)
}

// assertInterruptBefore checks that the fake claude in dir read exactly one interrupt, and read it before the prompt text.
func assertInterruptBefore(t *testing.T, dir, text string) {
	t.Helper()
	lines := stdinLines(t, dir)
	interrupt := slices.IndexFunc(lines, isInterrupt)
	prompt := slices.IndexFunc(lines, func(line string) bool { return isUserPrompt(line, text) })
	if interrupt < 0 || prompt < 0 || interrupt > prompt {
		t.Fatalf("stdin = %q, want an interrupt before the prompt %q", lines, text)
	}
	if n := len(slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return !isInterrupt(line) })); n != 1 {
		t.Fatalf("stdin holds %d interrupts, want 1: %q", n, lines)
	}
}

// assertWrittenNotHeld checks, as a Say returns, that session id holds no message and has the prompt promptID written.
func assertWrittenNotHeld(t *testing.T, m *Manager, id, promptID string) {
	t.Helper()
	held, err := storeOf(t, m).HeldPrompts(t.Context(), id)
	if err != nil {
		t.Fatalf("HeldPrompts: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("held messages after the Say = %+v, want none", held)
	}
	sent, err := storeOf(t, m).EnginePromptSent(t.Context(), id, promptID)
	if err != nil {
		t.Fatalf("EnginePromptSent: %v", err)
	}
	if !sent {
		t.Fatalf("the Say's prompt %s is not written when Say returns", promptID)
	}
}

// TestOwnerSayPreemptsAnEventTurn delivers a caller event whose turn runs until an interrupt (HANG). The owner's Say
// during it is not held: the event's turn is interrupted, which sends the owner no notice, and the Say is written as the
// next prompt at once, after the interrupt, and is answered as the next turn.
func TestOwnerSayPreemptsAnEventTurn(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := Notice{Session: id, Dir: dir, State: "done", Body: "fake reply 1"}
	if got := waitNotices(t, engineOf(m), sink); len(got) != 1 || got[0] != first {
		t.Fatalf("notices after the first turn = %+v, want exactly [%+v]", got, first)
	}
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "HANG event", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the event's hanging turn", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 2")
	})

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	assertWrittenNotHeld(t, m, id, "p-owner")
	history := waitHistory(t, engineOf(m), path, id, "the owner's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == "done"
	})
	if !turnEndsWithError(t, history) {
		t.Fatal("the event's turn did not end interrupted")
	}
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "HANG event", PromptID: "e1", Sender: store.PromptSenderEvent},
		{Text: "owner words", PromptID: "p-owner"},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	assertInterruptBefore(t, dir, "owner words")
	waitUntil(t, engineOf(m), "the owner's turn's notice", func() bool { return len(sink.got()) > 1 })
	second := Notice{Session: id, Dir: dir, State: "done", Body: "fake reply 3"}
	if got := sink.got(); len(got) != 2 || got[1] != second {
		t.Fatalf("notices = %+v, want [%+v %+v]: the interrupted event turn sends none", got, first, second)
	}
}

// TestOwnerSayDuringOwnerTurnIsHeld runs an owner's turn that runs until an interrupt (HANG, sent by Say). The owner's
// next Say during it is held, as before, and nothing interrupts the turn. Once the owner interrupts it, the held message
// is written as the next turn.
func TestOwnerSayDuringOwnerTurnIsHeld(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "HANG owner", PromptID: "p-hang"}); err != nil {
		t.Fatalf("Say HANG: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the owner's hanging turn", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 2")
	})

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	held, err := storeOf(t, m).HeldPrompts(t.Context(), id)
	if err != nil {
		t.Fatalf("HeldPrompts: %v", err)
	}
	if want := []store.HeldPrompt{{PromptID: "p-owner", Text: "owner words"}}; !slices.Equal(held, want) {
		t.Fatalf("held messages during the owner's turn = %+v, want %+v", held, want)
	}
	if lines := stdinLines(t, dir); slices.ContainsFunc(lines, isInterrupt) {
		t.Fatalf("stdin = %q, want no interrupt of the owner's turn", lines)
	}

	if err = m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the held message's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == "done"
	})
	if got := promptTexts(t, path, id); !slices.Equal(got, []string{"start", "HANG owner", "owner words"}) {
		t.Fatalf("prompts = %q, want [start HANG owner, owner words]", got)
	}
}
