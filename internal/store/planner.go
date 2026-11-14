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
