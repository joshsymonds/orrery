package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskLadderUnbuiltSkipsGappedRequirements checks that a Requirement held by a task that ended as a gap is not
// named, while an open task's Requirement without its proof still is.
func TestTaskLadderUnbuiltSkipsGappedRequirements(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the parser holds", Item: evidenceItem("the parser holds")}
	r2 := store.EpicRequirement{ID: "R2", Text: "the printer holds", Item: evidenceItem("the printer holds")}
	epic := store.EpicRow{
		Base:         "base-1",
		StoredBase:   "base-1",
		Requirements: []store.EpicRequirement{r1, r2},
		Tasks: []store.EpicTask{
			{Seq: 1, Status: taskGap, Requirements: []string{"R1"}},
			{Seq: 2, Status: taskOpen, Requirements: []string{"R2"}},
		},
	}
	redRun := store.NodeRun{ID: "red-run", Kind: "evidence", Outcome: outcomePassed, Attempt: 1}
	integrated := store.NodeRun{
		ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	red := store.EvidenceRun{
		ID: store.NodeWorkID("red-run", 1, "R2"), Phase: evidence.PhaseRed, Revision: "base-1",
		State: runEnded, Verdict: evidence.VerdictFails, Passed: true,
	}
	green := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R2"), Phase: evidence.PhaseGreen,
		State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
	}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{
			"open task's Requirement that is not green is named, the gapped one is not",
			Rows{Epic: epic},
			[]string{"R2 is not green at the stored base base-1"},
		},
		{
			"nothing is named once the open task's Requirement is green",
			Rows{Epic: epic, Runs: []store.NodeRun{redRun, integrated}, Evidence: []store.EvidenceRun{red, green}},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Unbuilt(tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unbuilt() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTaskLadderConcludeEndsWithGapsNamingEach checks that a clean review with tasks that ended as gaps ends passed in
// ended_with_gaps, naming each task and each Requirement it holds, and that a clean review with no gapped task ends clean.
func TestTaskLadderConcludeEndsWithGapsNamingEach(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	clean := store.Review{
		Frozen:   testRev,
		Verdicts: []store.ReviewVerdict{{Position: 1, Reviewer: "security", Verdict: "holds"}},
	}
	ladderRows := func(tasks []store.EpicTask) Rows {
		return Rows{Epic: store.EpicRow{ID: concludeEpicID, StoredBase: concludeBase, Tasks: tasks}, Review: clean}
	}
	checkDecisions(t, Conclude{}, []decideCase{
		{
			"clean review with two gapped tasks ends with gaps naming each task and Requirement",
			node,
			ladderRows([]store.EpicTask{
				{
					Seq: 1, Status: taskGap, Requirements: []string{"R1"},
					Build: store.TaskBuild{Failure: "the parser never went green"},
				},
				{
					Seq: 2, Status: taskGap, Requirements: []string{"R2", "R3"},
					Build: store.TaskBuild{Failure: "the printer never went green"},
				},
			}),
			concludeEnd(phaseEndedWithGaps, "Ended with 2 gaps:\n"+
				"- T1 ended as a gap holding R1: the parser never went green\n"+
				"- T2 ended as a gap holding R2, R3: the printer never went green"),
		},
		{
			"clean review with no gapped task still ends clean",
			node,
			ladderRows([]store.EpicTask{{Seq: 1, Status: taskDone}}),
			concludeEnd(phaseClean, "epic epic-1 ended clean at base-1"),
		},
	})
}

// TestTaskLadderTaskGaps checks that TaskGaps gives a line for each gapped task in seq order, and nothing when no task
// is a gap.
func TestTaskLadderTaskGaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []store.EpicTask
		want  []string
	}{
		{"no task is a gap gives none", []store.EpicTask{{Seq: 1, Status: taskDone}, {Seq: 2, Status: taskOpen}}, nil},
		{
			"gapped tasks give a line each in seq order",
			[]store.EpicTask{
				{
					Seq:          3,
					Status:       taskGap,
					Requirements: []string{"R3"},
					Build:        store.TaskBuild{Failure: "third failed"},
				},
				{Seq: 1, Status: taskDone, Requirements: []string{"R9"}},
				{
					Seq: 2, Status: taskGap, Requirements: []string{"R1", "R2"},
					Build: store.TaskBuild{Failure: "second failed"},
				},
			},
			[]string{
				"T2 ended as a gap holding R1, R2: second failed",
				"T3 ended as a gap holding R3: third failed",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TaskGaps(tc.tasks); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TaskGaps() = %q, want %q", got, tc.want)
			}
		})
	}
}
