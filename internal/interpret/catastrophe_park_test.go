package interpret

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// requireOwnerPark fails unless the test epic is parked as class with reason, its open questions are the one park question
// with reason, its untold tells are the one tell with reason, which carries that question, and no pending trigger is a
// planner park trigger.
func requireOwnerPark(t *testing.T, s *store.Store, class store.ParkClass, reason string) {
	t.Helper()
	ctx := context.Background()
	epic := epicOf(t, s)
	if epic.Parked != reason || epic.ParkClass != class {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, class, reason)
	}
	entries, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	idx := slices.IndexFunc(entries, func(entry store.EpicAttention) bool { return entry.Epic == runEpic })
	if idx < 0 {
		t.Fatalf("Attention = %+v, want an entry for %s", entries, runEpic)
	}
	entry := entries[idx]
	if len(entry.Open) != 1 || entry.Open[0].Label != store.ParkLabel || entry.Open[0].Text != reason {
		t.Fatalf("open questions = %+v, want one park question with text %q", entry.Open, reason)
	}
	if len(entry.Tells) != 1 || entry.Tells[0].Text != reason || entry.Tells[0].Question == nil ||
		entry.Tells[0].Question.Seq != entry.Open[0].Seq {
		t.Errorf("tells = %+v, want one untold tell %q that carries the park question", entry.Tells, reason)
	}
	triggers, err := s.TurnTriggers(ctx, runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	for _, trigger := range triggers {
		if strings.HasPrefix(trigger.ID, "park:") {
			t.Errorf("trigger %s = %+v, want no planner park trigger", trigger.ID, trigger)
		}
	}
}

// TestCatastropheParkInterpretOpensOwnerQuestion checks that the interpreter's owner-class catastrophe parks, an integrate
// batch whose epic branch moved or is checked out elsewhere, and a pinned workflow that does not load, each open the
// owner's park question and tell and add no planner park trigger.
func TestCatastropheParkInterpretOpensOwnerQuestion(t *testing.T) {
	t.Parallel()
	t.Run("integrate branch moved", func(t *testing.T) {
		t.Parallel()
		w := newIntegrateWorld(t)
		w.toCaptured()
		moved := w.commit(w.base, "e.go", "moved\n")
		w.git("update-ref", integrateBranch, moved)
		w.pass()
		requireOwnerPark(t, w.s, store.ParkClassBranch,
			fmt.Sprintf("epic/epic-runner moved: at %s, not the stored base %s", moved, w.base))
	})
	t.Run("integrate branch checked out elsewhere", func(t *testing.T) {
		t.Parallel()
		w := newIntegrateWorld(t)
		w.toCaptured()
		worktree := filepath.Join(t.TempDir(), "epic")
		w.git("worktree", "add", "-q", worktree, nodeBranchName)
		w.pass()
		requireOwnerPark(t, w.s, store.ParkClassBranch,
			"epic/epic-runner is checked out in a worktree, so it was not moved")
	})
	t.Run("pinned workflow does not load", func(t *testing.T) {
		t.Parallel()
		original := builtinPin(t)
		tampered := strings.Replace(original.Text, "id: build", "id: bulid", 1)
		s := openRunStore(t)
		createRunEpic(t, s, pinnedText(original, tampered, original.SHA256))
		newRunner(s, &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}).Pass(context.Background())
		requireOwnerPark(t, s, store.ParkClassOrchestrator,
			"workflow epic: text sha256 "+sha256Hex(tampered)+" does not match the pinned "+original.SHA256)
	})
}
