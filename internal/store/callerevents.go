package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// CallerSession is the sessions row of a caller: its id, kind, server, pane and state.
type CallerSession struct {
	ID     string
	Kind   string
	Server string
	Pane   string
	State  string
}

// CallerStep is an ended step together with the sessions row of the caller that started it.
type CallerStep struct {
	Step   StepRow
	Caller CallerSession
}

// CallerSteps returns the ended steps whose caller has a sessions row, whose caller has not read their end through
// MarkStepRead, and that have no caller_events mark for the event "step:" followed by the step id, under the caller's
// row id. It orders them by ended time, then id.
func (s *Store) CallerSteps(ctx context.Context) ([]CallerStep, error) {
	rows, err := sqlcgen.New(s.db).ListCallerSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list caller steps: %w", err)
	}
	out := make([]CallerStep, 0, len(rows))
	for _, row := range rows {
		step, stepErr := toStepRow(row.Step)
		if stepErr != nil {
			return nil, stepErr
		}
		out = append(out, CallerStep{Step: step, Caller: CallerSession{
			ID: row.CallerID, Kind: row.CallerKind, Server: row.CallerServer, Pane: row.CallerPane,
			State: row.CallerState,
		}})
	}
	return out, nil
}

// MarkCallerEvent records that the event was delivered to the session, at at. A repeat of the mark is a no-op, and it
// keeps the time of the first.
func (s *Store) MarkCallerEvent(ctx context.Context, event, session string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertCallerEvent(ctx, sqlcgen.InsertCallerEventParams{
			Event: event, Session: session, At: at.Unix(),
		}); err != nil {
			return fmt.Errorf("mark event %s delivered to session %s: %w", event, session, err)
		}
		return nil
	})
}

// CallerEventMarked reports whether MarkCallerEvent has recorded the event as delivered to the session.
func (s *Store) CallerEventMarked(ctx context.Context, event, session string) (bool, error) {
	count, err := sqlcgen.New(s.db).CountCallerEvent(ctx, sqlcgen.CountCallerEventParams{
		Event: event, Session: session,
	})
	if err != nil {
		return false, fmt.Errorf("read event %s delivered to session %s: %w", event, session, err)
	}
	return count > 0, nil
}
