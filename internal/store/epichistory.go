package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicHistory is the history of one epic: every trigger it has, and every tell it holds.
type EpicHistory struct {
	Triggers []EpicTrigger
	Tells    []EpicTell
}

// EpicHistory returns the triggers of epic ordered by creation time then id, and its tells in seq order. An unknown
// epic is ErrNoEpic.
func (s *Store) EpicHistory(ctx context.Context, id string) (EpicHistory, error) {
	queries := sqlcgen.New(s.db)
	if err := requireEpic(ctx, queries, id); err != nil {
		return EpicHistory{}, err
	}
	triggers, err := queries.ListEpicTriggers(ctx, id)
	if err != nil {
		return EpicHistory{}, fmt.Errorf("list triggers of epic %s: %w", id, err)
	}
	tells, err := queries.ListEpicTells(ctx, id)
	if err != nil {
		return EpicHistory{}, fmt.Errorf("list tells of epic %s: %w", id, err)
	}
	return EpicHistory{Triggers: mapInOrder(triggers, triggerOf), Tells: mapInOrder(tells, tellOf)}, nil
}

// tellOf returns the tell a stored row holds, told or not.
func tellOf(tell sqlcgen.EpicTell) EpicTell {
	return EpicTell{Seq: tell.Seq, Text: tell.Text, At: time.Unix(tell.At, 0), Told: tell.Told != 0}
}
