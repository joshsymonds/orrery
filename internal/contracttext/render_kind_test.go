package contracttext

import (
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestContractTextRendersAReleaseStepKind checks that a release step that names a kind says in its row that it runs as
// code, and that a step with no kind renders as it always did.
func TestContractTextRendersAReleaseStepKind(t *testing.T) {
	t.Parallel()
	contract := sampleContract()
	contract.ReleaseSteps = append(contract.GetReleaseSteps(), &v1.EpicReleaseStep{
		Step: "Push the tag", Target: "origin's tags", Undo: "delete the tag", Kind: "tag",
		Params: &v1.EpicReleaseParams{Prefix: "v"},
	})
	got := Render(contract, sampleDecisions())
	for _, want := range []string{"| 1 Fast-forward main |", "| 2 Push the tag (runs as code: tag) |"} {
		if !strings.Contains(got, want) {
			t.Errorf("Render = %q, want a release row holding %q", got, want)
		}
	}
}
