package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HostPanes returns the pane rows stored under host, in id order: each row's id, the tmux server it runs on, its tmux
// pane id and its fields. Engine and step rows are left out. It answers in one query, on a connection it opens and
// closes. It returns an error that matches ErrUnavailable when the file is missing or the database is busy. It never
// creates the file and never writes the database.
func (r *Reader) HostPanes(ctx context.Context, host string) ([]Session, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return nil, err
	}
	rows, err := sqlcgen.New(db).ListHostSessions(ctx, host)
	if closeErr := closeDB(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("list host panes: %w", err)
	}
	panes := make([]Session, 0, len(rows))
	for _, row := range rows {
		panes = append(panes, Session{ID: row.ID, Server: row.Server, Pane: row.Pane, Fields: fieldsOf(row)})
	}
	return panes, nil
}
