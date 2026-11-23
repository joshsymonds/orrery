package kinds

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSyncFailHolds: a plan whose latest turn succeeded with outcome complete, above its mark, with no open task, pending
// trigger or action, does not end while the latest sync at the stored base failed. A later sync at that base that passed
// and moved the stored base to its merge lets it end, and a later sync that ended skipped decides nothing, so it holds.
func TestSyncFailHolds(t *testing.T) {
	t.Parallel()
	at := func(minute int) time.Time { return time.Date(2026, 11, 22, 10, minute, 0, 0, time.UTC) }
	sync := func(path, outcome, origin, base, merge string, ended time.Time) store.NodeRun {
		result := `{"origin":"` + origin + `","base":"` + base + `","merge":"` + merge + `"}`
		return store.NodeRun{
			Path: path, Kind: kindSync, State: store.NodeEnded, Outcome: outcome, Result: result, Ended: ended,
		}
	}
	cases := []struct {
		name   string
		stored string
		runs   []store.NodeRun
		ends   bool
	}{
		{
			name:   "failed holds",
			stored: "base-1",
			runs:   []store.NodeRun{sync("plan/M1", outcomeFailed, "o1", "base-1", "m1", at(1))},
			ends:   false,
		},
		{
			name:   "passed later completes",
			stored: "m2",
			runs: []store.NodeRun{
				sync("plan/M1", outcomeFailed, "o1", "base-1", "m1", at(1)),
				sync("plan/M2", outcomePassed, "o2", "base-1", "m2", at(2)),
			},
			ends: true,
		},
		{
			name:   "skipped later holds",
			stored: "base-1",
			runs: []store.NodeRun{
				sync("plan/M1", outcomeFailed, "o1", "base-1", "m1", at(1)),
				sync("plan/M2", outcomeSkipped, "o2", "base-1", "m2", at(2)),
			},
			ends: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := Rows{
				Epic: store.EpicRow{
					StoredBase: tc.stored,
					Turns:      []store.EpicTurn{{Seq: 3, State: turnSucceeded, Outcome: outcomeComplete}},
					Tasks:      []store.EpicTask{{Seq: 1, Status: taskDone, Batch: "plan/S1/integrate"}},
				},
				Runs: tc.runs,
			}
			if got := (Plan{}).Decide(planNode(2), rows).Kind == ActEnd; got != tc.ends {
				t.Errorf("Plan.Decide() ends = %v, want %v", got, tc.ends)
			}
		})
	}
}
