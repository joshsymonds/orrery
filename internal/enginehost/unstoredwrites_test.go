package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestUnstoredWritesRetryInKeyOrderAndStopAtTheFirstRefusal keeps three values, has the store refuse the second, and
// checks that the retry writes in key order, forgets the first, stops at the refusal, and leaves the last two kept.
func TestUnstoredWritesRetryInKeyOrderAndStopAtTheFirstRefusal(t *testing.T) {
	t.Parallel()
	kept := unstoredWrites[string]{"c": "three", "a": "one", "b": "two"}
	var wrote []string
	stored := kept.retry(t.Context(), func(_ context.Context, key, value string) error {
		wrote = append(wrote, key+"="+value)
		if key == "b" {
			return errors.New("database is locked")
		}
		return nil
	})
	if stored != 1 || !slices.Equal(wrote, []string{"a=one", "b=two"}) {
		t.Fatalf("retry stored %d and wrote %v, want 1 stored after writing a then b", stored, wrote)
	}
	if len(kept) != 2 || kept["b"] != "two" || kept["c"] != "three" {
		t.Fatalf("kept after the refusal = %v, want b and c", kept)
	}
}

// TestSessionTitleRefusedWriteIsStoredByTheNextPassWithNoSecondCall has the store refuse the write of a session's title,
// then take writes again. The next pass stores the title the one call made, and makes no second call.
func TestSessionTitleRefusedWriteIsStoredByTheNextPassWithNoSecondCall(t *testing.T) {
	t.Parallel()
	st := &titleFakeStore{
		untitled: []store.UntitledSession{{ID: "e-delta", Dir: "/work/delta", FirstPrompt: "p"}},
		writeErr: errors.New("begin store write: database is locked (5) (SQLITE_BUSY)"),
	}
	haiku := &titleFakeHaiku{reply: json.RawMessage(`{"title":"Delta work"}`)}

	loop := startTitleLoop(t, st, haiku)
	if got := st.titleOf("e-delta"); got != "" {
		t.Fatalf("title after a refused write = %q, want empty", got)
	}
	loop.tick()
	if got := st.titleOf("e-delta"); got != "" {
		t.Fatalf("title after a pass the store still refuses = %q, want empty", got)
	}
	st.mu.Lock()
	st.writeErr = nil
	st.mu.Unlock()
	loop.tick()
	if got := st.titleOf("e-delta"); got != "Delta work" {
		t.Fatalf("title after the store takes writes = %q, want the call's title", got)
	}
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls = %d, want 1: the refused title is written again, not asked for again", got)
	}
}
