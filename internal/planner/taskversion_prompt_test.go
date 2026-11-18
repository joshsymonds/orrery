package planner

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// TestTaskVersionPlannerPromptRendersTheCurrentVersion: a task at version 2 has the lines taskspec.Lines gives it, each
// indented two spaces, consecutively between its title line and its deliverable, and one of them says version 2
// governs. A task at version 1 in the same list has no spec line.
func TestTaskVersionPlannerPromptRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	deliverable := "a cache that expires"
	changed := store.EpicTask{
		Seq: 1, Title: "add the cache", Deliverable: deliverable, Status: "open", Version: 2,
		Versions: []store.TaskVersion{{
			Version: 2, Source: store.SourceOrchestrator, Decision: "T1-request-1", Reason: "the cache must expire",
			State: store.VersionApplied, Deliverable: &deliverable, Created: created, Settled: created,
		}},
	}
	plain := store.EpicTask{Seq: 2, Title: "plain task", Deliverable: "a plain deliverable", Status: "open", Version: 1}
	got, err := taskLines([]store.EpicTask{changed, plain})
	if err != nil {
		t.Fatalf("taskLines: %v", err)
	}

	spec := taskspec.Lines(changed)
	block := make([]string, 0, 2+len(spec))
	block = append(block, "- T1 [open] add the cache")
	for _, line := range spec {
		block = append(block, "  "+line)
	}
	block = append(block, "  deliverable: a cache that expires")
	if !strings.Contains("\n"+strings.Join(got, "\n")+"\n", "\n"+strings.Join(block, "\n")+"\n") {
		t.Errorf("taskLines lack the version-2 block %q in %q", block, got)
	}

	governs := "  Spec: version 2 of T1 is this task's current spec, which every agent on the task reads. " +
		"Where it differs from an earlier version, version 2 governs."
	if !strings.Contains(strings.Join(got, "\n"), governs) {
		t.Errorf("taskLines lack the line %q in %q", governs, got)
	}

	plainBlock := "- T2 [open] plain task\n  deliverable: a plain deliverable\n"
	if !strings.Contains("\n"+strings.Join(got, "\n")+"\n", "\n"+plainBlock) {
		t.Errorf("taskLines of the version-1 task = %q, want it read as before", got)
	}
}
