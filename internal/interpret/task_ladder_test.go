package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// TestTaskLadderPlanEndsWithoutParkingOnGaps checks that a complete plan whose only unbuilt Requirement is held by a task
// that ended as a gap ends built without parking the epic. The control gives the same pass a task that failed instead,
// which parks the epic on the Requirement.
func TestTaskLadderPlanEndsWithoutParkingOnGaps(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	flow := append([]workflow.Node{baseEvidence("red")}, planWithChecks(batchNode())...)
	red := ended(startOf("red", "red", "evidence", 0), "passed")
	reg := integrateKinds(scripted{})
	one := []store.EpicRequirement{itemRequirement("R1")}
	unbuiltR1 := "epic not built: R1 is not green at the stored base s1"
	// rows returns the rows of a plan that completes after turn 1, with stored base s1, and whose task 2 of turn 2 holds
	// R1 with status.
	rows := func(status string) kinds.Rows {
		tasks := []store.EpicTask{
			doneTask(1, 1),
			{Seq: 2, Turn: 2, Status: status, Requirements: []string{"R1"}},
		}
		epicRows := rowsOf(turns, tasks, runningPlan(1), red, ended(batchRun(""), "passed"))
		epicRows.Epic.StoredBase = "s1"
		return withEvidence(epicRows, one, redRun(red, "R1", evidence.VerdictFails, true))
	}
	decideCases(t, []decideCase{
		{
			name: "plan whose only unbuilt Requirement is held by a gap ends built without parking",
			flow: flow,
			rows: rows("gap"),
			reg:  reg,
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, Built: true}},
		},
		{
			name: "control: the same plan with its task failed parks the epic on the Requirement",
			flow: flow,
			rows: rows("failed"),
			reg:  reg,
			want: []Action{{Kind: ActParkEpic, Reason: unbuiltR1, Trigger: unbuiltTrigger(unbuiltR1)}},
		},
	})
}
