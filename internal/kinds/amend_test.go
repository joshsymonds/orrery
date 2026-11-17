package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendedRows returns an epic at base-1 stored at stored-2 whose R1 holds item, with amendments, the given current
// evidence runs and checks, and an integrate run that ended passed at the stored base with a reproduction of R1 that
// exited 0. That run's capture of R1 is in evidence only when the test gives it.
func amendedRows(
	item evidence.Item, amendments []store.EvidenceAmendment, runs []store.EvidenceRun, checks ...store.NodeCheck,
) Rows {
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "stored-2",
		Attempt:  1,
	}
	return Rows{
		Epic: store.EpicRow{
			Base: "base-1", StoredBase: "stored-2",
			Requirements: []store.EpicRequirement{{ID: "R1", Text: "the suite passes", Item: item}},
			Amendments:   amendments,
		},
		Runs:     []store.NodeRun{integrated},
		Evidence: runs,
		Checks: append([]store.NodeCheck{
			{ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded, Exit: 0},
		}, checks...),
	}
}

// amendmentProof returns the amendment of R1 to item in state, with its red run red-1, and its green run green-1 and
// reproduction repro-1 at stored-2.
func amendmentProof(item evidence.Item, state string) store.EvidenceAmendment {
	return store.EvidenceAmendment{
		Seq: 1, Requirement: "R1", Item: item, State: state, Base: "base-1", RedRun: "red-1", StoredBase: "stored-2",
		GreenRun: "green-1", ReproCheck: "repro-1",
	}
}

// proofRuns returns the red run red-1 at base-1 and the green run green-1 at stored-2 of R1, each ended and passed.
func proofRuns() []store.EvidenceRun {
	return []store.EvidenceRun{
		{ID: "red-1", Item: "R1", Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded, Passed: true},
		{ID: "green-1", Item: "R1", Phase: evidence.PhaseGreen, Revision: "stored-2", State: runEnded, Passed: true},
	}
}

func TestUnbuiltReadsTheProofOfAnAmendment(t *testing.T) {
	t.Parallel()
	item := reproItem("the suite passes")
	other := reproItem("the suite passes")
	other.Capture = []string{"make", "other"}
	reproExit1 := store.NodeCheck{ID: "repro-1", State: store.NodeEnded, Exit: 1}
	const noRed = "R1 has no red at the epic's base base-1"
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{
			name: "an applied amendment's red, green and reproduction are R1's proof",
			rows: amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)},
				proofRuns(), reproExit1),
			want: nil,
		},
		{
			name: "a capturing amendment's proof counts once its runs have ended",
			rows: amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentCapturing)},
				proofRuns(), reproExit1),
			want: nil,
		},
		{
			name: "a checking amendment proves nothing",
			rows: amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentChecking)},
				proofRuns(), reproExit1),
			want: []string{noRed},
		},
		{
			name: "an amendment to an item R1 no longer holds proves nothing",
			rows: amendedRows(other, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)},
				proofRuns(), reproExit1),
			want: []string{noRed},
		},
		{
			name: "a later refused amendment leaves the applied one's proof",
			rows: amendedRows(item, []store.EvidenceAmendment{
				amendmentProof(item, store.AmendmentApplied),
				{Seq: 2, Requirement: "R1", Item: other, State: store.AmendmentRefused},
			}, proofRuns(), reproExit1),
			want: nil,
		},
		{
			name: "a green the amendment ran at another stored base is not green here",
			rows: func() Rows {
				amended := amendmentProof(item, store.AmendmentApplied)
				amended.StoredBase = "stored-1"
				return amendedRows(item, []store.EvidenceAmendment{amended}, proofRuns(), reproExit1)
			}(),
			want: []string{"R1 is not green at the stored base stored-2"},
		},
		{
			name: "the stale reproduction of an amended item is not read, so a missing one is named",
			rows: amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)},
				proofRuns()),
			want: []string{"R1 has no reproduction at the epic's base base-1"},
		},
		{
			name: "the amendment's own reproduction that exited 0 is a disagreement",
			rows: amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)},
				proofRuns(), store.NodeCheck{ID: "repro-1", State: store.NodeEnded, Exit: 0}),
			want: []string{"R1 judge and exit disagree at the epic's base base-1: red was judged fails, but its " +
				"reproduction with the candidate's tests exited 0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Unbuilt(tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unbuilt() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGreenAtReadsAnAmendmentsGreenOnlyAtItsStoredBase(t *testing.T) {
	t.Parallel()
	item := evidenceItem("lint is clean")
	rows := amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)}, proofRuns())
	if got := GreenAt(rows, "stored-2"); !got["R1"] {
		t.Errorf("GreenAt(stored-2) = %v, want R1 green by its amendment", got)
	}
	if got := GreenAt(rows, "base-1"); got["R1"] {
		t.Errorf("GreenAt(base-1) = %v, want R1 not green where its amendment ran no green", got)
	}
	failed := proofRuns()
	failed[1].Passed = false
	rows = amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)}, failed)
	if got := GreenAt(rows, "stored-2"); got["R1"] {
		t.Errorf("GreenAt(stored-2) with a green that did not pass = %v, want R1 not green", got)
	}
}

// TestPlanDoesNotCompleteWhileAnAmendmentIsUnderWay checks that a plan whose last turn completed waits while an
// amend_evidence action is pending or an amendment is checking or capturing, and completes once every amendment has
// settled, applied or refused.
func TestPlanDoesNotCompleteWhileAnAmendmentIsUnderWay(t *testing.T) {
	t.Parallel()
	complete := []store.EpicTurn{{Seq: 3, State: "succeeded", Outcome: "complete"}}
	withAmendments := func(states ...string) Rows {
		rows := planRows(complete, nil, 0)
		for i, state := range states {
			rows.Epic.Amendments = append(
				rows.Epic.Amendments,
				store.EvidenceAmendment{Seq: int64(i + 1), State: state},
			)
		}
		return rows
	}
	pendingAction := planRows(complete, nil, 0)
	pendingAction.AmendActions = 1
	cases := []struct {
		name string
		rows Rows
		want Action
	}{
		{"a pending amend_evidence action holds the plan", pendingAction, Action{Kind: ActRun}},
		{"a checking amendment holds the plan", withAmendments(store.AmendmentChecking), Action{Kind: ActRun}},
		{
			"a capturing amendment holds the plan",
			withAmendments(store.AmendmentRefused, store.AmendmentCapturing), Action{Kind: ActRun},
		},
		{
			"settled amendments let the plan complete",
			withAmendments(store.AmendmentApplied, store.AmendmentRefused),
			Action{Kind: ActEnd, Outcome: "complete"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (Plan{}).Decide(planNode(2), tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}
