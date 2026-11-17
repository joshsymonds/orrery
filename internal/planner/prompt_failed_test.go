package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// failedTaskEpic is syntheticEpic with a second task, T2, that failed with failure and named needsFiles as needed
// beyond its owned list. T1 stays open.
func failedTaskEpic(failure string, needsFiles []string) store.EpicRow {
	epic := syntheticEpic()
	epic.Tasks = append(epic.Tasks, store.EpicTask{
		Seq: 2, Turn: 1, Title: "a failed task", Deliverable: "a failed deliverable",
		Files: []string{"internal/planner/prompt.go"}, Requirements: []string{"R1"}, Gate: "check",
		Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the failed task is done", Judge: "a judge",
		},
		Status: "failed", Build: store.TaskBuild{Failure: failure}, NeedsFiles: needsFiles,
	})
	return epic
}

func TestFailedTaskContextShowsTheFailureReasonUnderItsLine(t *testing.T) {
	t.Parallel()
	epic := failedTaskEpic("returned blocked: the gate is broken", nil)
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	inOrder(t, prompt,
		"- T2 [failed] a failed task\n",
		"  evidence: ",
		"\n  failure: returned blocked: the gate is broken\n",
		"  files the next task may need: None.\n",
	)
}

func TestFailedTaskContextOwnedCheckFailureShowsTheRefusedPaths(t *testing.T) {
	t.Parallel()
	failure := "checks_failed: owned: changed paths outside owned: internal/other/x.go, internal/other/y.go"
	epic := failedTaskEpic(failure, nil)
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	inOrder(t, prompt,
		"- T2 [failed] a failed task\n",
		"\n  failure: "+failure+"\n",
	)
}

func TestFailedTaskContextOpenTaskShowsNeitherFailureNorNeededFiles(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	// T1 is open on its second attempt: the failure of its first attempt and a path its implementer named are stored,
	// but a task that has not failed shows neither.
	epic.Tasks[0].Build.Failure = "checks_failed: gate: lint failed"
	epic.Tasks[0].NeedsFiles = []string{"internal/other/a.go"}
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	if strings.Contains(prompt, "  failure: ") {
		t.Errorf("open task shows a failure line:\n%s", prompt)
	}
	if strings.Contains(prompt, "files the next task may need") {
		t.Errorf("open task shows its needed files:\n%s", prompt)
	}
}

func TestNeedsFilesPromptListsThePathsUnderTheFailedTask(t *testing.T) {
	t.Parallel()
	epic := failedTaskEpic("returned needs_context: the spec names another file",
		[]string{"internal/other/a.go", "internal/other/b.go"})
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	inOrder(t, prompt,
		"- T2 [failed] a failed task\n",
		"\n  failure: returned needs_context: the spec names another file\n",
		"  files the next task may need: internal/other/a.go, internal/other/b.go\n",
	)
}
