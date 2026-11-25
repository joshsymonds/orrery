package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestUnbuiltNamesTheJudgesReason: a Requirement not green at the stored base names the exit, verdict and reason of
// the judged capture that decided so, when it has a reason: its amendment's green at the stored base when it ran one
// there, and otherwise its capture in the integrate run that ended passed there. A capture with no reason, and a
// Requirement with no judged capture at the stored base, keep the line as it was.
func TestUnbuiltNamesTheJudgesReason(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	integrated := store.NodeRun{
		ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	failing := func(id, revision, reason string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: id, Item: "R1", Phase: evidence.PhaseGreen, Revision: revision, State: runEnded, Exit: 1,
			Verdict: evidence.VerdictFails, JudgeReason: reason,
		}
	}
	captureR1 := store.NodeWorkID("int-run", 1, "capture R1")
	epic := store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}}
	item := reproItem("the suite passes")
	amendedEvidence := []store.EvidenceRun{
		{ID: "red-1", Item: "R1", Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded, Passed: true},
		failing("green-1", "stored-2", "the amended capture ran no test"),
		failing(captureR1, "stored-2", "the capture before the amendment ran no test"),
	}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{
			"a judged capture with a reason names it",
			Rows{
				Epic: epic, Runs: []store.NodeRun{integrated},
				Evidence: []store.EvidenceRun{failing(captureR1, "base-1", "the run matched no test named TestX")},
			},
			[]string{"R1 is not green at the stored base base-1: its capture exited 1 and was judged fails: " +
				"the run matched no test named TestX"},
		},
		{
			"a judged capture with no reason keeps the line",
			Rows{
				Epic: epic, Runs: []store.NodeRun{integrated},
				Evidence: []store.EvidenceRun{failing(captureR1, "base-1", "")},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"a capture in a run that did not pass names nothing",
			Rows{
				Epic: epic,
				Runs: []store.NodeRun{{
					ID: "int-run", Kind: "integrate", Outcome: outcomeFailed, Revision: "base-1", Attempt: 1,
				}},
				Evidence: []store.EvidenceRun{failing(captureR1, "base-1", "the run matched no test named TestX")},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"an amendment's green at the stored base names its own reason",
			amendedRows(item, []store.EvidenceAmendment{amendmentProof(item, store.AmendmentApplied)},
				amendedEvidence),
			[]string{"R1 is not green at the stored base stored-2: its capture exited 1 and was judged fails: " +
				"the amended capture ran no test"},
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

// TestNotYetGreenNamesTheJudgesReason: an integrate that advances with a Requirement whose capture was judged fails
// for a reason names that capture's exit, verdict and reason on the Requirement's not yet green line.
func TestNotYetGreenNamesTheJudgesReason(t *testing.T) {
	t.Parallel()
	rows := Rows{
		Epic: integrateEpic(), Checks: gatesPassed(),
		Evidence: []store.EvidenceRun{judgedFailing(workID("capture R1")), greenCapture("capture R2", true)},
	}
	checkDecisions(t, Integrate{}, []decideCase{{
		"not yet green names the reason", batchNode(testRev, composedOne, true), rows,
		Action{
			Kind: ActAdvance,
			Output: "R1 not yet green: its capture exited 0 and was judged fails: the output shows TestX failing\n" +
				"R2 green\nR3 not yet green",
		},
	}})
}
