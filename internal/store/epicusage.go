package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicUsageSession is one session whose usage an epic spent. Session names the sessions row with that id, and
// ClaudeSession is that row's claude_session, or "" when the row is gone. Shared is true for the session that steers
// the epic or, for a record epic, that called the epic's steps, and false for a step of the epic.
type EpicUsageSession struct {
	Session       string
	ClaudeSession string
	Shared        bool
}

// SessionClaude returns the claude_session of the sessions row with id, of any kind, and true. It returns "" and false,
// and no error, when no row has the id. It returns an error that matches ErrUnavailable when the file is missing or the
// database is busy. It never creates the file and never writes the database.
func (r *Reader) SessionClaude(ctx context.Context, id string) (string, bool, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return "", false, err
	}
	claude, found, err := readSessionClaude(ctx, db, id)
	if closeErr := closeDB(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return "", false, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return "", false, err
	}
	return claude, found, nil
}

// readSessionClaude reads the claude_session of the row with id in one read transaction.
func readSessionClaude(ctx context.Context, db *sql.DB, id string) (string, bool, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", false, fmt.Errorf("begin store read: %w", err)
	}
	claude, err := sqlcgen.New(tx).GetSessionClaude(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, errors.Join(fmt.Errorf("read session %s: %w", id, err), tx.Rollback())
	}
	found := err == nil
	if err = tx.Commit(); err != nil {
		return "", false, fmt.Errorf("end store read: %w", err)
	}
	return claude, found, nil
}

// EpicUsageSessions returns the sessions whose usage epic spent, each once. The steps of epic come first, by session
// id, each with Shared false. Then comes the epic's steerer, with Shared true, when the epic's row names one. An epic
// with no row, or with no steerer, is a record epic, and its shared sessions are each distinct caller of its steps
// that is not itself one of its steps, by session id, with Shared true. A session that is both a step and a shared
// session is returned as a step. It reads epic_sessions never. It does the read in one read transaction on a
// connection it opens and closes. It returns an error that matches ErrUnavailable when the file is missing or the
// database is busy. It never creates the file and never writes the database.
func (r *Reader) EpicUsageSessions(ctx context.Context, epic string) ([]EpicUsageSession, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return nil, err
	}
	sessions, err := readEpicUsageSessions(ctx, db, epic)
	if closeErr := closeDB(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return sessions, nil
}

// readEpicUsageSessions runs the listing of epic in one read transaction.
func readEpicUsageSessions(ctx context.Context, db *sql.DB, epic string) ([]EpicUsageSession, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	queries := sqlcgen.New(tx)
	steps, err := queries.ListEpicStepSessions(ctx, epic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list steps of epic %s: %w", epic, err), tx.Rollback())
	}
	shared, err := epicSharedSessions(ctx, queries, epic)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	listed := make([]EpicUsageSession, 0, len(steps)+len(shared))
	stepIDs := make(map[string]bool, len(steps))
	for _, step := range steps {
		stepIDs[step.Session] = true
		listed = append(listed, EpicUsageSession{Session: step.Session, ClaudeSession: step.ClaudeSession})
	}
	for _, session := range shared {
		if !stepIDs[session.Session] {
			listed = append(listed, session)
		}
	}
	return listed, nil
}

// epicSharedSessions returns the shared sessions of epic, each with Shared true. It returns the steerer alone when
// epic's row names one. Otherwise it returns each distinct caller of epic's steps that is not one of them, by id.
func epicSharedSessions(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]EpicUsageSession, error) {
	steerer, err := queries.GetEpicSteererSession(ctx, epic)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read steerer of epic %s: %w", epic, err)
	}
	if err == nil && steerer.Session != "" {
		return []EpicUsageSession{{Session: steerer.Session, ClaudeSession: steerer.ClaudeSession, Shared: true}}, nil
	}
	callers, err := queries.ListEpicCallerSessions(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list callers of epic %s: %w", epic, err)
	}
	shared := make([]EpicUsageSession, 0, len(callers))
	for _, caller := range callers {
		shared = append(shared, EpicUsageSession{
			Session: caller.Session, ClaudeSession: caller.ClaudeSession, Shared: true,
		})
	}
	return shared, nil
}
