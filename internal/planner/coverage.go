package planner

import (
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// unclaimedLines returns one line per Requirement of the epic that no live stored task claims, in contract order, for the
// prompt's Unclaimed requirements section. requirements are the epic's Requirement ids in contract order, and tasks are
// the epic's stored tasks. A task is live while it is not failed, so a task on the ladder or ended as a gap holds its
// claims. Coverage is information the planner reads, never a refusal of its return.
func unclaimedLines(requirements []string, tasks []store.EpicTask) []string {
	claimed := liveClaims(tasks)
	var lines []string
	for _, id := range requirements {
		if !claimed[id] {
			lines = append(lines, fmt.Sprintf("- %s is claimed by no live task", id))
		}
	}
	return lines
}

// liveClaims returns the set of Requirement ids a stored task that is not failed claims.
func liveClaims(tasks []store.EpicTask) map[string]bool {
	claimed := make(map[string]bool)
	for _, task := range tasks {
		if task.Status == taskFailed {
			continue
		}
		for _, id := range task.Requirements {
			claimed[id] = true
		}
	}
	return claimed
}
