package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// droppedAt returns requirement dropped as already met by the owner's decision with seq 3.
func droppedAt(requirement store.EpicRequirement) store.EpicRequirement {
	requirement.DroppedBy = 3
	return requirement
}

// TestMetAtBaseQuestionEvidenceDecideStartsNoRedForDropped checks that an evidence node starts no red run for a
// Requirement dropped as already met, starts the red runs of the others, and ends passed on the others alone.
func TestMetAtBaseQuestionEvidenceDecideStartsNoRedForDropped(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	cases := []decideCase{
		{
			"dropped Requirement starts no run and the other starts",
			nodeWith(testRev, false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{droppedAt(r1), r2}}},
			Action{Kind: ActStart, IDs: []string{workID("R2")}, Items: []string{"R2"}},
		},
		{
			"dropped Requirement is not waited for once the other run ended",
			nodeWith(testRev, true),
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{droppedAt(r1), r2}},
				Evidence: []store.EvidenceRun{evidenceRun("R2", evidence.PhaseRed, true)},
			},
			Action{Kind: ActEnd, Outcome: "passed", Output: "R2 red: passed"},
		},
		{
			"every Requirement dropped ends passed with no run",
			nodeWith(testRev, false),
			Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{droppedAt(r1), droppedAt(r2)}}},
			Action{
				Kind: ActEnd, Outcome: "passed",
				Output: "every Requirement that names an evidence item was dropped as already met",
			},
		},
	}
	checkDecisions(t, Evidence{}, cases)
}

// TestMetAtBaseQuestionMetAtBaseSkipsDropped checks that a Requirement dropped as already met is never named as met at
// the base, even when its red run judged it holding, and that the others still are.
func TestMetAtBaseQuestionMetAtBaseSkipsDropped(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{ID: testRunID, Kind: "evidence", Attempt: 1}}
	holds := func(part string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: workID(part), Phase: evidence.PhaseRed, State: runEnded, Verdict: evidence.VerdictHolds,
		}
	}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	rows := Rows{
		Epic:     store.EpicRow{Requirements: []store.EpicRequirement{droppedAt(r1), r2}},
		Evidence: []store.EvidenceRun{holds("R1"), holds("R2")},
	}
	if got, want := MetAtBase(node, rows), []string{"R2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MetAtBase() = %v, want %v: the dropped R1 is not named", got, want)
	}
}

// TestMetAtBaseQuestionUnbuiltAsksOnlyGreenOfDropped checks what Unbuilt asks of a Requirement dropped as already met:
// its green at the stored base is still required, but it names neither a missing red at the base nor a reproduction
// line. The same test item that is not dropped names both.
func TestMetAtBaseQuestionUnbuiltAsksOnlyGreenOfDropped(t *testing.T) {
	t.Parallel()
	integrated := store.NodeRun{
		ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	green := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
		Verdict: evidence.VerdictHolds, Passed: true,
	}
	disagreeing := store.NodeCheck{
		ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded, Exit: 0,
	}
	testItem := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: reproItem("the suite passes")}
	base := store.EpicRow{Base: "base-1", StoredBase: "base-1"}
	withRequirement := func(requirement store.EpicRequirement) store.EpicRow {
		row := base
		row.Requirements = []store.EpicRequirement{requirement}
		return row
	}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{
			"dropped test item green at the stored base lacks no proof",
			Rows{
				Epic:     withRequirement(droppedAt(testItem)),
				Runs:     []store.NodeRun{integrated},
				Evidence: []store.EvidenceRun{green},
			},
			nil,
		},
		{
			"dropped test item green at the stored base with a reproduction that exits 0 lacks no proof",
			Rows{
				Epic:     withRequirement(droppedAt(testItem)),
				Runs:     []store.NodeRun{integrated},
				Checks:   []store.NodeCheck{disagreeing},
				Evidence: []store.EvidenceRun{green},
			},
			nil,
		},
		{
			"dropped test item not green at the stored base is named for its green",
			Rows{Epic: withRequirement(droppedAt(testItem)), Runs: []store.NodeRun{integrated}},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"test item that is not dropped and green at the stored base names its missing red",
			Rows{
				Epic:     withRequirement(testItem),
				Runs:     []store.NodeRun{integrated},
				Evidence: []store.EvidenceRun{green},
			},
			[]string{"R1 has no red at the epic's base base-1"},
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
