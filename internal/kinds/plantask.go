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
// generation 0 is the earlier plan run's alone: a plan's mark starts below the turn of every open task no plan run has
// built (PlanMark). The interpreter settles and integrates a plan's tasks by this rule, a correction round's closure
// records them, a loop round counts them as planned, and the clients are shown them.
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

// PlanMark returns the mark a plan run starts with when the epic already has a plan run: the epic's highest turn seq,
// lowered to one below the turn of the earliest open task at generation 0 that no build node run of the epic has
// built (neverBuilt). Such a task was planned while no plan ran, as when the planner takes a turn while the epic's
// release is stopped, so its turn stored no build child with it. A mark below its turn makes the task the new plan's
// (PlanTask), and the plan starts its build (interpret.pass.startTasks). The lowered mark takes no task an earlier plan
// run built: a plan run that ran when that task's turn ended would have stored its build child, and one that started
// after that turn would have started below it and built the task, so no plan run built a task of a later turn. An epic
// with no turn gives 0.
func PlanMark(rows Rows) int64 {
	var mark int64
	for _, turn := range rows.Epic.Turns {
		mark = max(mark, turn.Seq)
	}
	for _, task := range rows.Epic.Tasks {
		if neverBuilt(task, rows.Runs) {
			mark = min(mark, task.Turn-1)
		}
	}
	return mark
}

// neverBuilt reports whether task is open at generation 0 and no build node run of the epic, among runs, is the
// task's, running or ended, under any plan run.
func neverBuilt(task store.EpicTask, runs []store.NodeRun) bool {
	if task.Status != taskOpen || task.Generation != 0 {
		return false
	}
	return !slices.ContainsFunc(runs, func(run store.NodeRun) bool {
		return run.Kind == kindBuild && run.Task == task.Seq
	})
}
