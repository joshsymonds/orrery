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

// StepReturn sets a step's latest result and appends its checks to the ones already recorded. It also sets its loop
// seq to the newest event seq of its session and clears its gate, in the same write.
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

// StepLoop sets a step's loop state: its round, its continue count, the nudge texts the next turn end is continued
// with, whether it has been nudged, whether it is held, whether its judge has sent it back, and its usage-limit hold:
// whether it is limited, the kind of limit and the time it resets, zero when the reset is unknown. It sets every loop
// column, so a field left at its zero value is stored as zero.
type StepLoop struct {
	ID          string
	Round       int
	Continues   int
	Nudge       string
	OwnerNudge  string
	Nudged      bool
	Held        bool
	JudgeSent   bool
	Limited     bool
	LimitKind   string
	LimitResets time.Time
}

// StepGate sets a step's gate: the grant it holds, its holder and the round it holds in. An empty Holder clears all
// three.
type StepGate struct {
	ID     string
	Grant  string
	Holder string
	Round  int
}

// StepTurnEnd is the turn a step's session last ended after its last prompt. Seq is the turn end's event seq, IsError
// whether the turn ended in error, Output the session's turn output, and Returned whether the turn ended at or before
// the step's latest return.
type StepTurnEnd struct {
	Seq      int64
	IsError  bool
	Output   string
	Returned bool
}

// StepRow is a step row. Ended is zero while End is empty, and LoopLimitResets is zero when the limit's reset is
// unknown.
type StepRow struct {
	ID              string
	Agent           string
	Prompt          string
	Schema          string
	Workspace       string
	Revision        string
	Worktree        string
	DoneValue       string
	ReadOnly        bool
	Owned           []string
	Gate            []string
	GateName        string
	GateSource      string
	GateNeeds       []StepNeed
	GateLimit       time.Duration
	ModelLimit      time.Duration
	Started         time.Time
	Checks          []StepCheck
	Doing           string
	Verdicts        []StepVerdict
	Result          string
	Status          string
	End             string
	Reason          string
	Ended           time.Time
	Epic            string
	Task            string
	LoopRound       int
	LoopContinues   int
	LoopNudge       string
	LoopOwnerNudge  string
	LoopNudged      bool
	LoopHeld        bool
	LoopJudgeSent   bool
	LoopSeq         int64
	GateGrant       string
	GateHolder      string
	GateRound       int
	LoopLimited     bool
	LoopLimitKind   string
	LoopLimitResets time.Time
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

func (op StepLoop) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if _, err := requireStep(ctx, queries, op.ID); err != nil {
		return err
	}
	var resets int64
	if !op.LimitResets.IsZero() {
		resets = op.LimitResets.Unix()
	}
	if err := queries.UpdateStepLoop(ctx, sqlcgen.UpdateStepLoopParams{
		LoopRound: int64(op.Round), LoopContinues: int64(op.Continues), LoopNudge: op.Nudge,
		LoopOwnerNudge: op.OwnerNudge, LoopNudged: op.Nudged, LoopHeld: op.Held, LoopJudgeSent: op.JudgeSent,
		LoopLimited: op.Limited, LoopLimitKind: op.LimitKind, LoopLimitResets: resets, ID: op.ID,
	}); err != nil {
		return fmt.Errorf("update loop of step %s: %w", op.ID, err)
	}
	return nil
}

func (op StepGate) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if _, err := requireStep(ctx, queries, op.ID); err != nil {
		return err
	}
	params := sqlcgen.UpdateStepGateParams{
		GateGrant: op.Grant, GateHolder: op.Holder, GateRound: int64(op.Round), ID: op.ID,
	}
	if op.Holder == "" {
		params.GateGrant, params.GateRound = "", 0
	}
	if err := queries.UpdateStepGate(ctx, params); err != nil {
		return fmt.Errorf("update gate of step %s: %w", op.ID, err)
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

// StepTurn returns the last turn end of the step id's session that was stored after the session's last prompt, with
// its output and whether it is at or before the step's latest return. It returns false, and no error, when there is no
// such turn end or while the session's background flag is set.
func (s *Store) StepTurn(ctx context.Context, id string) (StepTurnEnd, bool, error) {
	row, err := sqlcgen.New(s.db).GetStepTurnEnd(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return StepTurnEnd{}, false, nil
	}
	if err != nil {
		return StepTurnEnd{}, false, fmt.Errorf("read turn end of step %s: %w", id, err)
	}
	if row.Background {
		return StepTurnEnd{}, false, nil
	}
	return StepTurnEnd{
		Seq: row.Seq, IsError: row.IsError != 0, Output: row.TurnOutput, Returned: row.Seq <= row.LoopSeq,
	}, true, nil
}

// OpenSteps returns every step row with no end, ordered by id.
func (s *Store) OpenSteps(ctx context.Context) ([]StepRow, error) {
	rows, err := sqlcgen.New(s.db).ListOpenSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open steps: %w", err)
	}
	return toStepRows(rows)
}

// LimitedSteps returns the open step rows whose loop is limited, ordered by start time then id.
func (s *Store) LimitedSteps(ctx context.Context) ([]StepRow, error) {
	rows, err := sqlcgen.New(s.db).ListLimitedSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list limited steps: %w", err)
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
	var limitResets time.Time
	if row.LoopLimitResets != 0 {
		limitResets = time.Unix(row.LoopLimitResets, 0)
	}
	return StepRow{
		ID: row.ID, Agent: row.Agent, Prompt: row.Prompt, Schema: row.OutputSchema, Workspace: row.Workspace,
		Revision: row.Revision, Worktree: row.Worktree, DoneValue: row.DoneValue, ReadOnly: row.ReadOnly,
		Owned: owned, Gate: gate, GateName: row.GateName, GateSource: row.GateSource, GateNeeds: gateNeeds,
		GateLimit: time.Duration(row.GateLimit), ModelLimit: time.Duration(row.ModelLimit),
		Started: time.Unix(row.Started, 0), Checks: checks, Doing: row.Doing, Verdicts: verdicts,
		Result: row.Result, Status: row.Status, End: row.EndValue, Reason: row.Reason, Ended: ended,
		Epic: row.Epic, Task: row.Task,
		LoopRound: int(row.LoopRound), LoopContinues: int(row.LoopContinues), LoopNudge: row.LoopNudge,
		LoopOwnerNudge: row.LoopOwnerNudge, LoopNudged: row.LoopNudged, LoopHeld: row.LoopHeld,
		LoopJudgeSent: row.LoopJudgeSent, LoopSeq: row.LoopSeq,
		GateGrant: row.GateGrant, GateHolder: row.GateHolder, GateRound: int(row.GateRound),
		LoopLimited: row.LoopLimited, LoopLimitKind: row.LoopLimitKind, LoopLimitResets: limitResets,
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
