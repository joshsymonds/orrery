package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrEpicWriteOutsideWrite reports that a row was written on a store connection outside a store write, to a table
// that names an epic or one no store write had touched yet, so no revision may have moved for it. Close returns it,
// naming the tables, so a write path that bypasses Store.write cannot go unseen.
var ErrEpicWriteOutsideWrite = errors.New("table written outside a store write")

// revisionUncounted reports whether table is one of the tables with an epic column whose writes move no revision: the
// revisions themselves, the steps, which a list of the epics carries by their running steps and a count of their ended
// ones, and the notice bookkeeping, which no epic read answers. The pre-update hook asks it for every written row, so it
// allocates nothing.
func revisionUncounted(table string) bool {
	switch table {
	case "epic_revisions", "pending_notices", "pushed_epic_notices", "steps":
		return true
	default:
		return false
	}
}

// epicKey says whether a written row of a table names an epic, and how: by the column that holds the epic's id, or,
// for byRun, by the column that holds the id of the evidence run whose epic it is.
type epicKey struct {
	counts bool
	column int
	byRun  bool
}

// epicColumn is the column a table that names an epic names it by, but epics and evidence_artifacts (keyColumn).
const epicColumn = "epic"

// keyColumn returns the column of table that names a row's epic, its name and whether it names it through a run: id
// for epics, run for evidence_artifacts, and epic for any other table.
func keyColumn(table string) (string, bool) {
	switch table {
	case "epics":
		return "id", false
	case "evidence_artifacts":
		return "run", true
	default:
		return epicColumn, false
	}
}

// keyOf returns the epic key of table, whose columns are columns in order: no key for an uncounted table or one with
// no column that names an epic.
func keyOf(table string, columns []string) epicKey {
	if revisionUncounted(table) {
		return epicKey{}
	}
	name, byRun := keyColumn(table)
	column := slices.Index(columns, name)
	if column < 0 {
		return epicKey{}
	}
	return epicKey{counts: true, column: column, byRun: byRun}
}

// columnsQuery returns a statement that reads no row of table and whose columns are table's.
func columnsQuery(table string) string {
	return `SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `" LIMIT 0`
}

