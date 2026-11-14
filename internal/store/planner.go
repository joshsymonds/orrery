package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNotClaimable reports that an epic has no turn to claim: it is paused or parked, it has a running turn, or it has
// no pending trigger.
var ErrNotClaimable = errors.New("epic has no turn to claim")

// ErrTurnNotRunning reports that a turn is not running.
var ErrTurnNotRunning = errors.New("turn is not running")

// EpicPlanState is the planning state of one epic with pending triggers. Parked is the park reason, empty when the
// epic is not parked. Running is true when the epic has a turn in state running.
type EpicPlanState struct {
	Epic    string
	Paused  bool
	Parked  string
	Running bool
	Pending int
}

// EpicTurn is one planner turn on an epic. Times are stored at whole seconds; Ended is zero while the turn runs.
type EpicTurn struct {
	Epic      string
	Seq       int64
	Step      string
	State     string
	Reason    string
	Seen      int64
	Outline   string
	Outcome   string
	Decisions []string
	Started   time.Time
	Ended     time.Time
}

// EpicPlan is the valid return of one planner turn: its decisions, its tasks, its outline, its questions and its
// outcome.
type EpicPlan struct {
	Decisions []string
	Tasks     []EpicTaskInput
	Outline   string
	Questions []string
	Outcome   string
}

// EpicTaskInput is one task of a planner return: its title, its deliverable, the files it touches, the Requirements
// it covers and its check.
type EpicTaskInput struct {
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Check        string
}

// PlanStates returns one entry per epic with at least one pending trigger, ordered by epic id.
func (s *Store) PlanStates(ctx context.Context) ([]EpicPlanState, error) {
	rows, err := sqlcgen.New(s.db).ListEpicPlanStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plan states: %w", err)
	}
	return mapInOrder(rows, func(state sqlcgen.ListEpicPlanStatesRow) EpicPlanState {
		return EpicPlanState{
			Epic:    state.ID,
			Paused:  state.Paused != 0,
			Parked:  state.Parked,
			Running: state.Running != 0,
			Pending: int(state.Pending),
		}
	}), nil
}

// ClaimTurn starts a running turn on epic in one write and claims every pending trigger of the epic for it. The turn's
// seq is the epic's highest turn seq plus one, and it sees the epic's highest decision seq. An unknown epic is
// ErrNoEpic. A paused or parked epic, one with a running turn, or one with no pending trigger is ErrNotClaimable and
// the write changes nothing.
func (s *Store) ClaimTurn(ctx context.Context, epic string, at time.Time) (EpicTurn, error) {
	var turn EpicTurn
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := checkClaimable(ctx, queries, epic); err != nil {
			return err
		}
		var err error
		turn, err = startTurn(ctx, queries, epic, at)
		return err
	})
	if err != nil {
		return EpicTurn{}, err
	}
	return turn, nil
}

