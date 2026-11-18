package enginehost

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cancelEnvelope is a control request the engine host wrote to the fake claude's stdin, decoded from its line.
type cancelEnvelope struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype     string `json:"subtype"`
		MessageUUID string `json:"message_uuid"`
	} `json:"request"`
}

// cancelEnvelopesOf returns the lines the fake claude in dir has read from its stdin that name a cancel_async_message
// request, each decoded as JSON, in their order. It does not filter on the envelope's type, so a line of another type
// shows in the result for the caller to check, and a line that is not JSON fails the test.
func cancelEnvelopesOf(t *testing.T, dir string) []cancelEnvelope {
	t.Helper()
	var cancels []cancelEnvelope
	for _, line := range stdinLines(t, dir) {
		if !strings.Contains(line, `"cancel_async_message"`) {
			continue
		}
		var req cancelEnvelope
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("cancel request line %q is not JSON: %v", line, err)
		}
		cancels = append(cancels, req)
	}
	return cancels
}

// holdWithdrawnLine starts a HANG session on m in dir, whose turn runs until an interrupt, and sends the owner the
// message "withdrawn words" under prompt id p-held while that turn runs. The fake reads the message and keeps it
// queued, since the turn takes no line in. It returns the session id once the turn's first block is recorded.
func holdWithdrawnLine(t *testing.T, m *Manager, path, dir string) string {
	t.Helper()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "withdrawn words", PromptID: "p-held"}); err != nil {
		t.Fatalf("Say during the hanging turn: %v", err)
	}
	return id
}

// TestWithdrawQueuedCancelledTrueReturnsTheTextAndWritesOneCancel withdraws the owner's line while the turn that holds it
// keeps it queued. The session writes one cancel_async_message control request that names the line's write id, and
// Withdraw returns the line's text.
func TestWithdrawQueuedCancelledTrueReturnsTheTextAndWritesOneCancel(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id := holdWithdrawnLine(t, m, path, dir)

	texts, err := m.Withdraw(t.Context(), id, "p-held")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if want := []string{"withdrawn words"}; !slices.Equal(texts, want) {
		t.Fatalf("Withdraw texts = %q, want %q", texts, want)
	}
	writeID := writeIDOf(t, path, id, "withdrawn words")
	cancels := cancelEnvelopesOf(t, dir)
	if len(cancels) != 1 {
		t.Fatalf("cancel requests = %+v, want one", cancels)
	}
	cancel := cancels[0]
	if cancel.Type != "control_request" {
		t.Fatalf("cancel request type = %q, want control_request", cancel.Type)
	}
	if cancel.RequestID == "" {
		t.Fatal("cancel request carries no request_id")
	}
	if cancel.Request.Subtype != "cancel_async_message" {
		t.Fatalf("cancel request subtype = %q, want cancel_async_message", cancel.Request.Subtype)
	}
	if cancel.Request.MessageUUID != writeID {
		t.Fatalf("cancel request message_uuid = %q, want the prompt's write id %q", cancel.Request.MessageUUID, writeID)
	}
}

// TestWithdrawQueuedTurnEndLeavesSessionDoneAndRunsNoWithdrawnLine withdraws the held line, then ends its turn with the
// owner's interrupt. The turn's end leaves the session done, and no turn runs for the withdrawn line.
func TestWithdrawQueuedTurnEndLeavesSessionDoneAndRunsNoWithdrawnLine(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id := holdWithdrawnLine(t, m, path, t.TempDir())
	if _, err := m.Withdraw(t.Context(), id, "p-held"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if err := m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	if n := turnEndCount(history); n != 1 {
		t.Fatalf("turn ends = %d, want the held turn alone", n)
	}
	if hasBlock(t, history, "fake reply 2") {
		t.Fatal("the withdrawn line ran as a turn of its own")
	}
}

// TestWithdrawQueuedReadoptedSessionOwesNoPrompt withdraws the held line, ends its turn, and crashes the manager. The
// re-adopted session owes no prompt, and the withdrawn text is never written to the fake again: a later Say is the only
// line the fake reads after the restart.
func TestWithdrawQueuedReadoptedSessionOwesNoPrompt(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id := holdWithdrawnLine(t, rig.manager, rig.path, dir)
	if _, err := rig.manager.Withdraw(t.Context(), id, "p-held"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if err := rig.manager.OwnerInterrupt(t.Context(), id); err != nil {
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
	if n := stdinLinesWith(t, dir, "withdrawn words"); n != 1 {
		t.Fatalf("stdin lines with the withdrawn text = %d, want the one written before its withdraw", n)
	}
}

// TestWithdrawQueuedCancelledFalseForTakenInLineReturnsNoText has the owner send a line during a turn that makes a tool
// call and takes the line in with the call's result, so the line is started before Withdraw asks for it. Claude reports
// that it did not cancel the line, and Withdraw returns no texts and no error.
func TestWithdrawQueuedCancelledFalseForTakenInLineReturnsNoText(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "MIDTURN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during the turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn that took the line in", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 1") && turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})

	texts, err := m.Withdraw(t.Context(), id, "p-owner")
	if err != nil {
		t.Fatalf("Withdraw of a line Claude took in: %v", err)
	}
	if len(texts) != 0 {
		t.Fatalf("Withdraw texts = %q, want none", texts)
	}
	if n := len(cancelEnvelopesOf(t, dir)); n != 1 {
		t.Fatalf("cancel requests = %d, want the one Withdraw sent", n)
	}
}

// TestWithdrawQueuedEmptyPromptIDWritesNothing withdraws with no prompt id while the held line is queued. Withdraw
// returns no texts and no error. A later Say, which the fake reads after anything Withdraw could have written, is read
// directly after the held line: nothing Withdraw wrote lies between them.
func TestWithdrawQueuedEmptyPromptIDWritesNothing(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id := holdWithdrawnLine(t, m, path, dir)

	texts, err := m.Withdraw(t.Context(), id, "")
	if err != nil {
		t.Fatalf("Withdraw with no prompt id: %v", err)
	}
	if len(texts) != 0 {
		t.Fatalf("Withdraw texts = %q, want none", texts)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "marker words", PromptID: "p-marker"}); err != nil {
		t.Fatalf("Say marker: %v", err)
	}
	waitUntil(t, engineOf(m), "the marker line read by the fake", func() bool {
		return stdinLinesWith(t, dir, "marker words") == 1
	})
	lines := stdinLines(t, dir)
	n := len(lines)
	if n < 2 || !strings.Contains(lines[n-2], "withdrawn words") || !strings.Contains(lines[n-1], "marker words") {
		t.Fatalf("the fake's stdin ends %q, want the held line then the marker with nothing between", lines)
	}
}

// TestWithdrawQueuedUnknownPromptIsRefused withdraws a prompt id no owner prompt of the session carries. Withdraw
// returns an error matching ErrUnknownPrompt and no texts.
func TestWithdrawQueuedUnknownPromptIsRefused(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id := holdWithdrawnLine(t, m, path, t.TempDir())

	texts, err := m.Withdraw(t.Context(), id, "p-unknown")
	if !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("Withdraw of an unknown prompt error = %v, want one matching ErrUnknownPrompt", err)
	}
	if len(texts) != 0 {
		t.Fatalf("Withdraw texts = %q, want none", texts)
	}
}
