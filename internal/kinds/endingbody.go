package kinds

import (
	"fmt"
	"strings"
)

// EndingBody returns the body of the tell that announces an epic's ending in phase. The label of the phase is Released
// for released, Done for done, Ended for ended_with_gaps, and Ended followed by the phase for any other. With no gaps the
// body is the label and "No gaps.", as in "Done. No gaps.". With n gaps it is the label and the count, "with 1 gap:" or
// "with n gaps:", then one "- gap" line per gap in the order given, joined by newlines.
func EndingBody(phase string, gaps []string) string {
	var label string
	switch phase {
	case "released":
		label = "Released"
	case "done":
		label = "Done"
	case "ended_with_gaps":
		label = "Ended"
	default:
		label = "Ended " + phase
	}
	if len(gaps) == 0 {
		return label + ". No gaps."
	}
	noun := "gaps"
	if len(gaps) == 1 {
		noun = "gap"
	}
	lines := make([]string, 0, len(gaps)+1)
	lines = append(lines, fmt.Sprintf("%s with %d %s:", label, len(gaps), noun))
	for _, gap := range gaps {
		lines = append(lines, "- "+gap)
	}
	return strings.Join(lines, "\n")
}
