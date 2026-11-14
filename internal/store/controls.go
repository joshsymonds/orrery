package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoTurn reports that the epic has no turn with the seq asked for.
var ErrNoTurn = errors.New("no turn")

// RefusalError is the error a control returns when the epic is outside the state the control needs. The control
// writes nothing, and the text says why.
type RefusalError string

// Error returns the reason the control was refused.
func (r RefusalError) Error() string {
	return string(r)
}

// PauseEpic pauses epic in one write, so the planner starts no turn for it. An unknown epic is ErrNoEpic, and an epic
// that is already paused is a RefusalError.
func (s *Store) PauseEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Paused != 0 {
			return RefusalError(fmt.Sprintf("epic %s is already paused", epic))
		}
		if err = queries.PauseEpic(ctx, epic); err != nil {
			return fmt.Errorf("pause epic %s: %w", epic, err)
		}
		return nil
	})
}

// ResumeEpic clears the pause of epic in one write and leaves any park in place. An unknown epic is ErrNoEpic. An
// epic that is not paused is a RefusalError, and a parked one names retry, since only retry ends a park.
func (s *Store) ResumeEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Paused == 0 {
			if head.Parked != "" {
				return RefusalError(fmt.Sprintf("epic %s is parked, not paused; retry ends a park", epic))
			}
			return RefusalError(fmt.Sprintf("epic %s is not paused", epic))
		}
		if err = queries.ResumeEpic(ctx, epic); err != nil {
			return fmt.Errorf("resume epic %s: %w", epic, err)
		}
		return nil
	})
}

// StopEpic pauses epic and flags its running turn to stop, in one write, and returns the turn's step, empty while the
// turn has none. The turn keeps its claimed triggers. An unknown epic is ErrNoEpic, and an epic with no running turn is
// a RefusalError.
func (s *Store) StopEpic(ctx context.Context, epic string) (string, error) {
	var step string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := epicHead(ctx, queries, epic); err != nil {
			return err
		}
		running, err := queries.GetRunningEpicTurn(ctx, epic)
		if errors.Is(err, sql.ErrNoRows) {
			return RefusalError(fmt.Sprintf("epic %s has no running turn", epic))
		}
		if err != nil {
			return fmt.Errorf("read running turn of epic %s: %w", epic, err)
		}
		if err = queries.PauseEpic(ctx, epic); err != nil {
			return fmt.Errorf("pause epic %s: %w", epic, err)
		}
		if err = queries.SetEpicTurnStop(ctx, sqlcgen.SetEpicTurnStopParams{Epic: epic, Seq: running.Seq}); err != nil {
			return fmt.Errorf("stop turn %d of epic %s: %w", running.Seq, epic, err)
		}
		step = running.Step
		return nil
	})
	if err != nil {
		return "", err
	}
	return step, nil
}

// RetryEpic ends the park of epic in one write and leaves its triggers pending. The failed turns up to the epic's
// highest turn seq stop counting toward a park. An unknown epic is ErrNoEpic, and an epic that is not parked is a
// RefusalError.
func (s *Store) RetryEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Parked == "" {
			return RefusalError(fmt.Sprintf("epic %s is not parked", epic))
		}
		highest, err := queries.MaxEpicTurnSeq(ctx, epic)
		if err != nil {
			return fmt.Errorf("read turn count of epic %s: %w", epic, err)
		}
		if err = queries.RetryEpic(ctx, sqlcgen.RetryEpicParams{Retried: highest, ID: epic}); err != nil {
			return fmt.Errorf("retry epic %s: %w", epic, err)
		}
		return nil
	})
}

// TurnStopped reports whether turn seq of epic was flagged to stop. A turn that does not exist is an error matching
// ErrNoTurn.
func (s *Store) TurnStopped(ctx context.Context, epic string, seq int64) (bool, error) {
	stop, err := sqlcgen.New(s.db).GetEpicTurnStop(ctx, sqlcgen.GetEpicTurnStopParams{Epic: epic, Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("turn %d of epic %s: %w", seq, epic, ErrNoTurn)
	}
	if err != nil {
		return false, fmt.Errorf("read stop of turn %d of epic %s: %w", seq, epic, err)
	}
	return stop != 0, nil
}

// epicHead returns the stored head of epic. An unknown epic is ErrNoEpic.
func epicHead(ctx context.Context, queries *sqlcgen.Queries, epic string) (sqlcgen.Epic, error) {
	head, err := queries.GetEpic(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Epic{}, fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	if err != nil {
		return sqlcgen.Epic{}, fmt.Errorf("get epic %s: %w", epic, err)
	}
	return head, nil
}
