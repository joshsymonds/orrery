package reviewers

import "strings"

// TestParsimony is the name of the default reviewer that judges the tests a change adds. Its review prompt carries the
// Test cost section (TestCost).
const TestParsimony = "test_parsimony"

// TestCostHeading is the line the Test cost section opens with.
const TestCostHeading = "Test cost (the budget lines of the gate output kept for this change):"

// NoBudgetLines is the line the Test cost section holds when the gate output it reads has no budget line.
const NoBudgetLines = "no budget lines"

// budgetLine reports whether line of a gate's output reports what its tests cost: scripts/test-budget.sh's per-package
// CPU budget lines and its count, check-changed's whole-run budget line, passed or exceeded, and the wall-clock budget
// lines of cover and web-check.
func budgetLine(line string) bool {
	for _, prefix := range []string{
		"test-budget: ",
		"check-changed: OK after ",
		"check-changed: exceeded ",
		"tests exceeded ",
		"web-check: the web suite exceeded ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// TestCost returns the Test cost section of a review prompt: TestCostHeading, then each budget line of outputs as
// a bullet, in the order outputs give them, or the single bullet NoBudgetLines when they have none. outputs are the
// kept stdout and stderr tails of the gate runs the change passed, so a budget line printed before the kept tail does
// not appear.
func TestCost(outputs ...string) []string {
	var found []string
	for _, output := range outputs {
		for line := range strings.Lines(output) {
			line = strings.TrimRight(line, "\r\n")
			if budgetLine(line) {
				found = append(found, "- "+line)
			}
		}
	}
	if len(found) == 0 {
		found = []string{"- " + NoBudgetLines}
	}
	return append([]string{TestCostHeading}, found...)
}
