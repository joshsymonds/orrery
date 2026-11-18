package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestSpeakLabelSayDuringTurnIsWrittenAtOnceStoresOwnerSender has the owner Say during a turn the fake keeps open until the
// test releases it. The Say is written at once, stored with the owner as its sender and with its prompt id, while the turn
// still runs. Once the turn ends, claude answers the message as the turn after it.
func TestSpeakLabelSayDuringTurnIsWrittenAtOnceStoresOwnerSender(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TRICKLEHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the trickle's last block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake trickle 1.59")
	})

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	want := []deliveredPrompt{
		{Text: "TRICKLEHELD"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts while the owner's Say is written = %+v, want %+v", got, want)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fake claude release: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the written message's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}
