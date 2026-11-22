package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestTurnClaudeStartsByItselfRecordsWorkingUntilItsEnd runs BGSELFHELD: its first turn ends with a background task
// still running, so the session is done, and once fakeclaude.release exists the task ends and claude starts a turn by
// itself, reading no prompt. That turn is held after its text block until fakeclaude.open exists, so while it is held
// the session reads working, the owner is told the session changed, and no second notice is sent. Once the turn ends, the
// session is done again and the owner's second notice is that turn's done.
func TestTurnClaudeStartsByItselfRecordsWorkingUntilItsEnd(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGSELFHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := Notice{Session: id, Dir: dir, State: "done", Body: "fake bgself 1"}
	if got := waitNotices(t, engineOf(m), sink); len(got) != 1 || !reflect.DeepEqual(got[0], first) {
		t.Fatalf("notices after the first turn = %+v, want exactly [%+v]", got, first)
	}
	changedBefore := len(sink.changedCalls())

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("end the background task: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the self-started turn's block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake self 1")
	})
	if history.State != "working" {
		t.Fatalf("state while the turn claude started by itself is held = %q, want working", history.State)
	}
	// The pump tells the owner after it stores the line that opened the turn.
	waitUntil(t, engineOf(m), "the owner told the session changed", func() bool {
		return len(sink.changedCalls()) > changedBefore
	})
	if got := sink.got(); len(got) != 1 {
		t.Fatalf("notices while the self-started turn is held = %+v, want only the first turn's", got)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("let the self-started turn end: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the self-started turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "done"
	})
	waitUntil(t, engineOf(m), "the second notice", func() bool { return len(sink.got()) > 1 })
	second := Notice{Session: id, Dir: dir, State: "done", Body: "fake self 1"}
	if got := sink.got(); len(got) != 2 || !reflect.DeepEqual(got[1], second) {
		t.Fatalf("notices after the self-started turn = %+v, want [%+v %+v]", got, first, second)
	}
}
