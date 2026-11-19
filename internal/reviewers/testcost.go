package reviewers

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// TestParsimony is the name of the default reviewer that judges the tests a change adds. Its review prompt carries the
// Test cost section (TestCost).
const TestParsimony = "test_parsimony"

// TestCostHeading is the line the Test cost section opens with.
const TestCostHeading = "Test cost (the budget lines of the gate output kept for this change):"

// NoBudgetLines is the line the Test cost section holds when the gate output it reads has no budget line.
const NoBudgetLines = "no budget lines"

// roundHalf is added to seconds before truncation to round them to the nearest whole second, as scripts/test-budget.sh
// does.
const roundHalf = 0.5

// overShare and overFloor are the allowance a change may exceed its base by before it is over: the larger of
// overShare of the base and overFloor seconds.
const (
	overShare = 0.15
	overFloor = 5.0
)

// costLine matches a cost line, which scripts/test-budget.sh prints once per package: "test-budget: cost <package>
// <seconds>", the seconds a decimal such as 190.4.
var costLine = regexp.MustCompile(`^test-budget: cost (\S+) ([0-9]+(?:\.[0-9]+)?)$`)

// baselineLine matches a gate's per-package line of the test budget: "test-budget: <package> <n> cpu-s", followed by
// its comparison with the baseline or by "no baseline".
var baselineLine = regexp.MustCompile(`^test-budget: \S+ [0-9]+ cpu-s`)

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

// costLines reads the cost lines of outputs: the packages they name, in order of first appearance, and each package's
// seconds, the last line winning for a package named twice.
func costLines(outputs ...string) ([]string, map[string]float64) {
	var pkgs []string
	costs := map[string]float64{}
	for _, output := range outputs {
		for line := range strings.Lines(output) {
			match := costLine.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
			if match == nil {
				continue
			}
			seconds, err := strconv.ParseFloat(match[2], 64)
			if err != nil {
				// A value too large for a float64 is not a cost line.
				continue
			}
			if _, seen := costs[match[1]]; !seen {
				pkgs = append(pkgs, match[1])
			}
			costs[match[1]] = seconds
		}
	}
	return pkgs, costs
}

// Costs returns each package's CPU seconds from the cost lines of outputs, the last line winning for a package named
// twice. It is empty when outputs hold no cost line.
func Costs(outputs ...string) map[string]float64 {
	_, costs := costLines(outputs...)
	return costs
}

// whole rounds seconds to the nearest whole second as scripts/test-budget.sh does: add roundHalf and truncate.
func whole(seconds float64) int {
	return int(seconds + roundHalf)
}

// costBullet is the Test cost bullet of a package with a cost line: its change and, when base has the package, its
// base and the delta between them in whole seconds, with ", over" when the change exceeds the base by more than the
// larger of 15% of the base and 5 seconds. A package base lacks has no base measurement to compare with.
func costBullet(pkg string, change float64, base map[string]float64) string {
	baseSeconds, inBase := base[pkg]
	if !inBase {
		return fmt.Sprintf("- %s: %d cpu-s, no base measurement", pkg, whole(change))
	}
	line := fmt.Sprintf("- %s: %d cpu-s, base %d cpu-s, %+d",
		pkg, whole(change), whole(baseSeconds), whole(change)-whole(baseSeconds))
	if change-baseSeconds > math.Max(overShare*baseSeconds, overFloor) {
		line += ", over"
	}
	return line
}

// TestCost returns the Test cost section of a review prompt: TestCostHeading, then each budget line of outputs as a
// bullet, in the order outputs give them, or the single bullet NoBudgetLines when they have none. outputs are the kept
// stdout and stderr tails of the gate runs the change passed, so a budget line printed before the kept tail does not
// appear. Cost lines (see Costs) are never bullets of their own. When base is not empty and outputs hold cost lines,
// the section first gives one bullet per package with a cost line, comparing it with base (costBullet), and leaves out
// the gate's per-package baseline lines, which those bullets replace.
func TestCost(base map[string]float64, outputs ...string) []string {
	pkgs, costs := costLines(outputs...)
	compared := len(base) > 0 && len(pkgs) > 0
	var found []string
	if compared {
		for _, pkg := range pkgs {
			found = append(found, costBullet(pkg, costs[pkg], base))
		}
	}
	for _, output := range outputs {
		for line := range strings.Lines(output) {
			line = strings.TrimRight(line, "\r\n")
			if !budgetLine(line) || costLine.MatchString(line) || (compared && baselineLine.MatchString(line)) {
				continue
			}
			found = append(found, "- "+line)
		}
	}
	if len(found) == 0 {
		found = []string{"- " + NoBudgetLines}
	}
	return append([]string{TestCostHeading}, found...)
}
