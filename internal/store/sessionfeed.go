package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// sessionChange is what a committed write did to the session feed: the largest seq of the events rows it inserted, 0
// when it inserted none, and the sorted, distinct session ids the write names (feedsSession and addSteerers). A write
// that names no session has no sessions, and the hook is not called for it.
type sessionChange struct {
	seq      int64
	sessions []string
}

// sessionRow is a row of the session feed's tables that the armed write wrote: its table, its seq (the rowid of an
// events row it inserted), and its values as the hook read them. The hook cannot read column names, so the flush reads
// session_id from the values by the column's name.
type sessionRow struct {
	table  string
	seq    int64
	values []any
}

// OnSessionEvents sets fn as the hook a store write that commits calls, once, after it commits and its connection is
// free again. seq is the largest seq of the events rows the write inserted, 0 when it inserted none, so a write that
// changed owed state with no event appended has seq 0. sessions is the sorted, distinct session ids the write names:
// the session of each events row it inserted, each engine text row it inserted, updated or deleted, each caller event,
// put-off mark and prompt state it inserted, updated or deleted, the caller of each step it inserted or updated when
// the caller is set, the session of each epic session it inserted or updated, and the steerer session of each epic
// whose revision it moved, when the epic has one. The prune of old events, which names no session, calls nothing for
// its deletes. A write that rolls back, fails to commit or names no session calls nothing. A nil fn clears the hook.
// It is safe to call while writes run.
func (s *Store) OnSessionEvents(fn func(seq int64, sessions []string)) {
	if fn == nil {
		s.sessionEvents.Store(nil)
		return
	}
	s.sessionEvents.Store(&fn)
}

// fireSessionEvents calls the hook OnSessionEvents set with change's seq and sessions, when one is set and change names
// a session.
func (s *Store) fireSessionEvents(change sessionChange) {
	if len(change.sessions) == 0 {
		return
	}
	if fn := s.sessionEvents.Load(); fn != nil {
		(*fn)(change.seq, change.sessions)
	}
}

// stepsTable is the table of steps, whose caller column names the session that started a step.
const stepsTable = "steps"

// feedsSession reports whether the session feed names the row data writes: an events row an insert wrote; an
// engine_text, caller_events, put_off_questions or prompt_states row any write wrote; and a steps or epic_sessions row
// an insert or update wrote. A steps row's caller and an epic session's session name nothing when empty
// (flushSessions).
func feedsSession(data *sqlite.SQLitePreUpdateData) bool {
	switch data.TableName {
	case "events":
		return data.Op == sqlite3.SQLITE_INSERT
	case "engine_text", "caller_events", "put_off_questions", "prompt_states":
		return true
	case stepsTable, "epic_sessions":
		return data.Op != sqlite3.SQLITE_DELETE
	default:
		return false
	}
}

// sessionColumnName is the name of the column of table that holds the session a row of the feed names: caller for
// steps, session for caller_events and epic_sessions, and session_id for the events, engine_text, put_off_questions and
// prompt_states tables.
func sessionColumnName(table string) string {
	switch table {
	case stepsTable:
		return "caller"
	case "caller_events", "epic_sessions":
		return "session"
	default:
		return "session_id"
	}
}

// recordSession records the row data writes when the session feed names it, while the write is armed: the row's values
// and, for an events row, its seq. The pre-update hook has no column names, so the flush finds the session's column
// among the values by its name (sessionColumnName). A row it cannot read sets the error the flush returns, which fails
// the write.
func (m *epicMoves) recordSession(data *sqlite.SQLitePreUpdateData) {
	if data.DatabaseName != "main" || !feedsSession(data) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.armed {
		return
	}
	read := data.New
	if data.Op == sqlite3.SQLITE_DELETE {
		read = removedRow(data)
	}
	values := make([]any, data.Count())
	if err := read(values...); err != nil {
		m.sessionErr = errors.Join(m.sessionErr, fmt.Errorf("read a written row of %s: %w", data.TableName, err))
		return
	}
	row := sessionRow{table: data.TableName, values: values}
	if data.TableName == "events" {
		row.seq = data.NewRowID
	}
	m.sessionRows = append(m.sessionRows, row)
}

// flushSessions returns what the armed write did to the session feed: the largest seq of its events rows and the
// sorted, distinct session ids its feed rows name, with the steerer of each epic in moved, which the write moved and
// which is read now, in the write. It resets the record, and returns the error the hook met, if any.
func (m *epicMoves) flushSessions(ctx context.Context, db sqlcgen.DBTX, moved []string) (sessionChange, error) {
	m.mu.Lock()
	rows, hookErr := m.sessionRows, m.sessionErr
	m.sessionRows, m.sessionErr = nil, nil
	m.mu.Unlock()
	if hookErr != nil {
		return sessionChange{}, hookErr
	}
	var change sessionChange
	var ids map[string]struct{}
	for _, row := range rows {
		column, err := m.sessionColumn(ctx, db, row.table)
		if err != nil {
			return sessionChange{}, err
		}
		id, err := sessionIDOf(row, column)
		if err != nil {
			return sessionChange{}, err
		}
		if id != "" {
			ids = addTo(ids, id)
		}
		change.seq = max(change.seq, row.seq)
	}
	ids, err := addSteerers(ctx, db, ids, moved)
	if err != nil {
		return sessionChange{}, err
	}
	change.sessions = sortedKeys(ids)
	return change, nil
}

// addSteerers adds to ids the steerer session of each epic in epics, read as EpicSteerer reads a steerer: the epic's
// steerer, when that session has a sessions row. An epic with no steerer adds nothing.
func addSteerers(
	ctx context.Context, db sqlcgen.DBTX, ids map[string]struct{}, epics []string,
) (map[string]struct{}, error) {
	queries := sqlcgen.New(db)
	for _, epic := range epics {
		steerer, err := queries.GetEpicSteerer(ctx, epic)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read steerer of epic %s: %w", epic, err)
		}
		ids = addTo(ids, steerer.ID)
	}
	return ids, nil
}

// sessionIDOf returns the session id row holds at column, which must be text.
func sessionIDOf(row sessionRow, column int) (string, error) {
	if column >= len(row.values) {
		return "", fmt.Errorf("written row of %s has no column %d", row.table, column)
	}
	id, ok := row.values[column].(string)
	if !ok {
		return "", fmt.Errorf("session of a written row of %s is not text", row.table)
	}
	return id, nil
}

// sessionColumn returns the column of table that holds a row's session, found by the column's name (sessionColumnName).
// It is read the first time the connection writes a row of table.
func (m *epicMoves) sessionColumn(ctx context.Context, db sqlcgen.DBTX, table string) (int, error) {
	if column, ok := m.sessionColumns[table]; ok {
		return column, nil
	}
	columns, err := readKeyColumns(ctx, db.QueryContext, table)
	if err != nil {
		return 0, err
	}
	name := sessionColumnName(table)
	column := slices.Index(columns, name)
	if column < 0 {
		return 0, fmt.Errorf("table %s has no %s column", table, name)
	}
	if m.sessionColumns == nil {
		m.sessionColumns = map[string]int{}
	}
	m.sessionColumns[table] = column
	return column, nil
}
