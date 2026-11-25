package enginehost

import (
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskStatusFailed is the status of a task whose build failed. A failed task claims and contributes nothing.
const taskStatusFailed = "failed"

// The states a Requirement shows, from the runs of its current item.
const (
	requirementGreen   = "green"
	requirementRed     = "red"
	requirementPending = "pending"
)

// requirementClaims sets the owner, contributors, state and disagreement of each Requirement of epic's contract from
// flow. flow.Epic.Tasks is in seq order, and flow.Evidence holds only the current runs. A task is live when its status
// is not failed. The disagreement is the line kinds.Disagreement gives from the rulings flow holds on the reproductions
// at the epic's base, "" when there is none.
func requirementClaims(epic *v1.StoredEpic, flow store.EpicFlow) {
	rows := kinds.Rows{
		Epic: flow.Epic, Runs: flow.Runs, Checks: flow.Checks, Evidence: flow.Evidence, Rulings: flow.Rulings,
	}
	for _, requirement := range epic.GetContract().GetRequirements() {
		requirement.Owner, requirement.Contributors = requirementOwnership(requirement.GetId(), flow.Epic.Tasks)
		requirement.State = requirementState(requirement.GetId(), flow.Evidence)
		requirement.Disagreement = kinds.Disagreement(rows, requirement.GetId())
	}
}

// requirementOwnership returns the T<seq> of the lowest-seq live task that claims the Requirement id, "" when none does,
// and the T<seq> of each live task that contributes to it, in seq order.
func requirementOwnership(id string, tasks []store.EpicTask) (string, []string) {
	owner := ""
	var contributors []string
	for _, task := range tasks {
		if task.Status == taskStatusFailed {
			continue
		}
		name := fmt.Sprintf("T%d", task.Seq)
		if owner == "" && slices.Contains(task.Requirements, id) {
			owner = name
		}
		if slices.Contains(task.Contributes, id) {
			contributors = append(contributors, name)
		}
	}
	return owner, contributors
}

// requirementState returns green when a green run of the item id passed, else red when a red run of it passed, else
// pending.
func requirementState(id string, runs []store.EvidenceRun) string {
	state := requirementPending
	for _, run := range runs {
		if run.Item != id || !run.Passed {
			continue
		}
		switch run.Phase {
		case evidence.PhaseGreen:
			return requirementGreen
		case evidence.PhaseRed:
			state = requirementRed
		}
	}
	return state
}
