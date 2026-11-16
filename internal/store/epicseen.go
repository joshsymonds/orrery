package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// SeeEpic stores at as the time epic was last seen, replacing the mark an earlier SeeEpic stored. An unknown epic is
// ErrNoEpic.
func (s *Store) SeeEpic(ctx context.Context, epic string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		if err := queries.UpsertEpicSeen(ctx, sqlcgen.UpsertEpicSeenParams{Epic: epic, At: at.Unix()}); err != nil {
			return fmt.Errorf("see epic %s: %w", epic, err)
		}
		return nil
	})
}

// loadEpicSeen fills the seen mark of row from the row stored under row.ID. Row keeps a zero Seen when no mark is
// stored.
func loadEpicSeen(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	at, err := queries.GetEpicSeen(ctx, row.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get seen: %w", err)
	}
	row.Seen = time.Unix(at, 0)
	return nil
}
