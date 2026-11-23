package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// MoveEpicBase moves the base and the stored base of epic from from to to, and adds a tell naming both commits, in one
// write. It moves only when the epic's base and stored base both equal from, its branch and candidate are "", and it
// has no task of any status. That guard is the UPDATE's own WHERE clause, so a task or branch added after the caller
// read the epic keeps the base. Otherwise it writes nothing, adds no tell and returns false; it returns true when it
// moved.
func (s *Store) MoveEpicBase(ctx context.Context, epic, from, to string, at time.Time) (bool, error) {
	moved := false
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MoveEpicBase(ctx, sqlcgen.MoveEpicBaseParams{ToBase: to, ID: epic, FromBase: from})
		if err != nil {
			return fmt.Errorf("move base of epic %s: %w", epic, err)
		}
		if rows == 0 {
			return nil
		}
		moved = true
		return insertTell(ctx, queries, epic, fmt.Sprintf("base moved from %s to %s, origin's main", from, to), at)
	})
	if err != nil {
		return false, err
	}
	return moved, nil
}

// TellEpic adds text to epic as one tell, written at at, in one write.
func (s *Store) TellEpic(ctx context.Context, epic, text string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return insertTell(ctx, queries, epic, text, at)
	})
}
