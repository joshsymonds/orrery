package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSyncRetriesNewBaseTries: a sync tried origin's commit at the stored base b1 its prepare merged into, whether its
// gate failed at the merge or its merge conflicted. Once the stored base moved to b2, the commit is not tried at b2:
// SyncTriedBy names no sync for it, and the prepare of a sync at b2 names none, so the commit is merged again.
func TestSyncRetriesNewBaseTries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		result string
	}{
		{"gate failed", `{"origin":"o1","base":"b1","merge":"m1"}`},
		{"conflicted", `{"origin":"o1","base":"b1","conflicts":["a.go"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tried := syncTried("plan/M0", "failed", tc.result)
			atB1 := Rows{Epic: store.EpicRow{StoredBase: "b1"}, Runs: []store.NodeRun{tried}}
			atB2 := Rows{Epic: store.EpicRow{StoredBase: "b2"}, Runs: []store.NodeRun{tried}}
			if got := SyncTriedBy(atB1, "o1"); got != "plan/M0" {
				t.Errorf("SyncTriedBy at b1 = %q, want plan/M0", got)
			}
			if got := SyncTriedBy(atB2, "o1"); got != "" {
				t.Errorf("SyncTriedBy at b2 = %q, want none", got)
			}
			checkDecisions(t, Sync{}, []decideCase{
				{
					"prepare names the try at b1", syncNode("", ""), atB1,
					Action{Kind: ActPrepare, IDs: []string{"plan/M0"}, Items: []string{"o1"}},
				},
				{"prepare names no try at b2", syncNode("", ""), atB2, Action{Kind: ActPrepare}},
			})
		})
	}
}
