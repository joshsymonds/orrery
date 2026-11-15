package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMain points git at no global or system config for the whole package, so the owner's git config never applies
// to the repositories the tests make or to the git the code under test runs. It is set here once rather than with
// t.Setenv in each test, because t.Setenv keeps a test from running in parallel.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

// buildRepo makes a throwaway repository under t.TempDir() with one commit on main.
func buildRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	buildRun(t, dir, "init", "-q", "-b", "main")
	buildRun(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	return dir
}

// buildRun runs git in dir with the test identity and returns its trimmed output. It fails the test on error.
func buildRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildHead returns the commit HEAD names in dir.
func buildHead(t *testing.T, dir string) string {
	t.Helper()
	return buildRun(t, dir, "rev-parse", "HEAD")
}

// buildCommit makes an empty commit on the checked-out branch and returns its id.
func buildCommit(t *testing.T, dir string) string {
	t.Helper()
	buildRun(t, dir, "commit", "-q", "--allow-empty", "-m", "change")
	return buildHead(t, dir)
}

// buildObject writes a commit on parent's tree without moving any ref, and returns its id.
func buildObject(t *testing.T, dir, parent string) string {
	t.Helper()
	return buildRun(t, dir, "commit-tree", "HEAD^{tree}", "-p", parent, "-m", "object")
}

// buildSnapshot runs Snapshot on repo and fails the test on error.
func buildSnapshot(t *testing.T, repo string, exclude []string) string {
	t.Helper()
	snap, err := Snapshot(context.Background(), repo, exclude)
	if err != nil {
		t.Fatalf("Snapshot %s: %v", repo, err)
	}
	return snap
}

// buildCompare runs Compare on two snapshots and fails the test on error.
func buildCompare(t *testing.T, before, after string) []RefMove {
	t.Helper()
	moves, err := Compare(before, after)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return moves
}

// buildMoves snapshots repo, runs change, snapshots again, and returns the moves between the two snapshots.
func buildMoves(t *testing.T, repo string, exclude []string, change func()) []RefMove {
	t.Helper()
	before := buildSnapshot(t, repo, exclude)
	change()
	return buildCompare(t, before, buildSnapshot(t, repo, exclude))
}

// assertMoves fails the test unless got holds exactly want, in order.
func assertMoves(t *testing.T, got, want []RefMove) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("moves\n got %+v\nwant %+v", got, want)
	}
}

func TestCompareUnchangedRepositoryHasNoMoves(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	assertMoves(t, buildMoves(t, dir, nil, func() {}), nil)
}

func TestCompareBranchCreatedMovedDeleted(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	tip := buildObject(t, dir, base)

	created := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "update-ref", "refs/heads/feature", base)
	})
	assertMoves(t, created, []RefMove{{Ref: "refs/heads/feature", From: "", To: base}})

	moved := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "update-ref", "refs/heads/feature", tip)
	})
	assertMoves(t, moved, []RefMove{{Ref: "refs/heads/feature", From: base, To: tip}})

	deleted := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "update-ref", "-d", "refs/heads/feature")
	})
	assertMoves(t, deleted, []RefMove{{Ref: "refs/heads/feature", From: tip, To: ""}})
}

func TestCompareMainMovedWhileCheckedOutElsewhereParks(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	buildRun(t, dir, "checkout", "-q", "-b", "side")
	tip := buildCommit(t, dir)

	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "update-ref", "refs/heads/main", tip)
	})
	assertMoves(t, moves, []RefMove{{Ref: "refs/heads/main", From: base, To: tip, Parks: true}})
}

func TestComparePrimaryCheckoutBranchMovedParksWithHEAD(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	buildRun(t, dir, "checkout", "-q", "-b", "work")

	var tip string
	moves := buildMoves(t, dir, nil, func() {
		tip = buildCommit(t, dir)
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "refs/heads/work", From: base, To: tip, Parks: true},
		{Ref: "HEAD", From: "refs/heads/work " + base, To: "refs/heads/work " + tip, Parks: true},
	})
}

func TestCompareBranchCheckedOutBeforeMovedAfterCheckoutParks(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	buildRun(t, dir, "checkout", "-q", "-b", "work")
	tip := buildObject(t, dir, base)

	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "checkout", "-q", "main")
		buildRun(t, dir, "update-ref", "refs/heads/work", tip)
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "refs/heads/work", From: base, To: tip, Parks: true},
		{Ref: "HEAD", From: "refs/heads/work " + base, To: "refs/heads/main " + base, Parks: true},
	})
}

