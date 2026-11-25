package planner

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestReleaseStepItemsNameTheKind checks that the planner's release step lines name the kind a step runs as when it
// names one, after its target and undo, and add nothing for a step with no kind.
func TestReleaseStepItemsNameTheKind(t *testing.T) {
	t.Parallel()
	got := releaseStepItems([]store.EpicReleaseStep{
		{Step: "push main", Target: "origin", Undo: "push a revert", Kind: "push-main"},
		{Step: "install", Target: "the phone", Undo: "reinstall"},
	})
	want := []string{
		"- push main", "  target: origin", "  undo: push a revert", "  runs as code: push-main",
		"- install", "  target: the phone", "  undo: reinstall",
	}
	if !slices.Equal(got, want) {
		t.Errorf("releaseStepItems = %q, want %q", got, want)
	}
}
