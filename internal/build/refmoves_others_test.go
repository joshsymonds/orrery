package build

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// looseCommit writes a commit on parent with message and returns its id, without moving any ref. Each test gives its
// commits distinct messages, since two commits on one parent with the same message in the same second are one commit.
func looseCommit(t *testing.T, dir, parent, message string) string {
	t.Helper()
	return buildRun(t, dir, "commit-tree", "HEAD^{tree}", "-p", parent, "-m", message)
}

// startImplementerStep returns a fixture whose task's implementer step has ended done with its worktree at commit, the
// one commit the step makes on the base, and commit. The primary checkout is on branch, checked out before the
// implementer's refs are snapshotted, so the switch is not a move of the step's and the move a test makes after the
// snapshot is the only ref change the step's check sees.
func startImplementerStep(t *testing.T, branch string) (*passFixture, string) {
	t.Helper()
	f := newPassFixture(t)
	if branch == "main" {
		buildRun(t, f.repo, "checkout", "-q", "main")
	} else {
		buildRun(t, f.repo, "checkout", "-q", "-b", branch, f.base)
	}
	commit := looseCommit(t, f.repo, f.base, "step")
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	worktree := ImplementWorktree(f.stateDir, passEpic, store.EpicTask{Seq: 1})
	buildRun(t, worktree, "checkout", "-q", "--detach", commit)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	return f, commit
}

// expectParkedNoRun fails the test unless the epic is parked with reason and only the red run has started, so the
// parked epic starts no green run.
func expectParkedNoRun(f *passFixture, reason string) {
	f.t.Helper()
	if got := f.epic().Parked; got != reason {
		f.t.Errorf("epic Parked = %q, want %q", got, reason)
	}
	if got := len(f.evidence.runs); got != 1 {
		f.t.Errorf("RunTask calls = %d, want only the red run while parked", got)
	}
}

// TestRefMovesOthersLeaveTheImplementerUnparkedAndUntold covers the rule that other worktrees moving never affect an
// epic: whatever another session does to the repository's refs during an implementer step, a commit in another
// worktree, a checkout or a commit in the primary checkout, a push of main or a fast-forward of main, moves no commit the
// implementer made, so the epic is not parked, nothing is told, and the build goes on to green.
func TestRefMovesOthersLeaveTheImplementerUnparkedAndUntold(t *testing.T) {
	t.Parallel()
	moves := []struct {
		name string
		move func(t *testing.T, f *passFixture)
	}{
		{name: "commit in another worktree", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			other := filepath.Join(t.TempDir(), "other")
			buildRun(t, f.repo, "worktree", "add", "-q", "-b", "feature", other, f.base)
			buildRun(t, other, "commit", "-q", "--allow-empty", "-m", "another session's work")
		}},
		{name: "checkout of another branch in the primary checkout", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			buildRun(t, f.repo, "update-ref", "refs/heads/side", looseCommit(t, f.repo, f.base, "side"))
			buildRun(t, f.repo, "checkout", "-q", "side")
		}},
		{name: "detach in the primary checkout", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			buildRun(t, f.repo, "checkout", "-q", "--detach", looseCommit(t, f.repo, f.base, "detached"))
		}},
		{name: "commit on main in the primary checkout", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			buildRun(t, f.repo, "commit", "-q", "--allow-empty", "-m", "another session moves main")
		}},
		{name: "fast-forward of main", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			buildRun(t, f.repo, "merge", "--ff-only", "-q", looseCommit(t, f.repo, f.base, "origin's main"))
		}},
		{name: "push of main", move: func(t *testing.T, f *passFixture) {
			t.Helper()
			buildRun(t, f.repo, "config", "receive.denyCurrentBranch", "updateInstead")
			clone := filepath.Join(t.TempDir(), "clone")
			buildRun(t, f.repo, "clone", "-q", "--branch", "main", f.repo, clone)
			buildRun(t, clone, "commit", "-q", "--allow-empty", "-m", "pushed from a clone")
			buildRun(t, clone, "push", "-q", "origin", "HEAD:refs/heads/main")
		}},
	}
	for _, tc := range moves {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, commit := startImplementerStep(t, "main")
			tc.move(t, f)
			f.pass()
			if got := f.epic().Parked; got != "" {
				t.Errorf("epic Parked = %q, want the epic running", got)
			}
			for _, tell := range f.tells() {
				if strings.Contains(tell, "implementer moved") {
					t.Errorf("tells = %v, want no move told", f.tells())
				}
			}
			f.expectGreenAt(commit)
		})
	}
}

// TestRefMovesImplementerCommitOutsideItsWorktreeParks covers the protection the rule keeps: an implementer whose commit
// lands on main, on the primary checkout's branch or in the primary checkout's HEAD wrote outside its worktree, so the
// epic parks with the move as its reason.
func TestRefMovesImplementerCommitOutsideItsWorktreeParks(t *testing.T) {
	t.Parallel()
	t.Run("on main", func(t *testing.T) {
		t.Parallel()
		f, commit := startImplementerStep(t, "work")
		buildRun(t, f.repo, "update-ref", "refs/heads/main", commit)
		f.pass()
		expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, commit))
	})
	t.Run("on main under a later commit", func(t *testing.T) {
		t.Parallel()
		f, commit := startImplementerStep(t, "main")
		over := looseCommit(t, f.repo, commit, "over step")
		buildRun(t, f.repo, "merge", "--ff-only", "-q", over)
		f.pass()
		expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, over))
	})
	t.Run("on the primary checkout's branch", func(t *testing.T) {
		t.Parallel()
		f, commit := startImplementerStep(t, "work")
		buildRun(t, f.repo, "merge", "--ff-only", "-q", commit)
		f.pass()
		expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/work (%s to %s)", f.base, commit))
	})
	t.Run("in the primary checkout's HEAD", func(t *testing.T) {
		t.Parallel()
		f, commit := startImplementerStep(t, "main")
		buildRun(t, f.repo, "checkout", "-q", "--detach", commit)
		f.pass()
		expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved HEAD (refs/heads/main %s to  %s)", f.base, commit))
	})
}
