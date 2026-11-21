package planner

import (
	"strings"
	"testing"
)

// roleTestFilesSentence is the sentence role.md gives the planner about the existing tests a task's files include.
const roleTestFilesSentence = "A task's files also include the existing test files that assert the behavior the task " +
	"changes, found by searching the tests for the symbols the task changes."

// roleReplacementFilesSentence is the sentence role.md gives the planner about the files of a task that replaces a
// failed one.
const roleReplacementFilesSentence = "A task that replaces a failed one also puts in its files the files that its " +
	"predecessor's failure reason or its implementer named."

// roleCallersSentence is the sentence role.md gives the planner about the callers of a changed signature among a task's
// files.
const roleCallersSentence = "A task's files also include every caller of a function, method or type whose signature " +
	"the task changes, found by searching the repository for its name."

// roleRegeneratedSentence is the sentence role.md gives the planner about the files a changed proto, query or schema
// regenerates among a task's files.
const roleRegeneratedSentence = "A task's files also include every file that a proto, query or schema the task " +
	"changes regenerates, such as the generated code `just gen` writes."

// assertRoleHasSentence asserts that the embedded role text contains sentence.
func assertRoleHasSentence(t *testing.T, sentence string) {
	t.Helper()
	if !strings.Contains(role, sentence) {
		t.Errorf("role text lacks the sentence %q", sentence)
	}
}

// TestPlannerRoleFilesIncludeTheExistingTestsThatAssertAChangedBehaviour asserts that the role text tells the planner
// to put in a task's files the existing test files that assert the behavior the task changes, found by searching the
// tests for the symbols it changes.
func TestPlannerRoleFilesIncludeTheExistingTestsThatAssertAChangedBehaviour(t *testing.T) {
	t.Parallel()
	assertRoleHasSentence(t, roleTestFilesSentence)
}

// TestPlannerRoleFilesGiveAReplacementTheFilesItsPredecessorNamed asserts that the role text tells the planner to give a
// task that replaces a failed one the files its predecessor's failure reason or its implementer named.
func TestPlannerRoleFilesGiveAReplacementTheFilesItsPredecessorNamed(t *testing.T) {
	t.Parallel()
	assertRoleHasSentence(t, roleReplacementFilesSentence)
}

// TestPlannerPromptsTightened asserts that the role text tells the planner to name the callers of a changed signature
// and the files a changed proto, query or schema regenerates among a task's files, and that the planner prompt labels
// the epic's exclusions Must Not Ship.
func TestPlannerPromptsTightened(t *testing.T) {
	t.Parallel()
	t.Run("the planner role names callers of a changed signature among a task's files", func(t *testing.T) {
		t.Parallel()
		assertRoleHasSentence(t, roleCallersSentence)
	})
	t.Run("the planner role names regenerated files among a task's files", func(t *testing.T) {
		t.Parallel()
		assertRoleHasSentence(t, roleRegeneratedSentence)
	})
	t.Run("the planner prompt says Must Not Ship", func(t *testing.T) {
		t.Parallel()
		prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
		if !strings.Contains(prompt, "\nMust Not Ship:\n- no routing\n") {
			t.Errorf("prompt has no Must Not Ship line followed by the entry %q", "- no routing")
		}
		if strings.Contains(prompt, "Exclusions:") {
			t.Errorf("prompt has an Exclusions line")
		}
	})
}
