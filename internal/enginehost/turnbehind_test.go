package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// recordedPromptStates returns every state recorded for the prompt that carries writeID in session id, in the order they
// were recorded, read from the prompt_states table at path.
func recordedPromptStates(t *testing.T, path, id, writeID string) []string {
	t.Helper()
	rows, err := openStoreDB(t, path).Query(
		`SELECT state FROM prompt_states WHERE session_id = ? AND write_id = ? ORDER BY seq`, id, writeID,
	)
	if err != nil {
		t.Fatalf("read states of prompt %s of session %s: %v", writeID, id, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close prompt state rows: %v", err)
		}
	}()
	var states []string
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatalf("scan state of prompt %s of session %s: %v", writeID, id, err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate states of prompt %s of session %s: %v", writeID, id, err)
	}
	return states
}

// TestTurnBehindSayIsQueuedAtOnceAndStartedAtTheToolResult has the owner Say during a FOLD turn, which holds until
// fakeclaude.release exists and then reads the Say's line in its Bash call. As Say returns, the Say's prompt is stored
// queued, before claude has read it. Once the turn takes the line in at its tool result and ends, the prompt's recorded
// states have started after queued.
func TestTurnBehindSayIsQueuedAtOnceAndStartedAtTheToolResult(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "FOLD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(m), "the FOLD turn to run", func() bool {
		_, turning := liveFlags(t, m, id)
		return turning
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during the FOLD turn: %v", err)
	}

	writeID := writeIDOf(t, path, id, "owner words")
	if got := recordedPromptStates(t, path, id, writeID); !reflect.DeepEqual(got, []string{store.PromptQueued}) {
		t.Fatalf("states of the Say's prompt as Say returns = %q, want [queued] before claude reads it", got)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("release the FOLD turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the FOLD turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	got := recordedPromptStates(t, path, id, writeID)
	queuedAt, startedAt := slices.Index(got, store.PromptQueued), slices.Index(got, store.PromptStarted)
	if queuedAt < 0 || startedAt <= queuedAt {
		t.Fatalf("states of the Say's prompt = %q, want started after queued", got)
	}
}
