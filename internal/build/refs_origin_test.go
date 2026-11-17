package build

import (
	"fmt"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// originHolds is what origin's main names when a test starts an implementer step.
type originHolds int

const (
	// originNoMain is origin with no main ref.
	originNoMain originHolds = iota
	// originBesideStep is a commit on the step's base that holds none of the step's commits.
	originBesideStep
	// originOverStep is a commit on the step's commit, so it holds the step's commit.
	originOverStep
)

// originObject writes a commit on parent with message and returns its id, without moving any ref. Each test gives its
// commits distinct messages, since two commits on one parent with the same message in the same second are one commit.
func originObject(t *testing.T, dir, parent, message string) string {
	t.Helper()
	return buildRun(t, dir, "commit-tree", "HEAD^{tree}", "-p", parent, "-m", message)
}

// startOriginStep returns a fixture whose task's implementer step is running with its worktree at commit, the one commit
// the step makes on the base. origin's main is set as holds says before the implementer's refs are snapshotted, so the
// move a test makes after the snapshot is the only ref change the step sees. The primary checkout is on main, as the
// rule judges a move of main or of HEAD, and it is checked out before the snapshot, so the switch is not a step move.
// It also returns origin's main, which is "" when holds is originNoMain.
func startOriginStep(t *testing.T, holds originHolds) (*passFixture, string, string) {
	t.Helper()
	f := newPassFixture(t)
	buildRun(t, f.repo, "checkout", "-q", "main")
	commit := originObject(t, f.repo, f.base, "step")
	origin := ""
	switch holds {
	case originNoMain:
	case originBesideStep:
		origin = originObject(t, f.repo, f.base, "origin")
	case originOverStep:
		origin = originObject(t, f.repo, commit, "origin")
	}
	if origin != "" {
		buildRun(t, f.repo, "update-ref", originMainRef, origin)
	}
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	worktree := ImplementWorktree(f.stateDir, passEpic, 1)
	buildRun(t, worktree, "checkout", "-q", "--detach", commit)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	return f, commit, origin
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

// TestOriginFastForwardMainAndHEADTellWithoutParking covers R3: a fast-forward of main to an origin commit that holds
// none of the implementer's commits is told for main and for HEAD, and the build goes on to green.
func TestOriginFastForwardMainAndHEADTellWithoutParking(t *testing.T) {
	t.Parallel()
	f, commit, origin := startOriginStep(t, originBesideStep)
	buildRun(t, f.repo, "merge", "--ff-only", "-q", origin)
	f.pass()
	if got := f.epic().Parked; got != "" {
		t.Errorf("epic Parked = %q, want the epic running", got)
	}
	for _, want := range []string{
		fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, origin),
		fmt.Sprintf("T1 implementer moved HEAD (refs/heads/main %s to refs/heads/main %s)", f.base, origin),
	} {
		if !slices.Contains(f.tells(), want) {
			t.Errorf("tells = %v, want %q", f.tells(), want)
		}
	}
	f.expectGreenAt(commit)
}

// TestOriginFastForwardMainToCommitContainingStepParks covers R3: a move of main to an origin commit that contains the
// implementer's commit parks the epic with the move as its reason.
func TestOriginFastForwardMainToCommitContainingStepParks(t *testing.T) {
	t.Parallel()
	f, _, origin := startOriginStep(t, originOverStep)
	buildRun(t, f.repo, "merge", "--ff-only", "-q", origin)
	f.pass()
	expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, origin))
}

// TestOriginFastForwardMainToCommitOffOriginParks covers R3: a move of main to a commit that origin's main does not
// reach parks the epic with the move as its reason.
func TestOriginFastForwardMainToCommitOffOriginParks(t *testing.T) {
	t.Parallel()
	f, _, _ := startOriginStep(t, originBesideStep)
	off := originObject(t, f.repo, f.base, "off origin")
	buildRun(t, f.repo, "merge", "--ff-only", "-q", off)
	f.pass()
	expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, off))
}

// TestOriginFastForwardHEADBranchSwitchParks covers R3: a switch of the primary checkout to another branch parks, even
// when that branch names a commit origin's main reaches that holds none of the implementer's commits.
func TestOriginFastForwardHEADBranchSwitchParks(t *testing.T) {
	t.Parallel()
	f, _, origin := startOriginStep(t, originBesideStep)
	buildRun(t, f.repo, "update-ref", "refs/heads/side", origin)
	buildRun(t, f.repo, "checkout", "-q", "side")
	f.pass()
	expectParkedNoRun(f, fmt.Sprintf(
		"T1 implementer moved HEAD (refs/heads/main %s to refs/heads/side %s)", f.base, origin))
}

// TestOriginFastForwardWithoutOriginMainParks covers R3: with no origin main to judge a move against, a move of main
// parks even to the commit origin's main would otherwise have reached.
func TestOriginFastForwardWithoutOriginMainParks(t *testing.T) {
	t.Parallel()
	f, _, _ := startOriginStep(t, originNoMain)
	moved := originObject(t, f.repo, f.base, "origin")
	buildRun(t, f.repo, "merge", "--ff-only", "-q", moved)
	f.pass()
	expectParkedNoRun(f, fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved))
}
