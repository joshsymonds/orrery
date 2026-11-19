package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// sessionChange is what a committed write did to the session feed: the largest seq of the events rows it inserted, 0
// when it inserted none, and the sorted, distinct session ids of those rows and of the engine text rows it wrote. A
// write that wrote neither table's row has no sessions, and the hook is not called for it.
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

// OnSessionEvents sets fn as the hook a store write that appends an event or writes an engine text row calls, once,
// after it commits and its connection is free again. seq is the largest seq of the events rows the write inserted, 0
// when it inserted none, and sessions is the sorted, distinct session ids of those rows and of the engine text rows it
// inserted, updated or deleted. The prune of old events, which names no session, calls nothing for its deletes. A
// write that rolls back, fails to commit or touches neither table calls nothing. A nil fn clears the hook. It is safe
// to call while writes run.
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

// feedsSession reports whether the session feed names the row data writes: an events row an insert wrote, and an
// engine_text row an insert, update or delete wrote.
func feedsSession(data *sqlite.SQLitePreUpdateData) bool {
	switch data.TableName {
	case "events":
		return data.Op == sqlite3.SQLITE_INSERT
	case "engine_text":
		return true
	default:
		return false
	}
}

// recordSession records the row data writes when the session feed names it, while the write is armed: the row's values
// and, for an events row, its seq. The pre-update hook has no column names, so the flush finds session_id among the
// values. A row it cannot read sets the error the flush returns, which fails the write.
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
// sorted, distinct session ids of its feed rows. It resets the record, and returns the error the hook met, if any.
func (m *epicMoves) flushSessions(ctx context.Context, db sqlcgen.DBTX) (sessionChange, error) {
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
		ids = addTo(ids, id)
		change.seq = max(change.seq, row.seq)
	}
	change.sessions = sortedKeys(ids)
	return change, nil
}

// sessionIDOf returns the session id row holds at column, which must be text.
func sessionIDOf(row sessionRow, column int) (string, error) {
	if column >= len(row.values) {
		return "", fmt.Errorf("written row of %s has no column %d", row.table, column)
	}
	id, ok := row.values[column].(string)
	if !ok {
		return "", fmt.Errorf("session_id of a written row of %s is not text", row.table)
	}
	return id, nil
}

// sessionColumn returns the column of table that holds a row's session id, found by the column's name session_id. It
// is read the first time the connection writes a row of table.
func (m *epicMoves) sessionColumn(ctx context.Context, db sqlcgen.DBTX, table string) (int, error) {
	if column, ok := m.sessionColumns[table]; ok {
		return column, nil
	}
	columns, err := readKeyColumns(ctx, db.QueryContext, table)
	if err != nil {
		return 0, err
	}
	column := slices.Index(columns, "session_id")
	if column < 0 {
		return 0, fmt.Errorf("table %s has no session_id column", table)
	}
	if m.sessionColumns == nil {
		m.sessionColumns = map[string]int{}
	}
	m.sessionColumns[table] = column
	return column, nil
}
