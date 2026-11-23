package interpret

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// triedRows returns the rows of quietRows after a sync of origin's commit o1 at turn 1: the sync run at plan/M1 ended
// failed with result, tried at the stored base c1 it merged into. Turn 2 is planned after it, so the next quiet point
// syncs at plan/M2. The stored base is base, the batch that passed at it has base as its revision, and the epic's read
// of origin found origin's commit ahead of base.
func triedRows(result, base string) kinds.Rows {
	rows := quietRows()
	tried := ended(syncRun(""), outcomeFailed)
	tried.Result = result
	rows.Runs = append(rows.Runs, tried)
	rows.Epic.Turns = append(rows.Epic.Turns, turnAt(2, "work"))
	for i := range rows.Runs {
		if rows.Runs[i].Path == batchPath {
			rows.Runs[i].Revision = base
		}
	}
	rows.Epic.StoredBase = base
	rows.Epic.OriginRead = store.OriginRead{
		Base: base, Commit: "o1", State: originAhead, At: decideNow().Add(-time.Minute),
	}
	return rows
}

// TestSyncRetriesNewBase: a sync of origin's commit that ended failed at the stored base c1, whether its gate failed
// at the merge or its merge conflicted. Once an integrate batch under the plan passed and moved the stored base to c2,
// and the epic's read of origin found origin's commit ahead for c2, the next quiet point starts a sync of that commit
// at the next turn's path, plan/M2. While the stored base is still c1 and the read is for c1, no sync starts, since
// the commit stays tried at c1.
func TestSyncRetriesNewBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, result string
	}{
		{"gate failed", `{"origin":"o1","base":"c1","merge":"m1"}`},
		{"conflicted", `{"origin":"o1","base":"c1","conflicts":["a.go"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			moved := triedRows(tc.result, "c2")
			want := []Action{{Kind: ActStart, Runs: []store.NodeRun{{
				Path: "plan/M2", ID: store.NodeRunID(decideEpic, "plan/M2", 0, "sync"), Node: "sync", Kind: "sync",
			}}}}
			if got := Decide(syncFlow(), moved, syncKinds()); !reflect.DeepEqual(got, want) {
				t.Errorf("Decide() with the stored base moved to c2 = %+v, want %+v", got, want)
			}
			still := triedRows(tc.result, "c1")
			if got := Decide(syncFlow(), still, syncKinds()); startsAt(got, "plan/M2") || !runsPlan(got) {
				t.Errorf("Decide() with the stored base still c1 = %+v, want the plan's run and no sync", got)
			}
		})
	}
}
