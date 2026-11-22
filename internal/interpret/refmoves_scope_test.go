package interpret

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestRefMovesScopeCheckNodeIgnoresOtherWorktreesMoves covers the rule that other worktrees moving never affect an epic:
// a check node makes no commits, so whatever another session does to the repository's refs while it runs, in another
// worktree, in the primary checkout or by pushing main, the node passes with the epic running and nothing is told.
func TestRefMovesScopeCheckNodeIgnoresOtherWorktreesMoves(t *testing.T) {
	t.Parallel()
	moves := []struct {
		name string
		move func(t *testing.T, repo string)
	}{
		{name: "branch made", move: func(t *testing.T, repo string) {
			t.Helper()
			nodeGit(t, repo, "branch", "side")
		}},
		{name: "commit in another worktree", move: func(t *testing.T, repo string) {
			t.Helper()
			other := filepath.Join(t.TempDir(), "other")
			nodeGit(t, repo, "worktree", "add", "-q", "-b", "feature", other)
			nodeGit(t, other, "commit", "-q", "--allow-empty", "-m", "another session's work")
		}},
		{name: "checkout in the primary checkout", move: func(t *testing.T, repo string) {
			t.Helper()
			nodeGit(t, repo, "checkout", "-q", "-b", "elsewhere")
			nodeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "elsewhere")
		}},
		{name: "commit on main in the primary checkout", move: func(t *testing.T, repo string) {
			t.Helper()
			nodeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "another session moves main")
		}},
		{name: "push of main", move: func(t *testing.T, repo string) {
			t.Helper()
			nodeGit(t, repo, "config", "receive.denyCurrentBranch", "updateInstead")
			clone := filepath.Join(t.TempDir(), "clone")
			nodeGit(t, repo, "clone", "-q", repo, clone)
			nodeGit(t, clone, "commit", "-q", "--allow-empty", "-m", "pushed from a clone")
			nodeGit(t, clone, "push", "-q", "origin", "HEAD:refs/heads/main")
		}},
	}
	for _, tc := range moves {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repo, base := nodeRepo(t)
			s, dbPath := openNodeStore(t)
			createNodeEpic(t, s, repo, base, nodeCommandFlow)
			nodeEpicBranch(t, repo, "b.go")
			runner := runNodes(s, newNodeFakes(s))
			runner.Pass(ctx)
			check := workID(t, s, "tests", "check")

			tc.move(t, repo)
			nodeEndCheck(t, s, check, 0, "")
			runner.Pass(ctx)

			if run := runAt(t, s, "tests"); run.State != store.NodeEnded || run.Outcome != "passed" {
				t.Errorf("node tests = %+v, want ended passed", run)
			}
			if got := epicOf(t, s).Parked; got != "" {
				t.Errorf("epic Parked = %q, want not parked", got)
			}
			if got := nodeTells(t, dbPath); len(got) != 0 {
				t.Errorf("tells = %q, want none", got)
			}
		})
	}
}
