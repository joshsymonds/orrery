package store

import (
	"context"
	"testing"
)

// TestEndSyncAddsItsTrigger: a failed sync that ends with a Trigger adds it pending in its write, once. A repeated end
// adds nothing, whatever trigger it carries.
func TestEndSyncAddsItsTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startSyncRun(t, s)
	if err := s.PrepareSync(ctx, integrateEpic, syncRunPath, 0, syncMerge, syncResult, syncRead()); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	pendingBefore := integratePending(t, s)
	trigger := NodeTrigger{ID: "sync:wakes", Cause: "sync " + syncMerge + " failed"}
	end := SyncEnd{Path: syncRunPath, Outcome: "failed", Output: "Done gate lint failed", Trigger: &trigger}
	if err := s.EndSync(ctx, integrateEpic, end, integrateEndedAt()); err != nil {
		t.Fatalf("EndSync: %v", err)
	}
	if state, cause, ok := integrateTrigger(t, s, trigger.ID); !ok || state != "pending" || cause != trigger.Cause {
		t.Errorf("trigger %s = %q %q stored %t, want pending %q", trigger.ID, state, cause, ok, trigger.Cause)
	}
	if got := integratePending(t, s); got != pendingBefore+1 {
		t.Errorf("pending triggers = %d, want %d: the end adds its trigger once", got, pendingBefore+1)
	}

	again := NodeTrigger{ID: "sync:again", Cause: "a repeated end"}
	end.Trigger = &again
	if err := s.EndSync(ctx, integrateEpic, end, integrateParkedAt()); err != nil {
		t.Fatalf("EndSync repeated: %v", err)
	}
	if _, _, ok := integrateTrigger(t, s, again.ID); ok {
		t.Errorf("trigger %s stored by a repeated end, want none", again.ID)
	}
	if got := integratePending(t, s); got != pendingBefore+1 {
		t.Errorf("pending triggers after a repeat = %d, want %d", got, pendingBefore+1)
	}
}
