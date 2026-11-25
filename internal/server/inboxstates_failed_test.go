package server

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// failedHeadlessID is the id of the headless session the failed tests make exit failing, 32 hex digits as session ids
// are. Each test writes its own store.
const failedHeadlessID = "5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a"

// failedExitReason is the reason a headless session that exited failing records: the exit error, then ": ", then the
// last non-empty stderr line, as exitFailure forms it.
const failedExitReason = "exit status 1: panic: boom"

// writeExitedFailing adds the headless session failedHeadlessID to the store at path in state working at started, then
// sets it to state failed with failedExitReason as its reason at failedAt.
func writeExitedFailing(t *testing.T, path string, started, failedAt time.Time) {
	t.Helper()
	applyHeadlessAt(t, path, started, store.EngineAdd{ID: failedHeadlessID, Dir: "/work/failed", State: "working"})
	applyHeadlessAt(t, path, failedAt, store.EngineSet{ID: failedHeadlessID, Changes: store.EngineChanges{
		State:  new("failed"),
		Reason: new(failedExitReason),
	}})
}

// TestInboxStatesFailed checks the headless session that exited failing, in two cases. Each writes its own store: both
// write failedHeadlessID, at different clocks, and the first asserts that its store holds exactly one row.
func TestInboxStatesFailed(t *testing.T) {
	t.Parallel()
	t.Run("ReasonInSummary", func(t *testing.T) {
		t.Parallel()
		path := archiveStorePath(t)
		failedAt := epicsNow()
		writeExitedFailing(t, path, failedAt.Add(-time.Hour), failedAt)

		sessions := sessionsClientAt(t, pooledReader(t, path), epicsNow)
		rows := listHeadlessRows(t, sessions)
		if len(rows) != 1 {
			t.Fatalf("ListHeadless rows = %d, want 1", len(rows))
		}
		row := rows[0]
		if row.GetId() != failedHeadlessID {
			t.Fatalf("ListHeadless id = %q, want %q", row.GetId(), failedHeadlessID)
		}
		if row.GetState() != "failed" {
			t.Fatalf("ListHeadless state = %q, want %q", row.GetState(), "failed")
		}
		if row.GetSummary() != failedExitReason {
			t.Fatalf("ListHeadless summary = %q, want %q", row.GetSummary(), failedExitReason)
		}
	})
	// The owner saw the session an hour after it failed. Read two days past the failure, unchanged, it is listed
	// neither archived nor ended.
	t.Run("SeenTwoDaysStaysUnarchived", func(t *testing.T) {
		t.Parallel()
		path := archiveStorePath(t)
		failedAt := epicsNow()
		seenAt := failedAt.Add(time.Hour)
		writeExitedFailing(t, path, failedAt.Add(-time.Hour), failedAt)
		applyHeadlessAt(t, path, seenAt, store.EngineSet{ID: failedHeadlessID, Changes: store.EngineChanges{
			Seen: new(stamp(seenAt)),
		}})

		now := failedAt.Add(2 * archiveDay)
		sessions := sessionsClientAt(t, pooledReader(t, path), func() time.Time { return now })
		rows := listHeadlessRows(t, sessions)
		if len(rows) != 1 {
			t.Fatalf("ListHeadless rows = %d, want 1", len(rows))
		}
		if rows[0].GetState() != "failed" || rows[0].GetSeen() != seenAt.Unix() {
			t.Fatalf("ListHeadless row state = %q seen = %d, want failed seen %d",
				rows[0].GetState(), rows[0].GetSeen(), seenAt.Unix())
		}
		expectArchived(t, "ListHeadless", archivedByID(rows), map[string]bool{failedHeadlessID: false})
		expectEnded(t, "ListHeadless", endedStates(rows), map[string]endedRowState{failedHeadlessID: {}})
	})
}
