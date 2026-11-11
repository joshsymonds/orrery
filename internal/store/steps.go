package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoStep reports that no step has the id asked for.
var ErrNoStep = errors.New("no step")

// StepCheck is one check a step's return ran, in the round it ran in.
type StepCheck struct {
	Round  int    `json:"round"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// StepAdd inserts a step row, started now. The row is never pruned.
type StepAdd struct {
	ID        string
	Agent     string
	Prompt    string
	Schema    string
	Workspace string
	Revision  string
	Worktree  string
	DoneValue string
	ReadOnly  bool
	Owned     []string
	Gate      []string
	GateLimit time.Duration
}

// StepReturn sets a step's latest result and appends its checks to the ones already recorded.
type StepReturn struct {
	ID     string
	Result string
	Checks []StepCheck
}

// StepEnd sets a step's end, status and reason, and its ended time now.
type StepEnd struct {
	ID     string
	End    string
	Status string
	Reason string
}

// StepRow is a step row. Ended is zero while End is empty.
type StepRow struct {
	ID        string
	Agent     string
	Prompt    string
	Schema    string
	Workspace string
	Revision  string
	Worktree  string
	DoneValue string
	ReadOnly  bool
	Owned     []string
	Gate      []string
	GateLimit time.Duration
	Started   time.Time
	Checks    []StepCheck
	Result    string
	Status    string
	End       string
	Reason    string
	Ended     time.Time
}

func (op StepAdd) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	owned, err := json.Marshal(op.Owned)
	if err != nil {
		return fmt.Errorf("encode owned paths of step %s: %w", op.ID, err)
	}
	gate, err := json.Marshal(op.Gate)
	if err != nil {
		return fmt.Errorf("encode gate of step %s: %w", op.ID, err)
	}
	if err = queries.InsertStep(ctx, sqlcgen.InsertStepParams{
		ID: op.ID, Agent: op.Agent, Prompt: op.Prompt, OutputSchema: op.Schema, Workspace: op.Workspace,
		Revision: op.Revision, Worktree: op.Worktree, DoneValue: op.DoneValue, ReadOnly: op.ReadOnly,
		Owned: string(owned), Gate: string(gate), GateLimit: int64(op.GateLimit), Started: now.Unix(),
	}); err != nil {
		return fmt.Errorf("insert step %s: %w", op.ID, err)
	}
	return nil
}

func (op StepReturn) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	row, err := requireStep(ctx, queries, op.ID)
	if err != nil {
		return err
	}
	checks, err := decodeStepChecks(row.Checks)
	if err != nil {
		return fmt.Errorf("decode checks of step %s: %w", op.ID, err)
	}
	checks = append(checks, op.Checks...)
	encoded, err := json.Marshal(checks)
	if err != nil {
		return fmt.Errorf("encode checks of step %s: %w", op.ID, err)
	}
	if err = queries.UpdateStepReturn(ctx, sqlcgen.UpdateStepReturnParams{
		Result: op.Result, Checks: string(encoded), ID: op.ID,
	}); err != nil {
		return fmt.Errorf("update return of step %s: %w", op.ID, err)
	}
	return nil
}

func (op StepEnd) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	if _, err := requireStep(ctx, queries, op.ID); err != nil {
		return err
	}
	if err := queries.UpdateStepEnd(ctx, sqlcgen.UpdateStepEndParams{
		EndValue: op.End, Status: op.Status, Reason: op.Reason, Ended: now.Unix(), ID: op.ID,
	}); err != nil {
		return fmt.Errorf("update end of step %s: %w", op.ID, err)
	}
	return nil
}

// Step returns the step row with id. It returns an error matching ErrNoStep when no step has that id.
func (s *Store) Step(ctx context.Context, id string) (StepRow, error) {
	row, err := sqlcgen.New(s.db).GetStep(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRow{}, fmt.Errorf("step %s: %w", id, ErrNoStep)
	}
	if err != nil {
		return StepRow{}, fmt.Errorf("get step %s: %w", id, err)
	}
	return toStepRow(row)
}

// OpenSteps returns every step row with no end, ordered by id.
func (s *Store) OpenSteps(ctx context.Context) ([]StepRow, error) {
	rows, err := sqlcgen.New(s.db).ListOpenSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open steps: %w", err)
	}
	out := make([]StepRow, 0, len(rows))
	for _, row := range rows {
		step, convErr := toStepRow(row)
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, step)
	}
	return out, nil
}

// requireStep returns the step row with id, or an error matching ErrNoStep when there is none.
func requireStep(ctx context.Context, queries *sqlcgen.Queries, id string) (sqlcgen.Step, error) {
	row, err := queries.GetStep(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Step{}, fmt.Errorf("step %s: %w", id, ErrNoStep)
	}
	if err != nil {
		return sqlcgen.Step{}, fmt.Errorf("read step %s: %w", id, err)
	}
	return row, nil
}

func toStepRow(row sqlcgen.Step) (StepRow, error) {
	owned, err := decodeStrings(row.Owned)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode owned paths of step %s: %w", row.ID, err)
	}
	gate, err := decodeStrings(row.Gate)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode gate of step %s: %w", row.ID, err)
	}
	checks, err := decodeStepChecks(row.Checks)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode checks of step %s: %w", row.ID, err)
	}
	var ended time.Time
	if row.EndValue != "" {
		ended = time.Unix(row.Ended, 0)
	}
	return StepRow{
		ID: row.ID, Agent: row.Agent, Prompt: row.Prompt, Schema: row.OutputSchema, Workspace: row.Workspace,
		Revision: row.Revision, Worktree: row.Worktree, DoneValue: row.DoneValue, ReadOnly: row.ReadOnly,
		Owned: owned, Gate: gate, GateLimit: time.Duration(row.GateLimit),
		Started: time.Unix(row.Started, 0), Checks: checks, Result: row.Result, Status: row.Status,
		End: row.EndValue, Reason: row.Reason, Ended: ended,
	}, nil
}

func decodeStrings(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode string list: %w", err)
	}
	return out, nil
}

func decodeStepChecks(raw string) ([]StepCheck, error) {
	var out []StepCheck
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode step checks: %w", err)
	}
	return out, nil
}
