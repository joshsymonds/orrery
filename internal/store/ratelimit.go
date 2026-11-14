package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EngineRateLimited stores the latest rejected rate limit of session ID: its status, kind and reset time, with at set
// to the op's now. A later op for the same session replaces the stored row. It appends no event.
type EngineRateLimited struct {
	ID       string
	Status   string
	Kind     string
	ResetsAt time.Time
}

// RateLimit is the stored rate limit of an engine session. Times are stored at whole seconds.
type RateLimit struct {
	Status   string
	Kind     string
	ResetsAt time.Time
	At       time.Time
}

func (op EngineRateLimited) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	if err := queries.UpsertEngineRateLimit(ctx, sqlcgen.UpsertEngineRateLimitParams{
		SessionID: op.ID, Status: op.Status, Kind: op.Kind, ResetsAt: op.ResetsAt.Unix(), At: now.Unix(),
	}); err != nil {
		return fmt.Errorf("upsert rate limit of session %s: %w", op.ID, err)
	}
	return nil
}

// RateLimit returns the stored rate limit of session and true. It returns the zero value and false when the session
// has none.
func (s *Store) RateLimit(ctx context.Context, session string) (RateLimit, bool, error) {
	row, err := sqlcgen.New(s.db).GetEngineRateLimit(ctx, session)
	if errors.Is(err, sql.ErrNoRows) {
		return RateLimit{}, false, nil
	}
	if err != nil {
		return RateLimit{}, false, fmt.Errorf("get rate limit of session %s: %w", session, err)
	}
	return rateLimitOf(row), true, nil
}

// StepRateLimit returns the stored rate limit of session id and true when its status is "rejected" and it was stored
// at or after the at of the session's newest engine_prompt event. It returns the zero value and false otherwise. A
// session with no prompt compares against zero.
func (s *Store) StepRateLimit(ctx context.Context, id string) (RateLimit, bool, error) {
	row, err := sqlcgen.New(s.db).GetStepRateLimit(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return RateLimit{}, false, nil
	}
	if err != nil {
		return RateLimit{}, false, fmt.Errorf("get step rate limit of session %s: %w", id, err)
	}
	return rateLimitOf(row), true, nil
}

func rateLimitOf(row sqlcgen.EngineRateLimit) RateLimit {
	return RateLimit{
		Status: row.Status, Kind: row.Kind, ResetsAt: time.Unix(row.ResetsAt, 0), At: time.Unix(row.At, 0),
	}
}
