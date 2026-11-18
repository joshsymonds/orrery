package interpret

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// TestTaskVersionReleaseIntegratorRendersTheCurrentVersion: an accepted task at version 2 is listed with the lines
// taskspec.Lines gives it, each indented two spaces, consecutively between its title line and its deliverable, and one
// of them says version 2 governs.
func TestTaskVersionReleaseIntegratorRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	deliverable := "a cache that expires"
	task := store.EpicTask{
		Seq: 1, Title: "add the cache", Deliverable: deliverable, Files: []string{"cache.go"}, Status: "done",
		Version: 2,
		Versions: []store.TaskVersion{{
			Version: 2, Source: store.SourceOrchestrator, Decision: "T1-request-1", Reason: "the cache must expire",
			State: store.VersionApplied, Deliverable: &deliverable, Created: created, Settled: created,
		}},
	}
	got := integratorTaskLines([]store.EpicTask{task}, nil)

	spec := taskspec.Lines(task)
	block := make([]string, 0, 2+len(spec))
	block = append(block, "- T1 add the cache")
	for _, line := range spec {
		block = append(block, "  "+line)
	}
	block = append(block, "  deliverable: a cache that expires")
	if !strings.Contains("\n"+strings.Join(got, "\n")+"\n", "\n"+strings.Join(block, "\n")+"\n") {
		t.Errorf("integratorTaskLines lack the version-2 block %q in %q", block, got)
	}

	governs := "  Spec: version 2 of T1 is this task's current spec, which every agent on the task reads. " +
		"Where it differs from an earlier version, version 2 governs."
	if !slices.Contains(got, governs) {
		t.Errorf("integratorTaskLines lack the line %q in %q", governs, got)
	}
}
