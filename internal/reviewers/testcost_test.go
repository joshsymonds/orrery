package reviewers

import (
	"maps"
	"slices"
	"testing"
)

// TestTestCostShowsChangeBaseAndDeltaBaseCost: with a base for the package, the Test cost section gives its cost line
// as change, base and delta, and not the gate's per-package baseline line.
func TestTestCostShowsChangeBaseAndDeltaBaseCost(t *testing.T) {
	t.Parallel()
	outputs := "test-budget: cost internal/store 195.0\n" +
		"test-budget: internal/store 195 cpu-s > 124 (+57%)\n"
	got := TestCost(map[string]float64{"internal/store": 190}, outputs)
	want := []string{TestCostHeading, "- internal/store: 195 cpu-s, base 190 cpu-s, +5"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCost = %q, want %q", got, want)
	}
}

// TestTestCostMarksOverBaseCost: a change more than the larger of 15% of its base and 5 seconds above the base is
// marked over.
func TestTestCostMarksOverBaseCost(t *testing.T) {
	t.Parallel()
	outputs := "test-budget: cost internal/store 260.0\n" +
		"test-budget: internal/store 260 cpu-s > 124 (+110%)\n"
	got := TestCost(map[string]float64{"internal/store": 190}, outputs)
	want := []string{TestCostHeading, "- internal/store: 260 cpu-s, base 190 cpu-s, +70, over"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCost = %q, want %q", got, want)
	}
}

// TestTestCostWithoutBaseBaseCost: with a nil base the Test cost section is the same as before cost lines were read,
// and it shows no cost line.
func TestTestCostWithoutBaseBaseCost(t *testing.T) {
	t.Parallel()
	outputs := "test-budget: cost internal/store 195.0\n" +
		"test-budget: internal/store 195 cpu-s > 124 (+57%)\n" +
		"test-budget: 1 packages checked, none over twice its baseline\n"
	got := TestCost(nil, outputs)
	want := []string{
		TestCostHeading,
		"- test-budget: internal/store 195 cpu-s > 124 (+57%)",
		"- test-budget: 1 packages checked, none over twice its baseline",
	}
	if !slices.Equal(got, want) {
		t.Errorf("TestCost = %q, want %q", got, want)
	}
}

// TestCostsReadsCostLinesBaseCost: Costs reads the cost lines of several outputs, the last line winning for a package
// named twice, and ignores the other lines, including a cost line whose seconds are not a decimal.
func TestCostsReadsCostLinesBaseCost(t *testing.T) {
	t.Parallel()
	got := Costs(
		"building...\n"+
			"test-budget: cost internal/store 195.0\n"+
			"test-budget: internal/store 195 cpu-s > 124 (+57%)\n",
		"test-budget: cost internal/reviewers 12.5\n"+
			"test-budget: cost internal/build abc\n"+
			"check-changed: OK after 40s\n",
		"test-budget: cost internal/store 200.4\n",
	)
	want := map[string]float64{"internal/store": 200.4, "internal/reviewers": 12.5}
	if !maps.Equal(got, want) {
		t.Errorf("Costs = %v, want %v", got, want)
	}
}
