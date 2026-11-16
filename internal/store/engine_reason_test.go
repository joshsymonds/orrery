package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestEngineStateWithoutReasonClearsTheReason fails an engine row with a reason, then changes a field that is not its
// state, which keeps the reason, then moves it to working with no reason, as a resume does, which clears it.
func TestEngineStateWithoutReasonClearsTheReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	apply := func(ops ...EngineOp) {
		t.Helper()
		if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
			t.Fatalf("ApplyEngine: %v", err)
		}
	}
	reason := func() string {
		t.Helper()
		row, err := s.EngineSession(ctx, "e1")
		if err != nil {
			t.Fatalf("EngineSession: %v", err)
		}
		return row.Reason
	}
	apply(EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineSet{ID: "e1", Changes: EngineChanges{State: strOf("failed"), Reason: strOf("spawn refused")}})
	apply(EngineSet{ID: "e1", Changes: EngineChanges{Seen: strOf("1700000100")}})
	if got := reason(); got != "spawn refused" {
		t.Fatalf("reason after a change of seen = %q, want the failure's", got)
	}
	apply(EngineSet{ID: "e1", Changes: EngineChanges{State: strOf("working")}})
	if got := reason(); got != "" {
		t.Fatalf("reason after working with no reason = %q, want none", got)
	}
}
