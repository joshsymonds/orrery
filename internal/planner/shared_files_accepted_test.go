package planner

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSharedFilesAcceptedUnorderedTasks checks that one file two return tasks name passes when depends_on does not order
// them: the second task depends on an open epic task that is not the first.
func TestSharedFilesAcceptedUnorderedTasks(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{epicTask(1, taskOpen, "internal/store/epics.go")}
	ret := Return{Tasks: []Task{
		validTask("A", "internal/a.go"),
		dependent("B", "internal/a.go", "T1"),
	}}
	checkLines(t, checkTasks(ret, tasks))
}

// TestSharedFilesAcceptedOpenOwnedFile checks that a file an open epic task owns passes when the naming task does not
// depend on that task.
func TestSharedFilesAcceptedOpenOwnedFile(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{validTask("T", "internal/store/epics.go")}}
	checkLines(t, checkPlan(ret, []string{"internal/store/epics.go"}))
}
