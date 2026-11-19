package planner

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// ladderedTask is the open task T1 of the ladder tests, on attempt 4, its change of approach. Rung 3 was moved by a
// finding with a gate and an assertion and ended failed with a reason. It claims R1.
func ladderedTask() store.EpicTask {
	return store.EpicTask{
		Seq: 1, Turn: 1, Title: "laddered task", Deliverable: "a laddered deliverable", Status: "open",
		Requirements: []string{"R1"}, Gate: "check", Build: store.TaskBuild{Attempt: 4},
		Rungs: []store.TaskRung{
			{Rung: 1, Kind: store.RungAttempt},
			{
				Rung: 3, Kind: store.RungOrchestrator, MovedBy: store.MovedByFinding,
				Cause: "F2 says the test passes without the change", Gate: "review",
				Assertion: "the test fails without the change", Outcome: store.RungFailed,
				Reason: "the orchestrator's attempt did not hold",
			},
			{Rung: 4, Kind: store.RungApproach, MovedBy: store.MovedByUnit, Cause: "the unit check failed"},
		},
	}
}

// gappedTask is the task T2 of the ladder tests. It ended its ladder as a gap on rung 6 and still holds R2.
func gappedTask() store.EpicTask {
	return store.EpicTask{
		Seq: 2, Turn: 1, Title: "gapped task", Deliverable: "a gapped deliverable", Status: "gap",
		Requirements: []string{"R2"}, Gate: "check", NeedsFiles: []string{"internal/other/a.go"},
		Build: store.TaskBuild{Attempt: 6, Failure: "gap: no rung is left"},
		Rungs: []store.TaskRung{{
			Rung: 6, Kind: store.RungGap, MovedBy: store.MovedByCannotMeet,
			Cause: "the claim needs a file it does not own", Outcome: store.RungGapped, Reason: "no rung is left",
		}},
	}
}

func TestTaskLadderPromptListsRungs(t *testing.T) {
	t.Parallel()
	plain := store.EpicTask{Seq: 3, Turn: 1, Title: "plain task", Deliverable: "a plain deliverable", Status: "open"}
	got, err := taskLines([]store.EpicTask{ladderedTask(), gappedTask(), plain})
	if err != nil {
		t.Fatalf("taskLines: %v", err)
	}
	text := strings.Join(got, "\n") + "\n"
	inOrder(t, text,
		"- T1 [open] laddered task\n"+
			"  rung 1 attempt\n"+
			"  rung 3 orchestrator moved by finding: F2 says the test passes without the change "+
			"(gate review: the test fails without the change) -> failed: the orchestrator's attempt did not hold\n"+
			"  rung 4 approach moved by unit: the unit check failed\n"+
			"  deliverable: a laddered deliverable\n",
		"- T2 [gap] gapped task\n"+
			"  rung 6 gap moved by cannot_meet: the claim needs a file it does not own -> gap: no rung is left\n"+
			"  deliverable: a gapped deliverable\n",
		"  failure: gap: no rung is left\n",
		"  files the next task may need: internal/other/a.go\n",
	)
	if n := strings.Count(text, "  failure: "); n != 1 {
		t.Errorf("taskLines has %d failure lines, want 1 for the gap task only:\n%s", n, text)
	}
	plainBlock := "- T3 [open] plain task\n  deliverable: a plain deliverable\n"
	if !strings.Contains(text, plainBlock) {
		t.Errorf("taskLines of the task with no rungs lack %q in:\n%s", plainBlock, text)
	}
}

func TestTaskLadderCheckRefusesClaimHeldOnLadder(t *testing.T) {
	t.Parallel()
	ret := coverageReturn("work",
		coverageTask("A", []string{"R1"}, nil),
		coverageTask("B", []string{"R2"}, nil))
	stored := []store.EpicTask{ladderedTask(), storedClaim(2, "gap", "R2")}
	want := []string{
		`tasks[0] "A" requirements[0]: R1 is claimed by T1`,
		`tasks[1] "B" requirements[0]: R2 is claimed by T2`,
	}
	if got := Coverage(ret, epicRequirements(), stored); !reflect.DeepEqual(got, want) {
		t.Errorf("Coverage() = %q, want %q", got, want)
	}

	failed := []store.EpicTask{storedClaim(3, "failed", "R1")}
	if got := Coverage(ret, epicRequirements(), failed); len(got) != 0 {
		t.Errorf("Coverage() over a failed claim = %q, want no refusal", got)
	}
}
