package enginehost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestTurnClaudeStartsByItselfUnderAHoldRecordsWorkingThroughItsEnd runs BGSELFHELD: its first turn ends while two
// background agents run, so the session is held working with no notice. Once fakeclaude.release exists the first agent
// ends and claude starts a turn by itself, reading no prompt: the turn runs, so the hold clears and the owner is told the
// session changed. That turn is held after its text block until fakeclaude.open exists, and it ends while the second
// agent still runs, so the session is held working again, and no notice is sent at any point.
func TestTurnClaudeStartsByItselfUnderAHoldRecordsWorkingThroughItsEnd(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGSELFHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)
	changedBefore := len(sink.changedCalls())

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("end the first background agent: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the self-started turn's block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake self 1")
	})
	if history.State != "working" {
		t.Fatalf("state while the turn claude started by itself runs = %q, want working", history.State)
	}
	// The pump tells the owner after it stores the line that opened the turn.
	waitUntil(t, engineOf(m), "the owner told the session changed", func() bool {
		return len(sink.changedCalls()) > changedBefore
	})
	if heldRow(t, m, id) {
		t.Fatal("row is held while the turn claude started by itself runs")
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("let the self-started turn end: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the self-started turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "working"
	})
	if !heldRow(t, m, id) {
		t.Fatal("row after the self-started turn ended with the second agent listed is not held")
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices = %+v, want none while an agent runs", got)
	}
}
