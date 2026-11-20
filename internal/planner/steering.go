package planner

import (
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// SteeringLines returns the lines a steering session's first prompt carries for row: the lines of its contract, the line
// Log:, then the lines of its decisions.
func SteeringLines(row store.EpicRow) ([]string, error) {
	lines, err := ContractLines(row)
	if err != nil {
		return nil, fmt.Errorf("contract lines of epic %s: %w", row.ID, err)
	}
	return append(append(lines, "Log:"), DecisionLines(row, 0)...), nil
}
