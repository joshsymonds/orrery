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

// StepVerdict is one Haiku verdict on a step: a monitor call on a trip, or a judge call on a done return. Verdict is
// empty when the call failed, and Error then says why. At is the unix second the verdict was recorded.
type StepVerdict struct {
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	Trip    string `json:"trip"`
	Round   int    `json:"round"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail"`
	Error   string `json:"error"`
	Action  string `json:"action"`
}

// StepNeed is one pool hold a gate needs: Count members of Pool.
type StepNeed struct {
	Pool  string `json:"pool"`
	Count int    `json:"count"`
}

// StepAdd inserts a step row, started now. The row is never pruned. Epic and Task are optional labels; a step with
// an empty Epic is never listed by LabelledSteps. Gate and GateLimit are the resolved argv and limit of the gate the
// step runs; GateName, GateSource and GateNeeds say which named gate that was, where it was declared and what it held.
type StepAdd struct {
	ID         string
	Agent      string
	Prompt     string
	Schema     string
	Workspace  string
	Revision   string
	Worktree   string
	DoneValue  string
	ReadOnly   bool
	Owned      []string
	Gate       []string
	GateName   string
	GateSource string
	GateNeeds  []StepNeed
	GateLimit  time.Duration
	ModelLimit time.Duration
	Epic       string
	Task       string
}

// StepWatch appends Verdict to a step's verdicts, stamped with the write time, and sets its doing line to Doing when
// Doing is not empty.
type StepWatch struct {
	ID      string
	Verdict StepVerdict
	Doing   string
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
	ID         string
	Agent      string
	Prompt     string
	Schema     string
	Workspace  string
	Revision   string
	Worktree   string
	DoneValue  string
	ReadOnly   bool
	Owned      []string
	Gate       []string
	GateName   string
	GateSource string
	GateNeeds  []StepNeed
	GateLimit  time.Duration
	ModelLimit time.Duration
	Started    time.Time
	Checks     []StepCheck
	Doing      string
	Verdicts   []StepVerdict
	Result     string
	Status     string
	End        string
	Reason     string
	Ended      time.Time
	Epic       string
	Task       string
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
	gateNeeds, err := json.Marshal(op.GateNeeds)
	if err != nil {
		return fmt.Errorf("encode gate needs of step %s: %w", op.ID, err)
	}
	if err = queries.InsertStep(ctx, sqlcgen.InsertStepParams{
		ID: op.ID, Agent: op.Agent, Prompt: op.Prompt, OutputSchema: op.Schema, Workspace: op.Workspace,
		Revision: op.Revision, Worktree: op.Worktree, DoneValue: op.DoneValue, ReadOnly: op.ReadOnly,
		Owned: string(owned), Gate: string(gate), GateName: op.GateName, GateSource: op.GateSource,
		GateNeeds: string(gateNeeds), GateLimit: int64(op.GateLimit),
		ModelLimit: int64(op.ModelLimit), Started: now.Unix(), Epic: op.Epic, Task: op.Task,
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

func (op StepWatch) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	row, err := requireStep(ctx, queries, op.ID)
	if err != nil {
		return err
	}
	verdicts, err := decodeStepVerdicts(row.Verdicts)
	if err != nil {
		return fmt.Errorf("decode verdicts of step %s: %w", op.ID, err)
	}
	verdict := op.Verdict
	verdict.At = now.Unix()
	verdicts = append(verdicts, verdict)
	encoded, err := json.Marshal(verdicts)
	if err != nil {
		return fmt.Errorf("encode verdicts of step %s: %w", op.ID, err)
	}
	doing := row.Doing
	if op.Doing != "" {
		doing = op.Doing
	}
	if err = queries.UpdateStepWatch(ctx, sqlcgen.UpdateStepWatchParams{
		Verdicts: string(encoded), Doing: doing, ID: op.ID,
	}); err != nil {
		return fmt.Errorf("update watch of step %s: %w", op.ID, err)
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
	return toStepRows(rows)
}

// LabelledSteps returns the step rows with a non-empty epic, narrowed to rows whose epic is epic when epic is not
// empty and to rows whose task is task when task is not empty, ordered by start time then id.
func (s *Store) LabelledSteps(ctx context.Context, epic, task string) ([]StepRow, error) {
	rows, err := sqlcgen.New(s.db).ListLabelledSteps(ctx, sqlcgen.ListLabelledStepsParams{Epic: epic, Task: task})
	if err != nil {
		return nil, fmt.Errorf("list labeled steps: %w", err)
	}
	return toStepRows(rows)
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

func toStepRows(rows []sqlcgen.Step) ([]StepRow, error) {
	out := make([]StepRow, 0, len(rows))
	for _, row := range rows {
		step, err := toStepRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, step)
	}
	return out, nil
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
	gateNeeds, err := decodeStepNeeds(row.GateNeeds)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode gate needs of step %s: %w", row.ID, err)
	}
	checks, err := decodeStepChecks(row.Checks)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode checks of step %s: %w", row.ID, err)
	}
	verdicts, err := decodeStepVerdicts(row.Verdicts)
	if err != nil {
		return StepRow{}, fmt.Errorf("decode verdicts of step %s: %w", row.ID, err)
	}
	var ended time.Time
	if row.EndValue != "" {
		ended = time.Unix(row.Ended, 0)
	}
	return StepRow{
		ID: row.ID, Agent: row.Agent, Prompt: row.Prompt, Schema: row.OutputSchema, Workspace: row.Workspace,
		Revision: row.Revision, Worktree: row.Worktree, DoneValue: row.DoneValue, ReadOnly: row.ReadOnly,
		Owned: owned, Gate: gate, GateName: row.GateName, GateSource: row.GateSource, GateNeeds: gateNeeds,
		GateLimit: time.Duration(row.GateLimit), ModelLimit: time.Duration(row.ModelLimit),
		Started: time.Unix(row.Started, 0), Checks: checks, Doing: row.Doing, Verdicts: verdicts,
		Result: row.Result, Status: row.Status, End: row.EndValue, Reason: row.Reason, Ended: ended,
		Epic: row.Epic, Task: row.Task,
	}, nil
}

func decodeStrings(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode string list: %w", err)
	}
	return out, nil
}

func decodeStepNeeds(raw string) ([]StepNeed, error) {
	var out []StepNeed
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode step needs: %w", err)
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

func decodeStepVerdicts(raw string) ([]StepVerdict, error) {
	var out []StepVerdict
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode step verdicts: %w", err)
	}
	return out, nil
}
