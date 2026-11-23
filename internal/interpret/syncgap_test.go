package interpret

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// syncGapCase is a sync of origin's commit o1 that ended failed at its merge merge into the stored base base, and the
// sync gap it leaves.
type syncGapCase struct {
	base, merge string
}

// key returns the key of the sync gap the case leaves.
func (c syncGapCase) key() string {
	return "sync:o1@" + c.base
}

// mergeLine returns the line that names the case's merge.
func (c syncGapCase) mergeLine() string {
	return fmt.Sprintf("merge %s of origin's commit o1 into the stored base %s", c.merge, c.base)
}

// gateLine returns the line of the Done gate that failed at the case's merge.
func (c syncGapCase) gateLine() string {
	return "Done gate lint failed on commit " + c.merge + ": it exited 1."
}

// run returns the sync run at path that ended failed at the case's merge. Its output is the merge's line, then the
// gate's.
func (c syncGapCase) run(path string) store.NodeRun {
	return store.NodeRun{
		Path: path, ID: store.NodeRunID(decideEpic, path, 0, "sync"), Node: "sync", Kind: kindSync,
		State: store.NodeEnded, Outcome: outcomeFailed,
		Result: fmt.Sprintf(`{"origin":"o1","base":%q,"merge":%q}`, c.base, c.merge),
		Output: c.mergeLine() + "\n" + c.gateLine(),
	}
}

// reason returns the reason of the case's gap: its first line names origin's commit and the stored base, and the gate's
// line follows it.
func (c syncGapCase) reason() string {
	return "the sync of origin's commit o1 into the stored base " + c.base + " failed\n" + c.gateLine()
}

// turn returns the gap turn the case's gap was given as turn n.
func (c syncGapCase) turn(n int64) store.GapTurn {
	return store.GapTurn{Gap: c.key(), Turn: n, Reason: c.reason()}
}

// declaration returns the planner's declaration that the case's gap cannot be resolved without the owner.
func (c syncGapCase) declaration() store.GapDeclaration {
	return store.GapDeclaration{Gap: c.key(), Reason: "only the owner can merge o1", Recommended: "correct"}
}

// wake returns the action that re-wakes the planner of the plan at "plan" for the case's gap as its turn n: a pending
// trigger whose cause names the sync that is still failed, and the gap turn n.
func (c syncGapCase) wake(n int64) Action {
	id := store.NodeRunID(decideEpic, "plan", 0, fmt.Sprintf("rewake %s %d", c.key(), n))
	cause := strings.Join([]string{
		fmt.Sprintf("sync %s: the sync of origin's commit o1 into the stored base %s failed", c.key(), c.base),
		c.mergeLine(),
		fmt.Sprintf("the sync is still failed: re-wake %d of 2", n),
		c.mergeLine(),
		c.gateLine(),
	}, "\n")
	return Action{
		Kind:     ActRewakeSync,
		Trigger:  &store.NodeTrigger{ID: kinds.SyncTriggerPrefix + id, Cause: cause},
		GapTurns: []store.GapTurn{c.turn(n)},
	}
}

// gapRows returns the rows of the sync gap cases: the quiet rows of a plan whose turn 1 ended complete, with runs as
// the sync runs of the epic.
func gapRows(runs ...store.NodeRun) kinds.Rows {
	rows := quietRows()
	rows.Epic.Turns = []store.EpicTurn{turnAt(1, "complete")}
	rows.Runs = append(rows.Runs, runs...)
	return rows
}

// builtEnd returns the actions of a plan that ends complete and built, its one run ending with no output.
func builtEnd() []Action {
	return []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, Built: true}}
}

// requireSyncDecision fails unless Decide of the sync cases' flow over rows gives want.
func requireSyncDecision(t *testing.T, rows kinds.Rows, want []Action) {
	t.Helper()
	if got := Decide(syncFlow(), rows, syncKinds()); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// concluded returns the conclusion's decision over rows. The review is frozen at the stored base, so the sync gap is
// the only gap the review and the tasks leave. The frozen review is set on a copy, which the plan's decisions never
// read.
func concluded(rows kinds.Rows) kinds.Action {
	rows.Review.Frozen = rows.Epic.StoredBase
	return kinds.Conclude{}.Decide(kinds.Node{}, rows)
}

// requireEndedWithGap fails unless got ends the epic with gaps, its output names reason, and its gaps question has an
// entry that names it.
func requireEndedWithGap(t *testing.T, got kinds.Action, reason string) {
	t.Helper()
	if got.Kind != kinds.ActEnd || got.Phase != phaseEndedWithGaps || !strings.Contains(got.Output, reason) {
		t.Errorf("Conclude() = %+v, want the epic ended with gaps, naming %q", got, reason)
	}
	if !slices.ContainsFunc(got.Question.Gaps, func(entry string) bool { return strings.Contains(entry, reason) }) {
		t.Errorf("gaps question = %q, want an entry naming %q", got.Question.Gaps, reason)
	}
}

// TestSyncFailGap: a sync that ends failed at the stored base leaves a sync gap. While it is live, a plan that is
// otherwise complete re-wakes the planner for it, with its gap turn, at most twice. A gap that has had its two turns,
// or that a planner turn named unresolvable, holds nothing: the plan ends built, and the conclusion ends the epic with
// gaps. A declaration stands for its key, so a new base gives a new gap that wakes the planner again.
func TestSyncFailGap(t *testing.T) {
	t.Parallel()
	t.Run("re-woken twice then a gap", func(t *testing.T) {
		t.Parallel()
		failed := syncGapCase{base: "c1", merge: "m1"}
		rows := gapRows(failed.run(syncPathOne))
		requireSyncDecision(t, rows, []Action{failed.wake(1)})
		rows.GapTurns = []store.GapTurn{failed.turn(1)}
		requireSyncDecision(t, rows, []Action{failed.wake(2)})
		rows.GapTurns = append(rows.GapTurns, failed.turn(2))
		requireSyncDecision(t, rows, builtEnd())
		requireEndedWithGap(t, concluded(rows), failed.reason())
	})
	t.Run("named unresolvable", func(t *testing.T) {
		t.Parallel()
		failed := syncGapCase{base: "c1", merge: "m1"}
		rows := gapRows(failed.run(syncPathOne))
		rows.SyncDeclarations = []store.GapDeclaration{failed.declaration()}
		requireSyncDecision(t, rows, builtEnd())
		requireEndedWithGap(t, concluded(rows), failed.reason())
	})
	t.Run("naming stands until the base moves", func(t *testing.T) {
		t.Parallel()
		first := syncGapCase{base: "c1", merge: "m1"}
		rows := gapRows(first.run(syncPathOne))
		// Turn 1 named the gap, and the latest turn 2 does not restate it, which still stands.
		rows.Epic.Turns = []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
		rows.SyncDeclarations = []store.GapDeclaration{first.declaration()}
		requireSyncDecision(t, rows, builtEnd())
		requireEndedWithGap(t, concluded(rows), first.reason())
		// The stored base moves to c2, and a sync of origin's commit fails there: the naming of c1 does not match it.
		second := syncGapCase{base: "c2", merge: "m2"}
		rows.Epic.StoredBase = second.base
		rows.Runs = append(rows.Runs, second.run("plan/M2"))
		requireSyncDecision(t, rows, []Action{second.wake(1)})
	})
}
