package build

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// TestRefMovesScopeImplementerUnrelatedBranchTellsNothing covers E2-R2: a branch another session moves during an
// implementer step to a commit that holds none of the step's commits is not told to the step, and the build goes on to
// green.
func TestRefMovesScopeImplementerUnrelatedBranchTellsNothing(t *testing.T) {
	t.Parallel()
	f, commit, _ := startOriginStep(t, originNoMain)
	unrelated := originObject(t, f.repo, f.base, "unrelated")
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
	f, commit, _ := startOriginStep(t, originNoMain)
	over := originObject(t, f.repo, commit, "over step")
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

// TestRefMovesScopeMainParksAndUnrelatedBranchIsNotTold covers E2-R2: a move of main during an implementer step parks
// the epic with the move as its reason, and a branch another session moves in the same step is not told.
func TestRefMovesScopeMainParksAndUnrelatedBranchIsNotTold(t *testing.T) {
	t.Parallel()
	f, _, _ := startOriginStep(t, originNoMain)
	unrelated := originObject(t, f.repo, f.base, "unrelated")
	buildRun(t, f.repo, "update-ref", "refs/heads/unrelated", unrelated)
	moved := originObject(t, f.repo, f.base, "moved main")
	buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
	f.pass()
	reason := fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved)
	expectParkedNoRun(f, reason)
	want := fmt.Sprintf("T1 implementer moved refs/heads/unrelated ( to %s)", unrelated)
	if slices.Contains(f.tells(), want) {
		t.Errorf("tells = %v, want no tell of %q", f.tells(), want)
	}
}

// TestRefMovesScopeKeepsBlamableMoves covers E2-R2: ScopeMoves keeps a move that does not park only when it is the
// epic's own ref, or when its new target holds one of the steps' commits, and drops every other move.
func TestRefMovesScopeKeepsBlamableMoves(t *testing.T) {
	t.Parallel()
	repo := buildRepo(t)
	base := buildHead(t, repo)
	step := originObject(t, repo, base, "step")
	holding := originObject(t, repo, step, "holding")
	unrelated := originObject(t, repo, base, "unrelated")
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ScopeMoves(context.Background(), repo, passEpic, []RefMove{tc.move}, base, step)
			if err != nil {
				t.Fatalf("ScopeMoves: %v", err)
			}
			if kept := len(got) == 1; kept != tc.want {
				t.Errorf("ScopeMoves kept = %v, want %v for %+v", kept, tc.want, tc.move)
			}
		})
	}
}

// TestRefMovesScopeKeepsParkingMoves covers E2-R2: ScopeMoves keeps a move that parks, of main, of the step's branch or
// of HEAD, unchanged, whichever commit it moves to.
func TestRefMovesScopeKeepsParkingMoves(t *testing.T) {
	t.Parallel()
	repo := buildRepo(t)
	base := buildHead(t, repo)
	step := originObject(t, repo, base, "step")
	unrelated := originObject(t, repo, base, "unrelated")
	moves := []RefMove{
		{Ref: mainRef, From: base, To: unrelated, Parks: true},
		{Ref: "refs/heads/work", From: base, To: unrelated, Parks: true},
		{Ref: headRef, From: "refs/heads/work " + base, To: "refs/heads/work " + unrelated, Parks: true},
	}
	got, err := ScopeMoves(context.Background(), repo, passEpic, moves, base, step)
	if err != nil {
		t.Fatalf("ScopeMoves: %v", err)
	}
	assertMoves(t, got, moves)
}
