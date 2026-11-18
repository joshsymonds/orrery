package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSpeakLabelSayWrittenAtOnceStoresOwnerSender interrupts an idle session and then has the owner Say to it. The Say
// is written at once, so its prompt is stored with the owner as its sender and with its prompt id.
func TestSpeakLabelSayWrittenAtOnceStoresOwnerSender(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})

	if err = m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	assertWrittenNotHeld(t, m, id, "p-owner")
	history := waitHistory(t, engineOf(m), path, id, "the owner's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

// TestSpeakLabelSayHeldStoresOwnerSenderOnRelease interrupts a turn that the fake keeps open until the test releases
// it, and has the owner Say during it. The Say is held, since the owner's turn runs, and once the turn ends the held
// message is written as the next prompt, stored with the owner as its sender and with its prompt id.
func TestSpeakLabelSayHeldStoresOwnerSenderOnRelease(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TRICKLEHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the trickle's last block before the hold", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake trickle 1.59")
	})

	if err = m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	want := []store.HeldPrompt{{PromptID: "p-owner", Text: "owner words"}}
	held, err := storeOf(t, m).HeldPrompts(t.Context(), id)
	if err != nil {
		t.Fatalf("HeldPrompts: %v", err)
	}
	if !slices.Equal(held, want) {
		t.Fatalf("held messages during the owner's turn = %+v, want %+v", held, want)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fake claude release: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the held message's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})
	wantPrompts := []deliveredPrompt{
		{Text: "TRICKLEHELD"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, wantPrompts) {
		t.Fatalf("prompts = %+v, want %+v", got, wantPrompts)
	}
}
