package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ClosureVerdict is the newest closure verdict of one pane or headless session: its Changed stamp, Closed flag and Why.
type ClosureVerdict struct {
	Session string
	Changed int64
	Closed  bool
	Why     string
}

// ClosureSession is the state of one engine session and its changed stamp as unix seconds.
type ClosureSession struct {
	State   string
	Changed int64
}

// SetClosureVerdict stores verdict as the newest closure verdict of its session at at, replacing the verdict an
// earlier call stored for that session. An empty session is ErrEmptyRowID.
func (s *Store) SetClosureVerdict(ctx context.Context, verdict ClosureVerdict, at time.Time) error {
	if verdict.Session == "" {
		return fmt.Errorf("closure verdict: %w", ErrEmptyRowID)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertClosureVerdict(ctx, sqlcgen.UpsertClosureVerdictParams{
			SessionID: verdict.Session, Changed: verdict.Changed, Closed: boolInt(verdict.Closed), Why: verdict.Why,
			At: at.Unix(),
		}); err != nil {
			return fmt.Errorf("set closure verdict of %s: %w", verdict.Session, err)
		}
		return nil
	})
}

// ClosureVerdicts returns the newest closure verdict of every session, ordered by session. It does the read in one
// read transaction on a connection it opens and closes. It returns an error that matches ErrUnavailable when the file
// is missing or the database is busy. It never creates the file and never writes the database.
func (r *Reader) ClosureVerdicts(ctx context.Context) ([]ClosureVerdict, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	verdicts, err := readClosureVerdicts(ctx, db)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return verdicts, nil
}

// readClosureVerdicts runs the listing of the verdicts in one read transaction.
func readClosureVerdicts(ctx context.Context, db *sql.DB) ([]ClosureVerdict, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListClosureVerdicts(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list closure verdicts: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	verdicts := make([]ClosureVerdict, 0, len(rows))
	for _, row := range rows {
		verdicts = append(verdicts, ClosureVerdict{
			Session: row.SessionID, Changed: row.Changed, Closed: row.Closed != 0, Why: row.Why,
		})
	}
	return verdicts, nil
}

// ClosureSession returns the state and changed stamp of the engine session with id. It returns false and no error
// when no engine session has id, so a pane or step session is not found. It returns an error when the changed stamp is
// not unix seconds.
func (s *Store) ClosureSession(ctx context.Context, id string) (ClosureSession, bool, error) {
	row, err := sqlcgen.New(s.db).GetClosureEngineSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ClosureSession{}, false, nil
	}
	if err != nil {
		return ClosureSession{}, false, fmt.Errorf("get closure session %s: %w", id, err)
	}
	changed, err := strconv.ParseInt(row.Changed, 10, 64)
	if err != nil {
		return ClosureSession{}, false, fmt.Errorf("changed of engine session %s: %w", id, err)
	}
	return ClosureSession{State: row.State, Changed: changed}, true, nil
}