func TestCompareCheckoutOtherBranchParksHEAD(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	buildRun(t, dir, "branch", "side")

	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "checkout", "-q", "side")
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "HEAD", From: "refs/heads/main " + base, To: "refs/heads/side " + base, Parks: true},
	})
}

func TestCompareDetachedHEADMoveHasEmptyBranch(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)

	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "checkout", "-q", "--detach")
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "HEAD", From: "refs/heads/main " + base, To: " " + base, Parks: true},
	})
}

func TestCompareSkipsOrreryAndExcludedRefs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ref     string
		exclude []string
	}{
		{name: "refs/orrery", ref: "refs/orrery/state"},
		{name: "excluded", ref: "refs/heads/epic/x", exclude: []string{"refs/heads/epic/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := buildRepo(t)
			base := buildHead(t, dir)
			tip := buildObject(t, dir, base)
			buildRun(t, dir, "update-ref", tc.ref, base)

			// The kept branch is created in the same step, so a move that is wrongly reported would show beside it.
			moves := buildMoves(t, dir, tc.exclude, func() {
				buildRun(t, dir, "update-ref", tc.ref, tip)
				buildRun(t, dir, "update-ref", "refs/heads/kept", base)
			})
			assertMoves(t, moves, []RefMove{{Ref: "refs/heads/kept", From: "", To: base}})
		})
	}
}

func TestCompareRemoteTrackingMoveDoesNotPark(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	tip := buildObject(t, dir, base)
	buildRun(t, dir, "update-ref", "refs/remotes/origin/main", base)
	buildRun(t, dir, "update-ref", "refs/remotes/origin/feature", base)

	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "update-ref", "refs/remotes/origin/main", tip)
		buildRun(t, dir, "update-ref", "refs/remotes/origin/feature", tip)
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "refs/remotes/origin/feature", From: base, To: tip},
		{Ref: "refs/remotes/origin/main", From: base, To: tip},
	})
}

func TestCompareMovesInRefOrderWithHEADLast(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)

	var tip string
	moves := buildMoves(t, dir, nil, func() {
		buildRun(t, dir, "branch", "zeta")
		buildRun(t, dir, "branch", "alpha")
		tip = buildCommit(t, dir)
	})
	assertMoves(t, moves, []RefMove{
		{Ref: "refs/heads/alpha", From: "", To: base},
		{Ref: "refs/heads/main", From: base, To: tip, Parks: true},
		{Ref: "refs/heads/zeta", From: "", To: base},
		{Ref: "HEAD", From: "refs/heads/main " + base, To: "refs/heads/main " + tip, Parks: true},
	})
}

func TestSnapshotFromLinkedWorktreeReadsPrimaryHEAD(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	buildRun(t, dir, "branch", "side")
	linked := filepath.Join(t.TempDir(), "linked")
	buildRun(t, dir, "worktree", "add", "-q", "-b", "linked", linked)

	// The linked worktree's own HEAD never changes, so only a read of the primary checkout's HEAD sees this move.
	before := buildSnapshot(t, linked, nil)
	buildRun(t, dir, "checkout", "-q", "side")
	after := buildSnapshot(t, linked, nil)
	assertMoves(t, buildCompare(t, before, after), []RefMove{
		{Ref: "HEAD", From: "refs/heads/main " + base, To: "refs/heads/side " + base, Parks: true},
	})
}

func TestCompareRejectsMalformedSnapshot(t *testing.T) {
	t.Parallel()
	if _, err := Compare("not json", "{}"); err == nil {
		t.Fatal("Compare accepted a malformed before snapshot")
	}
}

func TestBranchRefNamesEpicBranch(t *testing.T) {
	t.Parallel()
	const epic = "orrery-16ce974/orrery-o3d3-task-build"
	if got := Slug(epic); got != "orrery-o3d3-task-build" {
		t.Errorf("Slug(%q) = %q, want %q", epic, got, "orrery-o3d3-task-build")
	}
	if got := BranchRef(epic); got != "refs/heads/epic/orrery-o3d3-task-build" {
		t.Errorf("BranchRef(%q) = %q, want %q", epic, got, "refs/heads/epic/orrery-o3d3-task-build")
	}
}
