package build

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// versionedDeliverable is the Deliverable version 2 of the versioned task sets.
const versionedDeliverable = "the version's deliverable"

// versionedTask is task T3 at version 2: the planner's version 1 Deliverable, overlaid by applied version 2, which
// changes the Deliverable. Its prompts show version 2's Deliverable, as the store's read overlays set it.
func versionedTask() store.EpicTask {
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	deliverable := versionedDeliverable
	return store.EpicTask{
		Seq:          3,
		Title:        "Versioned task",
		Deliverable:  versionedDeliverable,
		Files:        []string{"internal/build/brief.go"},
		Requirements: []string{"R1"},
		Gate:         "check",
		Evidence: evidence.Item{
			Capture:    []string{"scripts/in-devenv", "-c", "go test -run TestTaskVersion ./internal/build/"},
			Conclusion: "the prompt shows the version",
		},
		Build:   store.TaskBuild{Base: "b3se"},
		Version: 2,
		Versions: []store.TaskVersion{{
			Version: 2, Source: store.SourceOrchestrator, Decision: "T1-request-1",
			Reason: "the planner named the wrong deliverable", State: store.VersionApplied,
			Deliverable: &deliverable, Created: created, Settled: created,
		}},
	}
}

// assertVersionLinesBeforeDeliverable checks that prompt holds the lines of taskspec.Lines(task) consecutively, right
// before its Deliverable line, and the sentence saying version 2 governs.
func assertVersionLinesBeforeDeliverable(t *testing.T, prompt string, task store.EpicTask) {
	t.Helper()
	spec := taskspec.Lines(task)
	if len(spec) == 0 {
		t.Fatalf("Lines of a task at version %d is empty, want its version lines", task.Version)
	}
	lines := strings.Split(prompt, "\n")
	at := slices.Index(lines, "Deliverable: "+task.Deliverable)
	if at < 0 {
		t.Fatalf("prompt has no line %q; prompt:\n%s", "Deliverable: "+task.Deliverable, prompt)
	}
	if at < len(spec) || !slices.Equal(lines[at-len(spec):at], spec) {
		t.Errorf("the lines before the Deliverable are not the version lines %q; prompt:\n%s", spec, prompt)
	}
	if !strings.Contains(prompt, "version 2 governs.") {
		t.Errorf("prompt does not say version 2 governs; prompt:\n%s", prompt)
	}
}

func TestTaskVersionBriefRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	task := versionedTask()
	gate := briefGate()
	prompt := Brief(briefEpic(), task, gate, store.EvidenceRun{}, nil, nil, "")
	assertVersionLinesBeforeDeliverable(t, prompt, task)
}

func TestTaskVersionReviewPromptRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	task := versionedTask()
	prompt := ReviewPrompt(briefEpic(), task, "floor", "b3se", "r3ev", store.StepCheck{})
	assertVersionLinesBeforeDeliverable(t, prompt, task)
}

func TestTaskVersionVerifierPromptRendersTheCurrentVersion(t *testing.T) {
	t.Parallel()
	task := versionedTask()
	prompt := VerifierPrompt(briefEpic(), task, "b3se", "r3ev", verifierFinding())
	assertVersionLinesBeforeDeliverable(t, prompt, task)
}
