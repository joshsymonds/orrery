package planner

import (
	"strings"
	"testing"
)

// TestRoleEvidenceWrittenAsString asserts that the prompt tells the planner a task's evidence is one object written as a
// JSON string, as args is, and no longer says it is an object with its fields.
func TestRoleEvidenceWrittenAsString(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
	const want = "A task's evidence is one object, written as a JSON string as args is,"
	const old = "A task's evidence is one object with kind"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt does not say a task's evidence is written as a JSON string, as args is: want %q", want)
	}
	if strings.Contains(prompt, old) {
		t.Errorf("prompt still says a task's evidence is one object with kind: %q", old)
	}
}
