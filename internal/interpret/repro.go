package interpret

import (
	"context"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/store"
)

// repro starts the reproduction each test-item Requirement of an integrate run's action names. A reproduction is the
// epic's base with the test files of the Requirement's test item taken from the run's candidate, pinned under the repro
// ref of its work. The item's capture runs at that overlay as a check under the work id, so its exit is what red is
// compared with.
func (r *Runner) repro(ctx context.Context, epic store.EpicRow, act Action) error {
	if r.checks == nil {
		return missingService("checks")
	}
	for i, id := range act.IDs {
		requirement, err := requirementOf(epic, act.Items[i])
		if err != nil {
			return err
		}
		ref := fmt.Sprintf("refs/orrery/%s/repro/%s", build.Slug(epic.ID), id)
		overlay, err := compose.Overlay(ctx, epic.Repository, epic.Base, act.Run.Revision, requirement.Item.Tests, ref)
		if err != nil {
			return fmt.Errorf("make reproduction of %s: %w", act.Items[i], err)
		}
		check := store.NodeCheck{
			ID: id, Epic: epic.ID, Path: act.Path, Revision: overlay, Argv: requirement.Item.Capture,
			Limit: requirement.Item.Limit,
		}
		if _, err = r.checks.Run(ctx, check); err != nil {
			return fmt.Errorf("run reproduction of %s: %w", act.Items[i], err)
		}
	}
	return nil
}

// requirementOf returns the Requirement of epic named id. A Requirement the epic does not hold is an error.
func requirementOf(epic store.EpicRow, id string) (store.EpicRequirement, error) {
	i := slices.IndexFunc(epic.Requirements, func(requirement store.EpicRequirement) bool {
		return requirement.ID == id
	})
	if i < 0 {
		return store.EpicRequirement{}, fmt.Errorf("requirement %s of epic %s is not stored", id, epic.ID)
	}
	return epic.Requirements[i], nil
}
