package build

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestDecideClosureEvidenceKind pins that a closure-kind task runs no red and no green: it starts at implement as
// attempt 1, and its done implementer step goes from its gate to review.
func TestDecideClosureEvidenceKind(t *testing.T) {
	t.Parallel()
	closure := evidence.Item{Kind: evidence.KindClosure, Conclusion: "the round's closure holds"}
	t.Run("a closure-kind task in a correction round runs no red and no green", func(t *testing.T) {
		t.Parallel()
		fresh := View{Epic: decideEpic, Branch: decideBranch, Task: store.EpicTask{Seq: 3, Evidence: closure}}
		wantFresh := Action{
			Kind: ActMove, Next: store.TaskBuild{Stage: StageImplement, Attempt: 1, Base: decideBranch},
		}
		if got := Decide(fresh); !reflect.DeepEqual(got, wantFresh) {
			t.Fatalf("fresh decision = %+v, want %+v", got, wantFresh)
		}

		implement := store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1, Step: "impl-step"}
		done := View{
			Epic: decideEpic, Task: store.EpicTask{Seq: 3, Evidence: closure, Build: implement},
			Step: &store.StepRow{
				ID: "impl-step", End: endDone, Result: `{"status":"done","summary":"built it","commit":"abc123"}`,
			},
		}
		wantDone := Action{
			Kind: ActGuard,
			Next: store.TaskBuild{Stage: StageReview, Attempt: 1, Base: decideBranch, Revision: "abc123"},
		}
		if got := Decide(done); !reflect.DeepEqual(got, wantDone) {
			t.Fatalf("done decision = %+v, want %+v", got, wantDone)
		}
	})
	t.Run("a closure-kind task moves from its gate to review", func(t *testing.T) {
		t.Parallel()
		// The build carries a stale gate and finding position, which the move to review resets to 0.
		implement := store.TaskBuild{
			Stage: StageImplement, Base: decideBranch, Attempt: 1, Step: "impl-step", GatePos: 2, FindingPos: 3,
		}
		done := View{
			Epic: decideEpic, Task: store.EpicTask{Seq: 3, Evidence: closure, Build: implement},
			Step: &store.StepRow{
				ID: "impl-step", End: endDone, Result: `{"status":"done","summary":"built it","commit":"abc123"}`,
			},
		}
		reviewed := store.TaskBuild{Stage: StageReview, Attempt: 1, Base: decideBranch, Revision: "abc123"}
		guard := Decide(done)
		if want := (Action{Kind: ActGuard, Next: reviewed}); !reflect.DeepEqual(guard, want) {
			t.Fatalf("guard decision = %+v, want %+v", guard, want)
		}

		atReview := View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Evidence: closure, Build: guard.Next}}
		wantAccept := Action{Kind: ActAccept, Next: reviewed, Reason: ReviewUnreviewed}
		if got := Decide(atReview); !reflect.DeepEqual(got, wantAccept) {
			t.Fatalf("review decision = %+v, want %+v", got, wantAccept)
		}
	})
}