// readKeyColumns returns the column names of table, read through query.
func readKeyColumns(ctx context.Context, query func(context.Context, string, ...any) (*sql.Rows, error), table string,
) (columns []string, err error) {
	rows, err := query(ctx, columnsQuery(table))
	if err != nil {
		return nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if columns, err = rows.Columns(); err != nil {
		return nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	return columns, nil
}

// epicKeys is a store's epic key of each table a write has touched, read from the table's columns the first time a
// store write touches it, so a table added with an epic column counts with no change to the store.
type epicKeys struct {
	mu   sync.Mutex
	keys map[string]epicKey
}

// get returns the key of table, and whether it has been read.
func (k *epicKeys) get(table string) (epicKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.keys[table]
	return key, ok
}

// resolve returns the key of table, reading its columns through query the first time.
func (k *epicKeys) resolve(
	ctx context.Context, query func(context.Context, string, ...any) (*sql.Rows, error), table string,
) (epicKey, error) {
	if key, ok := k.get(table); ok {
		return key, nil
	}
	columns, err := readKeyColumns(ctx, query, table)
	if err != nil {
		return epicKey{}, err
	}
	key := keyOf(table, columns)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.keys == nil {
		k.keys = map[string]epicKey{}
	}
	k.keys[table] = key
	return key, nil
}

// unresolvedRow is a row written to a table whose key was not read yet: its table, its rowid for an insert or an
// update, whose row the flush reads, and the row a delete removed, whose values the flush reads instead.
type unresolvedRow struct {
	table   string
	rowid   int64
	deleted []any
}

// epicMoves records, on one connection, what the rows written in its open store write name. The connection's
// pre-update hook records into it, Store.write arms it when its write begins and takes it before the write commits.
// A row written while it is not armed is counted as written outside a store write, by table.
type epicMoves struct {
	keys *epicKeys
	// counting reports whether the store records epics at all (trackedConnector.counting).
	counting func() bool

	mu         sync.Mutex
	armed      bool
	epics      map[string]struct{}
	runs       map[string]struct{}
	unresolved []unresolvedRow
	outside    map[string]int
	// sessionRows are the rows of the session feed's tables the armed write wrote (see recordSession), and sessionErr is
	// the error the hook met reading one. sessionColumns is the column that holds each table's session, by table, once
	// read; only the write running on the connection reads it.
	sessionRows    []sessionRow
	sessionErr     error
	sessionColumns map[string]int
}

// hook is the connection's pre-update hook. A row of a table whose key is read records the epic or the run it names:
// the new row's for an insert or an update, since no update moves a row to another epic, and the old row's for a
// delete. A row of a table whose key is not read yet is kept for the flush to resolve. The session feed's rows are
// recorded first, before the early returns below, which skip the tables that name no epic.
func (m *epicMoves) hook(data sqlite.SQLitePreUpdateData) {
	m.recordSession(&data)
	if data.DatabaseName != "main" || !m.counting() || revisionUncounted(data.TableName) {
		return
	}
	key, known := m.keys.get(data.TableName)
	if known && !key.counts {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.armed {
		if m.outside == nil {
			m.outside = map[string]int{}
		}
		m.outside[data.TableName]++
		return
	}
	if !known {
		m.unresolved = append(m.unresolved, unresolvedOf(&data))
		return
	}
	read := data.New
	if data.Op == sqlite3.SQLITE_DELETE {
		read = removedRow(&data)
	}
	m.add(key, textAt(read, key.column))
}

// removedRow returns the reader of the row data's delete removes. The driver names it Old, after SQLite's
// sqlite3_preupdate_old.
func removedRow(data *sqlite.SQLitePreUpdateData) func(dest ...any) error {
	return data.Old
}

// unresolvedOf returns the row data writes, kept for the flush: its rowid, or every value of the row a delete removes.
func unresolvedOf(data *sqlite.SQLitePreUpdateData) unresolvedRow {
	row := unresolvedRow{table: data.TableName, rowid: data.NewRowID}
	if data.Op == sqlite3.SQLITE_DELETE {
		row.deleted = make([]any, data.Count())
		if err := removedRow(data)(row.deleted...); err != nil {
			row.deleted = nil
		}
	}
	return row
}

// add records id as an epic, or as an evidence run when key names epics by run. An empty id names nothing.
func (m *epicMoves) add(key epicKey, id string) {
	if id == "" {
		return
	}
	if key.byRun {
		m.runs = addTo(m.runs, id)
		return
	}
	m.epics = addTo(m.epics, id)
}

// addTo returns set with id added, made when it is nil.
func addTo(set map[string]struct{}, id string) map[string]struct{} {
	if set == nil {
		set = map[string]struct{}{}
	}
	set[id] = struct{}{}
	return set
}

// textAt returns the value at column of the row read reads, as text, or "" when it is not text.
func textAt(read func(dest ...any) error, column int) string {
	dest := make([]any, column+1)
	if err := read(dest...); err != nil {
		return ""
	}
	text, ok := dest[column].(string)
	if !ok {
		return ""
	}
	return text
}

// arm starts recording a write: nothing is recorded yet.
func (m *epicMoves) arm() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.armed = true
	m.epics, m.runs, m.unresolved = nil, nil, nil
	m.sessionRows, m.sessionErr = nil, nil
}

// disarm ends the write's recording and drops what it recorded, as a write that rolls back does.
func (m *epicMoves) disarm() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.armed = false
	m.epics, m.runs, m.unresolved = nil, nil, nil
	m.sessionRows, m.sessionErr = nil, nil
}

// take ends the write's recording and returns what it recorded.
func (m *epicMoves) take() (map[string]struct{}, map[string]struct{}, []unresolvedRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.armed = false
	epics, runs, unresolved := m.epics, m.runs, m.unresolved
	m.epics, m.runs, m.unresolved = nil, nil, nil
	return epics, runs, unresolved
}

// outsideTables returns the tables written outside a store write, each with how many rows.
func (m *epicMoves) outsideTables() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps(m.outside)
}

// maps returns a copy of counts.
func maps(counts map[string]int) map[string]int {
	out := make(map[string]int, len(counts))
	for table, n := range counts {
		out[table] += n
	}
	return out
}

