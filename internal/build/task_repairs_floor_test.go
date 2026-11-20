package build

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskRepairsFloorPromptCarriesTheTasksChangeNotARequirementCapture: the floor prompt for a task that claims a
// Requirement whose capture another task writes is exactly the task's own change range, owned files, test changes and
// own evidence, with the Requirement's text and no Requirement capture. The task's own evidence and a claimed
// Requirement that carries a capture are set together, a combination the other review prompt tests do not set.
func TestTaskRepairsFloorPromptCarriesTheTasksChangeNotARequirementCapture(t *testing.T) {
	t.Parallel()
	epic := briefEpic()
	epic.Requirements = []store.EpicRequirement{
		{ID: "R1", Text: "one", Item: evidence.Item{
			Capture:    []string{"go", "test", "-run", "TestWriterOwnsIt", "./internal/writer/"},
			Conclusion: "the writer's capture passes",
		}},
	}
	task := briefTask()
	task.Requirements = []string{"R1"}
	got := ReviewPrompt(epic, task, "floor", "base-9", "rev-9", store.StepCheck{}, nil)
	want := "Task T4 of epic repo/epic-x (Build the task): Render brief\n" +
		"Deliverable: brief.go\n" +
		"Owned files:\n" +
		"- internal/build/brief.go\n" +
		"- internal/build/brief_test.go\n" +
		"Test changes:\n" +
		"- internal/build/brief_test.go\n" +
		reviewDeclared + "\n" +
		"- none\n" +
		"Requirements:\n" +
		"- R1: one\n" +
		reviewEvidenceHeading + "\n" +
		"- T4: the brief renders (capture: scripts/in-devenv -c 'go test -run Brief ./internal/build/')\n" +
		"The change under review is base-9..rev-9; this worktree is checked out at rev-9. " +
		"Inspect it with `git diff base-9 rev-9`."
	if got != want {
		t.Fatalf("floor prompt for a task claiming a Requirement with a capture\n got:\n%s\nwant:\n%s", got, want)
	}
}
