package store

import (
	"context"
	"fmt"
)

// costPath is the path of the cost node, the top-level node whose checks run the epic's first Done gate at the stored
// base and print its test-budget cost lines.
const costPath = "cost"

// CostChecks returns the checks of the cost node among checks that ended, ran and did not exit EnvironmentExit, in
// their given order. Those are the base measurement a review compares a change's test cost with. It returns none when
// no check qualifies.
func CostChecks(checks []NodeCheck) []NodeCheck {
	var cost []NodeCheck
	for _, check := range checks {
		if check.Path == costPath && check.State == NodeEnded && check.NotRun == "" &&
			!EnvironmentExited(check.Exit, check.TimedOut) {
			cost = append(cost, check)
		}
	}
	return cost
}

// BaseCost returns CostChecks of the checks of epic, ordered by created, then id: the base measurement of the epic's
// test cost.
func (s *Store) BaseCost(ctx context.Context, epic string) ([]NodeCheck, error) {
	checks, err := s.NodeChecks(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("base cost of epic %s: %w", epic, err)
	}
	return CostChecks(checks), nil
}
