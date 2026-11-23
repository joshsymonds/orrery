package interpret

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// storedTrigger is a trigger of the test epic as the store holds it.
type storedTrigger struct {
	id, state, cause string
	turn             int64
}

// syncTriggers returns the triggers of the test epic whose ids start with the sync trigger prefix, as stored.
func syncTriggers(t *testing.T, w *syncWorld) []storedTrigger {
	t.Helper()
	db, err := sql.Open("sqlite3", w.path)
	if err != nil {
		t.Fatalf("open %s: %v", w.path, err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(),
		`SELECT id, state, turn, cause FROM epic_triggers WHERE epic = ? ORDER BY id`, runEpic)
	if err != nil {
		t.Fatalf("query triggers of epic %s: %v", runEpic, err)
	}
	defer rows.Close()
	var triggers []storedTrigger
	for rows.Next() {
		var trigger storedTrigger
		if err := rows.Scan(&trigger.id, &trigger.state, &trigger.turn, &trigger.cause); err != nil {
			t.Fatalf("scan trigger of epic %s: %v", runEpic, err)
		}
		if strings.HasPrefix(trigger.id, "sync:") {
			triggers = append(triggers, trigger)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read triggers of epic %s: %v", runEpic, err)
	}
	return triggers
}

// syncFailCause returns the cause of a sync of origin's commit origin into the stored base base, merged as merge, that
// ended failed: the sync's gap and the merge, then causes, one to a line.
func syncFailCause(origin, base, merge string, causes ...string) string {
	lines := []string{
		fmt.Sprintf("sync sync:%s@%s: the sync of origin's commit %s into the stored base %s failed", origin, base,
			origin, base),
		fmt.Sprintf("merge %s of origin's commit %s into the stored base %s", merge, origin, base),
	}
	return strings.Join(append(lines, causes...), "\n")
}

// requireSyncWake fails unless the test epic holds exactly one sync trigger, pending with cause. The planner then
// claims a turn, which claims that trigger for it.
func requireSyncWake(t *testing.T, w *syncWorld, cause string) {
	t.Helper()
	triggers := syncTriggers(t, w)
	if len(triggers) != 1 || triggers[0].state != "pending" || triggers[0].cause != cause {
		t.Fatalf("sync triggers = %+v, want one pending with cause %q", triggers, cause)
	}
	turn := w.claimTurn()
	claimed := syncTriggers(t, w)
	if len(claimed) != 1 || claimed[0].id != triggers[0].id || claimed[0].state != "claimed" || claimed[0].turn != turn {
		t.Errorf("sync triggers after the planner's claim = %+v, want %s claimed by turn %d", claimed,
			triggers[0].id, turn)
	}
}

// TestSyncFailWakes: a sync that ends failed at its merge wakes the planner with one sync trigger, whose cause names
// the merge, origin's commit and how the check failed. A sync that ends skipped wakes no one with a sync trigger.
func TestSyncFailWakes(t *testing.T) {
	t.Parallel()
	t.Run("gate", func(t *testing.T) {
		t.Parallel()
		w := newSyncWorld(t)
		candidate := w.toCaptured()
		originHead, sync := w.syncGate()
		w.endGate(syncFirst, 1, "lint failed")
		if !w.passed() {
			t.Error("the planner got no turn after the sync's gate failed")
		}
		requireSyncWake(t, w, syncFailCause(originHead, candidate, sync.Revision,
			fmt.Sprintf("Done gate lint failed on commit %s: it exited 1.\nstderr tail:\nlint failed", sync.Revision),
		))
	})
	t.Run("capture", func(t *testing.T) {
		t.Parallel()
		w := newSyncWorld(t)
		candidate := w.toCaptured()
		originHead, sync := w.syncGate()
		w.endGate(syncFirst, 0, "")
		w.pass()
		w.endCaptureFailed(workID(t, w.s, syncFirst, "capture R1"))
		if !w.passed() {
			t.Error("the planner got no turn after the sync's capture regressed")
		}
		requireSyncWake(t, w, syncFailCause(originHead, candidate, sync.Revision,
			fmt.Sprintf("R1 was green at the stored base %s, and its capture on commit %s exited 0 and was judged fails: "+
				"the runner stopped", candidate, sync.Revision),
		))
	})
	t.Run("skipped", func(t *testing.T) {
		t.Parallel()
		w := newSyncWorld(t)
		w.toCaptured()
		originHead, sync := w.syncAdvance()
		w.pass()
		forced := w.forceOrigin("forced.txt")
		w.nextTurn(sync.Revision, 3, 4, integrateNextBatch)
		if !w.passed() {
			t.Error("the planner got no turn after the second sync")
		}
		w.requireSyncEnded(syncSecond, "skipped",
			fmt.Sprintf("the epic's change base %s is not in origin's commit %s", originHead, forced))
		if triggers := syncTriggers(t, w); len(triggers) != 0 {
			t.Errorf("sync triggers = %+v, want none for a sync that ended skipped", triggers)
		}
	})
}
