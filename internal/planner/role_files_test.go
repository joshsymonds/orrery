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

// roleStartsNowSentence is the sentence role.md gives the planner about when a task may build on another's work.
const roleStartsNowSentence = "Plan only tasks that can start now. Work that builds on an open task's change, such as " +
	"code that uses fields or functions another open task adds, waits for a later turn: once the open tasks are done, " +
	"orreryd integrates them and plans the epic again from the new base."

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

// TestPlannerRolePlansOnlyTasksThatCanStartNow asserts that the role text tells the planner that every task starts at
// once from the integrated branch, so work that builds on an open task's change waits for a later turn.
func TestPlannerRolePlansOnlyTasksThatCanStartNow(t *testing.T) {
	t.Parallel()
	assertRoleHasSentence(t, roleStartsNowSentence)
}
