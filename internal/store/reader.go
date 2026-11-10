package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrUnavailable reports a store the reader cannot read right now: the file is missing or the database is busy.
var ErrUnavailable = errors.New("store unavailable")

// readBusyTimeoutMillis matches the writer's busy timeout, so a reader waits as long as a writer does.
const readBusyTimeoutMillis = 200

// primaryCodeMask strips the extended bits from a SQLite result code, leaving the primary code.
const primaryCodeMask = 0xff

// Session is one sessions row.
type Session struct {
	ID     string
	Server string
	Pane   string
	Fields Fields
}

// Snapshot is every session row, in id order, with the largest event seq read in the same transaction. Seq is 0
// when there are no events.
type Snapshot struct {
	Sessions []Session
	Seq      int64
}

// Reader reads orreryd's store without writing it. Each Snapshot opens the file at the reader's path, reads it,
// and closes it, so the reader holds no connection between calls and never keeps the writer from being the
// store's last connection.
type Reader struct {
	path string
}

// NewReader returns a Reader for the store file at path. It opens nothing.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// Snapshot reads every sessions row and the newest event seq in one read transaction. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy. It never creates the file or its
// directory, and it never writes the database.
func (r *Reader) Snapshot(ctx context.Context) (Snapshot, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return Snapshot{}, fmt.Errorf("open store reader: %w", err)
	}
	snap, err := readSnapshot(ctx, db)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return Snapshot{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return Snapshot{}, err
	}
	return snap, nil
}

// readOnlyDSN opens the store file read-only, so the reader can never write it or create it.
func readOnlyDSN(path string) string {
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", readBusyTimeoutMillis))
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

// readSnapshot runs both reads in one read transaction, so the rows and the seq describe the same state.
func readSnapshot(ctx context.Context, db *sql.DB) (Snapshot, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin store read: %w", err)
	}
	snap, err := readInTx(ctx, tx)
	if err != nil {
		return Snapshot{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("end store read: %w", err)
	}
	return snap, nil
}

func readInTx(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	queries := sqlcgen.New(tx)
	rows, err := queries.ListSessions(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list sessions: %w", err)
	}
	seq, err := queries.NewestEventSeq(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read newest event seq: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, Session{ID: row.ID, Server: row.Server, Pane: row.Pane, Fields: fieldsOf(row)})
	}
	return Snapshot{Sessions: sessions, Seq: seq}, nil
}

// unavailable reports whether err is a SQLite busy, locked or cannot-open result, which is how a busy database
// or a missing file appears, or an fstat failure, which is how a file removed while it is open appears. Only the
// fstat extended code counts: other I/O errors share the primary IOERR code and stay internal.
func unavailable(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	if sqliteErr.Code() == sqlite3.SQLITE_IOERR_FSTAT {
		return true
	}
	switch sqliteErr.Code() & primaryCodeMask {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED, sqlite3.SQLITE_CANTOPEN:
		return true
	default:
		return false
	}
}