// checkClaimable returns ErrNoEpic for an unknown epic, and ErrNotClaimable when the epic is paused or parked, has a
// running turn, or has no pending trigger.
func checkClaimable(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := queries.GetEpic(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	if err != nil {
		return fmt.Errorf("get epic %s: %w", epic, err)
	}
	if head.Paused != 0 || head.Parked != "" {
		return fmt.Errorf("epic %s is paused or parked: %w", epic, ErrNotClaimable)
	}
	running, err := queries.CountRunningEpicTurns(ctx, epic)
	if err != nil {
		return fmt.Errorf("count running turns of epic %s: %w", epic, err)
	}
	if running > 0 {
		return fmt.Errorf("epic %s has a running turn: %w", epic, ErrNotClaimable)
	}
	pending, err := queries.CountPendingEpicTriggers(ctx, epic)
	if err != nil {
		return fmt.Errorf("count pending triggers of epic %s: %w", epic, err)
	}
	if pending == 0 {
		return fmt.Errorf("epic %s has no pending trigger: %w", epic, ErrNotClaimable)
	}
	return nil
}

// startTurn inserts the next running turn of epic, seen at the epic's highest decision seq, and claims the epic's
// pending triggers for it.
func startTurn(ctx context.Context, queries *sqlcgen.Queries, epic string, at time.Time) (EpicTurn, error) {
	highest, err := queries.MaxEpicTurnSeq(ctx, epic)
	if err != nil {
		return EpicTurn{}, fmt.Errorf("read turn count of epic %s: %w", epic, err)
	}
	seen, err := queries.MaxEpicDecisionSeq(ctx, epic)
	if err != nil {
		return EpicTurn{}, fmt.Errorf("read decision count of epic %s: %w", epic, err)
	}
	seq := highest + 1
	if err = queries.InsertEpicTurn(ctx, sqlcgen.InsertEpicTurnParams{
		Epic: epic, Seq: seq, Seen: seen, Started: at.Unix(),
	}); err != nil {
		return EpicTurn{}, fmt.Errorf("insert turn %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.ClaimEpicTriggers(ctx, sqlcgen.ClaimEpicTriggersParams{Turn: seq, Epic: epic}); err != nil {
		return EpicTurn{}, fmt.Errorf("claim triggers of epic %s for turn %d: %w", epic, seq, err)
	}
	return EpicTurn{Epic: epic, Seq: seq, State: "running", Seen: seen, Started: time.Unix(at.Unix(), 0)}, nil
}

// CompleteTurn stores plan as the return of the running turn seq of epic in one write: its decisions, its tasks with
// their files and Requirements, its outline, its questions and its outcome. The turn succeeds ended at at, and every
// trigger the turn claimed is consumed. A turn that is not running, or does not exist, is ErrTurnNotRunning and
// writes nothing.
func (s *Store) CompleteTurn(ctx context.Context, epic string, turn int64, plan EpicPlan, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.CompleteEpicTurn(ctx, sqlcgen.CompleteEpicTurnParams{
			Outline: plan.Outline, Outcome: plan.Outcome, Ended: at.Unix(), Epic: epic, Seq: turn,
		})
		if err != nil {
			return fmt.Errorf("complete turn %d of epic %s: %w", turn, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", turn, epic, ErrTurnNotRunning)
		}
		if err = insertInOrder(plan.Decisions, func(position int64, text string) error {
			return queries.InsertEpicTurnDecision(ctx, sqlcgen.InsertEpicTurnDecisionParams{
				Epic: epic, Turn: turn, Position: position, Text: text,
			})
		}); err != nil {
			return fmt.Errorf("decisions of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertTasks(ctx, queries, epic, turn, plan.Tasks); err != nil {
			return fmt.Errorf("tasks of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertQuestions(ctx, queries, epic, turn, plan.Questions); err != nil {
			return fmt.Errorf("questions of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = queries.ConsumeEpicTriggers(ctx, sqlcgen.ConsumeEpicTriggersParams{
			Epic: epic, Turn: turn,
		}); err != nil {
			return fmt.Errorf("consume triggers of turn %d of epic %s: %w", turn, epic, err)
		}
		return nil
	})
}

// insertTasks stores tasks as the next task seqs of epic after its highest, each with its files and Requirements in
// order.
func insertTasks(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, tasks []EpicTaskInput) error {
	highest, err := queries.MaxEpicTaskSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task count of epic %s: %w", epic, err)
	}
	return insertInOrder(tasks, func(taskPosition int64, task EpicTaskInput) error {
		seq := highest + taskPosition
		if err = queries.InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
			Epic: epic, Seq: seq, Turn: turn, Title: task.Title, Deliverable: task.Deliverable, TaskCheck: task.Check,
		}); err != nil {
			return fmt.Errorf("insert task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.Files, func(position int64, path string) error {
			return queries.InsertEpicTaskFile(ctx, sqlcgen.InsertEpicTaskFileParams{
				Epic: epic, Task: seq, Position: position, Path: path,
			})
		}); err != nil {
			return fmt.Errorf("files of task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.Requirements, func(position int64, requirement string) error {
			return queries.InsertEpicTaskRequirement(ctx, sqlcgen.InsertEpicTaskRequirementParams{
				Epic: epic, Task: seq, Position: position, Requirement: requirement,
			})
		}); err != nil {
			return fmt.Errorf("requirements of task %d of epic %s: %w", seq, epic, err)
		}
		return nil
	})
}

// insertQuestions stores questions as the next question seqs of epic after its highest, each open.
func insertQuestions(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, questions []string) error {
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	return insertInOrder(questions, func(position int64, text string) error {
		return queries.InsertEpicQuestion(ctx, sqlcgen.InsertEpicQuestionParams{
			Epic: epic, Seq: highest + position, Turn: turn, Text: text,
		})
	})
}

// SetTurnStep records step as the step of the running turn seq of epic in one write. A turn that is not running, or
// does not exist, is ErrTurnNotRunning.
func (s *Store) SetTurnStep(ctx context.Context, epic string, turn int64, step string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.SetEpicTurnStep(ctx, sqlcgen.SetEpicTurnStepParams{Step: step, Epic: epic, Seq: turn})
		if err != nil {
			return fmt.Errorf("set step of turn %d of epic %s: %w", turn, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", turn, epic, ErrTurnNotRunning)
		}
		return nil
	})
}
