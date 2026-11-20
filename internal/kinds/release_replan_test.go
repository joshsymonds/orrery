package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// replanRun returns the replan release run of attempt, from release tip start, with the integrator's report as reason.
func replanRun(attempt int64, start, reason string) store.ReleaseRun {
	return store.ReleaseRun{Part: store.ReleasePartReplan, Attempt: attempt, Start: start, Reason: reason}
}

// TestReleaseReplanDecisions checks how the release kind sends a release back to planning and resumes it: a failed round
// of the integrator with a replan of its attempt replans with the report in place of stopping, a replan of another
// attempt does not, a stopped epic whose run a retry turned running replans and one with no replan waits, and a
// correcting epic replans when no plan runs under its replan prefix, waits while its replan plan runs and resumes once it
// has ended.
func TestReleaseReplanDecisions(t *testing.T) {
	t.Parallel()
	report := "the release integrator stopped the merge: README: side: candidate"
	failed := integratorRound(1, "", report)
	stopped := func(releases ...store.ReleaseRun) Rows {
		rows := integratorRows(releases, nil, nil, nil)
		rows.Epic.Phase = store.PhaseStopped
		return rows
	}
	correcting := func(state string) Rows {
		rows := integratorRows([]store.ReleaseRun{failed, replanRun(1, "tip-1", report)}, nil, nil, nil)
		rows.Epic.Phase = phaseCorrecting
		rows.Slot = ""
		if state != "" {
			rows.Runs = []store.NodeRun{{Path: "release/A1/fix", Kind: kindPlan, State: state}}
		}
		return rows
	}
	node := releaseNode(1, "", integratorResult)
	node.Run.Path = "release"
	cases := []struct {
		name string
		rows Rows
		want Action
	}{
		{
			name: "failed round with a replan of its attempt replans",
			rows: integratorRows([]store.ReleaseRun{failed, replanRun(1, "tip-1", report)}, nil, nil, nil),
			want: Action{Kind: ActReplan, Output: report},
		},
		{
			name: "failed round with a replan of an earlier attempt stops",
			rows: integratorRows([]store.ReleaseRun{replanRun(0, "tip-0", "old"), failed}, nil, nil, nil),
			want: Action{Kind: ActStop, Output: report},
		},
		{
			name: "stopped epic retried with a replan replans",
			rows: stopped(failed, replanRun(1, "tip-1", report)),
			want: Action{Kind: ActReplan, Output: report},
		},
		{name: "stopped epic with no replan waits", rows: stopped(failed), want: Action{Kind: ActNone}},
		{
			name: "correcting epic waits while its plan runs",
			rows: correcting(store.NodeRunning),
			want: Action{Kind: ActNone},
		},
		{name: "correcting epic with no plan replans", rows: correcting(""), want: Action{Kind: ActReplan}},
		{
			name: "correcting epic resumes once its plan ended",
			rows: correcting(store.NodeEnded),
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

// TestReleaseAfterReplanReusesNoEarlierCandidate: a candidate that passed at or before the attempt of a replan is not
// reused, since the replan changed the epic's branch, so the release prepares its candidate again; one that passed after
// the replan is reused.
func TestReleaseAfterReplanReusesNoEarlierCandidate(t *testing.T) {
	t.Parallel()
	epic := releasingEpic(releaseStep("tag", "none"))
	before := Rows{
		Epic: epic, Slot: releaseEpicID,
		Releases: []store.ReleaseRun{candidateRun(0, "old"), replanRun(1, "tip-1", "report")},
	}
	if got := (Release{}).Decide(releaseNode(2, "", ""), before); got.Kind != ActPrepare {
		t.Errorf("Decide after a replan = %+v, want the candidate prepared again", got)
	}
	after := before
	after.Releases = append(after.Releases, candidateRun(2, "new"))
	if head, ok := currentCandidate(releaseNode(2, "", "").Run, after); !ok || head != "new" {
		t.Errorf("currentCandidate = %q, %v, want new, the candidate passed after the replan", head, ok)
	}
}
