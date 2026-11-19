package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestStepFactsMigration checks the migration that adds a step's line counts. The seeded steps are an ended step with
// counts and a running step mid-loop, so the Down has counts to lose and the Up has rows to read zeros into. Its Down
// drops the four columns and leaves each row's other columns as they were, and its Up restores the columns at zero and
// leaves the rows otherwise unchanged.
func TestStepFactsMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 75)
	at := time.Unix(1700000000, 0)
	applyStepOps(ctx, t, s, at,
		StepAdd{ID: "step-ended", Agent: "implementer", Prompt: "do it"},
		StepAdd{ID: "step-running", Agent: "implementer", Prompt: "keep going"},
	)
	applyStepOps(ctx, t, s, at.Add(time.Minute),
		StepReturn{ID: "step-ended", Result: `{"summary":"built"}`,
			Lines: &StepLines{Counted: true, Files: 3, Added: 40, Removed: 7}},
		StepEnd{ID: "step-ended", End: "done", Status: "passed", Reason: "gate green"},
		StepLoop{ID: "step-running", Round: 2, Stage: StepStageJudge, StageFrom: at.Add(time.Minute)},
	)

	seeded := rowsOf(t, s.db.DB, "steps")
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00075: %v", err)
	}
	if hasAny(columnsOf(t, s, "steps"), "lines_counted", "lines_files", "lines_added", "lines_removed") {
		t.Errorf("steps columns after Down = %v, want no lines columns", columnsOf(t, s, "steps"))
	}
	// The four columns are the steps' last, so each row keeps the leading columns it had.
	kept := rowsOf(t, s.db.DB, "steps")
	if want := leadingColumns(seeded, kept); !reflect.DeepEqual(kept, want) {
		t.Errorf("steps after Down = %v, want %v", kept, want)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00075: %v", err)
	}
	if !hasAll(columnsOf(t, s, "steps"), "lines_counted", "lines_files", "lines_added", "lines_removed") {
		t.Fatalf("steps columns after Up = %v, want the lines columns", columnsOf(t, s, "steps"))
	}
	zeros := []any{int64(0), int64(0), int64(0), int64(0)}
	want := make([][]any, len(kept))
	for i, row := range kept {
		want[i] = append(append([]any{}, row...), zeros...)
	}
	if got := rowsOf(t, s.db.DB, "steps"); !reflect.DeepEqual(got, want) {
		t.Errorf("steps after Up = %v, want %v", got, want)
	}
}
