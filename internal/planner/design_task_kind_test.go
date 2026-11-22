package planner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestDesignTaskKindAccepted checks that a return whose task has kind design passes the check, and that the turn
// storing its plan keeps the kind on the task.
func TestDesignTaskKindAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	designPlan := strings.Replace(validPlan, `"title":"Build it",`, `"title":"Build it","kind":"design",`, 1)
	ret, err := Parse(json.RawMessage(designPlan))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkLines(t, checkPlan(ret, nil))
	if err = s.CompleteTurn(ctx, runnerEpicID, turn.Seq, planOf(ret), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	tasks := epicOf(t, s).Tasks
	if len(tasks) != 1 || tasks[0].Kind != store.TaskKindDesign {
		t.Errorf("tasks = %+v, want one task of kind %q", tasks, store.TaskKindDesign)
	}
}

// TestDesignTaskKindUnknownRefused checks that a task whose kind is neither the empty kind nor design gives the one
// line naming the kind.
func TestDesignTaskKindUnknownRefused(t *testing.T) {
	t.Parallel()
	task := validTask("B", "internal/b.go")
	task.Kind = "blueprint"
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil), `tasks[0] "B" kind: unknown kind "blueprint"`)
}
