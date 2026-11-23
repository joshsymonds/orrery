package build

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// verifierFindingWithRuns is verifierFinding with two test runs in position order: the first has a result, the second
// has none.
func verifierFindingWithRuns() store.TaskFinding {
	finding := verifierFinding()
	finding.TestRuns = []store.TaskTestRun{
		{Position: 0, Command: "go test ./internal/build -run Brief", Revision: "rev222", Step: "test-1", Output: "PASS\nok"},
		{Position: 1, Command: "go vet ./internal/build", Revision: "rev222"},
	}
	return finding
}

func TestTestRunnerRequestsInVerifierPrompt(t *testing.T) {
	t.Parallel()
	got := VerifierPrompt(verifierEpic(), verifierTask(), "base111", "rev222", verifierFindingWithRuns())
	// The test runner block sits after the finding's verify-by step and before the instruction to return one result,
	// and a run with no step reads not run.
	want := strings.Join([]string{
		"Verify by: go test ./internal/build -run Brief",
		"Test runner output:",
		"Command: go test ./internal/build -run Brief",
		"Revision: rev222",
		"Output: PASS",
		"ok",
		"Command: go vet ./internal/build",
		"Revision: rev222",
		"Output: not run",
		"Return exactly one result, for F7.",
	}, "\n")
	if !strings.HasSuffix(got, want) {
		t.Fatalf("VerifierPrompt does not end with the test runner block\n got:\n%s\nwant suffix:\n%s", got, want)
	}
}

func TestTestRunnerRequestsAbsentLeavesPromptUnchanged(t *testing.T) {
	t.Parallel()
	absent := verifierFinding()
	empty := verifierFinding()
	empty.TestRuns = []store.TaskTestRun{}

	// A finding with no test runs, whether its runs are nil or empty, renders the prompt it rendered before runs were
	// stored: no test runner block, and the same prompt either way, at both the task and the epic stage.
	task := VerifierPrompt(verifierEpic(), verifierTask(), "base111", "rev222", absent)
	if strings.Contains(task, testRunsHeading) {
		t.Errorf("task prompt of a finding with no test runs has a test runner block:\n%s", task)
	}
	if got := VerifierPrompt(verifierEpic(), verifierTask(), "base111", "rev222", empty); got != task {
		t.Errorf("task prompt of a finding with empty test runs differs from the one with nil\n got:\n%s\nwant:\n%s",
			got, task)
	}

	epicSubject := Subject{Epic: verifierEpic(), Base: "base111", Revision: "rev222"}
	epicPrompt := VerifierStart(epicSubject, absent, verifyStepID, verifyAgent).Prompt
	if strings.Contains(epicPrompt, testRunsHeading) {
		t.Errorf("epic prompt of a finding with no test runs has a test runner block:\n%s", epicPrompt)
	}
	if got := VerifierStart(epicSubject, empty, verifyStepID, verifyAgent).Prompt; got != epicPrompt {
		t.Errorf("epic prompt of a finding with empty test runs differs from the one with nil\n got:\n%s\nwant:\n%s",
			got, epicPrompt)
	}
}
