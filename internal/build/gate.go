package build

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// ResolveGate returns the gate named name for epic the way the plan check resolves it: from the epic's gates, else from
// the repository's project file. A name neither defines is an error that names the gate and wraps gates.ErrUnknown.
func ResolveGate(
	ctx context.Context, epic store.EpicRow, name string, getenv func(string) string,
) (gates.Gate, error) {
	defined, err := planner.DefineEpicGates(epic.Gates)
	if err != nil {
		return gates.Gate{}, fmt.Errorf("resolve gate %s: %w", name, err)
	}
	resolved, err := gates.ResolveDefined(ctx, name, defined, epic.Repository, getenv)
	if err != nil {
		return gates.Gate{}, fmt.Errorf("resolve gate %s: %w", name, err)
	}
	return resolved.Gate, nil
}
