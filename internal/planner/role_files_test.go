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

// assertPlannerReceivesSentence asserts that the role text, and the prompt of a planner turn, contain sentence.
func assertPlannerReceivesSentence(t *testing.T, sentence string) {
	t.Helper()
	if !strings.Contains(role, sentence) {
		t.Errorf("role text lacks the sentence %q", sentence)
	}
	if prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers()); !strings.Contains(prompt, sentence) {
		t.Errorf("planner prompt lacks the sentence %q", sentence)
	}
}

// TestPlannerRoleFilesIncludeTheExistingTestsThatAssertAChangedBehaviour asserts that the role text tells the planner
// to put in a task's files the existing test files that assert the behavior the task changes, found by searching the
// tests for the symbols it changes.
func TestPlannerRoleFilesIncludeTheExistingTestsThatAssertAChangedBehaviour(t *testing.T) {
	t.Parallel()
	assertPlannerReceivesSentence(t, roleTestFilesSentence)
}

// TestPlannerRoleFilesGiveAReplacementTheFilesItsPredecessorNamed asserts that the role text tells the planner to give a
// task that replaces a failed one the files its predecessor's failure reason or its implementer named.
func TestPlannerRoleFilesGiveAReplacementTheFilesItsPredecessorNamed(t *testing.T) {
	t.Parallel()
	assertPlannerReceivesSentence(t, roleReplacementFilesSentence)
}
