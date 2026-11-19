package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestReleaseCorrectingWithoutPlanReplansGapQuestion checks how a correcting epic's running release run is decided by the
// plan run under its attempt's replan prefix: with none there, the release replans, so the plan that carries the owner's
// correction starts; a plan run of another attempt does not count; a plan that runs waits; and an ended one resumes the
// release.
func TestReleaseCorrectingWithoutPlanReplansGapQuestion(t *testing.T) {
	t.Parallel()
	node := releaseNode(1, "", "")
	node.Run.Path = "release"
	correcting := func(runs ...store.NodeRun) Rows {
		return Rows{Epic: store.EpicRow{ID: releaseEpicID, Phase: phaseCorrecting}, Runs: runs}
	}
	plan := func(path, state string) store.NodeRun {
		return store.NodeRun{Path: path, Kind: kindPlan, State: state}
	}
	cases := []struct {
		name string
		rows Rows
		want Action
	}{
		{name: "no plan replans", rows: correcting(), want: Action{Kind: ActReplan}},
		{
			name: "plan of an earlier attempt does not count",
			rows: correcting(plan("release/A0/plan", store.NodeEnded)),
			want: Action{Kind: ActReplan},
		},
		{
			name: "running plan waits",
			rows: correcting(plan("release/A1/plan", store.NodeRunning)),
			want: Action{Kind: ActNone},
		},
		{
			name: "ended plan resumes",
			rows: correcting(plan("release/A1/plan", store.NodeEnded)),
			want: Action{Kind: ActResume},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (Release{}).Decide(node, tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}
