package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"modernc.org/sqlite"
)

// errStoreClosing is what a connection the store's pool asks for once Close has begun gets. database/sql opens a
// connection on its own goroutine when a connection is discarded while a call waits for one, and DB.Close does not
// wait for that open, so the store refuses new connections and waits out the ones already made.
var errStoreClosing = errors.New("store is closing")

// sqliteConn is the part of the sqlite driver's connection that database/sql uses: the context forms of each call,
// the session reset, and the validity check that discards an interrupted connection.
type sqliteConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// trackedConnector opens the store's connections and counts each one from the start of its open to its Close, so the
// store's Close can wait until no connection is left. Once closing is set it opens no more.
type trackedConnector struct {
	// inner is the sqlite driver's connector for the store's DSN.
	inner driver.Connector
	// gate, when set, runs before each open. A test sets it to hold an open while it closes the store.
	gate func(context.Context)
	// waiting, when set, runs once Close has closed the pool and is about to wait for the connections left.
	waiting func()
	// readPool is set for the store's read pool. Its connections write nothing, so they register no pre-update hook and
	// record no epic moves.
	readPool bool

	// counting is set once Open has migrated the store: from then on each connection's pre-update hook records the
	// epics a write's rows name (epicMoves). It is unset while the migrations run, and on a store at an older schema,
	// which may hold no epic_revisions.
	counting atomic.Bool
	// keys is the epic key of each table a write has touched, shared by the connector's connections.
	keys epicKeys

	mu      sync.Mutex
	closing bool
	// open is how many connections the connector counts: each from the start of its open to its Close. It is a count,
	// not a sync.WaitGroup, because a WaitGroup that a synctest bubble adds to may not be released outside the bubble,
	// and a connection a test opens inside its bubble is closed by the store's Close outside it.
	open atomic.Int32
	// drained is set by beginClose while connections are counted, and closed once the count falls to zero.
	drained chan struct{}
	// moves is the epic moves of every connection the connector opened, for Close's report of writes outside a store
	// write.
	moves []*epicMoves
}

// outsideTables returns the tables written outside a store write on any of the connector's connections, each with how
// many rows.
func (c *trackedConnector) outsideTables() map[string]int {
	c.mu.Lock()
	moves := slices.Clone(c.moves)
	c.mu.Unlock()
	counts := map[string]int{}
	for _, m := range moves {
		for table, n := range m.outsideTables() {
			counts[table] += n
		}
	}
	return counts
}

// Connect opens one connection, unless the store is closing. The connection counts as open until its Close.
func (c *trackedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil, errStoreClosing
	}
	c.open.Add(1)
	c.mu.Unlock()
	if c.gate != nil {
		c.gate(ctx)
	}
	raw, err := c.inner.Connect(ctx)
	if err != nil {
		c.release()
		return nil, fmt.Errorf("open store connection: %w", err)
	}
	conn, ok := raw.(sqliteConn)
	hooked, hooks := raw.(interface {
		RegisterPreUpdateHook(fn sqlite.PreUpdateHookFn)
	})
	if !ok || !hooks {
		closeErr := raw.Close()
		c.release()
		return nil, errors.Join(fmt.Errorf("store connection %T lacks a method the store uses", raw), closeErr)
	}
	var moves *epicMoves
	if !c.readPool {
		moves = &epicMoves{keys: &c.keys, counting: c.counting.Load}
		hooked.RegisterPreUpdateHook(moves.hook)
		c.mu.Lock()
		c.moves = append(c.moves, moves)
		c.mu.Unlock()
	}
	return &trackedConn{sqliteConn: conn, done: c.release, moves: moves}, nil
}

// release counts a connection the connector opened as closed, or failed to open. The last one released after Close
// begins releases the Close that waits for the connector's connections.
func (c *trackedConnector) release() {
	if c.open.Add(-1) > 0 {
		return
	}
	c.mu.Lock()
	if c.drained != nil {
		close(c.drained)
		c.drained = nil
	}
	c.mu.Unlock()
}

// Driver returns the sqlite driver.
func (c *trackedConnector) Driver() driver.Driver {
	return c.inner.Driver()
}

// beginClose stops new connections. Connections already counted stay counted until they close.
func (c *trackedConnector) beginClose() {
	c.mu.Lock()
	c.closing = true
	if c.open.Load() > 0 {
		c.drained = make(chan struct{})
	}
	c.mu.Unlock()
}

