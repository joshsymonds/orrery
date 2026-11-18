package taskspec

import (
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestTaskVersionRenderNothingForThePlannersVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []int64{0, 1} {
		task := store.EpicTask{Seq: 2, Version: version}
		if got := Lines(task); got != nil {
			t.Errorf("Lines at version %d = %q, want nil", version, got)
		}
	}
}

func TestTaskVersionRenderNamesTheCurrentVersionAndSaysItGoverns(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	deliverable := "the renderer names the version"
	files := []string{"internal/taskspec/taskspec.go", "internal/build/brief.go"}
	task := store.EpicTask{
		Seq:     5,
		Version: 3,
		Versions: []store.TaskVersion{
			{
				Version: 2, Source: store.SourceOrchestrator, Decision: "T1-request-1",
				Reason: "the spec named the wrong file", State: store.VersionApplied, Deliverable: &deliverable,
				Created: created, Settled: created,
			},
			{
				Version: 3, Source: store.SourceGrant, Reason: "the implementer needs brief.go",
				State: store.VersionApplied, Files: &files, Created: created, Settled: created,
			},
			{
				Version: 4, Source: store.SourceOrchestrator, Decision: "T1-request-2", Reason: "refused for now",
				State: "refused", Deliverable: &deliverable, Created: created, Settled: created,
			},
		},
	}
	want := []string{
		"Spec: version 3 of T5 is this task's current spec, which every agent on the task reads. " +
			"Where it differs from an earlier version, version 3 governs.",
		"Changes since the planner's version 1, oldest first:",
		"- version 2 by orchestrator, decision T1-request-1: changes deliverable. the spec named the wrong file",
		"- version 3 by grant: changes files. the implementer needs brief.go",
	}
	if got := Lines(task); !slices.Equal(got, want) {
		t.Errorf("Lines = %q, want %q", got, want)
	}
}
