package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// PushedRow is the last push of one pane, one session, one epic or one usage limit. Exactly one of Pane, Session,
// Epic and Limit is set: a pane row has an empty Session, Epic and Limit, a session row an empty Pane, Epic and Limit,
// an epic row an empty Pane, Session and Limit, and a limit row an empty Pane, Session and Epic. Time is the unix
// second of the push.
type PushedRow struct {
	Pane    string
	Session string
	Epic    string
	Limit   string
	Time    int64
}

// PutPushed inserts the row for row's pane, session, epic or limit, or replaces the time of the row already stored
// under that key.
func (s *Store) PutPushed(ctx context.Context, row PushedRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if row.Epic != "" {
			if err := queries.UpsertPushedEpicNotice(ctx, sqlcgen.UpsertPushedEpicNoticeParams{
				Epic: row.Epic, Time: row.Time,
			}); err != nil {
				return fmt.Errorf("put pushed epic notice %q: %w", row.Epic, err)
			}
			return nil
		}
		if row.Limit != "" {
			if err := queries.UpsertPushedLimitNotice(ctx, sqlcgen.UpsertPushedLimitNoticeParams{
				LimitKey: row.Limit, Time: row.Time,
			}); err != nil {
				return fmt.Errorf("put pushed limit notice %q: %w", row.Limit, err)
			}
			return nil
		}
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

// DeletePushedEpic removes the row for epic. A key that is not stored is not an error.
func (s *Store) DeletePushedEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePushedEpicNotice(ctx, epic); err != nil {
			return fmt.Errorf("delete pushed epic notice %q: %w", epic, err)
		}
		return nil
	})
}

// DeletePushedLimit removes the row for limit. A limit that is not stored is not an error.
func (s *Store) DeletePushedLimit(ctx context.Context, limit string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePushedLimitNotice(ctx, limit); err != nil {
			return fmt.Errorf("delete pushed limit notice %q: %w", limit, err)
		}
		return nil
	})
}

// Pushed returns every stored push: the pane and session rows ordered by pane, then session, then the epic rows
// ordered by epic, then the limit rows ordered by limit. The three tables are read in one read transaction, so they
// come from one snapshot.
func (s *Store) Pushed(ctx context.Context) ([]PushedRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin pushed read: %w", err)
	}
	queries := sqlcgen.New(tx)
	rows, err := queries.ListPushedNotices(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list pushed notices: %w", err), tx.Rollback())
	}
	epics, err := queries.ListPushedEpicNotices(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list pushed epic notices: %w", err), tx.Rollback())
	}
	limits, err := queries.ListPushedLimitNotices(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list pushed limit notices: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end pushed read: %w", err)
	}
	out := make([]PushedRow, 0, len(rows)+len(epics)+len(limits))
	for _, row := range rows {
		out = append(out, PushedRow{Pane: row.Pane, Session: row.Session, Time: row.Time})
	}
	for _, row := range epics {
		out = append(out, PushedRow{Epic: row.Epic, Time: row.Time})
	}
	for _, row := range limits {
		out = append(out, PushedRow{Limit: row.LimitKey, Time: row.Time})
	}
	return out, nil
}
