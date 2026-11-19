// Package taskspec renders the lines that name the spec version a task's prompts show: the task's current version, and
// the applied versions that changed its spec since the planner's version 1.
package taskspec

import (
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Lines returns the lines a prompt prints just before a task's Deliverable to name the spec version it shows: that the
// task's current version governs where an earlier version differs, then one line for each applied version, oldest
// first, with its source, its decision when it has one, the fields it changes and its reason. A task at version 1 or
// lower has no such lines and returns nil, so its prompts read as they did before any version changed it. Checking and
// refused versions are not listed.
func Lines(task store.EpicTask) []string {
	if task.Version <= 1 {
		return nil
	}
	lines := []string{
		fmt.Sprintf("Spec: version %d of T%d is this task's current spec, which every agent on the task reads. "+
			"Where it differs from an earlier version, version %d governs.", task.Version, task.Seq, task.Version),
		"Changes since the planner's version 1, oldest first:",
	}
	for _, version := range task.Versions {
		if version.State != store.VersionApplied {
			continue
		}
		decision := ""
		if version.Decision != "" {
			decision = ", decision " + version.Decision
		}
		lines = append(lines, fmt.Sprintf("- version %d by %s%s: changes %s. %s",
			version.Version, version.Source, decision, strings.Join(version.Changed(), ", "), version.Reason))
	}
	return lines
}
