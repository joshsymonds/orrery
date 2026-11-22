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

// readBusyTimeoutMillis is how long a reader waits for a lock. In WAL mode a read never waits for the write lock, the
// one Litestream's checkpoints take, so a reader is busy only while a connection holds the file exclusively. A reader
// serves the orrery CLI, which reports the store unavailable at once rather than waiting out a writer's BusyTimeout.
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

// Reader reads orreryd's store without writing it. A reader from NewReader opens the file at its path on each call,
// reads it and closes it, so it holds no connection between calls and never keeps the writer from being the store's
// last connection. A reader from NewPooledReader keeps one read-only pool open until Close, so its reads reuse the
// connections an earlier read left open. Serve's reader holds connections to the store, so serve is stopped before the
// store file is replaced (a restore), orreryd closes last, and both are started after the store file is replaced.
type Reader struct {
	path string
	// pool is the read-only pool of a reader from NewPooledReader, and nil for a reader from NewReader. connector is the
	// pool's connector, which Close waits on.
	pool      *sql.DB
	connector *trackedConnector
	// poolErr is why NewPooledReader could not make its pool. Each read returns it.
	poolErr error
	// at, when set, runs at each point of a Snapshot or Events read (see readPoint). It is a test seam: production
	// leaves it nil.
	at func(readPoint)
}

// readPoint names a point in a Reader's read at which a test may act, between the read's calls into SQLite.
type readPoint string

const (
	// readOpened is once the database handle exists, before its connection is made.
	readOpened readPoint = "opened"
	// readBegun is once the read transaction has begun on the connection.
	readBegun readPoint = "begun"
	// readQueried is once the transaction's first query has returned.
	readQueried readPoint = "queried"
)

// NewReader returns a Reader for the store file at path. It opens nothing.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// NewPooledReader returns a Reader for the store file at path whose reads share one read-only pool of at most readConns
// connections. The pool opens its first connection for the first read, and a later read reuses a connection the pool
// holds. The reader holds those connections until Close.
func NewPooledReader(path string) *Reader {
	inner, err := sqlite.NewConnector(readOnlyDSN(path))
	if err != nil {
		return &Reader{path: path, poolErr: fmt.Errorf("open store reader: %w", err)}
	}
	connector := &trackedConnector{inner: inner, readPool: true}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(readConns)
	pool.SetMaxIdleConns(readConns)
	return &Reader{path: path, pool: pool, connector: connector}
}

// Close closes a pooled reader's pool. It refuses new connections, closes the pool and returns once the connections
// the pool made are closed. A reader from NewReader holds no connection, so Close returns nil for it.
func (r *Reader) Close() error {
	if r.pool == nil {
		return nil
	}
	r.connector.beginClose()
	err := r.pool.Close()
	r.connector.wait()
	if err != nil {
		return fmt.Errorf("close store reader: %w", err)
	}
	return nil
}

// database returns the database a read runs on, and closeDB, which the read calls once it has run. A pooled reader's
// database is its pool, and its closeDB leaves the pool open for the next read. Any other reader opens a connection for
// the read, and its closeDB closes that connection.
func (r *Reader) database() (*sql.DB, func() error, error) {
	if r.poolErr != nil {
		return nil, nil, r.poolErr
	}
	if r.pool != nil {
		return r.pool, func() error { return nil }, nil
	}
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, nil, fmt.Errorf("open store reader: %w", err)
	}
	return db, db.Close, nil
}

// reach runs r.at at point when it is set.
func (r *Reader) reach(point readPoint) {
	if r.at != nil {
		r.at(point)
	}
}

// Snapshot reads every sessions row and the newest event seq in one read transaction. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy. It never creates the file or its
// directory, and it never writes the database.
func (r *Reader) Snapshot(ctx context.Context) (Snapshot, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return Snapshot{}, err
	}
	r.reach(readOpened)
	snap, err := readSnapshot(ctx, db, r.reach)
	if closeErr := closeDB(); closeErr != nil {
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
	return readOnlyDSNBusy(path, readBusyTimeoutMillis)
}

// readOnlyDSNBusy is readOnlyDSN with busyTimeoutMillis as the busy timeout, in milliseconds, in place of the reader's.
func readOnlyDSNBusy(path string, busyTimeoutMillis int64) string {
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

// readSnapshot runs both reads in one read transaction, so the rows and the seq describe the same state.
func readSnapshot(ctx context.Context, db *sql.DB, reach func(readPoint)) (Snapshot, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin store read: %w", err)
	}
	reach(readBegun)
	snap, err := readInTx(ctx, tx, reach)
	if err != nil {
		return Snapshot{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("end store read: %w", err)
	}
	return snap, nil
}

func readInTx(ctx context.Context, tx *sql.Tx, reach func(readPoint)) (Snapshot, error) {
	queries := sqlcgen.New(tx)
	rows, err := queries.ListSessions(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list sessions: %w", err)
	}
	reach(readQueried)
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

// Busy reports whether err is a SQLite busy or locked result: a store call that waited out its busy timeout for a lock
// another connection held. Nothing about the call was wrong, so the same call can succeed once the lock is free.
func Busy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & primaryCodeMask {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

// unavailable reports whether err is a SQLite busy, locked or cannot-open result, which is how a busy database
// or a missing file appears, or an fstat failure, which is how a file removed while it is open appears. Only the
// fstat extended code counts: other I/O errors share the primary IOERR code and stay internal.
func unavailable(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return Busy(err) || sqliteErr.Code() == sqlite3.SQLITE_IOERR_FSTAT ||
		sqliteErr.Code()&primaryCodeMask == sqlite3.SQLITE_CANTOPEN
}
