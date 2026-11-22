package planner

import (
	"strings"
	"testing"
)

// TestPlannerRoleDesignTaskCarriesAKind asserts that the role text says a task carries a kind, design for a task whose
// deliverable is the interfaces and stubs as compiling code, and empty otherwise.
func TestPlannerRoleDesignTaskCarriesAKind(t *testing.T) {
	t.Parallel()
	const want = `a kind, "design" for a task whose deliverable is the interfaces and stubs as compiling code and "" ` +
		`otherwise,`
	if !strings.Contains(role, want) {
		t.Errorf("role text lacks %q", want)
	}
}

// TestPlannerRoleDesignInterfacesComeAsDesignTasks asserts that the role text says a judge checks the design tasks
// against the contract and that the other tasks of the turn wait until they integrate.
func TestPlannerRoleDesignInterfacesComeAsDesignTasks(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"A judge checks them against the contract",
		"the other tasks of that turn wait until they integrate",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestPlannerRoleDesignDependsOnNamesTheDesignTask asserts that the role text says a task using an interface names its
// design task in depends_on.
func TestPlannerRoleDesignDependsOnNamesTheDesignTask(t *testing.T) {
	t.Parallel()
	const want = "A task that uses an interface names its design task in depends_on"
	if !strings.Contains(role, want) {
		t.Errorf("role text lacks %q", want)
	}
}

// TestPlannerRoleDesignTasksMayShareFiles asserts that the role text says tasks may share the files they both need, and
// that integrate merges both sides of a conflict.
func TestPlannerRoleDesignTasksMayShareFiles(t *testing.T) {
	t.Parallel()
	const want = "Tasks may share files they both need, and integrate merges both sides of a conflict."
	if !strings.Contains(role, want) {
		t.Errorf("role text lacks %q", want)
	}
}

// TestPlannerRoleDesignReopenDesignAction asserts that the role text gives the reopen_design action its args, says when
// to use it, and says what it does to the tasks that depend on the design task.
func TestPlannerRoleDesignReopenDesignAction(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		`reopen_design {"task": <n>, "deliverable": "<the change>"}`,
		"Use reopen_design when a design task's interface proves wrong after it integrated.",
		"adds a revising design task, and once that integrates, every task naming the design task in depends_on, " +
			"directly or transitively, goes back at a new generation with the history kept.",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestPlannerRoleDesignDropsTheOldFileRules asserts that the role text no longer says a shared or open-owned file is
// refused, no longer speaks of a first wave, and no longer refuses a requested file an open task owns.
func TestPlannerRoleDesignDropsTheOldFileRules(t *testing.T) {
	t.Parallel()
	for _, gone := range []string{
		"first wave",
		"Two tasks may name the same file only when",
		"A task may name a file an open task owns only when",
		"where a file that another open task owns, or an accepted task has not yet integrated, is refused",
		"when no open task owns it",
	} {
		if strings.Contains(role, gone) {
			t.Errorf("role text still contains %q", gone)
		}
	}
}