// wait returns once every connection the connector opened is closed.
func (c *trackedConnector) wait() {
	if c.waiting != nil {
		c.waiting()
	}
	c.mu.Lock()
	drained := c.drained
	c.mu.Unlock()
	if drained != nil {
		<-drained
	}
}

// trackedConn is a store connection that tells its connector when it closes, and records each statement on the timer
// of the store call it serves (see callTimer).
type trackedConn struct {
	sqliteConn
	once sync.Once
	done func()
	// moves records the epics the rows written in the connection's store write name (see epicMoves).
	moves *epicMoves

	// mu guards active.
	mu sync.Mutex
	// active is the timer of the store call the connection serves: the one the last timed statement's context
	// carried. A statement whose context carries none, one of a transaction or of a write, belongs to it.
	active *callTimer
}

// ran records query on the timer of the store call the connection serves, and returns that timer, or nil when no
// timed call has used the connection.
func (c *trackedConn) ran(ctx context.Context, query string) *callTimer {
	timer := timerOf(ctx)
	c.mu.Lock()
	if timer == nil {
		timer = c.active
	} else {
		c.active = timer
	}
	c.mu.Unlock()
	if timer != nil {
		timer.ran(query)
	}
	return timer
}

// ExecContext runs query, recorded on its call's timer. Its error is the driver's, text unchanged.
func (c *trackedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.ran(ctx, query)
	result, err := c.sqliteConn.ExecContext(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return result, nil
}

// QueryContext runs query, recorded on its call's timer. The rows of a query the pool times end its call when they
// close. Its error is the driver's, text unchanged.
func (c *trackedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	timer := c.ran(ctx, query)
	rows, err := c.sqliteConn.QueryContext(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return timedRowsOf(ctx, timer, rows), nil
}

// PrepareContext prepares query as a statement that records each run on its call's timer, as ExecContext and
// QueryContext record theirs. Its error is the driver's, text unchanged.
func (c *trackedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.sqliteConn.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	inner, ok := stmt.(sqliteStmt)
	if !ok {
		return nil, errors.Join(fmt.Errorf("store: statement %T runs no context calls", stmt), stmt.Close())
	}
	return &trackedStmt{sqliteStmt: inner, conn: c, query: query}, nil
}

// timedRowsOf returns rows, which end the call of timer when they close if the pool times the query that ctx carries
// to its rows' close.
func timedRowsOf(ctx context.Context, timer *callTimer, rows driver.Rows) driver.Rows {
	if timer != nil && timer.end == endAtRowsClose && timerOf(ctx) == timer {
		return &timedRows{Rows: rows, timer: timer}
	}
	return rows
}

// sqliteStmt is the part of the sqlite driver's prepared statement that database/sql uses: the context forms of its
// runs.
type sqliteStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// trackedStmt is a statement prepared on conn that records each of its runs on the timer of the store call it serves.
type trackedStmt struct {
	sqliteStmt
	conn  *trackedConn
	query string
}

// ExecContext runs the statement, recorded on its call's timer. Its error is the driver's, text unchanged.
func (s *trackedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.conn.ran(ctx, s.query)
	result, err := s.sqliteStmt.ExecContext(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return result, nil
}

// QueryContext runs the statement, recorded on its call's timer, as trackedConn.QueryContext runs a query. Its error is
// the driver's, text unchanged.
func (s *trackedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	timer := s.conn.ran(ctx, s.query)
	rows, err := s.sqliteStmt.QueryContext(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return timedRowsOf(ctx, timer, rows), nil
}

// BeginTx begins a transaction, recorded on its call's timer. A transaction the pool times ends its call when it
// commits or rolls back. Its error is the driver's, text unchanged.
func (c *trackedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	timer := c.ran(ctx, "BEGIN")
	tx, err := c.sqliteConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if timer != nil && timer.end == endAtTxEnd && timerOf(ctx) == timer {
		return &timedTx{Tx: tx, timer: timer}, nil
	}
	return tx, nil
}

// Close closes the connection and counts it closed, once.
func (c *trackedConn) Close() error {
	err := c.sqliteConn.Close()
	c.once.Do(c.done)
	if err != nil {
		return fmt.Errorf("close store connection: %w", err)
	}
	return nil
}
