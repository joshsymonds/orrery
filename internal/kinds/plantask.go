package kinds

import (
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// kindBuild is the kind of a build node run, the build child of a plan's task.
const kindBuild = "build"

// PlanTask reports whether task is a task of the plan run plan, among the epic's node runs runs: a task of a turn
// above the plan's mark, or a task of a turn at or below it that a send_back reopened, at a reopen generation above 0,
// and that the plan builds, so a build child of the task runs under the plan's path. The running plan starts that
// child for a reopened task an earlier plan run planned (interpret.pass.startTasks). A task of an earlier turn at
// generation 0 is the earlier plan run's alone. The interpreter settles and integrates a plan's tasks by this rule, a
// correction round's closure records them, a loop round counts them as planned, and the clients are shown them.
func PlanTask(task store.EpicTask, plan store.NodeRun, runs []store.NodeRun) bool {
	if task.Turn > plan.Mark {
		return true
	}
	if task.Generation == 0 {
		return false
	}
	return slices.ContainsFunc(runs, func(run store.NodeRun) bool {
		return run.Kind == kindBuild && run.Task == task.Seq && strings.HasPrefix(run.Path, plan.Path+"/")
	})
}
