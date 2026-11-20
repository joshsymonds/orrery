package interpret

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestRefMovesScopeCheckNodeTellsNothingOfAnotherSessionsBranch covers E2-R2: a check node makes no commits, so a branch
// another session moves during its run is not told, and its node passes with the epic running.
func TestRefMovesScopeCheckNodeTellsNothingOfAnotherSessionsBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s, dbPath := openNodeStore(t)
	createNodeEpic(t, s, repo, base, nodeCommandFlow)
	nodeEpicBranch(t, repo, "b.go")
	runner := runNodes(s, newNodeFakes(s))
	runner.Pass(ctx)
	check := workID(t, s, "tests", "check")

	nodeGit(t, repo, "branch", "side")
	nodeEndCheck(t, s, check, 0, "")
	runner.Pass(ctx)

	if run := runAt(t, s, "tests"); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("node tests = %+v, want ended passed", run)
	}
	if got := epicOf(t, s).Parked; got != "" {
		t.Errorf("epic Parked = %q, want not parked", got)
	}
	if got := nodeTells(t, dbPath); len(got) != 0 {
		t.Errorf("tells = %q, want none of the move of refs/heads/side", got)
	}
}
