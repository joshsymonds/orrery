package build

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskVersionOrchestratorRerunAfterASpentAttemptKeepsItsCheckOutput: a carried rerun that answers an implementer step
// which ended checks_failed stores its version under its decision, and the build keeps the spent attempt's check output,
// which orreryd wrote, as its failure. Neither the version's deliverable nor the return's reason is the failure.
func TestTaskVersionOrchestratorRerunAfterASpentAttemptKeepsItsCheckOutput(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	const deliverable = "Widgets sort by their key."
	const reason = "the spec names the sort key"
	ret := orchReturn(t, map[string]any{
		"decision": "rerun", "reason": reason, "version": versionWith(map[string]any{
			"changed": []string{"deliverable"}, "deliverable": deliverable,
		}),
	})
	view := View{
		Epic: passEpic, Task: f.task(),
		Returned: &store.StepRow{
			ID: passStep(StageImplement, 1, 0), End: endChecksFailed, Reason: "owned: b.go is not owned",
			Checks: []store.StepCheck{
				{Round: 3, Name: "gate", OK: false},
				{Round: 4, Name: "head", OK: true}, {Round: 4, Name: "clean", OK: true},
				{Round: 4, Name: checkOwned, OK: false, Detail: "b.go is not owned"},
			},
		},
		Step: &store.StepRow{ID: orchID(0, 0), End: endDone, Status: statusDone, Result: ret},
	}
	if !f.runner.execute(f.ctx, f.epic(), view, Decide(view)) {
		t.Fatal("execute changed nothing, want the rerun carried out")
	}
	task := f.task()
	if len(task.Versions) != 1 {
		t.Fatalf("Versions = %+v, want the one version the rerun stored", task.Versions)
	}
	got := task.Versions[0]
	if got.Version != 2 || got.Source != store.SourceOrchestrator || got.Decision != "T1-request-1" {
		t.Errorf("version = %+v, want version 2 of the orchestrator's decision T1-request-1", got)
	}
	if task.Deliverable != deliverable || task.Version != 2 {
		t.Errorf("task deliverable %q at version %d, want the version's deliverable at version 2", task.Deliverable,
			task.Version)
	}
	const failure = "checks_failed: owned: b.go is not owned"
	if task.Build.Failure != failure {
		t.Errorf("Build.Failure = %q, want orreryd's check output %q", task.Build.Failure, failure)
	}
	for _, text := range []string{deliverable, reason} {
		if strings.Contains(task.Build.Failure, text) {
			t.Errorf("Build.Failure %q names %q, want orreryd's check output alone", task.Build.Failure, text)
		}
	}
}
