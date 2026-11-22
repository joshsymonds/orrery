package build

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// detachStep checks out the implementer's worktree of the test task at commit, as the implementer leaves it when it makes
// commit, so commit is one of the step's commits.
func detachStep(t *testing.T, f *passFixture, commit string) {
	t.Helper()
	buildRun(t, ImplementWorktree(f.stateDir, passEpic, store.EpicTask{Seq: 1}), "checkout", "-q", "--detach", commit)
}

// TestRefMovesScopeImplementerUnrelatedBranchTellsNothing covers E2-R2: a branch another session moves during an
// implementer step to a commit that holds none of the step's commits is not told to the step, and the build goes on to
// green.
func TestRefMovesScopeImplementerUnrelatedBranchTellsNothing(t *testing.T) {
	t.Parallel()
	f, commit := startImplementerStep(t, "main")
	unrelated := looseCommit(t, f.repo, f.base, "unrelated")
	buildRun(t, f.repo, "update-ref", "refs/heads/unrelated", unrelated)
	f.pass()
	want := fmt.Sprintf("T1 implementer moved refs/heads/unrelated ( to %s)", unrelated)
	if got := f.tells(); slices.Contains(got, want) {
		t.Errorf("tells = %v, want no tell of %q", got, want)
	}
	if got := f.epic().Parked; got != "" {
		t.Errorf("epic Parked = %q, want the epic running", got)
	}
	f.expectGreenAt(commit)
}

// TestRefMovesScopeImplementerBranchOntoStepCommitIsTold covers E2-R2: a branch moved during an implementer step to a
// commit that holds one of the step's commits is still told, and the build goes on to green.
func TestRefMovesScopeImplementerBranchOntoStepCommitIsTold(t *testing.T) {
	t.Parallel()
	f, commit := startImplementerStep(t, "main")
	over := looseCommit(t, f.repo, commit, "over step")
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", over)
	f.pass()
	want := fmt.Sprintf("T1 implementer moved refs/heads/feature ( to %s)", over)
	if got := f.tells(); !slices.Contains(got, want) {
		t.Errorf("tells = %v, want %q", got, want)
	}
	if got := f.epic().Parked; got != "" {
		t.Errorf("epic Parked = %q, want the epic running", got)
	}
	f.expectGreenAt(commit)
}

// TestRefMovesScopeMainParksAndUnrelatedBranchIsNotTold covers E2-R2: a move of main during an implementer step onto a
// commit that holds the step's commit parks the epic with the move as its reason, and a branch another session moves in
// the same step is not told.
func TestRefMovesScopeMainParksAndUnrelatedBranchIsNotTold(t *testing.T) {
	t.Parallel()
	f, commit := startImplementerStep(t, "main")
	unrelated := looseCommit(t, f.repo, f.base, "unrelated")
	buildRun(t, f.repo, "update-ref", "refs/heads/unrelated", unrelated)
	moved := looseCommit(t, f.repo, commit, "moved main")
	buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
	f.pass()
	reason := fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved)
	expectParkedNoRun(f, reason)
	want := fmt.Sprintf("T1 implementer moved refs/heads/unrelated ( to %s)", unrelated)
	if slices.Contains(f.tells(), want) {
		t.Errorf("tells = %v, want no tell of %q", f.tells(), want)
	}
}

// TestRefMovesScopeKeepsBlamableMoves covers E2-R2: ScopeMoves keeps a move only when it is the epic's own ref, or when
// its new target holds one of the step's commits, and drops every other move. A move of main, of the primary checkout's
// branch or of HEAD is judged by the same test, and one kept keeps its Parks.
func TestRefMovesScopeKeepsBlamableMoves(t *testing.T) {
	t.Parallel()
	repo := buildRepo(t)
	base := buildHead(t, repo)
	step := looseCommit(t, repo, base, "step")
	holding := looseCommit(t, repo, step, "holding")
	unrelated := looseCommit(t, repo, base, "unrelated")
	cases := []struct {
		name string
		move RefMove
		want bool
	}{
		{name: "unrelated branch", move: RefMove{Ref: "refs/heads/unrelated", To: unrelated}},
		{name: "branch to a step commit", move: RefMove{Ref: "refs/heads/feature", To: holding}, want: true},
		{name: "branch deleted", move: RefMove{Ref: "refs/heads/feature", From: step}},
		{name: "epic branch", move: RefMove{Ref: BranchRef(passEpic), From: base, To: unrelated}, want: true},
		{name: "epic refs", move: RefMove{Ref: "refs/orrery/epic/state", To: unrelated}, want: true},
		{name: "another epic's refs", move: RefMove{Ref: "refs/orrery/other/state", To: unrelated}},
		{name: "main to an unrelated commit", move: RefMove{Ref: mainRef, From: base, To: unrelated, Parks: true}},
		{name: "main to a step commit", move: RefMove{Ref: mainRef, From: base, To: holding, Parks: true}, want: true},
		{name: "main deleted", move: RefMove{Ref: mainRef, From: base, Parks: true}},
		{
			name: "primary branch to an unrelated commit",
			move: RefMove{Ref: "refs/heads/work", From: base, To: unrelated, Parks: true},
		},
		{
			name: "primary branch to a step commit",
			move: RefMove{Ref: "refs/heads/work", From: base, To: step, Parks: true}, want: true,
		},
		{
			name: "HEAD to an unrelated commit",
			move: RefMove{
				Ref:   headRef,
				From:  "refs/heads/work " + base,
				To:    "refs/heads/side " + unrelated,
				Parks: true,
			},
		},
		{
			name: "HEAD to a step commit",
			move: RefMove{Ref: headRef, From: "refs/heads/work " + base, To: " " + holding, Parks: true}, want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ScopeMoves(context.Background(), repo, passEpic, []RefMove{tc.move}, base, step)
			if err != nil {
				t.Fatalf("ScopeMoves: %v", err)
			}
			want := []RefMove(nil)
			if tc.want {
				want = []RefMove{tc.move}
			}
			assertMoves(t, got, want)
		})
	}
}

// TestRefMovesScopeCheckNodeKeepsOnlyTheEpicsOwnRefs covers the check node's case: with no base and head, no commit is
// the step's, so only the epic's own refs are kept, and a move of main or HEAD onto any commit is dropped.
func TestRefMovesScopeCheckNodeKeepsOnlyTheEpicsOwnRefs(t *testing.T) {
	t.Parallel()
	repo := buildRepo(t)
	base := buildHead(t, repo)
	moved := looseCommit(t, repo, base, "moved")
	own := RefMove{Ref: BranchRef(passEpic), From: base, To: moved}
	moves := []RefMove{
		{Ref: mainRef, From: base, To: moved, Parks: true},
		own,
		{Ref: headRef, From: "refs/heads/main " + base, To: "refs/heads/main " + moved, Parks: true},
	}
	got, err := ScopeMoves(context.Background(), repo, passEpic, moves, "", "")
	if err != nil {
		t.Fatalf("ScopeMoves: %v", err)
	}
	assertMoves(t, got, []RefMove{own})
}
