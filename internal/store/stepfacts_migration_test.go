package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
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
	// A version 75 store has no host column, so the seeds are raw inserts that name none. Each row is the one a StepAdd
	// with no epic stores, with the same id, agent, prompt and start time.
	for _, seed := range []struct{ id, prompt string }{{"step-ended", "do it"}, {"step-running", "keep going"}} {
		if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision,
			worktree, checkout_head, checkout_hash, done_value, read_only, owned, unowned, gate, gate_name, gate_source,
			gate_needs, gate_limit, model_limit, started, checks, result, status, end_value, reason, ended, doing, verdicts,
			epic, task, loop_stage, loop_stage_from, agents, caller)
			VALUES (?, 'implementer', ?, '', '', '', '', '', '', '', 0, 'null', 0, 'null', '', '', 'null', 0, 0, ?, '[]', '',
			'', '', '', 0, '', '[]', '', '', ?, ?, '', '')`,
			seed.id, seed.prompt, at.Unix(), StepStageTurn, at.Unix(),
		); err != nil {
			t.Fatalf("seed step %s: %v", seed.id, err)
		}
	}
	// A version 75 store has no host column, and the step ops read the step through GetStep, which names it. So the
	// return, lines, end and loop are written by the queries those ops run, with the same values.
	queries := sqlcgen.New(s.db)
	if err := queries.UpdateStepReturn(ctx, sqlcgen.UpdateStepReturnParams{
		Result: `{"summary":"built"}`, Checks: `[]`, ID: "step-ended",
	}); err != nil {
		t.Fatalf("update return of step-ended: %v", err)
	}
	if err := queries.UpdateStepLines(ctx, sqlcgen.UpdateStepLinesParams{
		LinesCounted: 1, LinesFiles: 3, LinesAdded: 40, LinesRemoved: 7, ID: "step-ended",
	}); err != nil {
		t.Fatalf("update lines of step-ended: %v", err)
	}
	if err := queries.UpdateStepEnd(ctx, sqlcgen.UpdateStepEndParams{
		EndValue: "done", Status: "passed", Reason: "gate green", Ended: at.Add(time.Minute).Unix(), ID: "step-ended",
	}); err != nil {
		t.Fatalf("update end of step-ended: %v", err)
	}
	if err := queries.UpdateStepLoop(ctx, sqlcgen.UpdateStepLoopParams{
		LoopRound: 2, LoopStage: StepStageJudge, LoopStageFrom: at.Add(time.Minute).Unix(), ID: "step-running",
	}); err != nil {
		t.Fatalf("update loop of step-running: %v", err)
	}

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
