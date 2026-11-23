package planner

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
)

// TestEpicBaseFirstTaskPlannerStepStartsAtMovedBase covers R2: the planner step of the turn that moves an epic's base to
// origin's main starts at that commit. The checkout's HEAD is a local commit that origin lacks, so the step's revision
// can only be origin's main, which is the commit the base moved to.
func TestEpicBaseFirstTaskPlannerStepStartsAtMovedBase(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	featureCommit(t, repo.checkout, "local.txt", "local\n", "local commit origin lacks")
	ahead := advanceOrigin(t, repo)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)

	started := runBaseStartTurn(t, s, enginehost.FetchOriginMain)

	requireStartsAt(t, started, ahead)
}
