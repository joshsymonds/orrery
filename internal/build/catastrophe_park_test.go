package build

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// requireOwnerPark fails unless the epic of f is parked as class with reason, its open questions are the one park question
// with reason, its untold tells are the one tell with reason, which carries that question, and no pending trigger is a
// planner park trigger.
func requireOwnerPark(t *testing.T, f *passFixture, class store.ParkClass, reason string) {
	t.Helper()
	epic := f.epic()
	if epic.Parked != reason || epic.ParkClass != class {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, class, reason)
	}
	entries, err := f.store.Attention(f.ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	idx := slices.IndexFunc(entries, func(entry store.EpicAttention) bool { return entry.Epic == passEpic })
	if idx < 0 {
		t.Fatalf("Attention = %+v, want an entry for %s", entries, passEpic)
	}
	entry := entries[idx]
	if len(entry.Open) != 1 || entry.Open[0].Label != store.ParkLabel || entry.Open[0].Text != reason {
		t.Fatalf("open questions = %+v, want one park question with text %q", entry.Open, reason)
	}
	if len(entry.Tells) != 1 || entry.Tells[0].Text != reason || entry.Tells[0].Question == nil ||
		entry.Tells[0].Question.Seq != entry.Open[0].Seq {
		t.Errorf("tells = %+v, want one untold tell %q that carries the park question", entry.Tells, reason)
	}
	for _, id := range f.pendingTriggers() {
		if strings.HasPrefix(id, "park:") {
			t.Errorf("pending trigger %s, want no planner park trigger", id)
		}
	}
}

// TestCatastropheParkBuildOpensOwnerQuestionForBranchAndRefMove checks that the build runner's owner-class parks of a
// catastrophe, an epic branch that already exists and a task that parks on an unexplained ref move, drive their park
// through the pass and leave the owner the park's question and tell.
func TestCatastropheParkBuildOpensOwnerQuestionForBranchAndRefMove(t *testing.T) {
	t.Parallel()
	t.Run("epic branch exists", func(t *testing.T) {
		t.Parallel()
		f := newPassFixture(t)
		elsewhere := buildObject(t, f.repo, f.base)
		buildRun(t, f.repo, "update-ref", BranchRef(passEpic), elsewhere)
		f.pass()
		requireOwnerPark(t, f, store.ParkClassBranch, "epic/epic exists")
	})
	t.Run("unexplained ref move", func(t *testing.T) {
		t.Parallel()
		f := newImplementerFixture(t)
		f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done"}`)
		moved := buildObject(t, f.repo, f.base)
		buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
		f.pass()
		requireOwnerPark(t, f, store.ParkClassRefMove,
			fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved))
	})
}