// sortedKeys returns the keys of set, sorted.
func sortedKeys[V any](set map[string]V) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// movesOf returns the epic moves of the store connection conn holds.
func movesOf(conn *sql.Conn) (*epicMoves, error) {
	var moves *epicMoves
	err := conn.Raw(func(driverConn any) error {
		tracked, ok := driverConn.(*trackedConn)
		if !ok {
			return fmt.Errorf("store connection %T is not the store's", driverConn)
		}
		moves = tracked.moves
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reach the store connection: %w", err)
	}
	return moves, nil
}

// flush moves the revision of each epic the write named by one, in the write: the epics its rows named, the epics of
// the evidence runs its artifacts named, and those its rows of tables whose key was not read yet name, which it reads
// now. A write moves an epic's revision once however many of its rows it wrote, since a reader compares the revision
// only to see that the epic changed. It returns the epics it moved, sorted.
func (m *epicMoves) flush(ctx context.Context, db sqlcgen.DBTX) ([]string, error) {
	epics, runs, unresolved := m.take()
	for _, row := range unresolved {
		key, err := m.keys.resolve(ctx, db.QueryContext, row.table)
		if err != nil {
			return nil, err
		}
		if !key.counts {
			continue
		}
		id, err := unresolvedID(ctx, db, row, key)
		if err != nil {
			return nil, err
		}
		switch {
		case id == "":
		case key.byRun:
			runs = addTo(runs, id)
		default:
			epics = addTo(epics, id)
		}
	}
	queries := sqlcgen.New(db)
	moved := sortedKeys(epics)
	if len(runs) > 0 {
		runEpics, err := queries.ListEvidenceRunEpics(ctx, sortedKeys(runs))
		if err != nil {
			return nil, fmt.Errorf("read the epics of written evidence runs: %w", err)
		}
		moved = append(moved, runEpics...)
		slices.Sort(moved)
		moved = slices.Compact(moved)
	}
	for _, epic := range moved {
		if err := queries.MoveEpicRevision(ctx, epic); err != nil {
			return nil, fmt.Errorf("move the revision of epic %s: %w", epic, err)
		}
	}
	return moved, nil
}

// unresolvedID returns the id row names by key: the deleted row's value, or the written row's, read by its rowid.
func unresolvedID(ctx context.Context, db sqlcgen.DBTX, row unresolvedRow, key epicKey) (string, error) {
	if row.deleted != nil {
		if key.column < len(row.deleted) {
			if text, ok := row.deleted[key.column].(string); ok {
				return text, nil
			}
		}
		return "", nil
	}
	name, _ := keyColumn(row.table)
	var id sql.NullString
	query := `SELECT "` + name + `" FROM "` + strings.ReplaceAll(row.table, `"`, `""`) + `" WHERE rowid = ?`
	err := db.QueryRowContext(ctx, query, row.rowid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the epic of a written row of %s: %w", row.table, err)
	}
	return id.String, nil
}

// OnEpicsChanged sets fn as the hook a store write that moved any epic's revision calls, once, after it commits and its
// connection is free again, with the ids of the epics it moved, sorted. A write that rolls back, fails to commit or
// moves no epic calls nothing. A nil fn clears the hook. It is safe to call while writes run.
func (s *Store) OnEpicsChanged(fn func(epics []string)) {
	if fn == nil {
		s.epicsChanged.Store(nil)
		return
	}
	s.epicsChanged.Store(&fn)
}

// fireEpicsChanged calls the hook OnEpicsChanged set with epics, when one is set and epics is not empty.
func (s *Store) fireEpicsChanged(epics []string) {
	if len(epics) == 0 {
		return
	}
	if fn := s.epicsChanged.Load(); fn != nil {
		(*fn)(epics)
	}
}

// outsideWrites returns the tables written outside a store write on any of the store's connections, each with how many
// rows, as text, or "" when none was: each table that names an epic, or that no store write had touched when the row
// was written, since its key was not read yet.
func (s *Store) outsideWrites() string {
	counts := s.connector.outsideTables()
	parts := make([]string, 0, len(counts))
	for _, table := range sortedKeys(counts) {
		parts = append(parts, fmt.Sprintf("%s (%d)", table, counts[table]))
	}
	return strings.Join(parts, ", ")
}
