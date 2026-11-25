package enginehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// subagentBlocks returns the number of engine_block events in history whose parent tool use is parent.
func subagentBlocks(t *testing.T, history store.EngineHistory, parent string) int {
	t.Helper()
	n := 0
	for _, event := range history.Events {
		if event.Kind != store.EventEngineBlock {
			continue
		}
		var payload struct {
			ParentToolUseID string `json:"parentToolUseId"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode block payload %q: %v", event.Payload, err)
		}
		if payload.ParentToolUseID == parent {
			n++
		}
	}
	return n
}

// subagentBlocksStored lets the background subagent of SUBAGENT stream, by creating fakeclaude.release in dir, waits
// until the store at path has both its blocks, its text and its tool call, under the Agent call's id, and returns the
// history then.
func subagentBlocksStored(t *testing.T, m *Manager, path, dir, id string) store.EngineHistory {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("let the subagent stream: %v", err)
	}
	return waitHistory(t, engineOf(m), path, id, "the subagent's blocks", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake subagent 1") && subagentBlocks(t, h, "toolu_agent_1") == 2
	})
}

// TestSubagentBlocksAfterTurnEndLeaveSessionHeldAndSayIsSent runs SUBAGENT: its turn launches a subagent in the
// background and ends, and the subagent then streams a text block and a tool call under the Agent call's id with no turn
// of the main conversation open. The turn's end leaves the session working and held, with no notice, and the
// subagent's blocks keep it so. A Say is written as the next prompt at once, with nothing held, and is answered; the
// subagent is still listed when that turn ends, so it is held too.
func TestSubagentBlocksAfterTurnEndLeaveSessionHeldAndSayIsSent(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "SUBAGENT"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)

	// The pump records the subagent's blocks after it has told the owner of the turn's end, so the notices are final.
	history := subagentBlocksStored(t, m, path, dir, id)
	if history.State != "working" || !heldRow(t, m, id) {
		t.Fatalf(
			"state after the subagent's blocks = %q, held %t, want working and held",
			history.State,
			heldRow(t, m, id),
		)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices while the subagent runs = %+v, want none", got)
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "hello", PromptID: "p-hello"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	assertWritten(t, m, id, "p-hello")
	waitHistory(t, engineOf(m), path, id, "the Say's answer", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "working"
	})
	if !heldRow(t, m, id) {
		t.Fatal("row after the Say's turn ended with the subagent listed is not held")
	}
	if got := promptTexts(t, path, id); !slices.Equal(got, []string{"SUBAGENT", "hello"}) {
		t.Fatalf("prompts = %q, want [SUBAGENT hello]", got)
	}
}

// TestTurnClaudeStartsAfterSubagentBlocksStillOpens runs SUBAGENTSELF: the subagent's blocks leave the session held,
// and once the task ends, the turn claude starts by itself on its main-conversation text block opens a turn, which
// clears the hold. That turn is claude's own, so the owner's Say made then is not held and interrupts nothing: the Say is written as
// the next prompt at once, and the turn reads it from stdin, which ends only at an interrupt.
func TestTurnClaudeStartsAfterSubagentBlocksStillOpens(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "SUBAGENTSELF"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)
	if history := subagentBlocksStored(t, m, path, dir, id); history.State != "working" {
		t.Fatalf("state after the subagent's blocks = %q, want working", history.State)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("end the subagent's task: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the self-started turn's block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake notified 1")
	})
	if history.State != "working" {
		t.Fatalf("state while the turn claude started by itself runs = %q, want working", history.State)
	}
	waitUntil(t, engineOf(m), "the self-started turn to clear the hold", func() bool { return !heldRow(t, m, id) })

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "hello", PromptID: "p-hello"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	assertWritten(t, m, id, "p-hello")
	waitUntil(t, engineOf(m), "the fake to read the Say", func() bool { return stdinLinesWith(t, dir, "hello") > 0 })
	if got := promptTexts(t, path, id); !slices.Equal(got, []string{"SUBAGENTSELF", "hello"}) {
		t.Fatalf("prompts = %q, want [SUBAGENTSELF hello]", got)
	}
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the Say = %d, want none", n)
	}
}
