package build

import (
	"slices"

	"github.com/joshsymonds/orrery/internal/store"
)

// hasApproach reports whether task has a version the planner's change of approach wrote, in any state.
func hasApproach(task store.EpicTask) bool {
	return slices.ContainsFunc(task.Versions, func(version store.TaskVersion) bool {
		return version.Source == store.SourceApproach
	})
}

// rungUnstarted reports whether the rung view's build is on has no unit started: the build is in implement with no step
// and no refs snapshot, or in red with no run. A rung's first unit is due on that state, which is why the wait for a
// change of approach (approachAwaited) reads it.
func rungUnstarted(view View) bool {
	build := view.Task.Build
	switch build.Stage {
	case StageImplement:
		return view.Step == nil && build.Refs == ""
	case StageRed:
		return view.Run == nil
	}
	return false
}

// approachAwaited reports whether view's task waits for its change of approach: it is on ApproachRung with no unit of
// the rung started, and no change of approach stored. The climb onto ApproachRung adds the trigger T<seq>-approach, and
// the planner writes the version on that trigger, so the rung's first unit does not start until the version exists. A
// task whose change of approach was refused is not awaited again, since hasApproach holds in any state.
func approachAwaited(view View) bool {
	return store.RungOf(view.Task.Build) == store.ApproachRung && !hasApproach(view.Task) && rungUnstarted(view)
}

// approachDue reports whether view's task on ApproachRung runs its change of approach's red run now: it is in implement
// with no step and no refs snapshot, and its checking version is a change of approach. An approach that changes the
// evidence is stored checking and is judged by red at the task's base, so the build moves to red before the implementer
// runs under it.
func approachDue(view View) bool {
	build := view.Task.Build
	if store.RungOf(build) != store.ApproachRung || build.Stage != StageImplement ||
		view.Step != nil || build.Refs != "" {
		return false
	}
	version, ok := view.Task.Checking()
	return ok && version.Source == store.SourceApproach
}
