package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// PushedRow is the last push of one pane or session. Pane and Session together are the key; a row for a pane has
// an empty Session and a row for a session has an empty Pane. Time is the unix second of the push.
type PushedRow struct {
	Pane    string
	Session string
	Time    int64
}

// PutPushed inserts the row for row.Pane and row.Session, or replaces the time of the row already stored under that key.
func (s *Store) PutPushed(ctx context.Context, row PushedRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertPushedNotice(ctx, sqlcgen.UpsertPushedNoticeParams{
			Pane: row.Pane, Session: row.Session, Time: row.Time,
		}); err != nil {
			return fmt.Errorf("put pushed notice %q/%q: %w", row.Pane, row.Session, err)
		}
		return nil
	})
}

// DeletePushed removes the row for pane and session. A key that is not stored is not an error.
func (s *Store) DeletePushed(ctx context.Context, pane, session string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePushedNotice(ctx, sqlcgen.DeletePushedNoticeParams{
			Pane: pane, Session: session,
		}); err != nil {
			return fmt.Errorf("delete pushed notice %q/%q: %w", pane, session, err)
		}
		return nil
	})
}

// Pushed returns every stored push ordered by pane, then session.
func (s *Store) Pushed(ctx context.Context) ([]PushedRow, error) {
	rows, err := sqlcgen.New(s.db).ListPushedNotices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pushed notices: %w", err)
	}
	out := make([]PushedRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, PushedRow{Pane: row.Pane, Session: row.Session, Time: row.Time})
	}
	return out, nil
}
