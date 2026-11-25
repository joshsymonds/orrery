package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// PushedRow is the last push of one pane, one session, one epic, one usage limit or one alert. Exactly one of Pane,
// Session, Epic, Limit and Alert is set: a pane row has an empty Session, Epic, Limit and Alert, a session row an empty
// Pane, Epic, Limit and Alert, an epic row an empty Pane, Session, Limit and Alert, a limit row an empty Pane, Session,
// Epic and Alert, and an alert row an empty Pane, Session, Epic and Limit. Host is the orrery name of the pane's host,
// set with Pane and only with Pane. Time is the unix second of the push.
type PushedRow struct {
	Host    string
	Pane    string
	Session string
	Epic    string
	Limit   string
	Alert   string
	Time    int64
}

// PutPushed inserts the row for row's pane, session, epic, limit or alert, or replaces the time of the row already
// stored under that key. A pane's key includes its host.
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
		if row.Alert != "" {
			if err := queries.UpsertPushedAlertNotice(ctx, sqlcgen.UpsertPushedAlertNoticeParams{
				Alert: row.Alert, Time: row.Time,
			}); err != nil {
				return fmt.Errorf("put pushed alert notice %q: %w", row.Alert, err)
			}
			return nil
		}
		if err := queries.UpsertPushedNotice(ctx, sqlcgen.UpsertPushedNoticeParams{
			Host: row.Host, Pane: row.Pane, Session: row.Session, Time: row.Time,
		}); err != nil {
			return fmt.Errorf("put pushed notice %q/%q: %w", row.Pane, row.Session, err)
		}
		return nil
	})
}

// DeletePushed removes the row for host, pane and session. A key that is not stored is not an error.
func (s *Store) DeletePushed(ctx context.Context, host, pane, session string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePushedNotice(ctx, sqlcgen.DeletePushedNoticeParams{
			Host: host, Pane: pane, Session: session,
		}); err != nil {
			return fmt.Errorf("delete pushed notice %q/%q/%q: %w", host, pane, session, err)
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

// DeletePushedAlert removes the row for alert. An alert that is not stored is not an error.
func (s *Store) DeletePushedAlert(ctx context.Context, alert string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePushedAlertNotice(ctx, alert); err != nil {
			return fmt.Errorf("delete pushed alert notice %q: %w", alert, err)
		}
		return nil
	})
}

// Pushed returns every stored push: the pane and session rows ordered by host, then pane, then session, then the
// epic rows ordered by epic, then the limit rows ordered by limit, then the alert rows ordered by alert. The four
// tables are read in one read transaction, so they come from one snapshot.
func (s *Store) Pushed(ctx context.Context) ([]PushedRow, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
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
	alerts, err := queries.ListPushedAlertNotices(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list pushed alert notices: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end pushed read: %w", err)
	}
	out := make([]PushedRow, 0, len(rows)+len(epics)+len(limits)+len(alerts))
	for _, row := range rows {
		out = append(out, PushedRow{Host: row.Host, Pane: row.Pane, Session: row.Session, Time: row.Time})
	}
	for _, row := range epics {
		out = append(out, PushedRow{Epic: row.Epic, Time: row.Time})
	}
	for _, row := range limits {
		out = append(out, PushedRow{Limit: row.LimitKey, Time: row.Time})
	}
	for _, row := range alerts {
		out = append(out, PushedRow{Alert: row.Alert, Time: row.Time})
	}
	return out, nil
}
