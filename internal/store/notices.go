package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// NoticeRow is one pending notice. Exactly one of Pane, Session and Epic names what it is about. Time is the unix
// second the notice was recorded. Due is the unix millisecond its phase timer runs out, 0 for none. Phase and
// SourceKind are stored as given; the store does not interpret them.
type NoticeRow struct {
	ID          int64
	Pane        string
	Session     string
	State       string
	Time        int64
	Phase       string
	Due         int64
	DelayServed bool
	WatchLogged bool
	SourceKind  string
	Source      []byte
	Silent      bool
	Epic        string
}

// PutNotice inserts the row named by row.ID, or replaces every column of the row already stored under that id. It
// raises the id floor to row.ID in the same write, so no id ever put is above the floor.
func (s *Store) PutNotice(ctx context.Context, row NoticeRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertPendingNotice(ctx, sqlcgen.UpsertPendingNoticeParams{
			ID: row.ID, Pane: row.Pane, Session: row.Session, State: row.State, Time: row.Time, Phase: row.Phase,
			Due: row.Due, DelayServed: row.DelayServed, WatchLogged: row.WatchLogged, SourceKind: row.SourceKind,
			Source: row.Source, Silent: row.Silent, Epic: row.Epic,
		}); err != nil {
			return fmt.Errorf("put notice %d: %w", row.ID, err)
		}
		if err := queries.RaiseNoticeIDFloor(ctx, row.ID); err != nil {
			return fmt.Errorf("raise notice id floor to %d: %w", row.ID, err)
		}
		return nil
	})
}

// DeleteNotice removes the row with id. An id that is not stored is not an error.
func (s *Store) DeleteNotice(ctx context.Context, id int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePendingNotice(ctx, id); err != nil {
			return fmt.Errorf("delete notice %d: %w", id, err)
		}
		return nil
	})
}

// Notices returns every stored notice ordered by id.
func (s *Store) Notices(ctx context.Context) ([]NoticeRow, error) {
	rows, err := sqlcgen.New(s.db).ListPendingNotices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	out := make([]NoticeRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, NoticeRow{
			ID: row.ID, Pane: row.Pane, Session: row.Session, State: row.State, Time: row.Time, Phase: row.Phase,
			Due: row.Due, DelayServed: row.DelayServed, WatchLogged: row.WatchLogged, SourceKind: row.SourceKind,
			Source: row.Source, Silent: row.Silent, Epic: row.Epic,
		})
	}
	return out, nil
}

// NoticeIDFloor returns the highest id ever put, or 0 when none has been. Deletes do not lower it.
func (s *Store) NoticeIDFloor(ctx context.Context) (int64, error) {
	floor, err := sqlcgen.New(s.db).GetNoticeIDFloor(ctx)
	if err != nil {
		return 0, fmt.Errorf("read notice id floor: %w", err)
	}
	return floor, nil
}
