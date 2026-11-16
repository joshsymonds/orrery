package planner

import (
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/store"
)

// Coverage returns the coverage refusals of ret, one per line, in task order then field order, with the outcome lines
// last, or nil when ret keeps coverage. A Requirement is claimed by a stored task that is not failed or by a task of
// ret. A Requirement a task claims that another live task already claims is refused in the later claim, a
// contribution needs a live claim that is not the contributing task's own, and outcome complete needs every epic
// Requirement claimed. requirements are the epic's Requirement ids in contract order, and tasks are the epic's stored
// tasks. Unknown ids in a task's requirements are Check's refusals and are not repeated here.
func Coverage(ret Return, requirements []string, tasks []store.EpicTask) []string {
	cov := coverage{known: stringSet(requirements), stored: storedClaims(tasks), first: returnClaims(ret)}
	var lines []string
	for i, task := range ret.Tasks {
		prefix := fmt.Sprintf(`tasks[%d] "%s"`, i, task.Title)
		lines = append(lines, cov.claimLines(prefix, i, task)...)
		lines = append(lines, cov.contributeLines(prefix, task)...)
	}
	if ret.Outcome == "complete" {
		lines = append(lines, cov.outcomeLines(requirements)...)
	}
	return lines
}

// coverage holds the claims Coverage judges a return against: the epic's Requirement ids, the seq of the lowest live
// stored claim of each, and the index of the first task of the return that claims each.
type coverage struct {
	known  map[string]bool
	stored map[string]int64
	first  map[string]int
}

// claimed reports whether a live stored task or a task of the return claims id.
func (c coverage) claimed(id string) bool {
	_, inStore := c.stored[id]
	_, inReturn := c.first[id]
	return inStore || inReturn
}

// claimLines returns the refusals of task's requirements, where task is the i-th task of the return: a Requirement a
// live stored task claims, or one an earlier task of the return claims.
func (c coverage) claimLines(prefix string, i int, task Task) []string {
	var lines []string
	for k, id := range task.Requirements {
		if !c.known[id] {
			continue
		}
		if seq, ok := c.stored[id]; ok {
			lines = append(lines, fmt.Sprintf("%s requirements[%d]: %s is claimed by T%d", prefix, k, id, seq))
		} else if j, found := c.first[id]; found && j < i {
			lines = append(lines, fmt.Sprintf("%s requirements[%d]: %s is claimed by tasks[%d]", prefix, k, id, j))
		}
	}
	return lines
}

// contributeLines returns the refusals of task's contributes: an id that is not a Requirement of the epic, one the task
// claims itself, or one no live claim covers.
func (c coverage) contributeLines(prefix string, task Task) []string {
	var lines []string
	for k, id := range task.Contributes {
		switch {
		case !c.known[id]:
			lines = append(lines, fmt.Sprintf("%s contributes[%d]: %s", prefix, k, reasonRequirement))
		case slices.Contains(task.Requirements, id):
			lines = append(lines, fmt.Sprintf("%s contributes[%d]: %s is claimed by this task", prefix, k, id))
		case !c.claimed(id):
			lines = append(lines, fmt.Sprintf("%s contributes[%d]: %s is claimed by no task", prefix, k, id))
		}
	}
	return lines
}

// outcomeLines returns the refusals of a complete outcome: each epic Requirement no live claim covers, in contract
// order.
func (c coverage) outcomeLines(requirements []string) []string {
	var lines []string
	for _, id := range requirements {
		if !c.claimed(id) {
			lines = append(lines, "outcome complete: "+id+" is claimed by no task")
		}
	}
	return lines
}

// storedClaims returns, for each Requirement id a stored task that is not failed claims, the seq of the lowest such
// task.
func storedClaims(tasks []store.EpicTask) map[string]int64 {
	claims := make(map[string]int64)
	for _, task := range tasks {
		if task.Status == taskFailed {
			continue
		}
		for _, id := range task.Requirements {
			if seq, ok := claims[id]; !ok || task.Seq < seq {
				claims[id] = task.Seq
			}
		}
	}
	return claims
}

// returnClaims returns, for each Requirement id a task of ret claims, the index of the first such task.
func returnClaims(ret Return) map[string]int {
	first := make(map[string]int)
	for i, task := range ret.Tasks {
		for _, id := range task.Requirements {
			if _, ok := first[id]; !ok {
				first[id] = i
			}
		}
	}
	return first
}
