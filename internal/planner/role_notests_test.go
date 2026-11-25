package planner

import (
	"strings"
	"testing"
)

// TestRoleSaysACaptureThatRanNoTestIsChangedNotRetried asserts that the role text names where a failure carries the
// judge's reason, and says a failure whose capture ran no test, or not the tests its conclusion names, is settled by
// amend_evidence for a Requirement and by change_approach's item for a task, never by retrying it as written.
func TestRoleSaysACaptureThatRanNoTestIsChangedNotRetried(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"a task's `green not met: exit <n>, verdict <v>: <reason>`",
		"`R<n> is not green at the stored base <base>: its capture exited <n> and was judged <v>: <reason>`",
		"A failure whose judge reason shows that its capture ran no test",
		"or not the tests its conclusion names, is not fixed by running that capture again",
		"For a Requirement, return amend_evidence with an item whose capture runs the tests its conclusion names.",
		"For a task, change its evidence item through change_approach's item",
		"Never retry either as written, by retry_node, retry_epic, send_back or a task with the same item.",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}
