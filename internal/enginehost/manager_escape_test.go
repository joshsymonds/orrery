package enginehost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// interruptEnvelope is a control request the engine host wrote to the fake claude's stdin, decoded from its line.
type interruptEnvelope struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype      string `json:"subtype"`
		CancelQueued bool   `json:"cancel_queued"`
	} `json:"request"`
}

// interruptEnvelopesOf returns the interrupt requests the fake claude in dir has read from its stdin, each decoded as
// JSON, in their order. A line that is not JSON fails the test.
func interruptEnvelopesOf(t *testing.T, dir string) []interruptEnvelope {
	t.Helper()
	var interrupts []interruptEnvelope
	for _, line := range stdinLines(t, dir) {
		if !isInterrupt(line) {
			continue
		}
		var req interruptEnvelope
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("interrupt line %q is not JSON: %v", line, err)
		}
		interrupts = append(interrupts, req)
	}
	return interrupts
}

// startQueuedBehindHang starts a HANG session on m in dir, whose turn runs until an interrupt. While that turn runs it
// sends the owner "first" and then "second", and a caller event "the event words" with prompt id e1. Each Say and the
// Deliver must return nil, since each is written at once. It returns the session id once the turn's first block is
// recorded.
func startQueuedBehindHang(t *testing.T, m *Manager, path, dir string) string {
	t.Helper()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	for _, text := range []string{"first", "second"} {
		if err = m.Say(t.Context(), SayRequest{ID: id, Text: text, PromptID: "p-" + text}); err != nil {
			t.Fatalf("Say %q during the hanging turn: %v", text, err)
		}
	}
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event words", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver during the hanging turn: %v", err)
	}
	return id
}

// TestEscapeQueuedInterruptReturnsOwnerTextsInSendOrder sends two owner messages and a caller event during a HANG turn,
// then interrupts it. The fake reads each line while the turn runs, so each was written at once. The owner's interrupt
// writes one cancel_queued interrupt and returns the owner's two texts in send order, without the event's text. The
// interrupted turn's end leaves the session done, and no turn runs for a cancelled line.
func TestEscapeQueuedInterruptReturnsOwnerTextsInSendOrder(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id := startQueuedBehindHang(t, m, path, dir)
	waitUntil(t, engineOf(m), "the fake reads the queued lines during the turn", func() bool {
		return stdinLinesWith(t, dir, `"text":"first"}`) == 1 &&
			stdinLinesWith(t, dir, `"text":"second"}`) == 1 &&
			stdinLinesWith(t, dir, `"text":"the event words"}`) == 1
	})

	texts, err := m.OwnerInterrupt(t.Context(), id)
	if err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	if want := []string{"first", "second"}; !slices.Equal(texts, want) {
		t.Fatalf("OwnerInterrupt texts = %q, want %q", texts, want)
	}
	interrupts := interruptEnvelopesOf(t, dir)
	if len(interrupts) != 1 {
		t.Fatalf("interrupt requests = %+v, want one", interrupts)
	}
	req := interrupts[0]
	if req.Type != "control_request" {
		t.Fatalf("interrupt type = %q, want control_request", req.Type)
	}
	if req.RequestID == "" {
		t.Fatal("interrupt carries no request_id")
	}
	if req.Request.Subtype != "interrupt" {
		t.Fatalf("interrupt subtype = %q, want interrupt", req.Request.Subtype)
	}
	if !req.Request.CancelQueued {
		t.Fatal("interrupt does not set cancel_queued")
	}

	history := waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	if n := turnEndCount(history); n != 1 {
		t.Fatalf("turn ends = %d, want the held turn alone: no turn runs for a cancelled line", n)
	}
}

// TestEscapeQueuedReadoptedSessionOwesNothingCancelled interrupts a HANG session that holds two owner messages and a
// caller event, then crashes the manager. The re-adopted session owes no prompt, and the cancelled texts are not written
// to the fake again: a later Say is the only line the fake reads after the restart.
func TestEscapeQueuedReadoptedSessionOwesNothingCancelled(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id := startQueuedBehindHang(t, rig.manager, rig.path, dir)
	if _, err := rig.manager.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	waitHistory(t, engineOf(rig.manager), rig.path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})

	rig.crash.refuse()
	restarted, _ := restartAfterCrash(t, rig)
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	owed, err := rig.store.HolderPromptsUnanswered(t.Context(), id)
	if err != nil {
		t.Fatalf("HolderPromptsUnanswered: %v", err)
	}
	if owed != 0 {
		t.Fatalf("prompts owed after the restart = %d, want none", owed)
	}

	if err = restarted.Say(t.Context(), SayRequest{ID: id, Text: "after", PromptID: "p-after"}); err != nil {
		t.Fatalf("Say after the restart: %v", err)
	}
	waitHistory(t, engineOf(rig.manager, restarted), rig.path, id, "the reply to the Say after the restart",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
		})
	for _, text := range []string{`"text":"first"}`, `"text":"second"}`} {
		if n := stdinLinesWith(t, dir, text); n != 1 {
			t.Fatalf("stdin lines with %s = %d, want the one written before the interrupt", text, n)
		}
	}
}

// TestEscapeQueuedIdleInterruptReturnsNoTexts interrupts a session whose turn has ended and that holds no queued line.
// Claude answers the interrupt with an empty list, so OwnerInterrupt returns no texts and no error.
func TestEscapeQueuedIdleInterruptReturnsNoTexts(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})

	texts, err := m.OwnerInterrupt(t.Context(), id)
	if err != nil {
		t.Fatalf("OwnerInterrupt of an idle session: %v", err)
	}
	if len(texts) != 0 {
		t.Fatalf("OwnerInterrupt texts = %q, want none", texts)
	}
}
