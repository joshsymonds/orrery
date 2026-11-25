package build

import (
	"strings"
	"testing"
)

// TestOrchestratorRoleChangesACaptureThatRanNoTest: the orchestrator role says that a red or green whose judge reason
// shows its capture ran no test, or not the tests its conclusion names, shows the same when run again, so a decision
// that reruns the task changes its evidence item.
func TestOrchestratorRoleChangesACaptureThatRanNoTest(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"The Gate section gives the task's red and green runs with the judge's reason for each.",
		"When a reason shows that the capture ran no test",
		"or not the tests its conclusion names, running that capture again shows the same",
		"So a rerun, grant or re_brief then returns a version whose changed names evidence",
		"never one that leaves the evidence item as it is.",
	} {
		if !strings.Contains(OrchestratorRole, want) {
			t.Errorf("orchestrator role lacks %q", want)
		}
	}
}
