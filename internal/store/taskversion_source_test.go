package store_test

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestTaskVersionSourcesWrittenArePlannerOrchestratorAndGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)

	task := buildTaskOf(ctx, t, s)
	if task.Version != 1 || task.Source() != store.SourcePlanner {
		t.Errorf("fresh task Version = %d, Source() = %q, want version 1 from the planner",
			task.Version, task.Source())
	}

	deliverable := "Each end writes the version"
	rerun := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "T1 rerun: the spec moved", Deliverable: &deliverable,
	}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), rerun, requestDecision(), writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	task = buildTaskOf(ctx, t, s)
	if task.Version != 2 || task.Source() != store.SourceOrchestrator {
		t.Errorf("after the rerun Version = %d, Source() = %q, want version 2 from the orchestrator",
			task.Version, task.Source())
	}
	if len(task.Versions) != 1 || task.Versions[0].Version != 2 || task.Versions[0].Source != store.SourceOrchestrator {
		t.Errorf("Versions = %+v, want the one stored version 2 with source orchestrator", task.Versions)
	}

	grant := store.TaskVersionInput{
		Source: store.SourceGrant, Step: "step-3", Reason: "the implementer needs c.go", Files: []string{"c.go"},
	}
	if err = s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), grant, writtenAt()); err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}
	task = buildTaskOf(ctx, t, s)
	if task.Version != 3 || task.Source() != store.SourceGrant {
		t.Errorf("after the grant Version = %d, Source() = %q, want version 3 from the grant",
			task.Version, task.Source())
	}
	if len(task.Versions) != 2 || task.Versions[1].Version != 3 || task.Versions[1].Source != store.SourceGrant ||
		task.Versions[1].Decision != "" {
		t.Errorf("Versions = %+v, want the grant stored as version 3 with source grant and no decision", task.Versions)
	}

	// A version that is checking, then refused, does not count: the task's source stays the grant's.
	item := evidence.Item{
		Capture: []string{"go", "test", "./internal/store/"}, Limit: 300, Artifacts: []string{"out/sources.json"},
		Conclusion: "the sources", Judge: "read the output",
	}
	check := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-4", Reason: "T1 rerun: the evidence moved", Evidence: &item,
	}
	checkDecision := store.TaskDecision{ID: "T1-request-2", Text: check.Reason}
	err = s.RerunTask(ctx, buildEpic, 1, grantedBuild(), grantedBuild(), check, checkDecision, writtenAt())
	if err != nil {
		t.Fatalf("RerunTask with evidence: %v", err)
	}
	task = buildTaskOf(ctx, t, s)
	if len(task.Versions) != 3 || task.Versions[2].State != store.VersionChecking {
		t.Fatalf("Versions = %+v, want version 4 stored checking", task.Versions)
	}
	if task.Source() != store.SourceGrant {
		t.Errorf("Source() = %q with a checking version, want the grant's %q", task.Source(), store.SourceGrant)
	}

	settle := store.TaskVersionSettle{Version: 4, State: store.VersionRefused, Refusal: "the evidence did not take"}
	if err = s.SettleTaskVersion(ctx, buildEpic, 1, grantedBuild(), grantedBuild(), settle, writtenAt()); err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}
	task = buildTaskOf(ctx, t, s)
	if len(task.Versions) != 3 || task.Versions[2].State != store.VersionRefused {
		t.Fatalf("Versions = %+v, want version 4 settled refused", task.Versions)
	}
	if task.Source() != store.SourceGrant {
		t.Errorf("Source() = %q with a refused version, want the grant's %q", task.Source(), store.SourceGrant)
	}
}
